// Package publicip records the public IP address and country via Cloudflare's trace endpoint.
package publicip

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/loehnertz/LANdlord/internal/record"
)

const DefaultURL = "https://www.cloudflare.com/cdn-cgi/trace"

type Collector struct {
	url    string
	every  time.Duration
	retry  time.Duration
	client *http.Client

	mu   sync.Mutex
	ip   string
	onIP func(ip string)
}

// New creates the collector; onIP (optional) is called whenever the public IP changes.
func New(url string, every time.Duration, onIP func(string)) *Collector {
	if url == "" {
		url = DefaultURL
	}
	if every <= 0 {
		every = 30 * time.Minute
	}
	if onIP == nil {
		onIP = func(string) {}
	}
	return &Collector{url: url, every: every, retry: time.Minute, client: &http.Client{Timeout: 10 * time.Second}, onIP: onIP}
}

func (c *Collector) Name() string { return record.CPublic }

// IP returns the last known public IP address.
func (c *Collector) IP() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ip
}

func (c *Collector) Run(ctx context.Context, sink record.Sink) error {
	for {
		wait := c.every
		if !c.check(ctx, sink) {
			wait = c.retry
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

func (c *Collector) check(ctx context.Context, sink record.Sink) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return false
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	fields := Parse(io.LimitReader(resp.Body, 16<<10))
	ip := fields["ip"]
	if ip == "" {
		return false
	}
	sink.Emit(record.Info(record.CPublic, record.NPublicIP, "", time.Now(), map[string]string{"ip": ip, "loc": fields["loc"]}))
	c.mu.Lock()
	changed := c.ip != ip
	c.ip = ip
	c.mu.Unlock()
	if changed {
		c.onIP(ip)
	}
	return true
}

// Parse reads the key=value lines of a Cloudflare trace response.
func Parse(r io.Reader) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}
