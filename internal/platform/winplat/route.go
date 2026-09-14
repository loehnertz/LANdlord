//go:build windows

package winplat

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/loehnertz/LANdlord/internal/platform"
)

const ifTypeIEEE80211 = 71

func adapterAddresses() ([]*windows.IpAdapterAddresses, error) {
	size := uint32(15000)
	for range 4 {
		buf := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_INCLUDE_GATEWAYS|windows.GAA_FLAG_INCLUDE_PREFIX, 0, first, &size)
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var out []*windows.IpAdapterAddresses
		for a := first; a != nil; a = a.Next {
			out = append(out, a)
		}
		return out, nil
	}
	return nil, windows.ERROR_BUFFER_OVERFLOW
}

func sockaddrToAddr(sa windows.SocketAddress, zoneIndex uint32) netip.Addr {
	ip := sa.IP()
	if ip == nil {
		return netip.Addr{}
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Addr{}
	}
	addr = addr.Unmap()
	if addr.Is6() && addr.IsLinkLocalUnicast() && zoneIndex != 0 {
		addr = addr.WithZone(strconv.Itoa(int(zoneIndex)))
	}
	return addr
}

// DefaultRoute picks the adapter Windows would use to reach the internet, falling back to the
// lowest-metric adapter that is up and has a gateway.
func (*Platform) DefaultRoute() (platform.Route, error) {
	adapters, err := adapterAddresses()
	if err != nil {
		return platform.Route{}, err
	}
	var bestIndex uint32
	probe := windows.SockaddrInet4{Addr: [4]byte{1, 1, 1, 1}}
	_ = windows.GetBestInterfaceEx(&probe, &bestIndex)

	var chosen *windows.IpAdapterAddresses
	for _, a := range adapters {
		if a.OperStatus != windows.IfOperStatusUp || a.FirstGatewayAddress == nil {
			continue
		}
		if bestIndex != 0 && a.IfIndex == bestIndex {
			chosen = a
			break
		}
		if chosen == nil || a.Ipv4Metric < chosen.Ipv4Metric {
			chosen = a
		}
	}
	if chosen == nil {
		return platform.Route{}, errors.New("no network adapter with a gateway is connected")
	}

	r := platform.Route{
		InterfaceName:  windows.UTF16PtrToString(chosen.FriendlyName),
		Description:    windows.UTF16PtrToString(chosen.Description),
		InterfaceIndex: int(chosen.IfIndex),
		InterfaceGUID:  windows.BytePtrToString(chosen.AdapterName),
		MAC:            net.HardwareAddr(append([]byte(nil), chosen.PhysicalAddress[:chosen.PhysicalAddressLength]...)),
		Wireless:       chosen.IfType == ifTypeIEEE80211,
		LinkMbps:       float64(chosen.ReceiveLinkSpeed) / 1e6,
	}
	for g := chosen.FirstGatewayAddress; g != nil; g = g.Next {
		addr := sockaddrToAddr(g.Address, chosen.Ipv6IfIndex)
		switch {
		case addr.Is4() && !r.Gateway4.IsValid():
			r.Gateway4 = addr
		case addr.Is6() && !r.Gateway6.IsValid():
			r.Gateway6 = addr
		}
	}
	for u := chosen.FirstUnicastAddress; u != nil; u = u.Next {
		if addr := sockaddrToAddr(u.Address, 0); addr.Is4() {
			r.Local4 = addr
			break
		}
	}
	for d := chosen.FirstDnsServerAddress; d != nil; d = d.Next {
		if addr := sockaddrToAddr(d.Address, chosen.Ipv6IfIndex); addr.IsValid() {
			r.DNSServers = append(r.DNSServers, addr)
		}
	}
	return r, nil
}
