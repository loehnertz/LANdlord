// Package dnscheck times name lookups through the Windows resolver, the configured DNS servers
// and two public resolvers, so slow or failing router DNS stands out.
package dnscheck

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
)

type resolver struct {
	label string
	addr  string // host:port; empty means the operating system's resolver
}

var publicResolvers = []resolver{{"1.1.1.1", "1.1.1.1:53"}, {"8.8.8.8", "8.8.8.8:53"}}

type Collector struct {
	p       platform.Platform
	hosts   []string
	every   time.Duration
	timeout time.Duration
	next    int

	// resolvers is replaceable in tests.
	resolvers func() []resolver
}

func New(p platform.Platform, hosts []string, every time.Duration) *Collector {
	if every <= 0 {
		every = 30 * time.Second
	}
	if len(hosts) == 0 {
		hosts = []string{"www.google.com"}
	}
	c := &Collector{p: p, hosts: hosts, every: every, timeout: 3 * time.Second}
	c.resolvers = c.defaultResolvers
	return c
}

func (c *Collector) Name() string { return record.CDNS }

func (c *Collector) defaultResolvers() []resolver {
	out := []resolver{{label: record.TSystem}}
	seen := map[string]bool{}
	if route, err := c.p.DefaultRoute(); err == nil {
		for _, a := range route.DNSServers {
			addr := netip.AddrPortFrom(a, 53).String()
			if seen[addr] {
				continue
			}
			seen[addr] = true
			out = append(out, resolver{label: "configured:" + a.String(), addr: addr})
		}
	}
	for _, r := range publicResolvers {
		if !seen[r.addr] {
			out = append(out, r)
		}
	}
	return out
}

func (c *Collector) Run(ctx context.Context, sink record.Sink) error {
	tick := time.NewTicker(c.every)
	defer tick.Stop()
	for {
		c.round(ctx, sink)
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func (c *Collector) round(ctx context.Context, sink record.Sink) {
	host := c.hosts[c.next%len(c.hosts)]
	c.next++
	var wg sync.WaitGroup
	for _, r := range c.resolvers() {
		wg.Go(func() { c.measure(ctx, r, host, sink) })
	}
	wg.Wait()
}

func (c *Collector) measure(ctx context.Context, r resolver, host string, sink record.Sink) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	start := time.Now()
	var err error
	elapsed := time.Duration(0)
	if r.addr == "" {
		_, err = net.DefaultResolver.LookupHost(ctx, host)
		elapsed = time.Since(start)
	} else {
		msg := new(dns.Msg)
		msg.SetQuestion(dns.Fqdn(host), dns.TypeA)
		client := &dns.Client{Timeout: c.timeout}
		var resp *dns.Msg
		resp, elapsed, err = client.ExchangeContext(ctx, msg, r.addr)
		if err == nil && resp.Rcode != dns.RcodeSuccess {
			err = rcodeError(resp.Rcode)
		}
	}
	if ctx.Err() != nil && err == nil {
		err = ctx.Err()
	}
	rec := record.Record{Time: start, Collector: record.CDNS, Kind: record.KindMetric, Name: record.NLookup, Target: r.label,
		Attrs: map[string]string{"host": host}}
	if err != nil {
		rec.Values = map[string]float64{"failed": 1}
		rec.Attrs["error"] = err.Error()
	} else {
		rec.Values = map[string]float64{"ms": float64(elapsed.Microseconds()) / 1000}
	}
	sink.Emit(rec)
}

type rcodeError int

func (e rcodeError) Error() string { return "DNS answer " + dns.RcodeToString[int(e)] }
