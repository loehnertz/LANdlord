package store

import (
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/record"
)

var t0 = time.Date(2026, 9, 14, 18, 30, 0, 0, time.UTC)

func readAll(t *testing.T, s *Session) []record.Record {
	t.Helper()
	var out []record.Record
	if err := s.ReadRecords(func(r record.Record) error { out = append(out, r); return nil }); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWriteFlushRead(t *testing.T) {
	s, err := Create(t.TempDir(), t0, 48*time.Hour, "test")
	if err != nil {
		t.Fatal(err)
	}
	w := s.NewWriter("records", 0)
	for i := range 3 {
		w.Emit(record.Metric(record.CPing, record.NEcho, record.TGateway, t0.Add(time.Duration(i)*time.Second), map[string]float64{"rtt_ms": float64(i)}))
	}
	// Records crossing into the next hour go to a second file.
	w.Emit(record.Metric(record.CPing, record.NEcho, record.TGateway, t0.Add(40*time.Minute), map[string]float64{"rtt_ms": 9}))
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	// Read while the writer is still open: the current gzip member has no footer yet.
	if got := len(readAll(t, s)); got != 4 {
		t.Fatalf("read %d records before close, want 4", got)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := len(readAll(t, s)); got != 4 {
		t.Fatalf("read %d records after close, want 4", got)
	}
}

func TestResumeAndMarks(t *testing.T) {
	root := t.TempDir()
	s1, resumed, err := OpenOrResume(root, t0, 48*time.Hour, "test")
	if err != nil || resumed {
		t.Fatalf("first open: resumed=%v err=%v", resumed, err)
	}
	if err := s1.AddMark(Mark{Time: t0.Add(time.Hour), Tag: "call"}); err != nil {
		t.Fatal(err)
	}
	w := s1.NewWriter("records", 0)
	w.Emit(record.Event(record.CApp, record.NStart, t0, nil))
	_ = w.Flush() // simulate a crash: no Close

	s2, resumed, err := OpenOrResume(root, t0.Add(3*time.Hour), 48*time.Hour, "test")
	if err != nil || !resumed || s2.Dir() != s1.Dir() {
		t.Fatalf("second open: resumed=%v dir=%s err=%v", resumed, s2.Dir(), err)
	}
	if m := s2.Meta(); len(m.Marks) != 1 || m.Marks[0].Tag != "call" {
		t.Fatalf("marks not persisted: %+v", m.Marks)
	}
	w2 := s2.NewWriter("records", 0)
	w2.Emit(record.Event(record.CApp, record.NStart, t0.Add(3*time.Hour), nil))
	if err := w2.Close(); err != nil {
		t.Fatal(err)
	}
	if got := len(readAll(t, s2)); got != 2 {
		t.Fatalf("read %d records across runs, want 2", got)
	}

	// Past the duration, or finished, starts a new session.
	if err := s2.Update(func(m *Meta) { m.Finished = true; m.FinishedAt = t0.Add(4 * time.Hour) }); err != nil {
		t.Fatal(err)
	}
	s3, resumed, err := OpenOrResume(root, t0.Add(5*time.Hour), 48*time.Hour, "test")
	if err != nil || resumed || s3.Dir() == s1.Dir() {
		t.Fatalf("after finish: resumed=%v err=%v", resumed, err)
	}
}

func TestDownsampleWhenOverCap(t *testing.T) {
	s, err := Create(t.TempDir(), t0, time.Hour, "test")
	if err != nil {
		t.Fatal(err)
	}
	w := s.NewWriter("records", 1) // 1 byte cap: exceeded after the first flush
	w.Emit(record.Event(record.CApp, record.NStart, t0, nil))
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		w.Emit(record.Metric(record.CPing, record.NEcho, record.TGateway, t0.Add(time.Duration(i+1)*time.Second), map[string]float64{"rtt_ms": 1}))
	}
	w.Emit(record.Event(record.CWifi, record.NRoam, t0.Add(20*time.Second), nil))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var echo, downsampled, roam int
	for _, r := range readAll(t, s) {
		switch r.Name {
		case record.NEcho:
			echo++
		case record.NDownsampled:
			downsampled++
		case record.NRoam:
			roam++
		}
	}
	if echo != 2 || downsampled != 1 || roam != 1 {
		t.Fatalf("echo=%d downsampled=%d roam=%d, want 2/1/1", echo, downsampled, roam)
	}
}

func TestMetaEndAndLocation(t *testing.T) {
	m := Meta{Start: t0, Duration: 2 * time.Hour, TZOffsetSec: 7200}
	if !m.End().Equal(t0.Add(2 * time.Hour)) {
		t.Fatal("End without finish wrong")
	}
	m.Finished, m.FinishedAt = true, t0.Add(time.Hour)
	if !m.End().Equal(t0.Add(time.Hour)) {
		t.Fatal("End with finish wrong")
	}
	if h := t0.In(m.Location()).Hour(); h != 20 {
		t.Fatalf("local hour = %d, want 20", h)
	}
}
