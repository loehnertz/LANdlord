package aggregate

import (
	"math"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/record"
	"github.com/loehnertz/LANdlord/internal/store"
)

var t0 = time.Date(2026, 9, 14, 18, 0, 0, 0, time.UTC)

func build(t *testing.T, meta store.Meta, recs []record.Record) *Session {
	t.Helper()
	s, err := Build(meta, DefaultWidth, func(fn func(record.Record) error) error {
		for _, r := range recs {
			if err := fn(r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func echo(target string, at time.Duration, rtt float64) record.Record {
	return record.Metric(record.CPing, record.NEcho, target, t0.Add(at), map[string]float64{"rtt_ms": rtt})
}

func lost(target string, at time.Duration) record.Record {
	return record.Metric(record.CPing, record.NEcho, target, t0.Add(at), map[string]float64{"lost": 1})
}

func TestStatsHelpers(t *testing.T) {
	sorted := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9}
	if Percentile(sorted, 0.5) != 5 || Percentile(sorted, 0.95) != 9 || Percentile(nil, 0.5) != 0 {
		t.Fatal("percentile wrong")
	}
	if Median([]float64{9, 1, 5}) != 5 {
		t.Fatal("median wrong")
	}
	if Jitter([]float64{10, 20, 10, 20}) != 10 || Jitter([]float64{5}) != 0 {
		t.Fatal("jitter wrong")
	}
}

func TestPathStats(t *testing.T) {
	var recs []record.Record
	for i := 1; i <= 9; i++ {
		recs = append(recs, echo(record.TGateway, time.Duration(i-1)*time.Second, float64(i)))
	}
	recs = append(recs, lost(record.TGateway, 9500*time.Millisecond))
	s := build(t, store.Meta{Start: t0, Duration: time.Hour, Finished: true, FinishedAt: t0.Add(10 * time.Second)}, recs)
	if len(s.Buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(s.Buckets))
	}
	p := s.Buckets[0].Paths[record.TGateway]
	if p.Sent != 10 || p.Recv != 9 || p.LossPct != 10 || p.P50 != 5 || p.P95 != 9 || p.Jitter != 1 {
		t.Fatalf("path stats wrong: %+v", *p)
	}
}

func TestBucketRangeUnfinishedTrimsToLastRecord(t *testing.T) {
	recs := []record.Record{echo(record.TGateway, 0, 1), echo(record.TGateway, 25*time.Second, 1)}
	s := build(t, store.Meta{Start: t0, Duration: time.Hour}, recs)
	if len(s.Buckets) != 3 {
		t.Fatalf("buckets = %d, want 3", len(s.Buckets))
	}
	if !s.Buckets[2].Start.Equal(t0.Add(20 * time.Second)) {
		t.Fatalf("bucket 2 start = %v", s.Buckets[2].Start)
	}
	if s.Index(t0.Add(-time.Second)) != -1 || s.Index(t0.Add(time.Hour)) != -1 {
		t.Fatal("Index out of range should be -1")
	}
}

func TestWifiRouterStunDNSHTTP(t *testing.T) {
	recs := []record.Record{
		{Time: t0, Collector: record.CWifi, Kind: record.KindMetric, Name: record.NLink,
			Values: map[string]float64{"connected": 1, "rssi_dbm": -60, "quality_pct": 80, "rx_mbps": 300, "tx_mbps": 200, "channel": 36, "retry_pct": 4},
			Attrs:  map[string]string{"ssid": "home", "bssid": "aa:bb", "band": "5"}},
		{Time: t0.Add(2 * time.Second), Collector: record.CWifi, Kind: record.KindMetric, Name: record.NLink,
			Values: map[string]float64{"connected": 1, "rssi_dbm": -70, "quality_pct": 60, "rx_mbps": 100, "tx_mbps": 100, "channel": 36, "retry_pct": 6},
			Attrs:  map[string]string{"ssid": "home", "bssid": "aa:bb", "band": "5"}},
		record.Metric(record.CSTUN, record.NBurst, "stun.example:3478", t0.Add(time.Second), map[string]float64{"sent": 500, "received": 490, "jitter_ms": 12, "rtt_p50_ms": 30}),
		record.Metric(record.CDNS, record.NLookup, "configured:192.168.178.1", t0, map[string]float64{"ms": 40}),
		record.Metric(record.CDNS, record.NLookup, "configured:192.168.178.1", t0.Add(time.Second), map[string]float64{"failed": 1}),
		record.Metric(record.CHTTP, record.NFetch, "zoom.us", t0, map[string]float64{"connect_ms": 20, "tls_ms": 30, "ttfb_ms": 90}),
		record.Metric(record.CUPnP, record.NWAN, "", t0, map[string]float64{"rx_bps": 40e6, "tx_bps": 1e6, "link_down_bps": 50e6, "link_up_bps": 10e6}),
		record.Metric(record.CSystem, record.NIface, "Wi-Fi", t0, map[string]float64{"rx_bps": 2e6, "tx_bps": 1e5}),
		record.Metric(record.CSystem, record.NPower, "", t0, map[string]float64{"on_ac": 0}),
	}
	s := build(t, store.Meta{Start: t0, Duration: time.Hour, Finished: true, FinishedAt: t0.Add(10 * time.Second)}, recs)
	b := s.Buckets[0]
	w := b.Wifi
	if !w.Present || !w.Connected || w.RSSIMin != -70 || w.RSSIMax != -60 || w.RSSIMean != -65 || w.RxMbps != 200 || w.RetryPct != 5 || w.Band != "5" || w.Channel != 36 {
		t.Fatalf("wifi stats wrong: %+v", w)
	}
	if b.STUN.Sent != 500 || b.STUN.Recv != 490 || b.STUN.LossPct != 2 || b.STUN.Jitter != 12 || b.STUN.P50 != 30 {
		t.Fatalf("stun stats wrong: %+v", b.STUN)
	}
	d := b.DNS["configured:192.168.178.1"]
	if d.Count != 2 || d.Failed != 1 || d.MeanMs != 40 {
		t.Fatalf("dns stats wrong: %+v", *d)
	}
	if h := b.HTTP["zoom.us"]; h.Count != 1 || h.ConnectMs != 20 || h.TTFBMs != 90 {
		t.Fatalf("http stats wrong: %+v", *h)
	}
	if !b.Router.Present || b.Router.RxBps != 40e6 || b.Router.LinkDownBps != 50e6 {
		t.Fatalf("router stats wrong: %+v", b.Router)
	}
	if b.LaptopRxBps != 2e6 || !b.PowerKnown || !b.OnBattery {
		t.Fatalf("laptop/power wrong: rx=%v known=%v battery=%v", b.LaptopRxBps, b.PowerKnown, b.OnBattery)
	}
}

func TestCarryForwardSleepEventsInfos(t *testing.T) {
	meta := store.Meta{Start: t0, Duration: time.Hour, Finished: true, FinishedAt: t0.Add(60 * time.Second)}
	recs := []record.Record{
		record.Metric(record.CWifi, record.NScan, "", t0, map[string]float64{"overlap_aps": 5, "total_aps": 12}),
		record.Metric(record.CSystem, record.NPower, "", t0, map[string]float64{"on_ac": 1}),
		{Time: t0.Add(50 * time.Second), Collector: record.CSystem, Kind: record.KindEvent, Name: record.NSleep,
			Values: map[string]float64{"seconds": 30},
			Attrs:  map[string]string{"from": t0.Add(20 * time.Second).Format(time.RFC3339), "to": t0.Add(50 * time.Second).Format(time.RFC3339)}},
		record.Event(record.CWifi, record.NRoam, t0.Add(31*time.Second), nil),
		record.Info(record.CPublic, record.NPublicIP, "", t0.Add(-time.Hour), map[string]string{"ip": "1.2.3.4", "loc": "DE"}),
		record.Info(record.CPublic, record.NPublicIP, "", t0.Add(time.Second), map[string]string{"ip": "5.6.7.8", "loc": "DE"}),
		record.Unavailable(record.CFritz, t0, "no password"),
		record.Unavailable(record.CFritz, t0.Add(time.Second), "no password"),
		echo(record.TGateway, -5*time.Second, 1), // before start: ignored
	}
	s := build(t, meta, recs)
	if len(s.Buckets) != 6 {
		t.Fatalf("buckets = %d, want 6", len(s.Buckets))
	}
	if s.Buckets[3].Wifi.OverlapAPs != 5 || !s.Buckets[3].Wifi.ScanKnown {
		t.Fatal("scan overlap not carried forward")
	}
	if !s.Buckets[4].PowerKnown || s.Buckets[4].OnBattery {
		t.Fatal("power not carried forward")
	}
	for i, want := range []bool{false, false, true, true, true, false} {
		if s.Buckets[i].Asleep != want {
			t.Fatalf("bucket %d asleep = %v, want %v", i, s.Buckets[i].Asleep, want)
		}
	}
	if len(s.Buckets[3].Events) != 1 || s.Buckets[3].Events[0].Name != record.NRoam {
		t.Fatalf("roam not attached to bucket 3: %+v", s.Buckets[3].Events)
	}
	if _, ok := s.Buckets[0].Paths[record.TGateway]; ok {
		t.Fatal("record before start was aggregated")
	}
	if ip, ok := s.LatestInfo(record.CPublic, record.NPublicIP); !ok || ip.Attrs["ip"] != "5.6.7.8" {
		t.Fatalf("LatestInfo = %+v", ip)
	}
	if len(s.Unavailable) != 1 {
		t.Fatalf("unavailable not deduplicated: %d", len(s.Unavailable))
	}
	if math.IsNaN(s.Buckets[0].STUN.Jitter) {
		t.Fatal("NaN in empty stats")
	}
}

func TestRouterSamplesHeldForSixtySeconds(t *testing.T) {
	meta := store.Meta{Start: t0, Duration: time.Hour, Finished: true, FinishedAt: t0.Add(2 * time.Minute)}
	recs := []record.Record{
		record.Metric(record.CUPnP, record.NWAN, "", t0, map[string]float64{"rx_bps": 40e6, "tx_bps": 1e6, "link_down_bps": 50e6, "link_up_bps": 10e6}),
	}
	s := build(t, meta, recs)
	if !s.Buckets[6].Router.Present || s.Buckets[6].Router.RxBps != 40e6 || s.Buckets[6].Router.LinkDownBps != 50e6 {
		t.Fatalf("bucket 6 (60 s) should hold the router sample: %+v", s.Buckets[6].Router)
	}
	if s.Buckets[7].Router.Present {
		t.Fatalf("bucket 7 (70 s) should no longer hold the router sample: %+v", s.Buckets[7].Router)
	}
}

func TestSpeedTestBucketsAreMarked(t *testing.T) {
	meta := store.Meta{Start: t0, Duration: time.Hour, Finished: true, FinishedAt: t0.Add(2 * time.Minute)}
	recs := []record.Record{
		{Time: t0.Add(25 * time.Second), Collector: record.CSpeed, Kind: record.KindMetric, Name: record.NTest,
			Values: map[string]float64{"down_mbps": 50, "up_mbps": 10, "bloat_ms": 3, "duration_s": 30}},
	}
	s := build(t, meta, recs)
	for i, want := range []bool{false, false, true, true, true, true, false} {
		if s.Buckets[i].SelfTest != want {
			t.Fatalf("bucket %d SelfTest = %v, want %v", i, s.Buckets[i].SelfTest, want)
		}
	}
}

func TestBuilderSnapshotsAreIndependent(t *testing.T) {
	meta := store.Meta{Start: t0, Duration: time.Hour}
	b := NewBuilder(meta, DefaultWidth)
	b.Add(echo(record.TGateway, 0, 10))
	b.Add(record.Event(record.CWifi, record.NRoam, t0.Add(time.Second), nil))
	first := b.Snapshot(meta)

	b.Add(echo(record.TGateway, 2*time.Second, 30))
	b.Add(record.Event(record.CWifi, record.NRoam, t0.Add(3*time.Second), nil))
	b.Add(echo(record.TGateway, 15*time.Second, 5))
	second := b.Snapshot(meta)

	if len(first.Buckets) != 1 || first.Buckets[0].Paths[record.TGateway].Sent != 1 || first.Buckets[0].Paths[record.TGateway].P95 != 10 {
		t.Fatalf("first snapshot changed: %+v", first.Buckets[0].Paths[record.TGateway])
	}
	if len(first.Buckets[0].Events) != 1 || len(first.Events) != 1 {
		t.Fatal("first snapshot events changed")
	}
	if len(second.Buckets) != 2 || second.Buckets[0].Paths[record.TGateway].Sent != 2 || second.Buckets[0].Paths[record.TGateway].P95 != 30 {
		t.Fatalf("second snapshot wrong: buckets=%d", len(second.Buckets))
	}
	if len(second.Events) != 2 {
		t.Fatalf("second snapshot events = %d, want 2", len(second.Events))
	}
	// Records beyond the configured duration are dropped.
	b.Add(echo(record.TGateway, 2*time.Hour, 1))
	if n := len(b.Snapshot(meta).Buckets); n != 2 {
		t.Fatalf("record after end grew buckets to %d", n)
	}
}
