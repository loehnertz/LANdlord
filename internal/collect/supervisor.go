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
	ctx        context.Context
	stopped    bool
	wg         sync.WaitGroup
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
	s.ctx = ctx
	for _, c := range s.collectors {
		s.wg.Go(func() { s.loop(ctx, c) })
	}
	s.mu.Unlock()
	<-ctx.Done()
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	s.wg.Wait()
}

// Launch starts a collector while Run is already running, for collectors that depend on
// something discovered during the recording. It does nothing before Run or after it stopped.
func (s *Supervisor) Launch(c Collector) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx == nil || s.stopped || s.ctx.Err() != nil {
		return false
	}
	if _, exists := s.states[c.Name()]; exists {
		return false
	}
	s.collectors = append(s.collectors, c)
	s.states[c.Name()] = &State{Name: c.Name(), Status: StatusStopped}
	ctx := s.ctx
	s.wg.Go(func() { s.loop(ctx, c) })
	return true
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
