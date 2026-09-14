//go:build !windows

package unixplat

import (
	"net"
	"net/netip"

	"github.com/loehnertz/LANdlord/internal/platform"
)

// Placeholders until the unix implementations land (Tasks 14 and 18).

func (*Platform) NewPinger(int) (platform.Pinger, error) { return nil, platform.ErrUnsupported }
func (*Platform) DefaultRoute() (platform.Route, error) {
	return platform.Route{}, platform.ErrUnsupported
}
func (*Platform) NeighborMAC(netip.Addr) (net.HardwareAddr, error) {
	return nil, platform.ErrUnsupported
}
func (*Platform) InterfaceCounters(int) (platform.Counters, error) {
	return platform.Counters{}, platform.ErrUnsupported
}
