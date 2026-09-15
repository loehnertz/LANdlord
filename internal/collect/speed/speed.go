// Package speed runs a capped download/upload test and grades bufferbloat: how much latency
// rises while the line is busy.
package speed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/diagnose"
	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
)

type Config struct {
	Interval         time.Duration
	FirstDelay       time.Duration
	MaxDownloadBytes int64
	MaxUploadBytes   int64
	DownURL, UpURL   string // DownURL contains %d for the byte count
	SkipAboveMbps    float64
	RetryAfterSkip   time.Duration
	BaselineFor      time.Duration
	PingEvery        time.Duration
	BloatTarget      netip.Addr
}

func DefaultConfig() Config {
	return Config{
		Interval:         time.Hour,
		FirstDelay:       2 * time.Minute,
		MaxDownloadBytes: 25 << 20,
		MaxUploadBytes:   10 << 20,
		DownURL:          "https://speed.cloudflare.com/__down?bytes=%d",
		UpURL:            "https://speed.cloudflare.com/__up",
		SkipAboveMbps:    1,
		RetryAfterSkip:   5 * time.Minute,
		BaselineFor:      5 * time.Second,
		PingEvery:        200 * time.Millisecond,
		BloatTarget:      netip.MustParseAddr("1.1.1.1"),
	}
}

type Collector struct {
	p          platform.Platform
	cfg        Config
	laptopMbps func(time.Duration) float64
	client     *http.Client
	trigger    chan struct{}
}

func New(p platform.Platform, cfg Config, laptopMbps func(time.Duration) float64) *Collector {
	if laptopMbps == nil {
		laptopMbps = func(time.Duration) float64 { return 0 }
	}
	return &Collector{p: p, cfg: cfg, laptopMbps: laptopMbps, client: &http.Client{Timeout: 2 * time.Minute}, trigger: make(chan struct{}, 1)}
}

func (c *Collector) Name() string { return record.CSpeed }

// Trigger requests a smaller test right away, used when the user marks a problem.
func (c *Collector) Trigger() {
	select {
	case c.trigger <- struct{}{}:
	default:
	}
}

func (c *Collector) Run(ctx context.Context, sink record.Sink) error {
	timer := time.NewTimer(c.cfg.FirstDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			next := c.cfg.Interval
			if !c.test(ctx, "interval", c.cfg.MaxDownloadBytes, c.cfg.MaxUploadBytes, sink) {
				next = c.cfg.RetryAfterSkip
			}
			timer.Reset(next)
		case <-c.trigger:
			c.test(ctx, "mark", min(c.cfg.MaxDownloadBytes, 10<<20), min(c.cfg.MaxUploadBytes, 2<<20), sink)
		}
	}
}

// test returns false when it was skipped or failed, so the caller retries sooner.
func (c *Collector) test(ctx context.Context, trigger string, down, up int64, sink record.Sink) bool {
	if busy := c.laptopMbps(30 * time.Second); busy > c.cfg.SkipAboveMbps {
		sink.Emit(record.Record{Time: time.Now(), Collector: record.CSpeed, Kind: record.KindEvent, Name: record.NSkipped,
			Values: map[string]float64{"laptop_mbps": busy},
			Attrs:  map[string]string{"reason": "this computer was busy, probably with a call or a stream"}})
		return false
	}
	targets := []netip.Addr{c.cfg.BloatTarget}
	if route, err := c.p.DefaultRoute(); err == nil && route.Gateway4.IsValid() {
		targets = append(targets, route.Gateway4)
	}
	start := time.Now()
	baseline := c.pingWhile(ctx, targets, func(context.Context) error {
		select {
		case <-ctx.Done():
		case <-time.After(c.cfg.BaselineFor):
		}
		return nil
	})
	var downMbps, upMbps float64
	var downErr, upErr error
	load := c.pingWhile(ctx, targets, func(ctx context.Context) error {
		downMbps, downErr = c.download(ctx, down)
		upMbps, upErr = c.upload(ctx, up)
		return nil
	})
	if ctx.Err() != nil || (downErr != nil && upErr != nil) {
		return false
	}
	bloat := 0.0
	for _, t := range targets {
		if len(baseline[t]) > 0 && len(load[t]) > 0 {
			bloat = max(bloat, aggregate.Median(load[t])-aggregate.Median(baseline[t]))
		}
	}
	sink.Emit(record.Record{Time: start, Collector: record.CSpeed, Kind: record.KindMetric, Name: record.NTest,
		Values: map[string]float64{"down_mbps": downMbps, "up_mbps": upMbps, "bloat_ms": bloat, "duration_s": time.Since(start).Seconds()},
		Attrs:  map[string]string{"grade": diagnose.BloatGrade(bloat), "trigger": trigger}})
	return true
}

// pingWhile pings every target until work returns, collecting successful RTTs in milliseconds.
func (c *Collector) pingWhile(ctx context.Context, targets []netip.Addr, work func(context.Context) error) map[netip.Addr][]float64 {
	pctx, cancel := context.WithCancel(ctx)
	var mu sync.Mutex
	out := map[netip.Addr][]float64{}
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Go(func() {
			pg, err := c.p.NewPinger(4)
			if err != nil {
				return
			}
			defer pg.Close()
			tick := time.NewTicker(c.cfg.PingEvery)
			defer tick.Stop()
			for {
				reply, err := pg.Echo(pctx, platform.EchoRequest{Dst: t, Timeout: time.Second})
				if err == nil && reply.Status == platform.EchoOK {
					mu.Lock()
					out[t] = append(out[t], float64(reply.RTT)/float64(time.Millisecond))
					mu.Unlock()
				}
				select {
				case <-pctx.Done():
					return
				case <-tick.C:
				}
			}
		})
	}
	_ = work(ctx)
	cancel()
	wg.Wait()
	return out
}

func (c *Collector) download(ctx context.Context, n int64) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(c.cfg.DownURL, n), nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	buf := make([]byte, 64<<10)
	var total int64
	var first time.Time
	for total < n {
		k, err := resp.Body.Read(buf)
		if k > 0 && first.IsZero() {
			first = time.Now()
		}
		total += int64(k)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, err
		}
	}
	secs := time.Since(first).Seconds()
	if total == 0 || secs <= 0 {
		return 0, errors.New("empty download")
	}
	return float64(total) * 8 / secs / 1e6, nil
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func (c *Collector) upload(ctx context.Context, n int64) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.UpURL, io.LimitReader(zeros{}, n))
	if err != nil {
		return 0, err
	}
	req.ContentLength = n
	req.Header.Set("Content-Type", "application/octet-stream")
	start := time.Now()
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	secs := time.Since(start).Seconds()
	if secs <= 0 {
		return 0, errors.New("upload took no time")
	}
	return float64(n) * 8 / secs / 1e6, nil
}
