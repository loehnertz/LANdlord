package collect

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/record"
)

type funcCollector struct {
	name string
	run  func(ctx context.Context, sink record.Sink) error
}

func (f funcCollector) Name() string                                    { return f.name }
func (f funcCollector) Run(ctx context.Context, sink record.Sink) error { return f.run(ctx, sink) }

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func stateOf(s *Supervisor, name string) State {
	for _, st := range s.States() {
		if st.Name == name {
			return st
		}
	}
	return State{}
}

func TestSupervisor(t *testing.T) {
	var buf record.Buffer
	sup := NewSupervisor(&buf, nil)
	sup.BaseBackoff = time.Millisecond

	var panics atomic.Int32
	sup.Add(funcCollector{"permanent", func(context.Context, record.Sink) error {
		return fmt.Errorf("%w: no Wi-Fi adapter", ErrPermanent)
	}})
	sup.Add(funcCollector{"panicky", func(ctx context.Context, sink record.Sink) error {
		if panics.Add(1) <= 2 {
			panic("boom")
		}
		<-ctx.Done()
		return nil
	}})
	sup.Add(funcCollector{"flaky", func(ctx context.Context, sink record.Sink) error {
		return errors.New("temporary")
	}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sup.Run(ctx); close(done) }()

	waitFor(t, func() bool { return stateOf(sup, "permanent").Status == StatusUnavailable })
	waitFor(t, func() bool { st := stateOf(sup, "panicky"); return st.Status == StatusRunning && st.Restarts == 2 })
	waitFor(t, func() bool { return stateOf(sup, "flaky").Restarts >= 3 })

	unavailable := buf.Filter("permanent", "")
	if len(unavailable) != 1 || unavailable[0].Kind != record.KindUnavailable || unavailable[0].Attrs["reason"] != "no Wi-Fi adapter" {
		t.Fatalf("unavailable records = %+v", unavailable)
	}
	if st := stateOf(sup, "panicky"); st.LastError != "panic: boom" {
		t.Fatalf("panic not recorded: %+v", st)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not stop")
	}
	for _, st := range sup.States() {
		if st.Name != "permanent" && st.Status != StatusStopped {
			t.Fatalf("%s status after cancel = %s", st.Name, st.Status)
		}
	}
}
