package diagnose

import (
	"slices"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/config"
	"github.com/loehnertz/LANdlord/internal/record"
	"github.com/loehnertz/LANdlord/internal/store"
)

var t0 = time.Date(2026, 9, 14, 18, 0, 0, 0, time.UTC)

const width = 10 * time.Second

func th() config.Thresholds { return config.Default().Thresholds }

func path(p50, p95 float64) *aggregate.PathStats {
	return &aggregate.PathStats{Sent: 10, Recv: 10, P50: p50, P95: p95, Jitter: 1}
}

func healthyBucket(start time.Time) aggregate.Bucket {
	b := aggregate.Bucket{
		Start: start,
		Paths: map[string]*aggregate.PathStats{
			record.TGateway:              path(3, 4),
			"hop1":                       path(10, 12),
			"hop2":                       path(11, 12),
			"hop3":                       path(12, 12),
			"inet:1.1.1.1":               path(20, 25),
			"inet:8.8.8.8":               path(20, 25),
			"inet:9.9.9.9":               path(20, 25),
			"inet6:2606:4700:4700::1111": path(20, 25),
			"inet6:2001:4860:4860::8888": path(20, 25),
		},
		STUN: aggregate.PathStats{Sent: 500, Recv: 500, P50: 20, Jitter: 2},
		Wifi: aggregate.WifiStats{Present: true, Connected: true, ScanKnown: true, RSSIMin: -55, RSSIMean: -54, RSSIMax: -53,
			RxMbps: 400, TxMbps: 300, RetryPct: 2, Band: "5", Channel: 36, OverlapAPs: 1},
		DNS: map[string]*aggregate.ResolverStats{
			record.TSystem:             {Count: 3, MeanMs: 20},
			"configured:192.168.178.1": {Count: 3, MeanMs: 20},
			"1.1.1.1":                  {Count: 3, MeanMs: 20},
		},
		HTTP: map[string]*aggregate.HTTPStats{
			"zoom.us":        {Count: 1, ConnectMs: 20, TTFBMs: 60},
			"www.google.com": {Count: 1, ConnectMs: 20, TTFBMs: 60},
		},
		Router:      aggregate.RouterStats{Present: true, RxBps: 3e6, TxBps: 5e5, LinkDownBps: 50e6, LinkUpBps: 10e6},
		LaptopRxBps: 1e6,
		PowerKnown:  true,
	}
	return b
}

func newSession(n int, mutate func(i int, b *aggregate.Bucket)) *aggregate.Session {
	d := time.Duration(n) * width
	s := &aggregate.Session{
		Meta:  store.Meta{Start: t0, Duration: d, Finished: true, FinishedAt: t0.Add(d)},
		Width: width,
	}
	for i := range n {
		b := healthyBucket(t0.Add(time.Duration(i) * width))
		if mutate != nil {
			mutate(i, &b)
		}
		s.Buckets = append(s.Buckets, b)
	}
	return s
}

func lossy(p *aggregate.PathStats, loss float64) {
	p.LossPct = loss
	p.Recv = p.Sent - int(loss/10+0.5)
}

func eachInternet(b *aggregate.Bucket, fn func(label string, p *aggregate.PathStats)) {
	for label, p := range b.Paths {
		if record.IsInetTarget(label) || record.IsInet6Target(label) {
			fn(label, p)
		}
	}
}

func signals(ev []Evidence) []string {
	out := make([]string, 0, len(ev))
	for _, e := range ev {
		out = append(out, e.Signal)
	}
	return out
}

func assertSignals(t *testing.T, got []Evidence, want ...string) {
	t.Helper()
	have := signals(got)
	for _, w := range want {
		if !slices.Contains(have, w) {
			t.Fatalf("signals %v missing %q", have, w)
		}
	}
}
