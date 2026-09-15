package app

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/loehnertz/LANdlord/internal/record"
	"github.com/loehnertz/LANdlord/internal/store"
)

// Inspect prints a summary of a stored session: how many records each collector produced and
// why collectors were unavailable. It's meant for checking a recording without opening the report.
func Inspect(w io.Writer, sessionDir string) error {
	sess, err := store.Open(sessionDir)
	if err != nil {
		return err
	}
	meta := sess.Meta()
	counts := map[string]int{}
	var unavailable []string
	var first, last string
	err = sess.ReadRecords(func(r record.Record) error {
		ts := r.Time.In(meta.Location()).Format("2006-01-02 15:04:05")
		if first == "" || ts < first {
			first = ts
		}
		if ts > last {
			last = ts
		}
		switch r.Kind {
		case record.KindUnavailable:
			unavailable = append(unavailable, fmt.Sprintf("%s: %s", r.Collector, r.Attrs["reason"]))
		default:
			counts[r.Collector+"/"+r.Name+" ("+string(r.Kind)+")"]++
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "session  %s (version %s)\n", meta.ID, meta.Version)
	fmt.Fprintf(w, "records  %s to %s\n", first, last)
	fmt.Fprintf(w, "finished %v, marks %d, report %s\n\n", meta.Finished, len(meta.Marks), meta.ReportPath)
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "%8d  %s\n", counts[k], k)
	}
	if len(unavailable) > 0 {
		slices.Sort(unavailable)
		fmt.Fprintf(w, "\nunavailable:\n  %s\n", strings.Join(slices.Compact(unavailable), "\n  "))
	}
	return nil
}
