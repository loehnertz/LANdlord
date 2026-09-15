package diagnose

import (
	"fmt"
	"strings"
	"time"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/record"
)

// BloatGrade grades the latency increase under load, in milliseconds.
func BloatGrade(ms float64) string {
	switch {
	case ms < 5:
		return "A"
	case ms < 30:
		return "B"
	case ms < 60:
		return "C"
	case ms < 200:
		return "D"
	case ms < 400:
		return "E"
	default:
		return "F"
	}
}

var collectorTitles = map[string]string{
	record.CPing:     "ping",
	record.CTrace:    "traceroute",
	record.CWifi:     "Wi-Fi details",
	record.CSystem:   "network adapter counters",
	record.CDNS:      "DNS",
	record.CHTTP:     "web connections",
	record.CSTUN:     "call-like UDP test",
	record.CSpeed:    "speed test",
	record.CPublic:   "public IP address",
	record.CMTU:      "packet size test",
	record.CRouter:   "router identification",
	record.CUPnP:     "router statistics (UPnP)",
	record.CFritz:    "FRITZ!Box line statistics",
	record.CEventLog: "Windows event log history",
	record.CAdmin:    "admin-only extras",
}

// CollectorTitle is a readable name for a collector.
func CollectorTitle(collector string) string {
	if t, ok := collectorTitles[collector]; ok {
		return t
	}
	return collector
}

