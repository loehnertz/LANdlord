//go:build !windows

// Package unixplat implements platform.Platform for macOS and Linux. It covers what the
// analysis-side development and internet measurements need; Wi-Fi details are Windows-only.
package unixplat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/loehnertz/LANdlord/internal/platform"
)

type Platform struct{}

var _ platform.Platform = (*Platform)(nil)

func New() *Platform { return &Platform{} }

func (*Platform) IsElevated() bool { return os.Geteuid() == 0 }

func (*Platform) RelaunchElevated([]string) error { return platform.ErrUnsupported }

func (*Platform) OpenURL(url string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", url).Start()
	}
	return exec.Command("xdg-open", url).Start()
}

func (*Platform) RevealFile(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", "-R", path).Start()
	}
	return exec.Command("xdg-open", filepath.Dir(path)).Start()
}

func (*Platform) DocumentsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if docs := filepath.Join(home, "Documents"); isDir(docs) {
		return docs, nil
	}
	return home, nil
}

func (*Platform) DataDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "LANdlord"), nil
}

func (*Platform) KeepAwake(context.Context) error { return platform.ErrUnsupported }

func (*Platform) Power() (platform.PowerStatus, error) {
	return platform.PowerStatus{}, platform.ErrUnsupported
}

func (*Platform) OpenWifi() (platform.Wifi, error) { return nil, platform.ErrUnsupported }

func (*Platform) Adapter(platform.Route) (platform.AdapterInfo, error) {
	return platform.AdapterInfo{}, platform.ErrUnsupported
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
