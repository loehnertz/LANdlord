package wifi

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
)

func mac(s string) net.HardwareAddr {
	m, _ := net.ParseMAC(s)
	return m
}

func connectedWifi() *platform.FakeWifi {
	return &platform.FakeWifi{
		LinkValue: platform.WifiLink{Connected: true, SSID: "home", BSSID: mac("aa:bb:cc:00:00:01"), PHY: "ax",
			QualityPct: 70, RSSI: -62, RxKbps: 433300, TxKbps: 390000, FreqMHz: 5180},
		CountersValue: platform.WifiCounters{TxFrames: 1000, Retries: 100},
		BSSValue: []platform.BSS{
			{SSID: "home", BSSID: mac("aa:bb:cc:00:00:01"), RSSI: -62, FreqMHz: 5180, WidthMHz: 80},
			{SSID: "neighbour", BSSID: mac("aa:bb:cc:00:00:02"), RSSI: -70, FreqMHz: 5240, WidthMHz: 20},
			{SSID: "far", BSSID: mac("aa:bb:cc:00:00:03"), RSSI: -85, FreqMHz: 5200, WidthMHz: 20},
			{SSID: "other channel", BSSID: mac("aa:bb:cc:00:00:04"), RSSI: -50, FreqMHz: 5500, WidthMHz: 40},
		},
	}
}

func TestLinkSamplesAndRetryRate(t *testing.T) {
	w := connectedWifi()
	c := New(&platform.Fake{Wifi: w}, 0, 0, nil)
	var buf record.Buffer
	c.sampleLink(w, &buf)
	w.Set(func(w *platform.FakeWifi) { w.CountersValue = platform.WifiCounters{TxFrames: 2000, Retries: 350} })
	c.sampleLink(w, &buf)

	links := buf.Filter(record.CWifi, record.NLink)
	if len(links) != 2 {
		t.Fatalf("links = %d", len(links))
	}
	first, second := links[0], links[1]
	if _, ok := first.Values["retry_pct"]; ok {
		t.Fatal("first sample should have no retry rate")
	}
	v := second.Values
	if v["connected"] != 1 || v["rssi_dbm"] != -62 || v["rx_mbps"] != 433.3 || v["channel"] != 36 || v["retry_pct"] != 25 {
		t.Fatalf("values = %v", v)
	}
	if second.Attrs["band"] != "5" || second.Attrs["bssid"] != "aa:bb:cc:00:00:01" || second.Attrs["ssid"] != "home" {
		t.Fatalf("attrs = %v", second.Attrs)
	}

	w.Set(func(w *platform.FakeWifi) {
		w.LinkValue = platform.WifiLink{Connected: true, QualityPct: 40, FreqMHz: 2437}
	})
	c.sampleLink(w, &buf)
	if got := buf.Filter(record.CWifi, record.NLink)[2].Values["rssi_dbm"]; got != -80 {
		t.Fatalf("rssi derived from quality = %v, want -80", got)
	}
}

func TestScanOverlap(t *testing.T) {
	w := connectedWifi()
	c := New(&platform.Fake{Wifi: w}, 0, 0, nil)
	c.scanWait = 0
	var buf record.Buffer
	c.sampleLink(w, &buf)
	c.scanOnce(context.Background(), w, &buf)
	scans := buf.Filter(record.CWifi, record.NScan)
	if len(scans) != 1 || w.Scans != 1 {
		t.Fatalf("scans = %d, fake scans = %d", len(scans), w.Scans)
	}
	v := scans[0].Values
	if v["total_aps"] != 4 || v["overlap_aps"] != 1 || v["width_mhz"] != 80 {
		t.Fatalf("scan values = %v", v)
	}
	var nets []network
	if err := json.Unmarshal([]byte(scans[0].Attrs["networks"]), &nets); err != nil || len(nets) != 4 || nets[1].Channel != 48 {
		t.Fatalf("networks = %+v err=%v", nets, err)
	}
}

func TestLocationDeniedAndRecovery(t *testing.T) {
	w := connectedWifi()
	w.LinkErr = platform.ErrLocationDenied
	var states []bool
	c := New(&platform.Fake{Wifi: w}, 0, 0, func(denied bool) { states = append(states, denied) })
	var buf record.Buffer
	c.sampleLink(w, &buf)
	c.sampleLink(w, &buf)
	if u := buf.Filter(record.CWifi, ""); len(u) != 1 || u[0].Kind != record.KindUnavailable || u[0].Attrs["reason"] != locationDeniedReason {
		t.Fatalf("records = %+v", u)
	}
	w.Set(func(w *platform.FakeWifi) { w.LinkErr = nil })
	c.sampleLink(w, &buf)
	if len(states) != 2 || !states[0] || states[1] {
		t.Fatalf("location callbacks = %v", states)
	}
	if len(buf.Filter(record.CWifi, record.NLink)) != 1 {
		t.Fatal("link not sampled after recovery")
	}
}

func TestRunForwardsEventsAndStops(t *testing.T) {
	w := connectedWifi()
	w.EventsCh = make(chan platform.WifiEvent, 1)
	c := New(&platform.Fake{Wifi: w}, 10*time.Millisecond, time.Hour, nil)
	c.scanWait = 0
	var buf record.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = c.Run(ctx, &buf) })
	w.EventsCh <- platform.WifiEvent{Name: record.NRoam, Reason: 7}
	deadline := time.Now().Add(2 * time.Second)
	for len(buf.Filter(record.CWifi, record.NRoam)) == 0 || len(buf.Filter(record.CWifi, record.NScan)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("roam event or scan not recorded")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	wg.Wait()
	if roam := buf.Filter(record.CWifi, record.NRoam)[0]; roam.Values["reason"] != 7 {
		t.Fatalf("roam = %+v", roam)
	}
}

func TestUnsupportedIsPermanent(t *testing.T) {
	c := New(&platform.Fake{}, 0, 0, nil)
	if err := c.Run(context.Background(), &record.Buffer{}); err == nil {
		t.Fatal("expected permanent error")
	}
}
