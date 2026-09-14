//go:build !windows

package unixplat

import "github.com/loehnertz/LANdlord/internal/platform"

// Placeholder until the unix counters land (Task 18).

func (*Platform) InterfaceCounters(int) (platform.Counters, error) {
	return platform.Counters{}, platform.ErrUnsupported
}
