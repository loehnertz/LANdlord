//go:build windows

package admin

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// maxWlanReportBytes keeps very large Wi-Fi reports out of the HTML report.
const maxWlanReportBytes = 8 << 20

func (*Collector) supported() error { return nil }

// wlanReport asks Windows to generate its Wi-Fi report and copies it into the session.
func (c *Collector) wlanReport(ctx context.Context) (string, bool) {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, "netsh.exe", "wlan", "show", "wlanreport")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	if err := cmd.Run(); err != nil {
		return "", false
	}
	src := filepath.Join(os.Getenv("ProgramData"), "Microsoft", "Windows", "WlanReport", "wlan-report-latest.html")
	in, err := os.Open(src)
	if err != nil {
		return "", false
	}
	defer in.Close()
	if fi, err := in.Stat(); err != nil || fi.Size() > maxWlanReportBytes {
		return "", false
	}
	out, err := os.Create(filepath.Join(c.sessionDir, WlanReportFile))
	if err != nil {
		return "", false
	}
	_, copyErr := io.Copy(out, in)
	if err := out.Close(); err != nil || copyErr != nil {
		return "", false
	}
	return WlanReportFile, true
}
