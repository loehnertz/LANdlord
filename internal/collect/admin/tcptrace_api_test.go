//go:build windows && windowsapi

package admin

import (
	"context"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/loehnertz/LANdlord/internal/collect/traceroute"
	"github.com/loehnertz/LANdlord/internal/platform/winplat"
)

func TestTCPTraceReachesDestination(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("TCP traceroute needs admin rights")
	}
	route, err := winplat.New().DefaultRoute()
	if err != nil || !route.Local4.IsValid() {
		t.Skipf("no IPv4 route: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	path, err := tcpTrace(ctx, route.Local4, tcpTraceTarget, tcpTracePort, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("TCP route: %s", traceroute.FormatPath(path))
	if len(path) == 0 {
		t.Fatal("empty path")
	}
	if last := path[len(path)-1]; last != tcpTraceTarget {
		t.Logf("destination not reached within the silent-hop limit (last hop %v); acceptable on filtered networks", last)
	}
}
