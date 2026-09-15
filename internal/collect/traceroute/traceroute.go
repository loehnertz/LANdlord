// Package traceroute discovers the path to the internet with ICMP and feeds the provider's
// first hops to the ping collector.
package traceroute

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/loehnertz/LANdlord/internal/collect"
	"github.com/loehnertz/LANdlord/internal/collect/ping"
	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
)

const (
	defaultMaxHops = 30
	attemptsPerHop = 3
)

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// Trace returns one entry per TTL; an invalid address means no hop answered.
func Trace(ctx context.Context, pg platform.Pinger, dst netip.Addr, maxHops int, timeout time.Duration) []netip.Addr {
	var path []netip.Addr
	for ttl := 1; ttl <= maxHops; ttl++ {
		var hop netip.Addr
		reached := false
		for range attemptsPerHop {
			reply, err := pg.Echo(ctx, platform.EchoRequest{Dst: dst, TTL: ttl, Timeout: timeout})
			if ctx.Err() != nil {
				return path
			}
			if err != nil {
				continue
			}
			switch reply.Status {
			case platform.EchoTTLExpired:
				hop = reply.From
			case platform.EchoOK:
				hop, reached = dst, true
			case platform.EchoUnreachable:
				hop, reached = reply.From, true
			default:
				continue
			}
			break
		}
		path = append(path, hop)
		if reached {
			break
		}
	}
	return trimTrailingSilence(path)
}

func trimTrailingSilence(path []netip.Addr) []netip.Addr {
	last := -1
	for i, a := range path {
		if a.IsValid() {
			last = i
		}
	}
	// Keep one silent entry after the last answer, so an unfinished trace is visible.
	return path[:min(len(path), last+2)]
}

// FormatPath renders a path as comma-separated addresses with "*" for silent hops.
func FormatPath(path []netip.Addr) string {
	parts := make([]string, len(path))
	for i, a := range path {
		if a.IsValid() {
			parts[i] = a.String()
		} else {
			parts[i] = "*"
		}
	}
	return strings.Join(parts, ",")
}

// comparable drops silent hops, so routers that sometimes don't answer don't count as a route change.
func comparable(path string) string {
	var out []string
	for _, p := range strings.Split(path, ",") {
		if p != "*" && p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ",")
}

// ProviderHops returns up to three public hops before the destination.
func ProviderHops(path []netip.Addr, dst netip.Addr) []netip.Addr {
	var out []netip.Addr
	for _, a := range path {
		if !a.IsValid() || a == dst || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || cgnat.Contains(a) {
			continue
		}
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
		if len(out) == 3 {
			break
		}
	}
	return out
}

type Collector struct {
	p        platform.Platform
	dests    []netip.Addr
	hops     *ping.HopSet
	interval time.Duration
	timeout  time.Duration
	maxHops  int
	trigger  chan struct{}
	last     map[netip.Addr]string
}

func New(p platform.Platform, dests []netip.Addr, hops *ping.HopSet, interval time.Duration) *Collector {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &Collector{p: p, dests: dests, hops: hops, interval: interval, timeout: time.Second, maxHops: defaultMaxHops,
		trigger: make(chan struct{}, 1), last: map[netip.Addr]string{}}
}

func (c *Collector) Name() string { return record.CTrace }

// Trigger requests an immediate trace, for example when the user marks a problem.
func (c *Collector) Trigger() {
	select {
	case c.trigger <- struct{}{}:
	default:
	}
}

func (c *Collector) Run(ctx context.Context, sink record.Sink) error {
	pg, err := c.p.NewPinger(4)
	if err != nil {
		if errors.Is(err, platform.ErrUnsupported) {
			return fmt.Errorf("%w: %w", collect.ErrPermanent, err)
		}
		return err
	}
	defer pg.Close()
	tick := time.NewTicker(c.interval)
	defer tick.Stop()
	for {
		c.traceAll(ctx, pg, sink)
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		case <-c.trigger:
		}
	}
}

func (c *Collector) traceAll(ctx context.Context, pg platform.Pinger, sink record.Sink) {
	for i, dst := range c.dests {
		path := Trace(ctx, pg, dst, c.maxHops, c.timeout)
		if ctx.Err() != nil {
			return
		}
		formatted := FormatPath(path)
		now := time.Now()
		sink.Emit(record.Record{Time: now, Collector: record.CTrace, Kind: record.KindInfo, Name: record.NRoute, Target: dst.String(),
			Values: map[string]float64{"hops": float64(len(path))}, Attrs: map[string]string{"path": formatted}})
		if prev, ok := c.last[dst]; ok && comparable(prev) != comparable(formatted) {
			sink.Emit(record.Record{Time: now, Collector: record.CTrace, Kind: record.KindEvent, Name: record.NRouteChange, Target: dst.String(),
				Attrs: map[string]string{"old": prev, "new": formatted}})
		}
		c.last[dst] = formatted
		if i == 0 && c.hops != nil {
			if hops := ProviderHops(path, dst); len(hops) > 0 {
				c.hops.Set(hops)
			}
		}
	}
}

// hopCount is a small helper for tests and logs.
func hopCount(path string) string { return strconv.Itoa(len(strings.Split(path, ","))) }
