//go:build windows

package winplat

import "github.com/loehnertz/LANdlord/internal/platform"

// Placeholders until the corresponding Windows implementations land (Tasks 17 to 26).

func (*Platform) InterfaceCounters(int) (platform.Counters, error) {
	return platform.Counters{}, platform.ErrUnsupported
}
func (*Platform) Adapter(platform.Route) (platform.AdapterInfo, error) {
	return platform.AdapterInfo{}, platform.ErrUnsupported
}
func (*Platform) Power() (platform.PowerStatus, error) {
	return platform.PowerStatus{}, platform.ErrUnsupported
}
func (*Platform) OpenWifi() (platform.Wifi, error) { return nil, platform.ErrUnsupported }
func (*Platform) RelaunchElevated([]string) error  { return platform.ErrUnsupported }
