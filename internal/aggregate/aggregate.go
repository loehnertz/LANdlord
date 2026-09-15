// Package aggregate turns raw records into fixed-width time buckets.
package aggregate

import (
	"math"
	"slices"
	"time"

	"github.com/loehnertz/LANdlord/internal/record"
	"github.com/loehnertz/LANdlord/internal/store"
)

const DefaultWidth = 10 * time.Second

type PathStats struct {
	Sent, Recv int
	LossPct    float64
	P50, P95   float64
	Jitter     float64
	rtts       []float64
}

type WifiStats struct {
	Present, Connected, ScanKnown bool
	RSSIMin, RSSIMean, RSSIMax    float64
	QualityPct                    float64
	RxMbps, TxMbps                float64
	RetryPct                      float64
	SSID, BSSID, Band             string
	Channel, OverlapAPs           int
}

type ResolverStats struct {
	Count, Failed int
	MeanMs        float64
}

type HTTPStats struct {
	Count, Failed     int
	ConnectMs, TTFBMs float64
}

type RouterStats struct {
	Present                              bool
	RxBps, TxBps, LinkDownBps, LinkUpBps float64
}

// DSLStats holds router line statistics: DSL values, or DOCSIS values when Cable is set.
type DSLStats struct {
	Present, Resync     bool
	SNRDownDB, CRCDelta float64

	Cable                            bool
	CableUncorrectableDelta          float64
	CableUSPowerMax                  float64
	CableDSPowerMin, CableDSPowerMax float64
	CableDSMERMin                    float64 // 0 when unknown
}

type Event struct {
	Time            time.Time
	Collector, Name string
	Values          map[string]float64
	Attrs           map[string]string
}

type Bucket struct {
	Start                         time.Time
	Paths                         map[string]*PathStats
	STUN                          PathStats
	Wifi                          WifiStats
	DNS                           map[string]*ResolverStats
	HTTP                          map[string]*HTTPStats
	Router                        RouterStats
	DSL                           DSLStats
	LaptopRxBps, LaptopTxBps      float64
	PowerKnown, OnBattery, Asleep bool
	// SelfTest marks buckets during LANdlord's own speed tests.
	SelfTest bool
	Events   []Event
}

type SpeedTest struct {
	Time                                 time.Time
	DownMbps, UpMbps, BloatMs, DurationS float64
	Grade, Trigger                       string
}

type Session struct {
	Meta        store.Meta
	Width       time.Duration
	Buckets     []Bucket
	Events      []Event
	Infos       []record.Record
	Unavailable []record.Record
	Scans       []record.Record
	PMTU        []record.Record
	SpeedTests  []SpeedTest
	Skipped     []Event
}

// accum holds running sums for one bucket until finalize.
type accum struct {
	rssiN, rssiSum, qualSum, rxSum, txSum float64
	retryN, retrySum                      float64
	laptopN, laptopRx, laptopTx           float64
	routerN, routerRx, routerTx           float64
	stunN, stunJitter, stunP50            float64
	powerN, powerAC                       float64
	dnsSum                                map[string]float64
	httpConn, httpTTFB                    map[string]float64
}

// Builder accumulates records incrementally. The live verdict feeds it every record as it
// is written; the final report builds a fresh one from disk.
type Builder struct {
	width           time.Duration
	limit           int // bucket count covered by the configured duration
	s               Session
	acc             []accum
	last            time.Time
	seenUnavailable map[string]bool
}

func NewBuilder(meta store.Meta, width time.Duration) *Builder {
	if width <= 0 {
		width = DefaultWidth
	}
	return &Builder{
		width:           width,
		limit:           bucketCount(meta.Start, meta.Start.Add(meta.Duration), width),
		s:               Session{Meta: meta, Width: width},
		seenUnavailable: map[string]bool{},
	}
}

func Build(meta store.Meta, width time.Duration, read func(func(record.Record) error) error) (*Session, error) {
	b := NewBuilder(meta, width)
	if err := read(func(r record.Record) error { b.Add(r); return nil }); err != nil {
		return nil, err
	}
	return b.Snapshot(meta), nil
}

func (b *Builder) Add(r record.Record) {
	s := &b.s
	switch r.Kind {
	case record.KindInfo:
		s.Infos = append(s.Infos, r)
		return
	case record.KindUnavailable:
		key := r.Collector + "\x00" + r.Attrs["reason"]
		if !b.seenUnavailable[key] {
			b.seenUnavailable[key] = true
			s.Unavailable = append(s.Unavailable, r)
		}
		return
	}
	if r.Time.Before(s.Meta.Start) {
		return
	}
	i := int(r.Time.Sub(s.Meta.Start) / b.width)
	if i >= b.limit {
		return
	}
	b.grow(i + 1)
	if r.Time.After(b.last) {
		b.last = r.Time
	}
	s.add(&s.Buckets[i], &b.acc[i], r)
}

