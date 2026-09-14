// Package store persists a recording session: metadata plus hourly gzip JSONL record files.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const metaFile = "meta.json"

var ErrNotFound = errors.New("store: no session found")

type Mark struct {
	Time time.Time `json:"t"`
	Tag  string    `json:"tag,omitempty"`
}

type Meta struct {
	ID           string        `json:"id"`
	Version      string        `json:"version"`
	Start        time.Time     `json:"start"`
	Duration     time.Duration `json:"duration"`
	TZOffsetSec  int           `json:"tz_offset_sec"`
	Finished     bool          `json:"finished"`
	FinishedAt   time.Time     `json:"finished_at,omitzero"`
	HelperAsked  bool          `json:"helper_asked"`
	ContractMbps float64       `json:"contract_mbps,omitempty"`
	ReportPath   string        `json:"report_path,omitempty"`
	Marks        []Mark        `json:"marks,omitempty"`
}

// End is when the session stops (or stopped) recording.
func (m Meta) End() time.Time {
	if m.Finished {
		return m.FinishedAt
	}
	return m.Start.Add(m.Duration)
}

// Location is the UTC offset the session was recorded in, for local-time display and hour-of-day rules.
func (m Meta) Location() *time.Location {
	return time.FixedZone("session", m.TZOffsetSec)
}

type Session struct {
	dir  string
	mu   sync.Mutex
	meta Meta
}

func Create(root string, now time.Time, d time.Duration, version string) (*Session, error) {
	_, off := now.Zone()
	m := Meta{
		ID:          now.UTC().Format("20060102-150405"),
		Version:     version,
		Start:       now.UTC(),
		Duration:    d,
		TZOffsetSec: off,
	}
	dir := filepath.Join(root, "sessions", m.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}
	s := &Session{dir: dir, meta: m}
	return s, s.save()
}

func Open(dir string) (*Session, error) {
	b, err := os.ReadFile(filepath.Join(dir, metaFile))
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}
	s := &Session{dir: dir}
	if err := json.Unmarshal(b, &s.meta); err != nil {
		return nil, fmt.Errorf("parse %s: %w", metaFile, err)
	}
	return s, nil
}

func Latest(root string) (*Session, error) {
	entries, err := os.ReadDir(filepath.Join(root, "sessions"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	if len(ids) == 0 {
		return nil, ErrNotFound
	}
	sort.Strings(ids)
	return Open(filepath.Join(root, "sessions", ids[len(ids)-1]))
}

// OpenOrResume continues the latest session if it is unfinished and still within its duration.
func OpenOrResume(root string, now time.Time, d time.Duration, version string) (*Session, bool, error) {
	s, err := Latest(root)
	switch {
	case err == nil:
		if m := s.Meta(); !m.Finished && now.Before(m.Start.Add(m.Duration)) {
			return s, true, nil
		}
	case !errors.Is(err, ErrNotFound):
		return nil, false, err
	}
	s, err = Create(root, now, d, version)
	return s, false, err
}

func (s *Session) Dir() string { return s.dir }

func (s *Session) Meta() Meta {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.meta
	m.Marks = append([]Mark(nil), s.meta.Marks...)
	return m
}

func (s *Session) Update(fn func(*Meta)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.meta)
	return s.save()
}

func (s *Session) AddMark(m Mark) error {
	return s.Update(func(meta *Meta) { meta.Marks = append(meta.Marks, m) })
}

// save writes meta.json atomically. Callers hold s.mu (or own s exclusively).
func (s *Session) save() error {
	b, err := json.MarshalIndent(s.meta, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, metaFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
