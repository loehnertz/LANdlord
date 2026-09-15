//go:build !windows

package app

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// acquireInstanceLock takes an exclusive flock on the lock file; it is released when the file closes.
func acquireInstanceLock(dataDir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dataDir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errInstanceRunning
		}
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}
