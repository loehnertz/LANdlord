// Package app wires sessions, collectors, the diagnosis, the report and the UI together.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/config"
	"github.com/loehnertz/LANdlord/internal/diagnose"
	"github.com/loehnertz/LANdlord/internal/report"
	"github.com/loehnertz/LANdlord/internal/sim"
	"github.com/loehnertz/LANdlord/internal/store"
	"github.com/loehnertz/LANdlord/internal/version"
)

// wlanReportFile is where the elevated helper stores Windows' wlanreport inside a session.
const wlanReportFile = "wlanreport.html"

// Simulate records a scenario into a new session under outDir and writes its report.
// The session ends before now, so its local times look realistic.
func Simulate(scenario, outDir string, seed int64, now time.Time) (string, error) {
	sc, ok := sim.Get(scenario)
	if !ok {
		return "", fmt.Errorf("unknown scenario %q (available: %s)", scenario, strings.Join(sim.Names(), ", "))
	}
	start := now.Add(-sc.Duration).Truncate(time.Hour)
	sess, err := store.Create(filepath.Join(outDir, "data"), start, sc.Duration, version.String())
	if err != nil {
		return "", err
	}
	w := sess.NewWriter("records", 0)
	sim.Run(sc, start.UTC(), start.Location(), seed, w)
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("write simulated records: %w", err)
	}
	err = sess.Update(func(m *store.Meta) {
		m.Finished = true
		m.FinishedAt = m.Start.Add(sc.Duration)
		// Minute 31 and minute 1 fall into the episodes most scenarios use.
		m.Marks = []store.Mark{
			{Time: m.Start.Add(91 * time.Minute), Tag: "call"},
			{Time: m.Start.Add(3*time.Hour + time.Minute), Tag: "streaming"},
		}
	})
	if err != nil {
		return "", err
	}
	out := filepath.Join(outDir, "report-"+scenario+".html")
	return out, RebuildReport(sess.Dir(), out, false, config.Default().Thresholds)
}

// RebuildReport reads a stored session and writes its report to outPath.
func RebuildReport(sessionDir, outPath string, redact bool, th config.Thresholds) error {
	sess, err := store.Open(sessionDir)
	if err != nil {
		return err
	}
	meta := sess.Meta()
	s, err := aggregate.Build(meta, aggregate.DefaultWidth, sess.ReadRecords)
	if err != nil {
		return fmt.Errorf("read session: %w", err)
	}
	opts := report.Options{
		Redact:     redact,
		Partial:    !meta.Finished,
		Thresholds: &th,
		WlanReport: readWlanReport(sessionDir),
	}
	return writeReport(outPath, s, diagnose.Analyze(s, th), opts)
}

func readWlanReport(sessionDir string) string {
	b, err := os.ReadFile(filepath.Join(sessionDir, wlanReportFile))
	if err != nil {
		return ""
	}
	return string(b)
}

// writeReport renders into a temporary file first, so a crash never leaves a half-written report.
func writeReport(path string, s *aggregate.Session, r diagnose.Result, opts report.Options) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".report-*.html")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after a successful rename
	if err := report.Render(tmp, s, r, opts); err != nil {
		tmp.Close()
		return fmt.Errorf("render report: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
