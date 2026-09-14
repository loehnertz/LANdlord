// Package collect runs measurement collectors and keeps them alive.
package collect

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/loehnertz/LANdlord/internal/record"
)

// ErrPermanent marks a collector that can't work in this environment. Wrap it with a
// reason: fmt.Errorf("%w: no Wi-Fi adapter", collect.ErrPermanent).
var ErrPermanent = errors.New("collector cannot run")

type Collector interface {
	Name() string
	Run(ctx context.Context, sink record.Sink) error
}

const (
	StatusRunning     = "running"
	StatusBackoff     = "backoff"
	StatusUnavailable = "unavailable"
	StatusStopped     = "stopped"
)

type State struct {
	Name, Status, LastError string
	Restarts                int
}

type Supervisor struct {
	sink record.Sink
	now  func() time.Time

	BaseBackoff, MaxBackoff, HealthyAfter time.Duration

	mu         sync.Mutex
	collectors []Collector
	states     map[string]*State
}

func NewSupervisor(sink record.Sink, now func() time.Time) *Supervisor {
	if now == nil {
		now = time.Now
	}
	return &Supervisor{
		sink:         sink,
		now:          now,
		BaseBackoff:  time.Second,
		MaxBackoff:   5 * time.Minute,
		HealthyAfter: 10 * time.Minute,
		states:       map[string]*State{},
	}
}

func (s *Supervisor) Add(c Collector) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.collectors = append(s.collectors, c)
	s.states[c.Name()] = &State{Name: c.Name(), Status: StatusStopped}
}

// Run starts every collector and blocks until ctx is done and all of them have returned.
func (s *Supervisor) Run(ctx context.Context) {
	s.mu.Lock()
	collectors := slices.Clone(s.collectors)
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, c := range collectors {
		wg.Go(func() { s.loop(ctx, c) })
	}
	wg.Wait()
}

func (s *Supervisor) loop(ctx context.Context, c Collector) {
	name := c.Name()
	backoff := s.BaseBackoff
	for {
		s.update(name, func(st *State) { st.Status = StatusRunning })
		started := time.Now()
		err := s.runOnce(ctx, c)
		if ctx.Err() != nil {
			s.update(name, func(st *State) { st.Status = StatusStopped })
			return
		}
		if err == nil {
			err = errors.New("collector stopped unexpectedly")
		}
		if errors.Is(err, ErrPermanent) {
			reason := strings.TrimPrefix(err.Error(), ErrPermanent.Error()+": ")
			s.sink.Emit(record.Unavailable(name, s.now(), reason))
			s.update(name, func(st *State) { st.Status, st.LastError = StatusUnavailable, reason })
			return
		}
		if time.Since(started) >= s.HealthyAfter {
			backoff = s.BaseBackoff
		}
		s.update(name, func(st *State) {
			st.Status, st.LastError = StatusBackoff, err.Error()
			st.Restarts++
		})
		select {
		case <-ctx.Done():
			s.update(name, func(st *State) { st.Status = StatusStopped })
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, s.MaxBackoff)
	}
}

func (s *Supervisor) runOnce(ctx context.Context, c Collector) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return c.Run(ctx, s.sink)
}

func (s *Supervisor) update(name string, fn func(*State)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.states[name])
}

// States returns a snapshot of every collector's status, sorted by name.
func (s *Supervisor) States() []State {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]State, 0, len(s.states))
	for _, st := range s.states {
		out = append(out, *st)
	}
	slices.SortFunc(out, func(a, b State) int { return strings.Compare(a.Name, b.Name) })
	return out
}
