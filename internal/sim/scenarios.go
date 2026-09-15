package sim

import (
	"time"

	"github.com/loehnertz/LANdlord/internal/record"
)

// Episodes use wall-clock minutes (for example minutes 0-2 and 30-32 of every hour), so they
// line up with the local-time hour boundaries the diagnosis reports.
func init() {
	register(Scenario{
		Name: "healthy", Description: "Everything fine for 6 hours", Duration: 6 * time.Hour,
		Cond: func(time.Time, *time.Location) Condition { return Condition{} },
	})

	register(Scenario{
		Name: "weak_wifi", Description: "Every 30 minutes the signal drops to -79 dBm for 3 minutes", Duration: 6 * time.Hour,
		Cond: func(t time.Time, _ *time.Location) Condition {
			if t.Minute()%30 >= 3 {
				return Condition{}
			}
			return Condition{RSSI: -79, RxMbps: 20, RetryPct: 25, GatewayLossPct: 6, GatewayRTTms: 45, InetLossPct: 15, InetRTTms: 170, Inet6LossPct: 15, JitterMs: 40, Roam: true}
		},
	})

	register(Scenario{
		Name: "access_line", Description: "Every 30 minutes the line degrades for 3 minutes, with CRC errors", Duration: 6 * time.Hour,
		Cond: func(t time.Time, _ *time.Location) Condition {
			if t.Minute()%30 >= 3 {
				return Condition{}
			}
			return Condition{HopLossPct: 15, HopRTTms: 160, InetLossPct: 15, Inet6LossPct: 15, InetRTTms: 170, JitterMs: 35, FritzCRC: 150}
		},
	})

	register(Scenario{
		Name: "cable_line", Description: "A cable connection where the modem reports uncorrectable errors and high upstream power during outages", Duration: 6 * time.Hour,
		Cond: func(t time.Time, _ *time.Location) Condition {
			if t.Minute()%30 >= 3 {
				return Condition{Cable: true}
			}
			return Condition{Cable: true, HopLossPct: 15, HopRTTms: 160, InetLossPct: 15, Inet6LossPct: 15, InetRTTms: 170, JitterMs: 35,
				CableUncorrectable: 250, CableUSPower: 53, CableDSMER: 28}
		},
	})

	register(Scenario{
		Name: "evening_congestion", Description: "Latency beyond the router rises every evening from 19:00 to 23:00", Duration: 48 * time.Hour,
		Cond: func(t time.Time, loc *time.Location) Condition {
			if h := t.In(loc).Hour(); h < 19 || h >= 23 {
				return Condition{}
			}
			return Condition{HopRTTms: 150, InetRTTms: 190, JitterMs: 45, InetLossPct: 2, Inet6LossPct: 2, HopLossPct: 2}
		},
	})

	register(Scenario{
		Name: "hop_ratelimit", Description: "The second provider hop drops 40 % of pings but traffic is fine", Duration: 6 * time.Hour,
		Cond: func(time.Time, *time.Location) Condition { return Condition{Hop2ExtraLossPct: 40} },
	})

	register(Scenario{
		Name: "periodic_scan", Description: "Latency spikes to the router every 60 s with a strong signal", Duration: 6 * time.Hour,
		Cond: func(t time.Time, _ *time.Location) Condition {
			if t.Second() >= 2 {
				return Condition{}
			}
			return Condition{GatewayRTTms: 180, InetRTTms: 220, JitterMs: 60}
		},
	})

	register(Scenario{
		Name: "dns_trouble", Description: "Every 30 minutes the router's DNS fails for 2 minutes", Duration: 6 * time.Hour,
		Cond: func(t time.Time, _ *time.Location) Condition {
			return Condition{ConfiguredDNSFail: t.Minute()%30 < 2}
		},
	})

	register(Scenario{
		Name: "ipv4_tunnel", Description: "DS-Lite line where only IPv4 degrades", Duration: 6 * time.Hour,
		Cond: func(t time.Time, _ *time.Location) Condition {
			if t.Minute()%30 >= 3 {
				return Condition{}
			}
			return Condition{InetLossPct: 25, InetRTTms: 170, JitterMs: 35, IPv4Only: true}
		},
		Extra: func(g *Gen) {
			g.Sink.Emit(record.Info(record.CUPnP, record.NTunnel, "", g.Start, map[string]string{"kind": "dslite", "evidence": "router has no public IPv4 address"}))
		},
	})

	register(Scenario{
		Name: "lan_saturation", Description: "Another device fills the line every 30 minutes for 5 minutes", Duration: 6 * time.Hour,
		Cond: func(t time.Time, _ *time.Location) Condition {
			if t.Minute()%30 >= 5 {
				return Condition{}
			}
			return Condition{RouterRxBps: 48e6, LaptopRxBps: 0.5e6, GatewayRTTms: 6, InetRTTms: 240, JitterMs: 50, InetLossPct: 1.5, Inet6LossPct: 1.5}
		},
	})
}
