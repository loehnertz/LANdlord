package store

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/loehnertz/LANdlord/internal/record"
)

// ReadRecords calls fn for every readable record in the session. Records are grouped
// by file, not globally sorted. Truncated files (after a crash or while a writer is
// still open) yield everything up to the truncation.
func (s *Session) ReadRecords(fn func(record.Record) error) error {
	files, err := filepath.Glob(filepath.Join(s.dir, "*.jsonl.gz"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, p := range files {
		if err := readFile(p, fn); err != nil {
			return err
		}
	}
	return nil
}

func readFile(path string, fn func(record.Record) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil // empty or header-only file
		}
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	sc := bufio.NewScanner(gz)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var r record.Record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, gzip.ErrChecksum) {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return nil
}
