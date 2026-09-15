//go:build windows

package winplat

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"

	"github.com/loehnertz/LANdlord/internal/platform"
)

// RelaunchElevated starts this executable again with the "runas" verb, which shows the admin prompt.
func (*Platform) RelaunchElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	params, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(args))
	if err != nil {
		return err
	}
	err = windows.ShellExecute(0, verb, file, params, nil, windows.SW_HIDE)
	if errors.Is(err, windows.ERROR_CANCELLED) {
		return platform.ErrDeclined
	}
	return err
}
