package diagnose

import (
	"fmt"
	"strings"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/record"
)

// classify assigns a bad bucket to the lowest layer whose rule matches.
func (c *sessionCtx) classify(b *aggregate.Bucket) (Culprit, []Evidence) {
	th := c.th
	var ev []Evidence
	gw := gatewayPath(b)
	gwBad := c.gatewayDegraded(b)
	wifiEvent := hasEvent(b, record.CWifi, record.NDisconnect) || hasEvent(b, record.CWifi, record.NRoam)

	if gwBad || wifiEvent {
		if gwBad {
			ev = append(ev, Evidence{"gateway", fmt.Sprintf("Router ping: %.0f%% loss, 95th percentile %.0f ms", gw.LossPct, gw.P95)})
		}
		if wifiEvent {
			ev = append(ev, Evidence{"wifi_event", "Wi-Fi disconnected or switched to another access point"})
		}
		w := b.Wifi
		if w.Connected {
			weakRSSI := w.RSSIMin < th.RSSIWeakDBm
			weakRx := c.bestRx > 0 && w.RxMbps < th.RxRatioWeak*c.bestRx
			if weakRSSI {
				ev = append(ev, Evidence{"rssi", fmt.Sprintf("Wi-Fi signal dropped to %.0f dBm", w.RSSIMin)})
			}
			if weakRx {
				ev = append(ev, Evidence{"rx_rate", fmt.Sprintf("Wi-Fi link speed fell to %.0f Mbit/s (usually %.0f)", w.RxMbps, c.bestRx)})
			}
			if weakRSSI || weakRx || wifiEvent {
				return WifiSignal, ev
			}
			interference := false
			if w.RetryPct > th.RetryPct {
				ev = append(ev, Evidence{"retries", fmt.Sprintf("%.0f%% of Wi-Fi frames had to be resent", w.RetryPct)})
				interference = true
			}
			if w.ScanKnown && w.OverlapAPs >= th.OverlapAPs {
				ev = append(ev, Evidence{"crowded", fmt.Sprintf("%d neighbouring networks share or overlap the channel", w.OverlapAPs)})
				interference = true
			}
			if w.Band == "2.4" {
				ev = append(ev, Evidence{"band_24", "Connected on the crowded 2.4 GHz band"})
				interference = true
			}
			if interference {
				return WifiInterference, ev
			}
		} else if wifiEvent {
			return WifiSignal, ev
		}
		if ok, lanEv := c.lanSaturated(b); ok {
			return LANSaturation, append(ev, lanEv...)
		}
		return ClientDevice, append(ev, Evidence{"client", "Signal and channel look fine, so the delay comes from this computer or the router itself"})
	}

	if ok, lanEv := c.lanSaturated(b); ok {
		return LANSaturation, lanEv
	}

	if c.tunnel && c.hasIPv6 {
		v4, v6 := c.internet(b, 4), c.internet(b, 6)
		if v4.Targets > 0 && v4.Degraded >= min(2, v4.Targets) && v6.Targets > 0 && v6.Degraded == 0 {
			return IPv4Tunnel, []Evidence{
				{"ipv4_only", "IPv4 connections were degraded while IPv6 to the same services was fine"},
				{"tunnel", "The connection shares its IPv4 address through DS-Lite or CGNAT"},
			}
		}
	}

	if all := c.internet(b, 0); all.Targets > 0 && all.Degraded >= min(2, all.Targets) {
		ev = append(ev, Evidence{"internet", fmt.Sprintf("%d of %d internet targets were degraded while the router answered normally", all.Degraded, all.Targets)})
		switch first, bad := c.firstHop(b); {
		case first == "":
			ev = append(ev, Evidence{"no_hops", "The provider's routers don't answer pings, so the line can't be separated from the wider internet"})
		case bad:
			ev = append(ev, Evidence{"hop1", "Degradation already starts at the first router of the provider"})
		default:
			ev = append(ev, Evidence{"later_hop", "The provider's first router was fine; degradation starts further into its network"})
		}
		ev = append(ev, c.lineEvidence(b)...)
		return AccessLine, ev
	}

	if ok, dnsEv := c.dnsProblem(b); ok {
		return DNS, dnsEv
	}
	if host := failedService(b); host != "" {
		return RemoteService, []Evidence{{"service", "Only " + host + " was affected"}}
	}
	if hasEvent(b, record.CUPnP, record.NWANReconnect) {
		return AccessLine, c.lineEvidence(b)
	}
	return Unknown, nil
}

