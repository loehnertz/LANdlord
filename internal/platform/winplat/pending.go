//go:build windows

package winplat

import "github.com/loehnertz/LANdlord/internal/platform"

// Placeholder until the elevated helper lands (Task 26).

func (*Platform) RelaunchElevated([]string) error { return platform.ErrUnsupported }
