package diagnose

import "strings"

type recText struct {
	group Group
	text  string
}

var culpritRecs = map[Culprit][]recText{
	WifiSignal: {
		{Tenant, "Sit closer to the router, or open the doors between you and the router."},
		{Tenant, "Use a network cable for important video calls."},
		{Landlord, "Add a mesh access point or repeater near the room where the problems happen."},
		{Landlord, "Move the router to a more central spot in the apartment."},
	},
	WifiInterference: {
		{Tenant, "If the Wi-Fi offers a separate 5 GHz network, connect to that one."},
		{Landlord, "Change the router's Wi-Fi channel to a less crowded one (see the neighbouring networks in the inspection details)."},
		{Landlord, "Turn on 5 GHz or 6 GHz on the router if it supports it."},
	},
	ClientDevice: {
		{Tenant, "Keep the laptop plugged in during calls and set Windows' power mode to Best performance."},
		{Tenant, "Install the latest Wi-Fi driver from the laptop manufacturer's website."},
		{Landlord, "Check the Wi-Fi adapter's roaming and power-saving settings in Device Manager."},
	},
	LANSaturation: {
		{Tenant, "Ask others in the home to pause large downloads, uploads or backups during calls."},
		{Landlord, "Turn on QoS or Smart Queue Management on the router, or move to a faster plan."},
	},
	AccessLine: {
		{Tenant, "Restart the router once and check whether the problems continue."},
		{Landlord, "Contact the provider with this report: the problems start beyond the router, on the line or in the provider's network."},
		{Landlord, "Have the in-house wiring and the wall socket checked, especially in an old building."},
	},
	ISPCongestion: {
		{Tenant, "Schedule large downloads outside 18:00 to 24:00."},
		{Landlord, "Report the evening congestion to the provider and ask for a line check, or consider another provider."},
	},
	IPv4Tunnel: {
		{Landlord, "Ask the provider for a public IPv4 address (dual stack) instead of DS-Lite or CGNAT."},
	},
	DNS: {
		{Tenant, "Set a public DNS server such as 1.1.1.1 in the Windows network settings."},
		{Landlord, "Change the router's DNS server to a public resolver such as 1.1.1.1 or 9.9.9.9."},
	},
	RemoteService: {
		{Tenant, "Only one service was affected. Check its status page before changing anything at home."},
	},
	Unknown: {
		{Tenant, "Keep pressing the button when something goes wrong. More marks make the diagnosis more precise."},
	},
}

var findingRecs = map[string][]recText{
	"weak_signal":     culpritRecs[WifiSignal],
	"band_24":         culpritRecs[WifiInterference][:1],
	"crowded_channel": culpritRecs[WifiInterference][1:],
	"tunnel":          culpritRecs[IPv4Tunnel],
	"slow_dns":        culpritRecs[DNS],
	"battery":         culpritRecs[ClientDevice][:1],
	"periodic_spikes": culpritRecs[ClientDevice],
	"bufferbloat":     culpritRecs[LANSaturation][1:],
	"slow_speed":      {{Landlord, "Tell the provider the contracted speed isn't delivered, using the speed test table in this report."}},
	"mtu":             {{Landlord, "Check the router's MTU setting; the path MTU is below 1500."}},
}

const germanyHint = "In Germany, measure with the Bundesnetzagentur's Breitbandmessung desktop app. If the provider keeps delivering less than contracted, section 57 of the Telecommunications Act (TKG) lets you reduce the price or cancel early."

func recommend(r *Result) []Recommendation {
	var out []Recommendation
	seen := map[string]bool{}
	add := func(forID string, recs []recText) {
		for _, rt := range recs {
			if !seen[rt.text] {
				seen[rt.text] = true
				out = append(out, Recommendation{Group: rt.group, For: forID, Text: rt.text})
			}
		}
	}
	if r.Main != "" {
		add(string(r.Main), culpritRecs[r.Main])
	}
	for _, c := range AllCulprits {
		if c != r.Main && r.Shares[c] > 0.1 {
			add(string(c), culpritRecs[c])
		}
	}
	slowSpeed := false
	for _, f := range r.Findings {
		if strings.HasPrefix(f.ID, "unavailable:") {
			continue
		}
		if f.ID == "slow_speed" {
			slowSpeed = true
		}
		add(f.ID, findingRecs[f.ID])
	}
	if r.Country == "DE" && (r.Main == AccessLine || r.Main == ISPCongestion || slowSpeed) {
		add("country:DE", []recText{{Landlord, germanyHint}})
	}
	return out
}