func (c *sessionCtx) lineEvidence(b *aggregate.Bucket) []Evidence {
	var ev []Evidence
	if hasEvent(b, record.CUPnP, record.NWANReconnect) {
		ev = append(ev, Evidence{"wan_reconnect", "The router lost its internet connection and reconnected"})
	}
	if b.DSL.Present && b.DSL.CRCDelta > 0 {
		ev = append(ev, Evidence{"crc", fmt.Sprintf("The router counted %.0f transmission errors on the line", b.DSL.CRCDelta)})
	}
	if b.DSL.Resync {
		ev = append(ev, Evidence{"resync", "The DSL line re-synchronised"})
	}
	if b.DSL.Present && !b.DSL.Cable && b.DSL.SNRDownDB > 0 && b.DSL.SNRDownDB < 6 {
		ev = append(ev, Evidence{"snr", fmt.Sprintf("The line's noise margin is low (%.1f dB)", b.DSL.SNRDownDB)})
	}
	if d := b.DSL; d.Cable {
		th := c.th
		if d.CableUncorrectableDelta > 0 {
			ev = append(ev, Evidence{"cable_errors", fmt.Sprintf("The cable modem counted %.0f uncorrectable errors", d.CableUncorrectableDelta)})
		}
		if d.CableUSPowerMax > th.CableUSPowerMaxDBmV {
			ev = append(ev, Evidence{"cable_upstream", fmt.Sprintf("The cable modem had to transmit at %.1f dBmV, above the usual limit of %.0f dBmV", d.CableUSPowerMax, th.CableUSPowerMaxDBmV)})
		}
		if d.CableDSMERMin > 0 && d.CableDSMERMin < th.CableMERMinDB {
			ev = append(ev, Evidence{"cable_mer", fmt.Sprintf("The weakest cable channel's signal quality (MER) dropped to %.1f dB", d.CableDSMERMin)})
		}
	}
	return ev
}

// firstHop returns the first provider hop that answers during the session, and whether it was degraded here.
func (c *sessionCtx) firstHop(b *aggregate.Bucket) (string, bool) {
	for _, label := range c.hops {
		p := b.Paths[label]
		if p == nil || p.Sent == 0 {
			continue
		}
		return label, c.degraded(p)
	}
	return "", false
}

func (c *sessionCtx) lanSaturated(b *aggregate.Bucket) (bool, []Evidence) {
	r := b.Router
	if !r.Present || r.LinkDownBps <= 0 {
		return false, nil
	}
	util := r.RxBps / r.LinkDownBps
	if r.LinkUpBps > 0 {
		util = max(util, r.TxBps/r.LinkUpBps)
	}
	routerTotal := r.RxBps + r.TxBps
	laptop := b.LaptopRxBps + b.LaptopTxBps
	if util <= c.th.RouterUtil || routerTotal <= 0 || laptop/routerTotal >= c.th.LaptopShare {
		return false, nil
	}
	return true, []Evidence{
		{"router_util", fmt.Sprintf("The router's internet line was %.0f%% full", util*100)},
		{"laptop_share", fmt.Sprintf("This computer caused only %.0f%% of that traffic", laptop/routerTotal*100)},
	}
}

func isLocalResolver(label string) bool {
	return label == record.TSystem || strings.HasPrefix(label, "configured:")
}

func (c *sessionCtx) dnsProblem(b *aggregate.Bucket) (bool, []Evidence) {
	var badLocal []string
	publicSeen, publicOK := false, false
	for _, label := range sortedKeys(b.DNS) {
		r := b.DNS[label]
		if r.Count == 0 {
			continue
		}
		bad := r.Failed > 0 || r.MeanMs > c.th.DNSSlowMs
		if isLocalResolver(label) {
			if bad {
				badLocal = append(badLocal, label)
			}
			continue
		}
		publicSeen = true
		if !bad {
			publicOK = true
		}
	}
	if len(badLocal) == 0 || (publicSeen && !publicOK) {
		return false, nil
	}
	ev := []Evidence{{"dns_local", fmt.Sprintf("Name lookups via %s failed or took over %.0f ms", strings.Join(badLocal, ", "), c.th.DNSSlowMs)}}
	if publicOK {
		ev = append(ev, Evidence{"dns_public_ok", "A public DNS server answered normally at the same time"})
	}
	return true, ev
}

// failedService returns the only HTTP host that failed while others worked, or "".
func failedService(b *aggregate.Bucket) string {
	var failed []string
	ok := 0
	for _, host := range sortedKeys(b.HTTP) {
		h := b.HTTP[host]
		if h.Count == 0 {
			continue
		}
		if h.Failed > 0 {
			failed = append(failed, host)
		} else {
			ok++
		}
	}
	if len(failed) == 1 && ok > 0 {
		return failed[0]
	}
	return ""
}
