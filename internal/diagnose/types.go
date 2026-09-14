// Package diagnose turns aggregated buckets into incidents, culprits, findings and recommendations.
package diagnose

import "time"

type Culprit string

const (
	WifiSignal       Culprit = "wifi_signal"
	WifiInterference Culprit = "wifi_interference"
	ClientDevice     Culprit = "client_device"
	LANSaturation    Culprit = "lan_saturation"
	AccessLine       Culprit = "access_line"
	ISPCongestion    Culprit = "isp_congestion"
	IPv4Tunnel       Culprit = "ipv4_tunnel"
	DNS              Culprit = "dns"
	RemoteService    Culprit = "remote_service"
	Unknown          Culprit = "unknown"
)

// AllCulprits is ordered from the lowest network layer upwards; ties are broken in this order.
var AllCulprits = []Culprit{WifiSignal, WifiInterference, ClientDevice, LANSaturation, AccessLine, ISPCongestion, IPv4Tunnel, DNS, RemoteService, Unknown}

var culpritTitles = map[Culprit]string{
	WifiSignal:       "Weak Wi-Fi signal",
	WifiInterference: "Wi-Fi interference or crowded channel",
	ClientDevice:     "The laptop itself",
	LANSaturation:    "Router overloaded or line shared",
	AccessLine:       "Access line, building wiring or provider access",
	ISPCongestion:    "Provider congestion",
	IPv4Tunnel:       "IPv4 tunnel (DS-Lite/CGNAT)",
	DNS:              "DNS",
	RemoteService:    "A single remote service",
	Unknown:          "Unexplained",
}

func (c Culprit) Title() string { return culpritTitles[c] }

type Confidence string

const (
	High   Confidence = "high"
	Medium Confidence = "medium"
	Low    Confidence = "low"
)

func confidenceFor(strongSignals int) Confidence {
	switch {
	case strongSignals >= 3:
		return High
	case strongSignals == 2:
		return Medium
	default:
		return Low
	}
}

type Evidence struct {
	Signal string `json:"signal"`
	Text   string `json:"text"`
}

// weakSignals describe missing or ambiguous data. They are shown but don't raise confidence.
var weakSignals = map[string]bool{"no_hops": true, "later_hop": true, "client": true}

type BucketVerdict struct {
	Bad      bool
	Culprit  Culprit
	Symptoms []Evidence
	Evidence []Evidence
}

type Incident struct {
	Start, End  time.Time
	First, Last int
	Culprit     Culprit
	Confidence  Confidence
	Shares      map[Culprit]float64
	RunnersUp   []Culprit
	Symptoms    []Evidence
	Evidence    []Evidence
}

func (i Incident) Duration() time.Duration { return i.End.Sub(i.Start) }

type MarkResult struct {
	Time     time.Time
	Tag      string
	Incident int // index into Result.Incidents, -1 when no incident matched
}

type Finding struct {
	ID, Title, Detail string
}

type Group string

const (
	Tenant   Group = "tenant"
	Landlord Group = "landlord"
)

type Recommendation struct {
	Group     Group
	For, Text string
}

type Result struct {
	Verdicts                 []BucketVerdict
	Incidents                []Incident
	AwakeBuckets, BadBuckets int
	ProblemPct               float64
	Shares                   map[Culprit]float64
	Main                     Culprit
	MainConfidence           Confidence
	Marks                    []MarkResult
	PeriodicSpikes           bool
	BloatGrade, Country      string
	Findings                 []Finding
	Recommendations          []Recommendation
}
