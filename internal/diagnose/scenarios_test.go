package diagnose

import (
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/record"
	"github.com/loehnertz/LANdlord/internal/sim"
	"github.com/loehnertz/LANdlord/internal/store"
)

func runScenario(t *testing.T, name string) Result {
	t.Helper()
	sc, ok := sim.Get(name)
	if !ok {
		t.Fatalf("unknown scenario %s", name)
	}
	start := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	var buf record.Buffer
	sim.Run(sc, start, time.UTC, 7, &buf)
	meta := store.Meta{Start: start, Duration: sc.Duration, Finished: true, FinishedAt: start.Add(sc.Duration)}
	s, err := aggregate.Build(meta, aggregate.DefaultWidth, func(fn func(record.Record) error) error {
		for _, r := range buf.Records() {
			if err := fn(r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return Analyze(s, th())
}

func TestScenarios(t *testing.T) {
	tests := []struct {
		scenario string
		want     Culprit // empty: no incidents expected
		long     bool
	}{
		{"healthy", "", false},
		{"hop_ratelimit", "", false},
		{"weak_wifi", WifiSignal, false},
		{"access_line", AccessLine, false},
		{"cable_line", AccessLine, false},
		{"periodic_scan", ClientDevice, false},
		{"dns_trouble", DNS, false},
		{"ipv4_tunnel", IPv4Tunnel, false},
		{"lan_saturation", LANSaturation, false},
		{"evening_congestion", ISPCongestion, true},
	}
	for _, tt := range tests {
		t.Run(tt.scenario, func(t *testing.T) {
			if tt.long && testing.Short() {
				t.Skip("48 h scenario")
			}
			r := runScenario(t, tt.scenario)
			if tt.want == "" {
				if len(r.Incidents) != 0 {
					t.Fatalf("expected no incidents, got %d (main %s, first %+v)", len(r.Incidents), r.Main, r.Incidents[0])
				}
				return
			}
			if r.Main != tt.want {
				t.Fatalf("main = %s, want %s (shares %v)", r.Main, tt.want, r.Shares)
			}
			if tt.scenario == "periodic_scan" && !r.PeriodicSpikes {
				t.Fatal("periodic spikes not detected")
			}
			if tt.scenario == "cable_line" {
				found := false
				for _, inc := range r.Incidents {
					for _, e := range inc.Evidence {
						found = found || e.Signal == "cable_errors"
					}
				}
				if !found {
					t.Fatal("cable errors missing from the incident evidence")
				}
			}
		})
	}
}
