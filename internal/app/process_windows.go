//go:build windows

package app

import (
	"context"

	"golang.org/x/sys/windows"
)

// waitForProcessExit returns when the process exits or ctx is done.
func waitForProcessExit(ctx context.Context, pid int) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return // already gone
	}
	defer windows.CloseHandle(h)
	for ctx.Err() == nil {
		if ev, _ := windows.WaitForSingleObject(h, 1000); ev == windows.WAIT_OBJECT_0 {
			return
		}
	}
}

func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	ev, _ := windows.WaitForSingleObject(h, 0)
	return ev == uint32(windows.WAIT_TIMEOUT)
}
