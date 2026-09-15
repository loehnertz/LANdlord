// Package ping measures round-trip time and loss to the router, the provider's first hops
// and internet targets, one echo per target per interval.
package ping

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/loehnertz/LANdlord/internal/collect"
	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
)

// HopSet holds the provider hops discovered by traceroute.
type HopSet struct {
	mu   sync.Mutex
	hops []netip.Addr
}

func (h *HopSet) Set(hops []netip.Addr) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hops = append([]netip.Addr(nil), hops...)
}

func (h *HopSet) Get() []netip.Addr {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]netip.Addr(nil), h.hops...)
}

type Config struct {
	Targets4, Targets6 []netip.Addr
	Interval, Timeout  time.Duration
	// RouteRefresh is how often the default route (gateway, IPv6 availability) is re-read.
	RouteRefresh time.Duration
}

type Collector struct {
	p    platform.Platform
	cfg  Config
	hops *HopSet
}

func New(p platform.Platform, cfg Config, hops *HopSet) *Collector {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = time.Second
	}
	if cfg.RouteRefresh <= 0 {
		cfg.RouteRefresh = 30 * time.Second
	}
	if hops == nil {
		hops = &HopSet{}
	}
	return &Collector{p: p, cfg: cfg, hops: hops}
}

func (c *Collector) Name() string { return record.CPing }

type target struct {
	label string
	addr  netip.Addr
	delay time.Duration
}

func (t target) key() string { return t.label + "|" + t.addr.String() }

func (c *Collector) Run(ctx context.Context, sink record.Sink) error {
	probe, err := c.p.NewPinger(4)
	if err != nil {
		if errors.Is(err, platform.ErrUnsupported) {
			return fmt.Errorf("%w: %w", collect.ErrPermanent, err)
		}
		return err
	}
	probe.Close()

	var wg sync.WaitGroup
	workers := map[string]context.CancelFunc{}
	defer func() {
		for _, cancel := range workers {
			cancel()
		}
		wg.Wait()
	}()

	var route platform.Route
	var routeAt time.Time
	reconcile := func() {
		if time.Since(routeAt) >= c.cfg.RouteRefresh {
			if r, err := c.p.DefaultRoute(); err == nil {
				route = r
			}
			routeAt = time.Now()
		}
		want := map[string]target{}
		for _, t := range c.targets(route) {
			want[t.key()] = t
		}
		for key, cancel := range workers {
			if _, ok := want[key]; !ok {
				cancel()
				delete(workers, key)
			}
		}
		for key, t := range want {
			if _, ok := workers[key]; ok {
				continue
			}
			wctx, cancel := context.WithCancel(ctx)
			workers[key] = cancel
			wg.Go(func() { c.worker(wctx, sink, t) })
		}
	}

	reconcile()
	tick := time.NewTicker(min(5*time.Second, c.cfg.RouteRefresh))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			reconcile()
		}
	}
}

func (c *Collector) targets(route platform.Route) []target {
	var out []target
	if route.Gateway4.IsValid() {
		out = append(out, target{label: record.TGateway, addr: route.Gateway4})
	}
	hasIPv6 := route.Gateway6.IsValid()
	if hasIPv6 {
		out = append(out, target{label: record.TGateway6, addr: route.Gateway6})
	}
	for i, h := range c.hops.Get() {
		if i >= 3 {
			break
		}
		out = append(out, target{label: record.HopTarget(i + 1), addr: h})
	}
	internet := append([]netip.Addr(nil), c.cfg.Targets4...)
	// Without an IPv6 route every IPv6 echo would be lost and distort the internet median.
	if hasIPv6 {
		internet = append(internet, c.cfg.Targets6...)
	}
	for i, a := range internet {
		out = append(out, target{label: record.InetTarget(a), addr: a, delay: c.cfg.Interval * time.Duration(i) / time.Duration(len(internet))})
	}
	return out
}

func (c *Collector) worker(ctx context.Context, sink record.Sink, t target) {
	family := 4
	if t.addr.Is6() && !t.addr.Is4In6() {
		family = 6
	}
	pg, err := c.p.NewPinger(family)
	if err != nil {
		return
	}
	defer pg.Close()
	if t.delay > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(t.delay):
		}
	}
	tick := time.NewTicker(c.cfg.Interval)
	defer tick.Stop()
	for {
		now := time.Now()
		reply, err := pg.Echo(ctx, platform.EchoRequest{Dst: t.addr, Timeout: c.cfg.Timeout})
		if ctx.Err() != nil {
			return
		}
		attrs := map[string]string{"ip": t.addr.String()}
		values := map[string]float64{"lost": 1}
		switch {
		case err != nil:
			attrs["error"] = err.Error()
		case reply.Status == platform.EchoOK:
			values = map[string]float64{"rtt_ms": float64(reply.RTT.Microseconds()) / 1000}
		}
		sink.Emit(record.Record{Time: now, Collector: record.CPing, Kind: record.KindMetric, Name: record.NEcho, Target: t.label, Values: values, Attrs: attrs})
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
