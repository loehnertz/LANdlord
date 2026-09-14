// Package system records network adapter counters, power source, sleep gaps and adapter details,
// and keeps the computer awake while recording.
package system

import (
	"context"
	"sync"
	"time"

	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
)

type sample struct {
	at   time.Time
	mbps float64
}

type Collector struct {
	p          platform.Platform
	every      time.Duration
	powerEvery time.Duration
	sleepTick  time.Duration
	sleepGap   time.Duration
	now        func() time.Time

	mu      sync.Mutex
	samples []sample

	// Owned by the Run goroutine.
	lastWall time.Time
}

func New(p platform.Platform, every time.Duration) *Collector {
	if every <= 0 {
		every = 2 * time.Second
	}
	return &Collector{p: p, every: every, powerEvery: 30 * time.Second, sleepTick: 5 * time.Second, sleepGap: 30 * time.Second,
		// Round(0) drops the monotonic reading: monotonic clocks can pause during sleep, wall time doesn't.
		now: func() time.Time { return time.Now().Round(0) }}
}

func (c *Collector) Name() string { return record.CSystem }

func (c *Collector) Run(ctx context.Context, sink record.Sink) error {
	go func() { _ = c.p.KeepAwake(ctx) }()

	var route platform.Route
	var routeOK, adapterSent bool
	var prev platform.Counters
	var prevAt time.Time
	refreshRoute := func() {
		r, err := c.p.DefaultRoute()
		if err != nil {
			return
		}
		if routeOK && r.InterfaceIndex != route.InterfaceIndex {
			prevAt, adapterSent = time.Time{}, false
		}
		route, routeOK = r, true
		if !adapterSent {
			adapterSent = true
			c.emitAdapter(route, sink)
		}
	}
	refreshRoute()
	c.emitPower(sink)
	c.lastWall = c.now()

	ifaceTick := time.NewTicker(c.every)
	defer ifaceTick.Stop()
	powerTick := time.NewTicker(c.powerEvery)
	defer powerTick.Stop()
	sleepTick := time.NewTicker(c.sleepTick)
	defer sleepTick.Stop()
	routeTick := time.NewTicker(30 * time.Second)
	defer routeTick.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-routeTick.C:
			refreshRoute()
		case <-powerTick.C:
			c.emitPower(sink)
		case <-sleepTick.C:
			c.checkSleep(c.now(), sink)
		case <-ifaceTick.C:
			if !routeOK {
				refreshRoute()
				continue
			}
			cur, err := c.p.InterfaceCounters(route.InterfaceIndex)
			if err != nil {
				continue
			}
			now := time.Now()
			if !prevAt.IsZero() && cur.RxBytes >= prev.RxBytes && cur.TxBytes >= prev.TxBytes {
				secs := now.Sub(prevAt).Seconds()
				if secs > 0 {
					rx := float64(cur.RxBytes-prev.RxBytes) * 8 / secs
					tx := float64(cur.TxBytes-prev.TxBytes) * 8 / secs
					sink.Emit(record.Metric(record.CSystem, record.NIface, route.InterfaceName, now, map[string]float64{
						"rx_bps":     rx,
						"tx_bps":     tx,
						"in_errors":  float64(sub(cur.InErrors, prev.InErrors)),
						"out_errors": float64(sub(cur.OutErrors, prev.OutErrors)),
						"discards":   float64(sub(cur.InDiscards, prev.InDiscards) + sub(cur.OutDiscards, prev.OutDiscards)),
						"link_mbps":  float64(cur.LinkBps) / 1e6,
					}))
					c.addSample(now, (rx+tx)/1e6)
				}
			}
			prev, prevAt = cur, now
		}
	}
}

func sub(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}

func (c *Collector) emitAdapter(route platform.Route, sink record.Sink) {
	attrs := map[string]string{
		"name":        route.InterfaceName,
		"description": route.Description,
		"mac":         route.MAC.String(),
		"wireless":    boolString(route.Wireless),
	}
	if info, err := c.p.Adapter(route); err == nil {
		attrs["driver_version"], attrs["driver_date"] = info.DriverVersion, info.DriverDate
	}
	sink.Emit(record.Info(record.CSystem, record.NAdapter, "", time.Now(), attrs))
}

func (c *Collector) emitPower(sink record.Sink) {
	st, err := c.p.Power()
	if err != nil || !st.Known {
		return
	}
	onAC := 0.0
	if st.OnAC {
		onAC = 1
	}
	sink.Emit(record.Metric(record.CSystem, record.NPower, "", time.Now(), map[string]float64{"on_ac": onAC}))
}

// checkSleep emits a sleep event when wall-clock time jumped further than a missed tick explains.
func (c *Collector) checkSleep(now time.Time, sink record.Sink) {
	last := c.lastWall
	c.lastWall = now
	if last.IsZero() {
		return
	}
	if gap := now.Sub(last); gap > c.sleepGap {
		sink.Emit(record.Record{Time: now, Collector: record.CSystem, Kind: record.KindEvent, Name: record.NSleep,
			Values: map[string]float64{"seconds": gap.Seconds()},
			Attrs:  map[string]string{"from": last.UTC().Format(time.RFC3339), "to": now.UTC().Format(time.RFC3339)}})
	}
}

func (c *Collector) addSample(at time.Time, mbps float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.samples = append(c.samples, sample{at, mbps})
	cutoff := at.Add(-5 * time.Minute)
	for len(c.samples) > 0 && c.samples[0].at.Before(cutoff) {
		c.samples = c.samples[1:]
	}
}

// LaptopMbps is this computer's average traffic (download plus upload) over the recent window.
func (c *Collector) LaptopMbps(window time.Duration) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	cutoff := time.Now().Add(-window)
	var sum float64
	n := 0
	for _, s := range c.samples {
		if !s.at.Before(cutoff) {
			sum += s.mbps
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
