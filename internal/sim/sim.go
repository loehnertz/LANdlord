// Package sim generates synthetic record streams for tests and report development.
package sim

import (
	"encoding/json"
	"math/rand"
	"net/netip"
	"sort"
	"time"

	"github.com/loehnertz/LANdlord/internal/record"
)

// Condition describes the network at one instant. Zero values mean "healthy default".
type Condition struct {
	GatewayLossPct, GatewayRTTms float64
	HopLossPct, HopRTTms         float64
	Hop2ExtraLossPct             float64
	InetLossPct, InetRTTms       float64
	Inet6LossPct                 float64
	JitterMs                     float64
	RSSI, RxMbps, RetryPct       float64
	RouterRxBps, LaptopRxBps     float64
	ConfiguredDNSFail, Roam      bool
	IPv4Only                     bool // internet degradation applies to IPv4 targets only
	FritzCRC                     float64
	// Cable switches the router statistics from DSL to DOCSIS.
	Cable                    bool
	CableUncorrectable       float64
	CableUSPower, CableDSMER float64
}

type Scenario struct {
	Name, Description string
	Duration          time.Duration
	Cond              func(t time.Time, loc *time.Location) Condition
	Extra             func(g *Gen)
}

type Gen struct {
	Start, End time.Time
	Loc        *time.Location
	R          *rand.Rand
	Sink       record.Sink
}

func (g *Gen) every(step time.Duration, fn func(t time.Time)) {
	for t := g.Start; t.Before(g.End); t = t.Add(step) {
		fn(t)
	}
}

var registry = map[string]Scenario{}

func register(sc Scenario) { registry[sc.Name] = sc }

func Names() []string {
	var out []string
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func Get(name string) (Scenario, bool) {
	sc, ok := registry[name]
	return sc, ok
}

var (
	gateway  = netip.MustParseAddr("192.168.178.1")
	hops     = []netip.Addr{netip.MustParseAddr("62.155.244.1"), netip.MustParseAddr("217.0.118.5"), netip.MustParseAddr("80.156.160.1")}
	inet4    = []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("9.9.9.9")}
	inet6    = []netip.Addr{netip.MustParseAddr("2606:4700:4700::1111"), netip.MustParseAddr("2001:4860:4860::8888")}
	httpHost = []string{"zoom.us", "teams.microsoft.com", "meet.google.com", "www.youtube.com", "www.netflix.com", "www.google.com"}
)

const pingStep = 2 * time.Second

