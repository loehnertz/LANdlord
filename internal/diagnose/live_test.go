package diagnose

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/record"
	"github.com/loehnertz/LANdlord/internal/store"
)

func weakSignal(b *aggregate.Bucket) {
	lossy(b.Paths[record.TGateway], 8)
	eachInternet(b, func(_ string, p *aggregate.PathStats) { lossy(p, 8) })
	b.Wifi.RSSIMin = -78
}

func bucketsFor(d time.Duration) int { return int(d / width) }

func TestLiveVerdictStates(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		bad      func(i int) bool
		want     LiveState
		headline string
	}{
		{"too early", 25 * time.Minute, func(i int) bool { return i < 12 }, Collecting, "Still collecting data"},
		{"likely", 45 * time.Minute, func(i int) bool { return i < 12 }, Likely, "Likely cause so far: Weak Wi-Fi signal (based on 1 problem in 45 minutes)"},
		{"confident", 3 * time.Hour, func(i int) bool { return (i >= 100 && i < 112) || (i >= 600 && i < 612) }, Confident, "Most likely cause: Weak Wi-Fi signal."},
		{"quiet", 3 * time.Hour, func(int) bool { return false }, NoProblems, "No problems measured so far."},
		{"quiet but too early", time.Hour, func(int) bool { return false }, Collecting, "Still collecting data"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSession(bucketsFor(tt.duration), func(i int, b *aggregate.Bucket) {
				if tt.bad(i) {
					weakSignal(b)
				}
			})
			lv := Analyze(s, th()).Live
			if lv.State != tt.want {
				t.Fatalf("state = %s, want %s (%+v)", lv.State, tt.want, lv)
			}
			if !strings.HasPrefix(lv.Headline, tt.headline) {
				t.Fatalf("headline = %q, want prefix %q", lv.Headline, tt.headline)
			}
			if (tt.want == Likely || tt.want == Confident) != (lv.Culprit == WifiSignal) {
				t.Fatalf("culprit = %q for state %s", lv.Culprit, lv.State)
			}
		})
	}
}

func TestLiveVerdictCaveats(t *testing.T) {
	s := newSession(bucketsFor(3*time.Hour), nil)
	s.Meta.Marks = []store.Mark{{Time: t0.Add(time.Hour)}}
	s.Unavailable = []record.Record{
		record.Unavailable(record.CWifi, t0, "location access for desktop apps is turned off"),
		record.Unavailable(record.CWifi, t0, "another reason"),
		record.Unavailable(record.CFritz, t0, "router password not entered"),
	}
	caveats := Analyze(s, th()).Live.Caveats
	for _, want := range []string{
		"Evening congestion can only be detected after recording through an evening and a night.",
		"1 of your marks happened while the connection measured fine.",
		"Not measured: Wi-Fi details, FRITZ!Box line statistics.",
	} {
		if !slices.Contains(caveats, want) {
			t.Fatalf("caveats %q missing %q", caveats, want)
		}
	}
}

func TestLiveVerdictEveningCaveatClearsAfterFullDay(t *testing.T) {
	if testing.Short() {
		t.Skip("48 h session")
	}
	s := newSession(bucketsFor(48*time.Hour), nil)
	for _, c := range Analyze(s, th()).Live.Caveats {
		if strings.HasPrefix(c, "Evening congestion") {
			t.Fatal("evening caveat still shown after 48 h")
		}
	}
}