func (b *Builder) grow(n int) {
	for len(b.s.Buckets) < n {
		b.s.Buckets = append(b.s.Buckets, emptyBucket(b.s.Meta.Start.Add(time.Duration(len(b.s.Buckets))*b.width)))
		b.acc = append(b.acc, accum{})
	}
}

// Snapshot finalises a copy of the current state. For a finished session it covers the
// whole recording; otherwise it ends with the bucket of the latest record.
func (b *Builder) Snapshot(meta store.Meta) *Session {
	n := 0
	switch {
	case meta.Finished:
		n = min(b.limit, bucketCount(meta.Start, meta.FinishedAt, b.width))
	case !b.last.IsZero():
		n = int(b.last.Sub(meta.Start)/b.width) + 1
	}
	b.grow(n)
	src := &b.s
	out := &Session{
		Meta:        meta,
		Width:       b.width,
		Buckets:     make([]Bucket, n),
		Events:      slices.Clone(src.Events),
		Infos:       slices.Clone(src.Infos),
		Unavailable: slices.Clone(src.Unavailable),
		Scans:       slices.Clone(src.Scans),
		PMTU:        slices.Clone(src.PMTU),
		SpeedTests:  slices.Clone(src.SpeedTests),
		Skipped:     slices.Clone(src.Skipped),
	}
	for i := range n {
		out.Buckets[i] = cloneBucket(src.Buckets[i])
		a := b.acc[i]
		finalize(&out.Buckets[i], &a)
	}
	out.carryForward()
	out.applySleep()
	out.applySelfTests()
	slices.SortStableFunc(out.Events, func(x, y Event) int { return x.Time.Compare(y.Time) })
	slices.SortStableFunc(out.Infos, func(x, y record.Record) int { return x.Time.Compare(y.Time) })
	slices.SortStableFunc(out.SpeedTests, func(x, y SpeedTest) int { return x.Time.Compare(y.Time) })
	return out
}

func bucketCount(start, end time.Time, width time.Duration) int {
	return max(0, int(math.Ceil(float64(end.Sub(start))/float64(width))))
}

func emptyBucket(start time.Time) Bucket {
	return Bucket{Start: start, Paths: map[string]*PathStats{}, DNS: map[string]*ResolverStats{}, HTTP: map[string]*HTTPStats{}}
}

// cloneBucket deep-copies everything finalize and carryForward write to.
func cloneBucket(src Bucket) Bucket {
	dst := src
	dst.Paths = make(map[string]*PathStats, len(src.Paths))
	for k, p := range src.Paths {
		cp := *p
		dst.Paths[k] = &cp
	}
	dst.DNS = make(map[string]*ResolverStats, len(src.DNS))
	for k, r := range src.DNS {
		cp := *r
		dst.DNS[k] = &cp
	}
	dst.HTTP = make(map[string]*HTTPStats, len(src.HTTP))
	for k, h := range src.HTTP {
		cp := *h
		dst.HTTP[k] = &cp
	}
	dst.Events = slices.Clone(src.Events)
	return dst
}

// Index returns the bucket index for t, or -1 when t is outside the session.
func (s *Session) Index(t time.Time) int {
	if t.Before(s.Meta.Start) {
		return -1
	}
	i := int(t.Sub(s.Meta.Start) / s.Width)
	if i >= len(s.Buckets) {
		return -1
	}
	return i
}

// LatestInfo returns the most recent info record with the given collector and name.
func (s *Session) LatestInfo(collector, name string) (record.Record, bool) {
	var out record.Record
	found := false
	for _, r := range s.Infos {
		if r.Collector == collector && r.Name == name && (!found || !r.Time.Before(out.Time)) {
			out, found = r, true
		}
	}
	return out, found
}

