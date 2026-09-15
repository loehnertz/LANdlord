package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/config"
)

func TestSimulateAndRebuildReport(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 14, 20, 0, 0, 0, time.UTC)
	path, err := Simulate("access_line", dir, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Access line, building wiring or provider access") {
		t.Fatal("simulated report doesn't name the access line")
	}

	sessions, err := filepath.Glob(filepath.Join(dir, "data", "sessions", "*"))
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %v, err = %v", sessions, err)
	}
	redactedPath := filepath.Join(dir, "redacted.html")
	if err := RebuildReport(sessions[0], redactedPath, true, config.Default().Thresholds); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(redactedPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "203.0.113.7") {
		t.Fatal("redacted report contains the public IP")
	}

	var out strings.Builder
	if err := Inspect(&out, sessions[0]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ping/echo (metric)") || !strings.Contains(out.String(), "eventlog: simulated session") {
		t.Fatalf("inspect output:\n%s", out.String())
	}
}

func TestSimulateUnknownScenario(t *testing.T) {
	_, err := Simulate("nope", t.TempDir(), 1, time.Now())
	if err == nil || !strings.Contains(err.Error(), "weak_wifi") {
		t.Fatalf("expected error listing scenarios, got %v", err)
	}
}
