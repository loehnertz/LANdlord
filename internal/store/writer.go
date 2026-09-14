package store

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/loehnertz/LANdlord/internal/record"
)

// Writer appends records to hourly gzip JSONL files. Each Writer instance uses its own
// files (the name includes its creation time), so a resumed run never appends to a file
// a crashed run left truncated.
type Writer struct {
	dir      string
	prefix   string
	runID    int64
	maxBytes int64

	mu         sync.Mutex
	hour       string
	f          *os.File
	gz         *gzip.Writer
	enc        *json.Encoder
	downsample bool
	echoSeen   uint64
	err        error
}

func (s *Session) NewWriter(prefix string, maxBytes int64) *Writer {
	return &Writer{dir: s.dir, prefix: prefix, runID: time.Now().UnixNano(), maxBytes: maxBytes}
}

func (w *Writer) Emit(r record.Record) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.downsample && r.Collector == record.CPing && r.Name == record.NEcho {
		w.echoSeen++
		if w.echoSeen%5 != 0 {
			return
		}
	}
	w.encode(r)
}

func (w *Writer) encode(r record.Record) {
	if err := w.rotate(r.Time); err != nil {
		w.err = errors.Join(w.err, err)
		return
	}
	if err := w.enc.Encode(r); err != nil {
		w.err = errors.Join(w.err, err)
	}
}

func (w *Writer) rotate(t time.Time) error {
	h := t.UTC().Format("2006010215")
	if h == w.hour && w.gz != nil {
		return nil
	}
	if err := w.closeFile(); err != nil {
		return err
	}
	name := fmt.Sprintf("%s-%s-%d.jsonl.gz", w.prefix, h, w.runID)
	f, err := os.OpenFile(filepath.Join(w.dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	w.f, w.gz, w.hour = f, gzip.NewWriter(f), h
	w.enc = json.NewEncoder(w.gz)
	return nil
}

// Flush makes everything emitted so far readable and enforces the size cap.
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.gz != nil {
		if err := w.gz.Flush(); err != nil {
			w.err = errors.Join(w.err, err)
		}
		if err := w.f.Sync(); err != nil {
			w.err = errors.Join(w.err, err)
		}
	}
	if w.maxBytes > 0 && !w.downsample {
		if size, err := dirSize(w.dir); err == nil && size > w.maxBytes {
			w.downsample = true
			w.encode(record.Event(record.CApp, record.NDownsampled, time.Now(), nil))
			if w.gz != nil {
				_ = w.gz.Flush()
			}
		}
	}
	err := w.err
	w.err = nil
	return err
}

// Run flushes every interval until ctx is done, then closes the writer.
func (w *Writer) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = w.Close()
			return
		case <-t.C:
			_ = w.Flush()
		}
	}
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return errors.Join(w.err, w.closeFile())
}

func (w *Writer) closeFile() error {
	if w.gz == nil {
		return nil
	}
	err := errors.Join(w.gz.Close(), w.f.Close())
	w.gz, w.f, w.enc, w.hour = nil, nil, nil, ""
	return err
}

func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}