func (s *Session) add(b *Bucket, a *accum, r record.Record) {
	v := r.Values
	if r.Kind == record.KindEvent {
		ev := Event{Time: r.Time, Collector: r.Collector, Name: r.Name, Values: v, Attrs: r.Attrs}
		b.Events = append(b.Events, ev)
		s.Events = append(s.Events, ev)
		switch {
		case r.Collector == record.CSpeed && r.Name == record.NSkipped:
			s.Skipped = append(s.Skipped, ev)
		case r.Collector == record.CFritz && r.Name == record.NResync:
			b.DSL.Resync = true
		}
		return
	}
	switch r.Collector + "/" + r.Name {
	case record.CPing + "/" + record.NEcho:
		p := b.Paths[r.Target]
		if p == nil {
			p = &PathStats{}
			b.Paths[r.Target] = p
		}
		p.Sent++
		if rtt, ok := v["rtt_ms"]; ok && v["lost"] == 0 {
			p.Recv++
			p.rtts = append(p.rtts, rtt)
		}
	case record.CSTUN + "/" + record.NBurst:
		b.STUN.Sent += int(v["sent"])
		b.STUN.Recv += int(v["received"])
		a.stunN++
		a.stunJitter += v["jitter_ms"]
		a.stunP50 += v["rtt_p50_ms"]
	case record.CWifi + "/" + record.NLink:
		w := &b.Wifi
		w.Present = true
		if v["connected"] != 1 {
			return
		}
		rssi := v["rssi_dbm"]
		if !w.Connected || rssi < w.RSSIMin {
			w.RSSIMin = rssi
		}
		if !w.Connected || rssi > w.RSSIMax {
			w.RSSIMax = rssi
		}
		w.Connected = true
		a.rssiN++
		a.rssiSum += rssi
		a.qualSum += v["quality_pct"]
		a.rxSum += v["rx_mbps"]
		a.txSum += v["tx_mbps"]
		if rp, ok := v["retry_pct"]; ok {
			a.retryN++
			a.retrySum += rp
		}
		w.Channel = int(v["channel"])
		w.SSID, w.BSSID, w.Band = r.Attrs["ssid"], r.Attrs["bssid"], r.Attrs["band"]
	case record.CWifi + "/" + record.NScan:
		b.Wifi.OverlapAPs = int(v["overlap_aps"])
		b.Wifi.ScanKnown = true
		s.Scans = append(s.Scans, r)
	case record.CSystem + "/" + record.NIface:
		a.laptopN++
		a.laptopRx += v["rx_bps"]
		a.laptopTx += v["tx_bps"]
	case record.CSystem + "/" + record.NPower:
		a.powerN++
		a.powerAC += v["on_ac"]
	case record.CDNS + "/" + record.NLookup:
		rs := b.DNS[r.Target]
		if rs == nil {
			rs = &ResolverStats{}
			b.DNS[r.Target] = rs
		}
		rs.Count++
		if v["failed"] == 1 {
			rs.Failed++
		} else {
			if a.dnsSum == nil {
				a.dnsSum = map[string]float64{}
			}
			a.dnsSum[r.Target] += v["ms"]
		}
	case record.CHTTP + "/" + record.NFetch:
		hs := b.HTTP[r.Target]
		if hs == nil {
			hs = &HTTPStats{}
			b.HTTP[r.Target] = hs
		}
		hs.Count++
		if v["failed"] == 1 {
			hs.Failed++
		} else {
			if a.httpConn == nil {
				a.httpConn, a.httpTTFB = map[string]float64{}, map[string]float64{}
			}
			a.httpConn[r.Target] += v["connect_ms"]
			a.httpTTFB[r.Target] += v["ttfb_ms"]
		}
	case record.CUPnP + "/" + record.NWAN:
		b.Router.Present = true
		a.routerN++
		a.routerRx += v["rx_bps"]
		a.routerTx += v["tx_bps"]
		if d := v["link_down_bps"]; d > 0 {
			b.Router.LinkDownBps = d
		}
		if u := v["link_up_bps"]; u > 0 {
			b.Router.LinkUpBps = u
		}
	case record.CFritz + "/" + record.NDSL:
		b.DSL.Present = true
		b.DSL.SNRDownDB = v["snr_down_db"]
		b.DSL.CRCDelta += v["crc_delta"]
	case record.CFritz + "/" + record.NDOCSIS:
		d := &b.DSL
		if !d.Cable {
			d.CableDSPowerMin, d.CableDSPowerMax = v["ds_power_min_dbmv"], v["ds_power_max_dbmv"]
			d.CableUSPowerMax, d.CableDSMERMin = v["us_power_max_dbmv"], v["ds_mer_min_db"]
		} else {
			d.CableDSPowerMin = min(d.CableDSPowerMin, v["ds_power_min_dbmv"])
			d.CableDSPowerMax = max(d.CableDSPowerMax, v["ds_power_max_dbmv"])
			d.CableUSPowerMax = max(d.CableUSPowerMax, v["us_power_max_dbmv"])
			if mer := v["ds_mer_min_db"]; mer > 0 && (d.CableDSMERMin == 0 || mer < d.CableDSMERMin) {
				d.CableDSMERMin = mer
			}
		}
		d.Present, d.Cable = true, true
		d.CableUncorrectableDelta += v["noncorr_errors_delta"]
	case record.CSpeed + "/" + record.NTest:
		s.SpeedTests = append(s.SpeedTests, SpeedTest{
			Time: r.Time, DownMbps: v["down_mbps"], UpMbps: v["up_mbps"], BloatMs: v["bloat_ms"], DurationS: v["duration_s"],
			Grade: r.Attrs["grade"], Trigger: r.Attrs["trigger"],
		})
	case record.CMTU + "/" + record.NPMTU:
		s.PMTU = append(s.PMTU, r)
	}
}

