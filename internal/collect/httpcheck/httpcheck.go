// Package httpcheck times TCP connect, TLS handshake and first byte for the services people
// use for calls and streaming.
package httpcheck

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"

	"github.com/loehnertz/LANdlord/internal/record"
	"github.com/loehnertz/LANdlord/internal/version"
)

const maxBody = 64 << 10

type Collector struct {
	hosts   []string
	every   time.Duration
	timeout time.Duration

	// Replaceable in tests.
	url       func(host string) string
	tlsConfig *tls.Config
}

func New(hosts []string, every time.Duration) *Collector {
	if every <= 0 {
		every = time.Minute
	}
	return &Collector{hosts: hosts, every: every, timeout: 10 * time.Second,
		url: func(host string) string { return "https://" + host + "/" }}
}

func (c *Collector) Name() string { return record.CHTTP }

func (c *Collector) Run(ctx context.Context, sink record.Sink) error {
	tick := time.NewTicker(c.every)
	defer tick.Stop()
	for {
		var wg sync.WaitGroup
		for _, h := range c.hosts {
			wg.Go(func() { c.fetch(ctx, h, sink) })
		}
		wg.Wait()
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func (c *Collector) fetch(ctx context.Context, host string, sink record.Sink) {
	var mu sync.Mutex
	var connectStart, connectDone, tlsStart, tlsDone, firstByte time.Time
	set := func(t *time.Time) {
		mu.Lock()
		defer mu.Unlock()
		if t.IsZero() {
			*t = time.Now()
		}
	}
	trace := &httptrace.ClientTrace{
		ConnectStart:         func(string, string) { set(&connectStart) },
		ConnectDone:          func(string, string, error) { set(&connectDone) },
		TLSHandshakeStart:    func() { set(&tlsStart) },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { set(&tlsDone) },
		GotFirstResponseByte: func() { set(&firstByte) },
	}
	transport := &http.Transport{
		DisableKeepAlives:   true,
		Proxy:               http.ProxyFromEnvironment,
		TLSHandshakeTimeout: c.timeout,
		ForceAttemptHTTP2:   true,
		TLSClientConfig:     c.tlsConfig,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		Timeout:       c.timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	start := time.Now()
	rec := record.Record{Time: start, Collector: record.CHTTP, Kind: record.KindMetric, Name: record.NFetch, Target: host}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, c.url(host), nil)
	if err == nil {
		req.Header.Set("User-Agent", "LANdlord/"+version.Version+" (+https://github.com/loehnertz/LANdlord)")
		var resp *http.Response
		if resp, err = client.Do(req); err == nil {
			_, _ = io.CopyN(io.Discard, resp.Body, maxBody)
			resp.Body.Close()
		}
	}
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		rec.Values = map[string]float64{"failed": 1}
		rec.Attrs = map[string]string{"error": err.Error()}
		sink.Emit(rec)
		return
	}
	mu.Lock()
	defer mu.Unlock()
	ms := func(from, to time.Time) float64 {
		if from.IsZero() || to.IsZero() {
			return 0
		}
		return float64(to.Sub(from).Microseconds()) / 1000
	}
	rec.Values = map[string]float64{
		"connect_ms": ms(connectStart, connectDone),
		"tls_ms":     ms(tlsStart, tlsDone),
		"ttfb_ms":    ms(start, firstByte),
	}
	sink.Emit(rec)
}
