package app

import (
	"errors"
	"testing"
)

func TestInstanceLockIsExclusive(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireInstanceLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireInstanceLock(dir); !errors.Is(err, errInstanceRunning) {
		t.Fatalf("second lock: err = %v, want errInstanceRunning", err)
	}
	release()
	again, err := acquireInstanceLock(dir)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again()
}