func finalize(b *Bucket, a *accum) {
	for _, p := range b.Paths {
		if p.Sent > 0 {
			p.LossPct = float64(p.Sent-p.Recv) / float64(p.Sent) * 100
		}
		p.Jitter = Jitter(p.rtts)
		sorted := slices.Clone(p.rtts)
		slices.Sort(sorted)
		p.P50, p.P95 = Percentile(sorted, 0.5), Percentile(sorted, 0.95)
		p.rtts = nil
	}
	if b.STUN.Sent > 0 {
		b.STUN.LossPct = float64(b.STUN.Sent-b.STUN.Recv) / float64(b.STUN.Sent) * 100
	}
	if a.stunN > 0 {
		b.STUN.Jitter, b.STUN.P50 = a.stunJitter/a.stunN, a.stunP50/a.stunN
	}
	if a.rssiN > 0 {
		b.Wifi.RSSIMean = a.rssiSum / a.rssiN
		b.Wifi.QualityPct = a.qualSum / a.rssiN
		b.Wifi.RxMbps, b.Wifi.TxMbps = a.rxSum/a.rssiN, a.txSum/a.rssiN
	}
	if a.retryN > 0 {
		b.Wifi.RetryPct = a.retrySum / a.retryN
	}
	for target, rs := range b.DNS {
		if ok := rs.Count - rs.Failed; ok > 0 {
			rs.MeanMs = a.dnsSum[target] / float64(ok)
		}
	}
	for target, hs := range b.HTTP {
		if ok := hs.Count - hs.Failed; ok > 0 {
			hs.ConnectMs, hs.TTFBMs = a.httpConn[target]/float64(ok), a.httpTTFB[target]/float64(ok)
		}
	}
	if a.routerN > 0 {
		b.Router.RxBps, b.Router.TxBps = a.routerRx/a.routerN, a.routerTx/a.routerN
	}
	if a.laptopN > 0 {
		b.LaptopRxBps, b.LaptopTxBps = a.laptopRx/a.laptopN, a.laptopTx/a.laptopN
	}
	if a.powerN > 0 {
		b.PowerKnown, b.OnBattery = true, a.powerAC/a.powerN < 0.5
	}
}

// routerHold is how long a router sample stays valid for buckets without their own sample.
// UPnP is polled every 30 s, so without it two out of three buckets would lack router data.
const routerHold = 60 * time.Second

// carryForward fills slow-changing values (scan results, router stats, power) into later buckets.
func (s *Session) carryForward() {
	var overlap int
	var scanKnown, powerKnown, onBattery bool
	var linkDown, linkUp float64
	var lastRouter RouterStats
	lastRouterIdx := -1
	hold := int(routerHold / s.Width)
	for i := range s.Buckets {
		b := &s.Buckets[i]
		if b.Wifi.ScanKnown {
			overlap, scanKnown = b.Wifi.OverlapAPs, true
		} else if scanKnown {
			b.Wifi.OverlapAPs, b.Wifi.ScanKnown = overlap, true
		}
		if b.PowerKnown {
			powerKnown, onBattery = true, b.OnBattery
		} else if powerKnown {
			b.PowerKnown, b.OnBattery = true, onBattery
		}
		if b.Router.Present {
			if b.Router.LinkDownBps > 0 {
				linkDown, linkUp = b.Router.LinkDownBps, b.Router.LinkUpBps
			} else {
				b.Router.LinkDownBps, b.Router.LinkUpBps = linkDown, linkUp
			}
			lastRouter, lastRouterIdx = b.Router, i
		} else if lastRouterIdx >= 0 && i-lastRouterIdx <= hold {
			b.Router = lastRouter
		}
	}
}

// applySelfTests marks the buckets a speed test ran in, including the one it ended in.
func (s *Session) applySelfTests() {
	for _, st := range s.SpeedTests {
		if st.DurationS <= 0 {
			continue
		}
		first := s.Index(st.Time)
		last := s.Index(st.Time.Add(time.Duration(st.DurationS * float64(time.Second))))
		if first < 0 {
			continue
		}
		if last < 0 {
			last = len(s.Buckets) - 1
		}
		for i := first; i <= last; i++ {
			s.Buckets[i].SelfTest = true
		}
	}
}

func (s *Session) applySleep() {
	for _, ev := range s.Events {
		if ev.Collector != record.CSystem || ev.Name != record.NSleep {
			continue
		}
		from, err1 := time.Parse(time.RFC3339, ev.Attrs["from"])
		to, err2 := time.Parse(time.RFC3339, ev.Attrs["to"])
		if err1 != nil || err2 != nil {
			continue
		}
		for i := range s.Buckets {
			st := s.Buckets[i].Start
			if !st.Before(from) && st.Before(to) {
				s.Buckets[i].Asleep = true
			}
		}
	}
}