// Run emits the scenario's records from start for its duration.
func Run(sc Scenario, start time.Time, loc *time.Location, seed int64, sink record.Sink) {
	g := &Gen{Start: start, End: start.Add(sc.Duration), Loc: loc, R: rand.New(rand.NewSource(seed)), Sink: sink}
	g.infos()
	cond := func(t time.Time) Condition { return withDefaults(sc.Cond(t, loc)) }
	g.every(pingStep, func(t time.Time) {
		c := cond(t)
		g.echo(record.TGateway, gateway, t, c.GatewayRTTms, c.GatewayLossPct, c.JitterMs/4)
		for i, h := range hops {
			loss := c.HopLossPct
			if i == 1 {
				loss += c.Hop2ExtraLossPct
			}
			g.echo(record.HopTarget(i+1), h, t, c.HopRTTms+float64(i)*2, loss, c.JitterMs/2)
		}
		for _, a := range inet4 {
			g.echo(record.InetTarget(a), a, t, c.InetRTTms, c.InetLossPct, c.JitterMs)
		}
		for _, a := range inet6 {
			if c.IPv4Only {
				g.echo(record.InetTarget(a), a, t, 22, 0, 2)
				continue
			}
			g.echo(record.InetTarget(a), a, t, c.InetRTTms, c.Inet6LossPct, c.JitterMs)
		}
		g.Sink.Emit(record.Record{Time: t, Collector: record.CWifi, Kind: record.KindMetric, Name: record.NLink,
			Values: map[string]float64{"connected": 1, "rssi_dbm": c.RSSI + g.noise(2), "quality_pct": 2 * (c.RSSI + 100),
				"rx_mbps": c.RxMbps, "tx_mbps": c.RxMbps * 0.8, "channel": 36, "freq_mhz": 5180, "retry_pct": c.RetryPct},
			Attrs: map[string]string{"ssid": "FRITZ!Box 7590 XY", "bssid": "3c:a6:2f:00:00:01", "band": "5", "phy": "ax"}})
		g.Sink.Emit(record.Metric(record.CSystem, record.NIface, "Wi-Fi", t, map[string]float64{"rx_bps": c.LaptopRxBps, "tx_bps": c.LaptopRxBps / 5}))
		if c.Roam && t.Sub(g.Start)%time.Minute == 0 {
			g.Sink.Emit(record.Record{Time: t, Collector: record.CWifi, Kind: record.KindEvent, Name: record.NRoam, Values: map[string]float64{"reason": 0}})
		}
	})
	g.every(30*time.Second, func(t time.Time) {
		c := cond(t)
		g.Sink.Emit(record.Metric(record.CSystem, record.NPower, "", t, map[string]float64{"on_ac": 1}))
		g.Sink.Emit(record.Metric(record.CUPnP, record.NWAN, "", t, map[string]float64{
			"rx_bps": c.RouterRxBps, "tx_bps": c.RouterRxBps / 8, "link_down_bps": 50e6, "link_up_bps": 10e6, "uptime_s": t.Sub(g.Start).Seconds() + 86400}))
		for _, target := range []string{record.TSystem, "configured:192.168.178.1", "1.1.1.1"} {
			if c.ConfiguredDNSFail && target != "1.1.1.1" {
				g.Sink.Emit(record.Record{Time: t, Collector: record.CDNS, Kind: record.KindMetric, Name: record.NLookup, Target: target,
					Values: map[string]float64{"failed": 1}, Attrs: map[string]string{"host": "www.google.com", "error": "i/o timeout"}})
				continue
			}
			g.Sink.Emit(record.Metric(record.CDNS, record.NLookup, target, t, map[string]float64{"ms": 20 + g.noise(5)}))
		}
	})
	g.every(time.Minute, func(t time.Time) {
		c := cond(t)
		sent := 500.0
		received := sent * (1 - c.InetLossPct/100)
		g.Sink.Emit(record.Metric(record.CSTUN, record.NBurst, "stun.cloudflare.com:3478", t, map[string]float64{
			"sent": sent, "received": received, "loss_pct": c.InetLossPct, "jitter_ms": c.JitterMs, "rtt_p50_ms": c.InetRTTms}))
		for _, h := range httpHost {
			g.Sink.Emit(record.Metric(record.CHTTP, record.NFetch, h, t, map[string]float64{"connect_ms": c.InetRTTms, "tls_ms": c.InetRTTms * 1.5, "ttfb_ms": c.InetRTTms * 3}))
		}
		if c.Cable {
			us, mer := c.CableUSPower, c.CableDSMER
			if us == 0 {
				us = 44
			}
			if mer == 0 {
				mer = 38
			}
			g.Sink.Emit(record.Metric(record.CFritz, record.NDOCSIS, "", t, map[string]float64{
				"ds_channels": 32, "us_channels": 5, "ds_power_min_dbmv": 2.5, "ds_power_max_dbmv": 7.1,
				"us_power_max_dbmv": us, "ds_mer_min_db": mer, "corr_errors_delta": 12, "noncorr_errors_delta": c.CableUncorrectable}))
		} else {
			g.Sink.Emit(record.Metric(record.CFritz, record.NDSL, "", t, map[string]float64{
				"snr_down_db": 9, "snr_up_db": 8, "sync_down_kbps": 100000, "sync_up_kbps": 40000, "crc_delta": c.FritzCRC}))
		}
	})
	g.every(10*time.Minute, func(t time.Time) {
		nets, _ := json.Marshal([]map[string]any{{"ssid": "neighbour", "rssi": -72, "channel": 40}})
		g.Sink.Emit(record.Record{Time: t, Collector: record.CWifi, Kind: record.KindMetric, Name: record.NScan,
			Values: map[string]float64{"total_aps": 9, "overlap_aps": 2, "width_mhz": 80}, Attrs: map[string]string{"networks": string(nets)}})
	})
	g.every(time.Hour, func(t time.Time) {
		c := cond(t)
		down := 50 * (1 - c.InetLossPct/20)
		g.Sink.Emit(record.Record{Time: t.Add(5 * time.Minute), Collector: record.CSpeed, Kind: record.KindMetric, Name: record.NTest,
			Values: map[string]float64{"down_mbps": max(down, 1), "up_mbps": 9, "bloat_ms": 12}, Attrs: map[string]string{"grade": "B", "trigger": "interval"}})
	})
	if sc.Extra != nil {
		sc.Extra(g)
	}
}

