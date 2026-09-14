//go:build !windows

package unixplat

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/platform"
)

func TestParseDarwinRoute(t *testing.T) {
	out := `   route to: default
destination: default
       mask: default
    gateway: 192.168.178.1
  interface: en0
      flags: <UP,GATEWAY,DONE,STATIC,PRCLONING,GLOBAL>`
	gw, iface := parseDarwinRoute(out)
	if gw != netip.MustParseAddr("192.168.178.1") || iface != "en0" {
		t.Fatalf("got %v %q", gw, iface)
	}
	gw6, _ := parseDarwinRoute("gateway: fe80::1\ninterface: en0\n")
	if gw6.String() != "fe80::1%en0" {
		t.Fatalf("link-local gateway = %v", gw6)
	}
}

func TestParseLinuxRoutes(t *testing.T) {
	route := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"wlan0\t00000000\t01B2A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n" +
		"wlan0\t00B2A8C0\t00000000\t0001\t0\t0\t600\t00FFFFFF\t0\t0\t0\n"
	gw, iface := parseLinuxRoute(strings.NewReader(route))
	if gw != netip.MustParseAddr("192.168.178.1") || iface != "wlan0" {
		t.Fatalf("got %v %q", gw, iface)
	}
	v6 := "00000000000000000000000000000000 00 00000000000000000000000000000000 00 fe800000000000000000000000000001 00000400 00000001 00000000 00000003 wlan0\n"
	if got := parseLinuxIPv6Route(strings.NewReader(v6), "wlan0"); got.String() != "fe80::1%wlan0" {
		t.Fatalf("ipv6 gateway = %v", got)
	}
}

func TestParseResolvConfAndARP(t *testing.T) {
	dns := parseResolvConf(strings.NewReader("# comment\nnameserver 192.168.178.1\nnameserver fd00::1\nsearch fritz.box\n"))
	if len(dns) != 2 || dns[0].String() != "192.168.178.1" {
		t.Fatalf("dns = %v", dns)
	}
	mac, err := parseDarwinARP("? (192.168.178.1) at 3c:a6:2f:0:0:1 on en0 ifscope [ethernet]\n")
	if err != nil || mac.String() != "3c:a6:2f:00:00:01" {
		t.Fatalf("darwin arp = %v %v", mac, err)
	}
	arp := "IP address       HW type     Flags       HW address            Mask     Device\n192.168.178.1    0x1         0x2         3c:a6:2f:00:00:01     *        wlan0\n"
	mac, err = parseLinuxARP(strings.NewReader(arp), netip.MustParseAddr("192.168.178.1"))
	if err != nil || mac.String() != "3c:a6:2f:00:00:01" {
		t.Fatalf("linux arp = %v %v", mac, err)
	}
}

func TestPingLoopback(t *testing.T) {
	pg, err := New().NewPinger(4)
	if err != nil {
		t.Skipf("unprivileged ICMP unavailable: %v", err)
	}
	defer pg.Close()
	reply, err := pg.Echo(context.Background(), platform.EchoRequest{Dst: netip.MustParseAddr("127.0.0.1"), Timeout: time.Second})
	if err != nil || reply.Status != platform.EchoOK {
		t.Fatalf("echo 127.0.0.1: status=%v err=%v", reply.Status, err)
	}
}
