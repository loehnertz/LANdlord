// Package record defines the measurement record every collector emits and
// every analysis step consumes.
package record

import (
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type Kind string

const (
	KindMetric      Kind = "metric"
	KindEvent       Kind = "event"
	KindInfo        Kind = "info"
	KindUnavailable Kind = "unavailable"
)

type Record struct {
	Time      time.Time          `json:"t"`
	Collector string             `json:"c"`
	Kind      Kind               `json:"k"`
	Name      string             `json:"n,omitempty"`
	Target    string             `json:"tg,omitempty"`
	Values    map[string]float64 `json:"v,omitempty"`
	Attrs     map[string]string  `json:"a,omitempty"`
}

type Sink interface {
	Emit(Record)
}

type SinkFunc func(Record)

func (f SinkFunc) Emit(r Record) { f(r) }

// Buffer is an in-memory Sink, mainly for tests.
type Buffer struct {
	mu   sync.Mutex
	recs []Record
}

func (b *Buffer) Emit(r Record) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.recs = append(b.recs, r)
}

func (b *Buffer) Records() []Record {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Record(nil), b.recs...)
}

// Filter returns records of the collector whose Name matches; an empty name matches all.
func (b *Buffer) Filter(collector, name string) []Record {
	var out []Record
	for _, r := range b.Records() {
		if r.Collector == collector && (name == "" || r.Name == name) {
			out = append(out, r)
		}
	}
	return out
}

func Metric(collector, name, target string, t time.Time, v map[string]float64) Record {
	return Record{Time: t, Collector: collector, Kind: KindMetric, Name: name, Target: target, Values: v}
}

func Event(collector, name string, t time.Time, attrs map[string]string) Record {
	return Record{Time: t, Collector: collector, Kind: KindEvent, Name: name, Attrs: attrs}
}

func Info(collector, name, target string, t time.Time, attrs map[string]string) Record {
	return Record{Time: t, Collector: collector, Kind: KindInfo, Name: name, Target: target, Attrs: attrs}
}

func Unavailable(collector string, t time.Time, reason string) Record {
	return Record{Time: t, Collector: collector, Kind: KindUnavailable, Name: collector, Attrs: map[string]string{"reason": reason}}
}

func HopTarget(n int) string { return fmt.Sprintf("hop%d", n) }

func InetTarget(a netip.Addr) string {
	if a.Is4() || a.Is4In6() {
		return "inet:" + a.Unmap().String()
	}
	return "inet6:" + a.String()
}

func IsInetTarget(t string) bool  { return strings.HasPrefix(t, "inet:") }
func IsInet6Target(t string) bool { return strings.HasPrefix(t, "inet6:") }
