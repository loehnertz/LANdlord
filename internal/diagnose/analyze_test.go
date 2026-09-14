package diagnose

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/record"
)

func findingIDs(r Result) []string {
	var ids []string
	for _, f := range r.Findings {
		ids = append(ids, f.ID)
	}
	return ids
}

func TestPeriodicSpikesBecomeClientDevice(t *testing.T) {
	s := newSession(240, func(i int, b *aggregate.Bucket) {
		if i%6 == 0 {
			b.Paths[record.TGateway].P95 = 200
			eachInternet(b, func(_ string, p *aggregate.PathStats) { p.P95 = 220 })
		}
	})
	r := Analyze(s, th())
	if !r.PeriodicSpikes || r.Main != ClientDevice {
		t.Fatalf("periodic=%v main=%s", r.PeriodicSpikes, r.Main)
	}
	assertSignals(t, r.Incidents[0].Evidence, "periodic")
	if !slices.Contains(findingIDs(r), "periodic_spikes") {
		t.Fatalf("findings %v missing periodic_spikes", findingIDs(r))
	}
}

func eveningSession(dirtyNight bool) *aggregate.Session {
	return newSession(int(48*time.Hour/width), func(i int, b *aggregate.Bucket) {
		h := b.Start.Hour()
		bad := h >= 19 && h < 23
		if dirtyNight && h == 3 && b.Start.Minute() < 10 {
			bad = true
		}
		if bad {
			for _, hop := range []string{"hop1", "hop2", "hop3"} {
				b.Paths[hop].P95 = 160
			}
			eachInternet(b, func(_ string, p *aggregate.PathStats) { p.P95 = 190 })
		}
	})
}

func TestEveningCongestion(t *testing.T) {
	if testing.Short() {
		t.Skip("48 h session")
	}
	if r := Analyze(eveningSession(false), th()); r.Main != ISPCongestion {
		t.Fatalf("main = %s, want isp_congestion", r.Main)
	}
	if r := Analyze(eveningSession(true), th()); r.Main != AccessLine {
		t.Fatalf("with a dirty night main = %s, want access_line", r.Main)
	}
}

func TestFindingsAndRecommendations(t *testing.T) {
	s := newSession(400, func(_ int, b *aggregate.Bucket) {
		b.Wifi.RSSIMean = -72
	})
	s.SpeedTests = []aggregate.SpeedTest{{DownMbps: 30, BloatMs: 250}, {DownMbps: 32, BloatMs: 260}}
	s.Meta.ContractMbps = 100
	s.Unavailable = []record.Record{record.Unavailable(record.CFritz, t0, "router password not entered")}
	s.Infos = []record.Record{
		record.Info(record.CUPnP, record.NTunnel, "", t0, map[string]string{"kind": "dslite", "evidence": "router has no public IPv4 address"}),
		record.Info(record.CPublic, record.NPublicIP, "", t0, map[string]string{"ip": "203.0.113.7", "loc": "DE"}),
	}
	r := Analyze(s, th())
	if r.BloatGrade != "E" {
		t.Fatalf("bloat grade = %s", r.BloatGrade)
	}
	ids := findingIDs(r)
	for _, want := range []string{"weak_signal", "slow_speed", "bufferbloat", "tunnel", "unavailable:fritz"} {
		if !slices.Contains(ids, want) {
			t.Fatalf("findings %v missing %s", ids, want)
		}
	}
	var hasDE, hasCable bool
	for _, rec := range r.Recommendations {
		hasDE = hasDE || strings.Contains(rec.Text, "Breitbandmessung")
		hasCable = hasCable || rec.Text == "Use a network cable for important video calls."
	}
	if !hasDE || !hasCable {
		t.Fatalf("recommendations missing DE hint (%v) or weak signal advice (%v)", hasDE, hasCable)
	}

	s.Infos[1].Attrs["loc"] = "NL"
	for _, rec := range Analyze(s, th()).Recommendations {
		if strings.Contains(rec.Text, "Breitbandmessung") {
			t.Fatal("DE hint shown outside Germany")
		}
	}
}

func TestAsleepBucketsIgnored(t *testing.T) {
	s := newSession(10, func(i int, b *aggregate.Bucket) {
		if i == 3 {
			b.Asleep = true
			eachInternet(b, func(_ string, p *aggregate.PathStats) { lossy(p, 50) })
		}
	})
	r := Analyze(s, th())
	if r.AwakeBuckets != 9 || r.BadBuckets != 0 || len(r.Incidents) != 0 {
		t.Fatalf("awake=%d bad=%d incidents=%d", r.AwakeBuckets, r.BadBuckets, len(r.Incidents))
	}
}

func TestBloatGrade(t *testing.T) {
	for ms, want := range map[float64]string{0: "A", 4.9: "A", 5: "B", 29: "B", 45: "C", 100: "D", 250: "E", 500: "F"} {
		if got := BloatGrade(ms); got != want {
			t.Fatalf("BloatGrade(%v) = %s, want %s", ms, got, want)
		}
	}
}