func findings(s *aggregate.Session, c *sessionCtx, r *Result) []Finding {
	th := c.th
	var out []Finding
	add := func(id, title, detail string) { out = append(out, Finding{ID: id, Title: title, Detail: detail}) }

	var usPower, dsPowerMin, dsPowerMax, mer []float64
	var rssi, local, public []float64
	var connected, band24, batteryAwake, batteryBad, acAwake, acBad int
	for i := range s.Buckets {
		b := &s.Buckets[i]
		if b.Asleep {
			continue
		}
		if d := b.DSL; d.Cable {
			usPower = append(usPower, d.CableUSPowerMax)
			dsPowerMin = append(dsPowerMin, d.CableDSPowerMin)
			dsPowerMax = append(dsPowerMax, d.CableDSPowerMax)
			if d.CableDSMERMin > 0 {
				mer = append(mer, d.CableDSMERMin)
			}
		}
		if b.Wifi.Connected {
			connected++
			rssi = append(rssi, b.Wifi.RSSIMean)
			if b.Wifi.Band == "2.4" {
				band24++
			}
		}
		if b.PowerKnown {
			degraded := c.gatewayDegraded(b)
			if b.OnBattery {
				batteryAwake++
				if degraded {
					batteryBad++
				}
			} else {
				acAwake++
				if degraded {
					acBad++
				}
			}
		}
		for label, rs := range b.DNS {
			if rs.Count > rs.Failed && rs.MeanMs > 0 {
				if isLocalResolver(label) {
					local = append(local, rs.MeanMs)
				} else {
					public = append(public, rs.MeanMs)
				}
			}
		}
	}

	if connected > 0 {
		if m := aggregate.Median(rssi); m < th.RSSIStandingDBm {
			add("weak_signal", "Weak Wi-Fi signal",
				fmt.Sprintf("The Wi-Fi signal averaged %.0f dBm. Video calls work reliably from about %.0f dBm upwards.", m, th.RSSIStandingDBm))
		}
		if share := float64(band24) / float64(connected); share > 0.2 {
			add("band_24", "Often connected on 2.4 GHz",
				fmt.Sprintf("The laptop used the slower, more crowded 2.4 GHz band %.0f%% of the time.", share*100))
		}
	}
	if n := len(s.Scans); n > 0 {
		if overlap := int(s.Scans[n-1].Values["overlap_aps"]); overlap >= th.OverlapAPs {
			add("crowded_channel", "Crowded Wi-Fi channel",
				fmt.Sprintf("%d neighbouring networks use the same or an overlapping channel.", overlap))
		}
	}
	if len(s.SpeedTests) > 0 && s.Meta.ContractMbps > 0 {
		var down []float64
		for _, st := range s.SpeedTests {
			down = append(down, st.DownMbps)
		}
		if md := aggregate.Median(down); md < 0.5*s.Meta.ContractMbps {
			add("slow_speed", "Much slower than the contract",
				fmt.Sprintf("Downloads averaged %.0f Mbit/s, less than half of the contracted %.0f Mbit/s.", md, s.Meta.ContractMbps))
		}
	}
	if g := r.BloatGrade; g == "D" || g == "E" || g == "F" {
		add("bufferbloat", "Bufferbloat",
			fmt.Sprintf("Latency rises sharply while the line is busy (grade %s), so calls suffer whenever someone downloads or uploads.", g))
	}
	minMTU := 0
	for _, p := range s.PMTU {
		if m := int(p.Values["mtu"]); m > 0 && (minMTU == 0 || m < minMTU) {
			minMTU = m
		}
	}
	if minMTU > 0 && minMTU < 1500 {
		add("mtu", "Reduced packet size",
			fmt.Sprintf("The largest packet that gets through is %d bytes instead of 1500. That's normal for some connection types, such as DSL with PPPoE or DS-Lite, but can cause stalls when the router is misconfigured.", minMTU))
	}
	if t, ok := s.LatestInfo(record.CUPnP, record.NTunnel); ok {
		name := map[string]string{"cgnat": "CGNAT", "dslite": "DS-Lite"}[t.Attrs["kind"]]
		if name == "" {
			name = t.Attrs["kind"]
		}
		add("tunnel", "Shared IPv4 address",
			fmt.Sprintf("The connection uses %s, so IPv4 traffic passes through a gateway the provider shares between customers. Evidence: %s.", name, t.Attrs["evidence"]))
	}
	if len(local) > 0 && len(public) > 0 {
		if ml, mp := aggregate.Median(local), aggregate.Median(public); mp > 0 && ml > 3*mp {
			add("slow_dns", "Slow DNS",
				fmt.Sprintf("Name lookups through the router or provider took %.0f ms on average, compared with %.0f ms for a public DNS server.", ml, mp))
		}
	}
	if len(usPower) > 0 {
		var issues []string
		if m := aggregate.Median(usPower); m > th.CableUSPowerMaxDBmV {
			issues = append(issues, fmt.Sprintf("the modem transmits at %.1f dBmV (normal: up to %.0f)", m, th.CableUSPowerMaxDBmV))
		}
		if m := aggregate.Median(dsPowerMin); m < th.CableDSPowerMinDBmV {
			issues = append(issues, fmt.Sprintf("the weakest downstream channel arrives at %.1f dBmV (normal: from %.0f)", m, th.CableDSPowerMinDBmV))
		}
		if m := aggregate.Median(dsPowerMax); m > th.CableDSPowerMaxDBmV {
			issues = append(issues, fmt.Sprintf("the strongest downstream channel arrives at %.1f dBmV (normal: up to %.0f)", m, th.CableDSPowerMaxDBmV))
		}
		if len(mer) > 0 {
			if m := aggregate.Median(mer); m < th.CableMERMinDB {
				issues = append(issues, fmt.Sprintf("the weakest channel's signal quality (MER) is %.1f dB (normal: from %.0f)", m, th.CableMERMinDB))
			}
		}
		if len(issues) > 0 {
			add("cable_levels", "Cable signal levels out of range",
				"The cable modem reports signal levels outside the normal range: "+strings.Join(issues, "; ")+". This usually points to the wiring or amplifiers in the building, or the provider's cable network.")
		}
	}
	if h, ok := s.LatestInfo(record.CEventLog, record.NWlanHistory); ok {
		if d := h.Values["disconnects_7d"]; d > 0 {
			add("wlan_history", "Wi-Fi disconnects before the recording",
				fmt.Sprintf("Windows logged %.0f Wi-Fi disconnects in the 7 days before the recording started.", d))
		}
	}
	if r.PeriodicSpikes {
		add("periodic_spikes", "Regular latency spikes",
			"Latency to the router spikes about once a minute. This usually comes from Windows scanning for Wi-Fi networks in the background.")
	}
	if hour := int(time.Hour / s.Width); batteryAwake >= hour && acAwake > 0 && batteryBad > 0 {
		batteryRate := float64(batteryBad) / float64(batteryAwake)
		acRate := float64(acBad) / float64(acAwake)
		if batteryRate > 2*acRate {
			detail := "Wi-Fi delays happened on battery power but not while plugged in."
			if acRate > 0 {
				detail = fmt.Sprintf("Wi-Fi delays were %.0f times as frequent on battery power as while plugged in.", batteryRate/acRate)
			}
			add("battery", "Worse on battery", detail)
		}
	}
	for _, u := range s.Unavailable {
		add("unavailable:"+u.Collector, "Not measured: "+CollectorTitle(u.Collector), u.Attrs["reason"])
	}
	return out
}
