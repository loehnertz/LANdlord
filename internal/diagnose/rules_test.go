package diagnose

import (
	"testing"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/record"
)

func TestClassify(t *testing.T) {
	internetLoss := func(b *aggregate.Bucket, loss float64) {
		eachInternet(b, func(_ string, p *aggregate.PathStats) { lossy(p, loss) })
	}
	tests := []struct {
		name    string
		setup   func(s *aggregate.Session)
		mutate  func(b *aggregate.Bucket)
		want    Culprit
		signals []string
	}{
		{
			name: "weak signal",
			mutate: func(b *aggregate.Bucket) {
				lossy(b.Paths[record.TGateway], 8)
				internetLoss(b, 8)
				b.Wifi.RSSIMin = -78
			},
			want: WifiSignal, signals: []string{"gateway", "rssi"},
		},
		{
			name: "roam event with healthy gateway",
			mutate: func(b *aggregate.Bucket) {
				b.Events = append(b.Events, aggregate.Event{Collector: record.CWifi, Name: record.NRoam})
			},
			want: WifiSignal, signals: []string{"wifi_event"},
		},
		{
			name: "interference",
			mutate: func(b *aggregate.Bucket) {
				lossy(b.Paths[record.TGateway], 8)
				internetLoss(b, 8)
				b.Wifi.RetryPct = 25
				b.Wifi.OverlapAPs = 6
			},
			want: WifiInterference, signals: []string{"gateway", "retries", "crowded"},
		},
		{
			name: "client device",
			mutate: func(b *aggregate.Bucket) {
				b.Paths[record.TGateway].P95 = 200
				eachInternet(b, func(_ string, p *aggregate.PathStats) { p.P95 = 220 })
			},
			want: ClientDevice, signals: []string{"gateway", "client"},
		},
		{
			name: "wired computer",
			mutate: func(b *aggregate.Bucket) {
				b.Wifi = aggregate.WifiStats{}
				b.Paths[record.TGateway].P95 = 200
			},
			want: ClientDevice, signals: []string{"gateway"},
		},
		{
			name: "shared line",
			mutate: func(b *aggregate.Bucket) {
				b.Router.RxBps = 45e6
				eachInternet(b, func(_ string, p *aggregate.PathStats) { p.P95 = 250 })
			},
			want: LANSaturation, signals: []string{"router_util", "laptop_share"},
		},
		{
			name: "busy line caused by this laptop is not shared usage",
			mutate: func(b *aggregate.Bucket) {
				b.Router.RxBps = 45e6
				b.LaptopRxBps = 20e6
				eachInternet(b, func(_ string, p *aggregate.PathStats) { p.P95 = 250 })
				for _, h := range []string{"hop1", "hop2", "hop3"} {
					b.Paths[h].P95 = 240
				}
			},
			want: AccessLine,
		},
		{
			name: "access line",
			mutate: func(b *aggregate.Bucket) {
				for _, h := range []string{"hop1", "hop2", "hop3"} {
					lossy(b.Paths[h], 6)
				}
				internetLoss(b, 6)
				b.DSL = aggregate.DSLStats{Present: true, SNRDownDB: 9, CRCDelta: 100}
			},
			want: AccessLine, signals: []string{"internet", "hop1", "crc"},
		},
		{
			name: "cable line",
			mutate: func(b *aggregate.Bucket) {
				for _, h := range []string{"hop1", "hop2", "hop3"} {
					lossy(b.Paths[h], 6)
				}
				internetLoss(b, 6)
				b.DSL = aggregate.DSLStats{Present: true, Cable: true, CableUncorrectableDelta: 40, CableUSPowerMax: 53, CableDSMERMin: 27, CableDSPowerMin: 1, CableDSPowerMax: 5}
			},
			want: AccessLine, signals: []string{"internet", "hop1", "cable_errors", "cable_upstream", "cable_mer"},
		},
		{
			name: "degradation beyond first hop",
			mutate: func(b *aggregate.Bucket) {
				lossy(b.Paths["hop2"], 6)
				lossy(b.Paths["hop3"], 6)
				internetLoss(b, 6)
			},
			want: AccessLine, signals: []string{"internet", "later_hop"},
		},
		{
			name: "no responding hops",
			setup: func(s *aggregate.Session) {
				for i := range s.Buckets {
					for _, h := range []string{"hop1", "hop2", "hop3"} {
						delete(s.Buckets[i].Paths, h)
					}
				}
			},
			mutate: func(b *aggregate.Bucket) { internetLoss(b, 6) },
			want:   AccessLine, signals: []string{"internet", "no_hops"},
		},
		{
			name: "ipv4 tunnel",
			setup: func(s *aggregate.Session) {
				s.Infos = append(s.Infos, record.Info(record.CUPnP, record.NTunnel, "", t0, map[string]string{"kind": "dslite"}))
			},
			mutate: func(b *aggregate.Bucket) {
				eachInternet(b, func(label string, p *aggregate.PathStats) {
					if record.IsInetTarget(label) {
						lossy(p, 8)
					}
				})
			},
			want: IPv4Tunnel, signals: []string{"ipv4_only", "tunnel"},
		},
		{
			name: "ipv4 loss without tunnel info is the line",
			mutate: func(b *aggregate.Bucket) {
				eachInternet(b, func(label string, p *aggregate.PathStats) {
					if record.IsInetTarget(label) {
						lossy(p, 8)
					}
				})
			},
			want: AccessLine,
		},
		{
			name:    "dns",
			mutate:  func(b *aggregate.Bucket) { b.DNS["configured:192.168.178.1"].Failed = 2 },
			want:    DNS,
			signals: []string{"dns_local", "dns_public_ok"},
		},
		{
			name: "all resolvers failing is not a local dns problem",
			mutate: func(b *aggregate.Bucket) {
				for _, r := range b.DNS {
					r.Failed = 3
				}
			},
			want: Unknown,
		},
		{
			name:    "single remote service",
			mutate:  func(b *aggregate.Bucket) { b.HTTP["zoom.us"].Failed = 1 },
			want:    RemoteService,
			signals: []string{"service"},
		},
		{
			name:   "unexplained udp loss",
			mutate: func(b *aggregate.Bucket) { b.STUN.Recv = 475; b.STUN.LossPct = 5 },
			want:   Unknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSession(10, nil)
			if tt.setup != nil {
				tt.setup(s)
			}
			tt.mutate(&s.Buckets[0])
			c := newCtx(s, th())
			got, ev := c.classify(&s.Buckets[0])
			if got != tt.want {
				t.Fatalf("culprit = %s, want %s (evidence %v)", got, tt.want, signals(ev))
			}
			assertSignals(t, ev, tt.signals...)
		})
	}
}
