//go:build windows

// Package winplat implements platform.Platform with Windows APIs.
package winplat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/loehnertz/LANdlord/internal/platform"
)

type Platform struct{}

var _ platform.Platform = (*Platform)(nil)

func New() *Platform { return &Platform{} }

func (*Platform) IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func (*Platform) OpenURL(url string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	target, err := windows.UTF16PtrFromString(url)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL)
}

func (*Platform) RevealFile(path string) error {
	cmd := exec.Command("explorer.exe")
	// explorer.exe parses its own command line; quoting the path as one argument keeps spaces intact.
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + path + `"`}
	return cmd.Start()
}

func (*Platform) DocumentsDir() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Documents, 0)
}

func (*Platform) DataDir() (string, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "LANdlord"), nil
}

// hiddenCommand runs a console tool without flashing a console window.
func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	cmd.Env = os.Environ()
	return cmd
}

var _ = hiddenCommand

// The methods below are filled in by later tasks.

func (*Platform) KeepAwake(context.Context) error { return platform.ErrUnsupported }
