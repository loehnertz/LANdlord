package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/collect"
	"github.com/loehnertz/LANdlord/internal/config"
	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
	"github.com/loehnertz/LANdlord/internal/store"
	"github.com/loehnertz/LANdlord/internal/ui"
)

// fakePings emits weak-signal problems: every echo to the gateway and internet is slow and lossy.
type fakePings struct{}

func (fakePings) Name() string { return record.CPing }

func (fakePings) Run(ctx context.Context, sink record.Sink) error {
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	i := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		now := time.Now()
		i++
		for _, target := range []string{record.TGateway, "inet:1.1.1.1", "inet:8.8.8.8", "inet:9.9.9.9"} {
			v := map[string]float64{"rtt_ms": 250}
			if i%3 == 0 {
				v = map[string]float64{"lost": 1}
			}
			sink.Emit(record.Metric(record.CPing, record.NEcho, target, now, v))
		}
		sink.Emit(record.Record{Time: now, Collector: record.CWifi, Kind: record.KindMetric, Name: record.NLink,
			Values: map[string]float64{"connected": 1, "rssi_dbm": -80, "rx_mbps": 20}, Attrs: map[string]string{"band": "5"}})
	}
}

func TestRunMarkFinish(t *testing.T) {
	dir := t.TempDir()
	fake := &platform.Fake{Docs: filepath.Join(dir, "Documents"), Data: filepath.Join(dir, "data")}
	cfg := config.Default()
	cfg.Duration = config.Duration{Duration: time.Hour}

	var rec *Recorder
	ready := make(chan struct{})
	opts := RunOptions{
		NoBrowser: true, NoTray: true, NoHelper: true,
		LingerAfterFinish: 50 * time.Millisecond,
		AnalyzeEvery:      50 * time.Millisecond,
		Collectors: func(r *Recorder) []collect.Collector {
			rec = r
			close(ready)
			return []collect.Collector{fakePings{}}
		},
	}
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), cfg, fake, opts) }()
	<-ready

	// The status page answers with the token from running.json.
	var info runningInfo
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := os.ReadFile(filepath.Join(fake.Data, runningFile))
		if err == nil && json.Unmarshal(b, &info) == nil && info.BaseURL != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("running.json not written")
		}
		time.Sleep(10 * time.Millisecond)
	}
	req, _ := http.NewRequest(http.MethodGet, info.BaseURL+"/api/status", nil)
	req.Header.Set("X-Landlord-Token", info.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var st ui.Status
	_ = json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if !st.Recording || st.RemainingSeconds <= 0 {
		t.Fatalf("status = %+v", st)
	}
	if url, ok := existingInstance(fake.Data); ok || url != "" {
		// Same process: existingInstance ignores its own PID.
		t.Fatalf("existingInstance matched the current process: %s", url)
	}

	time.Sleep(300 * time.Millisecond)
	if err := rec.Mark("call"); err != nil {
		t.Fatal(err)
	}
	if err := rec.SetContractMbps(100); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for rec.Status().Live.AwakeMinutes == 0 {
		if time.Now().After(deadline) {
			t.Fatal("live verdict never computed")
		}
		time.Sleep(20 * time.Millisecond)
	}

	var soFar strings.Builder
	if err := rec.ReportSoFar(&soFar); err != nil || !strings.Contains(soFar.String(), "Recording still in progress") {
		t.Fatalf("report so far: %v", err)
	}

	path, err := rec.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if !strings.HasPrefix(path, filepath.Join(fake.Docs, "LANdlord")) {
		t.Fatalf("report path = %s", path)
	}
	html, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "LANdlord inspection report") || len(fake.Revealed) != 0 {
		t.Fatalf("report not written, or revealed despite NoBrowser (revealed %v)", fake.Revealed)
	}
	sess, err := store.Latest(fake.Data)
	if err != nil {
		t.Fatal(err)
	}
	meta := sess.Meta()
	if !meta.Finished || len(meta.Marks) != 1 || meta.Marks[0].Tag != "call" || meta.ContractMbps != 100 || meta.ReportPath != path {
		t.Fatalf("meta = %+v", meta)
	}
	if _, err := os.Stat(filepath.Join(fake.Data, runningFile)); !os.IsNotExist(err) {
		t.Fatal("running.json not removed")
	}
	if err := rec.Mark("late"); err == nil {
		t.Fatal("marking after finish should fail")
	}
}

func TestPauseKeepsSessionResumable(t *testing.T) {
	dir := t.TempDir()
	fake := &platform.Fake{Docs: dir, Data: filepath.Join(dir, "data"), RouteValue: platform.Route{Gateway4: netip.MustParseAddr("192.168.1.1")}}
	cfg := config.Default()
	ctx, cancel := context.WithCancel(context.Background())
	opts := RunOptions{NoBrowser: true, NoTray: true, NoHelper: true,
		Collectors: func(*Recorder) []collect.Collector { return []collect.Collector{fakePings{}} }}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, fake, opts) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	sess, err := store.Latest(fake.Data)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Meta().Finished {
		t.Fatal("paused session should not be finished")
	}
	n := 0
	_ = sess.ReadRecords(func(record.Record) error { n++; return nil })
	if n == 0 {
		t.Fatal("no records flushed on pause")
	}
	if _, resumed, err := store.OpenOrResume(fake.Data, time.Now(), cfg.Duration.Duration, "test"); err != nil || !resumed {
		t.Fatalf("resume = %v, %v", resumed, err)
	}
	_ = io.Discard
}
