package sim

import (
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/record"
	"github.com/loehnertz/LANdlord/internal/store"
)

func TestAllScenariosRegistered(t *testing.T) {
	want := []string{"access_line", "cable_line", "dns_trouble", "evening_congestion", "healthy", "hop_ratelimit", "ipv4_tunnel", "lan_saturation", "periodic_scan", "weak_wifi"}
	got := Names()
	if len(got) != len(want) {
		t.Fatalf("Names() = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names() = %v, want %v", got, want)
		}
	}
}

func TestWeakWifiProducesDegradedGatewayBuckets(t *testing.T) {
	sc, _ := Get("weak_wifi")
	start := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	var buf record.Buffer
	Run(sc, start, time.UTC, 1, &buf)
	recs := buf.Records()
	if len(recs) == 0 {
		t.Fatal("no records")
	}
	meta := store.Meta{Start: start, Duration: sc.Duration, Finished: true, FinishedAt: start.Add(sc.Duration)}
	s, err := aggregate.Build(meta, aggregate.DefaultWidth, func(fn func(record.Record) error) error {
		for _, r := range recs {
			if err := fn(r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var weak, lossy int
	for _, b := range s.Buckets {
		if b.Wifi.RSSIMin <= -75 {
			weak++
		}
		if p := b.Paths[record.TGateway]; p != nil && p.LossPct > 1 {
			lossy++
		}
	}
	if weak == 0 || lossy == 0 {
		t.Fatalf("weak=%d lossy=%d, expected both > 0", weak, lossy)
	}
	if _, ok := s.LatestInfo(record.CRouter, record.NIdentity); !ok {
		t.Fatal("router identity info missing")
	}
}
