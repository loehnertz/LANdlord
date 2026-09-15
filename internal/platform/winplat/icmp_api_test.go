//go:build windows && windowsapi

package winplat

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/platform"
)

func TestEchoLoopback(t *testing.T) {
	p := New()
	for _, tc := range []struct {
		family int
		addr   string
	}{{4, "127.0.0.1"}, {6, "::1"}} {
		pg, err := p.NewPinger(tc.family)
		if err != nil {
			t.Fatalf("NewPinger(%d): %v", tc.family, err)
		}
		reply, err := pg.Echo(context.Background(), platform.EchoRequest{Dst: netip.MustParseAddr(tc.addr), Timeout: time.Second})
		pg.Close()
		if err != nil || reply.Status != platform.EchoOK {
			t.Fatalf("echo %s: status=%v err=%v", tc.addr, reply.Status, err)
		}
		// Verifies the reply address offset, especially the packed IPV6_ADDRESS_EX layout.
		if reply.From != netip.MustParseAddr(tc.addr) {
			t.Fatalf("echo %s: reply from %v", tc.addr, reply.From)
		}
	}
}

func TestEchoTTLExpired(t *testing.T) {
	pg, err := New().NewPinger(4)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	reply, err := pg.Echo(context.Background(), platform.EchoRequest{Dst: netip.MustParseAddr("8.8.8.8"), TTL: 1, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Status != platform.EchoTTLExpired && reply.Status != platform.EchoTimeout {
		t.Fatalf("status = %v, want TTL expired or timeout", reply.Status)
	}
}

func TestDefaultRoute(t *testing.T) {
	r, err := New().DefaultRoute()
	if err != nil {
		t.Skipf("no default route: %v", err)
	}
	if !r.Gateway4.IsValid() && !r.Gateway6.IsValid() {
		t.Fatalf("route without gateway: %+v", r)
	}
}
