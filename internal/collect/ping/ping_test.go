package ping

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
)

func TestPingCollector(t *testing.T) {
	fake := &platform.Fake{
		RouteValue: platform.Route{Gateway4: netip.MustParseAddr("192.168.178.1")},
		EchoFunc: func(req platform.EchoRequest) (platform.EchoReply, error) {
			if req.Dst.String() == "8.8.8.8" {
				return platform.EchoReply{Status: platform.EchoTimeout}, nil
			}
			return platform.EchoReply{From: req.Dst, RTT: 3500 * time.Microsecond, Status: platform.EchoOK}, nil
		},
	}
	hops := &HopSet{}
	hops.Set([]netip.Addr{netip.MustParseAddr("62.155.244.1")})
	c := New(fake, Config{
		Targets4:     []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8")},
		Targets6:     []netip.Addr{netip.MustParseAddr("2606:4700:4700::1111")},
		Interval:     20 * time.Millisecond,
		RouteRefresh: 30 * time.Millisecond,
	}, hops)

	var buf record.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := c.Run(ctx, &buf); err != nil {
			t.Errorf("Run: %v", err)
		}
	})
	time.Sleep(150 * time.Millisecond)

	// A new gateway and IPv6 availability are picked up on the next refresh.
	changedAt := time.Now()
	fake.SetRoute(platform.Route{Gateway4: netip.MustParseAddr("192.168.0.1"), Gateway6: netip.MustParseAddr("fe80::1")})
	time.Sleep(250 * time.Millisecond)
	cancel()
	wg.Wait()

	byTarget := map[string][]record.Record{}
	for _, r := range buf.Filter(record.CPing, record.NEcho) {
		byTarget[r.Target] = append(byTarget[r.Target], r)
	}
	gw := byTarget[record.TGateway]
	if len(gw) == 0 || gw[0].Values["rtt_ms"] != 3.5 || gw[0].Attrs["ip"] != "192.168.178.1" {
		t.Fatalf("gateway records wrong: %+v", gw)
	}
	if last := gw[len(gw)-1]; last.Attrs["ip"] != "192.168.0.1" {
		t.Fatalf("gateway change not picked up: last ip %s", last.Attrs["ip"])
	}
	if len(byTarget["hop1"]) == 0 || len(byTarget["inet:1.1.1.1"]) == 0 {
		t.Fatalf("missing hop or internet records: %v", keys(byTarget))
	}
	if lost := byTarget["inet:8.8.8.8"]; len(lost) == 0 || lost[0].Values["lost"] != 1 {
		t.Fatalf("timeouts should be lost: %+v", lost)
	}
	if len(byTarget[record.TGateway6]) == 0 || len(byTarget["inet6:2606:4700:4700::1111"]) == 0 {
		t.Fatalf("IPv6 targets not added once a v6 gateway exists: %v", keys(byTarget))
	}
	var early int
	for _, r := range byTarget["inet6:2606:4700:4700::1111"] {
		if r.Time.Before(changedAt) {
			early++
		}
	}
	if early > 0 {
		t.Fatalf("IPv6 target pinged before IPv6 was available (%d records)", early)
	}
}

func TestPingUnsupportedIsPermanent(t *testing.T) {
	c := New(&unsupported{}, Config{}, nil)
	err := c.Run(context.Background(), &record.Buffer{})
	if err == nil || err.Error() == "" {
		t.Fatal("expected permanent error")
	}
}

type unsupported struct{ platform.Fake }

func (*unsupported) NewPinger(int) (platform.Pinger, error) { return nil, platform.ErrUnsupported }

func keys(m map[string][]record.Record) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