func withDefaults(c Condition) Condition {
	if c.GatewayRTTms == 0 {
		c.GatewayRTTms = 3
	}
	if c.HopRTTms == 0 {
		c.HopRTTms = 12
	}
	if c.InetRTTms == 0 {
		c.InetRTTms = 22
	}
	if c.JitterMs == 0 {
		c.JitterMs = 2
	}
	if c.RSSI == 0 {
		c.RSSI = -55
	}
	if c.RxMbps == 0 {
		c.RxMbps = 400
	}
	if c.RetryPct == 0 {
		c.RetryPct = 2
	}
	if c.RouterRxBps == 0 {
		c.RouterRxBps = 3e6
	}
	if c.LaptopRxBps == 0 {
		c.LaptopRxBps = 1.5e6
	}
	return c
}

func (g *Gen) noise(amplitude float64) float64 { return (g.R.Float64()*2 - 1) * amplitude }

func (g *Gen) echo(target string, ip netip.Addr, t time.Time, rtt, lossPct, jitter float64) {
	attrs := map[string]string{"ip": ip.String()}
	if g.R.Float64()*100 < lossPct {
		g.Sink.Emit(record.Record{Time: t, Collector: record.CPing, Kind: record.KindMetric, Name: record.NEcho, Target: target,
			Values: map[string]float64{"lost": 1}, Attrs: attrs})
		return
	}
	g.Sink.Emit(record.Record{Time: t, Collector: record.CPing, Kind: record.KindMetric, Name: record.NEcho, Target: target,
		Values: map[string]float64{"rtt_ms": max(0.3, rtt+g.noise(jitter))}, Attrs: attrs})
}

func (g *Gen) infos() {
	t := g.Start
	g.Sink.Emit(record.Info(record.CRouter, record.NIdentity, gateway.String(), t, map[string]string{"vendor": "AVM", "model": "FRITZ!Box 7590", "method": "tr064", "mac": "3c:a6:2f:00:00:01"}))
	g.Sink.Emit(record.Info(record.CPublic, record.NPublicIP, "", t, map[string]string{"ip": "203.0.113.7", "loc": "DE"}))
	g.Sink.Emit(record.Info(record.CSystem, record.NAdapter, "", t, map[string]string{"name": "Wi-Fi", "description": "Intel(R) Wi-Fi 6E AX211 160MHz", "driver_version": "23.60.0.10", "wireless": "true"}))
	g.Sink.Emit(record.Info(record.CTrace, record.NRoute, "1.1.1.1", t, map[string]string{"path": "192.168.178.1,62.155.244.1,217.0.118.5,80.156.160.1,1.1.1.1"}))
	g.Sink.Emit(record.Unavailable(record.CEventLog, t, "simulated session"))
}
