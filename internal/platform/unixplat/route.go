//go:build !windows

package unixplat

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/loehnertz/LANdlord/internal/platform"
)

func (*Platform) DefaultRoute() (platform.Route, error) {
	var r platform.Route
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("route", "-n", "get", "default").Output()
		if err != nil {
			return r, err
		}
		r.Gateway4, r.InterfaceName = parseDarwinRoute(string(out))
		if out6, err := exec.Command("route", "-n", "get", "-inet6", "default").Output(); err == nil {
			r.Gateway6, _ = parseDarwinRoute(string(out6))
		}
	} else {
		f, err := os.Open("/proc/net/route")
		if err != nil {
			return r, err
		}
		r.Gateway4, r.InterfaceName = parseLinuxRoute(f)
		f.Close()
		if f6, err := os.Open("/proc/net/ipv6_route"); err == nil {
			r.Gateway6 = parseLinuxIPv6Route(f6, r.InterfaceName)
			f6.Close()
		}
	}
	if !r.Gateway4.IsValid() && !r.Gateway6.IsValid() {
		return r, errors.New("no default route")
	}
	if ifc, err := net.InterfaceByName(r.InterfaceName); err == nil {
		r.InterfaceIndex, r.MAC = ifc.Index, ifc.HardwareAddr
		if addrs, err := ifc.Addrs(); err == nil {
			for _, a := range addrs {
				if pfx, err := netip.ParsePrefix(a.String()); err == nil && pfx.Addr().Is4() {
					r.Local4 = pfx.Addr()
					break
				}
			}
		}
	}
	if f, err := os.Open("/etc/resolv.conf"); err == nil {
		r.DNSServers = parseResolvConf(f)
		f.Close()
	}
	return r, nil
}

func parseDarwinRoute(out string) (gateway netip.Addr, iface string) {
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "gateway":
			if a, err := netip.ParseAddr(val); err == nil {
				gateway = a
			}
		case "interface":
			iface = val
		}
	}
	if gateway.Is6() && gateway.IsLinkLocalUnicast() && gateway.Zone() == "" && iface != "" {
		gateway = gateway.WithZone(iface)
	}
	return gateway, iface
}

func parseLinuxRoute(r io.Reader) (gateway netip.Addr, iface string) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 || f[1] != "00000000" {
			continue
		}
		b, err := hex.DecodeString(f[2])
		if err != nil || len(b) != 4 {
			continue
		}
		var ip [4]byte
		binary.LittleEndian.PutUint32(ip[:], binary.BigEndian.Uint32(b))
		return netip.AddrFrom4(ip), f[0]
	}
	return netip.Addr{}, ""
}

func parseLinuxIPv6Route(r io.Reader, iface string) netip.Addr {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[0] != strings.Repeat("0", 32) || f[1] != "00" || f[4] == strings.Repeat("0", 32) {
			continue
		}
		if iface != "" && f[9] != iface {
			continue
		}
		b, err := hex.DecodeString(f[4])
		if err != nil || len(b) != 16 {
			continue
		}
		a := netip.AddrFrom16([16]byte(b))
		if a.IsLinkLocalUnicast() {
			a = a.WithZone(f[9])
		}
		return a
	}
	return netip.Addr{}
}

func parseResolvConf(r io.Reader) []netip.Addr {
	var out []netip.Addr
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[0] == "nameserver" {
			if a, err := netip.ParseAddr(f[1]); err == nil {
				out = append(out, a)
			}
		}
	}
	return out
}

func (*Platform) NeighborMAC(ip netip.Addr) (net.HardwareAddr, error) {
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("arp", "-n", ip.String()).Output()
		if err != nil {
			return nil, err
		}
		return parseDarwinARP(string(out))
	}
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseLinuxARP(f, ip)
}

func parseDarwinARP(out string) (net.HardwareAddr, error) {
	f := strings.Fields(out)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == "at" {
			return parseMAC(f[i+1])
		}
	}
	return nil, errors.New("no ARP entry")
}

func parseLinuxARP(r io.Reader, ip netip.Addr) (net.HardwareAddr, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 4 && f[0] == ip.String() {
			return parseMAC(f[3])
		}
	}
	return nil, errors.New("no ARP entry")
}

// parseMAC accepts macOS's shortened octets such as "a4:2b:b0:1:2:3".
func parseMAC(s string) (net.HardwareAddr, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 6 {
		return nil, errors.New("invalid MAC address " + s)
	}
	for i, p := range parts {
		if len(p) == 1 {
			parts[i] = "0" + p
		}
	}
	return net.ParseMAC(strings.Join(parts, ":"))
}
