package traceroute

import (
	"context"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/collect/ping"
	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
)

var dst = netip.MustParseAddr("1.1.1.1")

// scripted answers each TTL from a table; missing entries time out.
func scripted(table map[int]string) func(platform.EchoRequest) (platform.EchoReply, error) {
	return func(req platform.EchoRequest) (platform.EchoReply, error) {
		from, ok := table[req.TTL]
		switch {
		case !ok:
			return platform.EchoReply{Status: platform.EchoTimeout}, nil
		case from == req.Dst.String():
			return platform.EchoReply{From: req.Dst, Status: platform.EchoOK}, nil
		default:
			return platform.EchoReply{From: netip.MustParseAddr(from), Status: platform.EchoTTLExpired}, nil
		}
	}
}

var firstRoute = map[int]string{1: "192.168.178.1", 2: "100.64.0.1", 4: "62.155.244.1", 5: "217.0.118.5", 6: "1.1.1.1"}

func TestTraceAndProviderHops(t *testing.T) {
	fake := &platform.Fake{EchoFunc: scripted(firstRoute)}
	pg, _ := fake.NewPinger(4)
	path := Trace(context.Background(), pg, dst, 30, time.Millisecond)
	if got := FormatPath(path); got != "192.168.178.1,100.64.0.1,*,62.155.244.1,217.0.118.5,1.1.1.1" {
		t.Fatalf("path = %s", got)
	}
	hops := ProviderHops(path, dst)
	if len(hops) != 2 || hops[0].String() != "62.155.244.1" || hops[1].String() != "217.0.118.5" {
		t.Fatalf("provider hops = %v", hops)
	}

	quiet := &silentAfter{}
	unreached := Trace(context.Background(), quiet, dst, 30, time.Millisecond)
	if got := FormatPath(unreached); got != "192.168.178.1,*" {
		t.Fatalf("unfinished trace = %s", got)
	}
	if want := 1 + maxSilentHops*attemptsPerHop; quiet.calls != want {
		t.Fatalf("silent network took %d echoes, want %d (early stop)", quiet.calls, want)
	}
}

type silentAfter struct{ calls int }

func (s *silentAfter) Echo(_ context.Context, req platform.EchoRequest) (platform.EchoReply, error) {
	s.calls++
	if req.TTL == 1 {
		return platform.EchoReply{From: netip.MustParseAddr("192.168.178.1"), Status: platform.EchoTTLExpired}, nil
	}
	return platform.EchoReply{Status: platform.EchoTimeout}, nil
}
func (*silentAfter) Close() error { return nil }

func TestRouteChangeIgnoresSilentHops(t *testing.T) {
	if comparable("a,*,b") != comparable("a,b,*") {
		t.Fatal("silent hops should not matter")
	}
	if comparable("a,b") == comparable("a,c") {
		t.Fatal("different hops should matter")
	}
}

func TestCollector(t *testing.T) {
	var second atomic.Bool
	first := scripted(firstRoute)
	changed := scripted(map[int]string{1: "192.168.178.1", 2: "62.155.244.9", 3: "1.1.1.1"})
	fake := &platform.Fake{EchoFunc: func(req platform.EchoRequest) (platform.EchoReply, error) {
		if second.Load() {
			return changed(req)
		}
		return first(req)
	}}
	hops := &ping.HopSet{}
	c := New(fake, []netip.Addr{dst}, hops, time.Hour)
	c.timeout = time.Millisecond

	var buf record.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = c.Run(ctx, &buf) })

	waitFor(t, func() bool { return len(buf.Filter(record.CTrace, record.NRoute)) == 1 })
	if got := hops.Get(); len(got) != 2 {
		t.Fatalf("hop set = %v", got)
	}
	second.Store(true)
	c.Trigger()
	waitFor(t, func() bool { return len(buf.Filter(record.CTrace, record.NRouteChange)) == 1 })
	cancel()
	wg.Wait()

	change := buf.Filter(record.CTrace, record.NRouteChange)[0]
	if change.Attrs["new"] != "192.168.178.1,62.155.244.9,1.1.1.1" || change.Target != "1.1.1.1" {
		t.Fatalf("route change = %+v", change)
	}
	if got := hops.Get(); len(got) != 1 || got[0].String() != "62.155.244.9" {
		t.Fatalf("hop set after change = %v", got)
	}
	if hopCount("a,b,c") != "3" {
		t.Fatal("hopCount wrong")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
