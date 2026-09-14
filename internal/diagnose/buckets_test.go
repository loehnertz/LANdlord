package diagnose

import (
	"testing"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/record"
)

func TestSymptoms(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(b *aggregate.Bucket)
		want   []string
	}{
		{"healthy", func(*aggregate.Bucket) {}, nil},
		{"internet loss", func(b *aggregate.Bucket) {
			eachInternet(b, func(_ string, p *aggregate.PathStats) { lossy(p, 8) })
		}, []string{"internet_loss"}},
		{"single lossy target ignored", func(b *aggregate.Bucket) { lossy(b.Paths["inet:9.9.9.9"], 40) }, nil},
		{"stun jitter", func(b *aggregate.Bucket) { b.STUN.Jitter = 45 }, []string{"jitter"}},
		{"stun loss", func(b *aggregate.Bucket) { b.STUN.Recv = 470; b.STUN.LossPct = 6 }, []string{"udp_loss"}},
		{"dns failure", func(b *aggregate.Bucket) { b.DNS["configured:192.168.178.1"].Failed = 1 }, []string{"dns_failure"}},
		{"http failure", func(b *aggregate.Bucket) { b.HTTP["zoom.us"].Failed = 1 }, []string{"http_failure"}},
		{"wan reconnect", func(b *aggregate.Bucket) {
			b.Events = append(b.Events, aggregate.Event{Collector: record.CUPnP, Name: record.NWANReconnect})
		}, []string{"wan_reconnect"}},
		{"wifi disconnect", func(b *aggregate.Bucket) {
			b.Events = append(b.Events, aggregate.Event{Collector: record.CWifi, Name: record.NDisconnect})
		}, []string{"wifi_disconnect"}},
		{"hop rate limiting is not a symptom", func(b *aggregate.Bucket) { lossy(b.Paths["hop2"], 40) }, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSession(1, func(_ int, b *aggregate.Bucket) { tt.mutate(b) })
			got := newCtx(s, th()).symptoms(&s.Buckets[0])
			if len(tt.want) == 0 && len(got) != 0 {
				t.Fatalf("expected no symptoms, got %v", signals(got))
			}
			if len(tt.want) > 0 {
				assertSignals(t, got, tt.want...)
			}
		})
	}
}

func TestGatewayDegradedUsesSessionMedian(t *testing.T) {
	tests := []struct {
		name     string
		baseP50  float64
		p95      float64
		degraded bool
	}{
		{"fine below floor", 3, 25, false},
		{"above floor", 3, 40, true},
		{"slow baseline raises the limit", 10, 40, false},
		{"far above slow baseline", 10, 60, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSession(20, func(i int, b *aggregate.Bucket) {
				b.Paths[record.TGateway].P50 = tt.baseP50
				b.Paths[record.TGateway].P95 = tt.baseP50 + 1
				if i == 7 {
					b.Paths[record.TGateway].P95 = tt.p95
				}
			})
			c := newCtx(s, th())
			if got := c.gatewayDegraded(&s.Buckets[7]); got != tt.degraded {
				t.Fatalf("gatewayDegraded = %v, want %v (median %.1f)", got, tt.degraded, c.gwMedian)
			}
		})
	}
}

func TestContextBaselines(t *testing.T) {
	s := newSession(10, func(i int, b *aggregate.Bucket) {
		delete(b.Paths, "hop2")
		b.Paths["hop3"].Recv = 0
		if i == 5 {
			b.Asleep = true
			b.Paths[record.TGateway].P50 = 500
		}
	})
	s.Infos = append(s.Infos, record.Info(record.CUPnP, record.NTunnel, "", t0, map[string]string{"kind": "dslite"}))
	c := newCtx(s, th())
	if c.gwMedian != 3 || c.bestRx != 400 || !c.hasIPv6 || !c.tunnel {
		t.Fatalf("baselines wrong: %+v", c)
	}
	if len(c.hops) != 1 || c.hops[0] != "hop1" {
		t.Fatalf("responding hops = %v, want [hop1]", c.hops)
	}
}
