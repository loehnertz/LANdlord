//go:build !windows

package app

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

func waitForProcessExit(ctx context.Context, pid int) {
	for ctx.Err() == nil && processAlive(pid) {
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission)
}
