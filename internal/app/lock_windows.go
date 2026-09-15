//go:build windows

package app

import (
	"errors"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// acquireInstanceLock opens the lock file without sharing; a second process gets a sharing violation.
func acquireInstanceLock(dataDir string) (func(), error) {
	path, err := windows.UTF16PtrFromString(filepath.Join(dataDir, lockFile))
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return nil, errInstanceRunning
		}
		return nil, err
	}
	return func() { _ = windows.CloseHandle(h) }, nil
}
