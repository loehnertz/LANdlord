package diagnose

import (
	"fmt"
	"slices"
	"strings"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/config"
	"github.com/loehnertz/LANdlord/internal/record"
)

// sessionCtx holds session-wide baselines the per-bucket rules compare against.
type sessionCtx struct {
	th       config.Thresholds
	gwMedian float64
	bestRx   float64
	hops     []string // responding hop labels, in order
	tunnel   bool
	hasIPv6  bool
	// Targets and services that worked at least once. Ones that never did (blocked by a
	// firewall, broken IPv6) are ignored, so they can't turn every bucket into a problem.
	alive  map[string]bool
	httpOK map[string]bool
	stunOK bool
}

func newCtx(s *aggregate.Session, th config.Thresholds) *sessionCtx {
	c := &sessionCtx{th: th, alive: map[string]bool{}, httpOK: map[string]bool{}}
	var gw, rx []float64
	responding := map[string]bool{}
	for i := range s.Buckets {
		b := &s.Buckets[i]
		if b.Asleep {
			continue
		}
		if p := gatewayPath(b); p != nil && p.Recv > 0 {
			gw = append(gw, p.P50)
		}
		if b.Wifi.Connected && b.Wifi.RxMbps > 0 {
			rx = append(rx, b.Wifi.RxMbps)
		}
		if b.STUN.Recv > 0 {
			c.stunOK = true
		}
		for host, h := range b.HTTP {
			if h.Count > h.Failed {
				c.httpOK[host] = true
			}
		}
		for label, p := range b.Paths {
			if p.Recv == 0 {
				continue
			}
			c.alive[label] = true
			if strings.HasPrefix(label, "hop") {
				responding[label] = true
			}
			if record.IsInet6Target(label) {
				c.hasIPv6 = true
			}
		}
	}
	c.gwMedian = aggregate.Median(gw)
	slices.Sort(rx)
	c.bestRx = aggregate.Percentile(rx, 0.9)
	for n := 1; n <= 3; n++ {
		if l := record.HopTarget(n); responding[l] {
			c.hops = append(c.hops, l)
		}
	}
	for _, r := range s.Infos {
		if r.Collector == record.CUPnP && r.Name == record.NTunnel {
			c.tunnel = true
		}
	}
	return c
}

func gatewayPath(b *aggregate.Bucket) *aggregate.PathStats {
	if p := b.Paths[record.TGateway]; p != nil && p.Sent > 0 {
		return p
	}
	if p := b.Paths[record.TGateway6]; p != nil && p.Sent > 0 {
		return p
	}
	return nil
}

func (c *sessionCtx) degraded(p *aggregate.PathStats) bool {
	return p != nil && p.Sent > 0 && (p.LossPct > c.th.LossPct || p.P95 > c.th.RTTP95Ms || p.Jitter > c.th.JitterMs)
}

func (c *sessionCtx) gatewayDegraded(b *aggregate.Bucket) bool {
	p := gatewayPath(b)
	if p == nil {
		return false
	}
	limit := max(c.th.GatewayFloorMs, c.th.GatewayFactor*c.gwMedian)
	return p.LossPct > c.th.LossPct || p.P95 > limit
}

type inetSummary struct {
	Targets, Degraded    int
	LossPct, P95, Jitter float64
}

// internet summarises the internet targets of one family: 4, 6, or 0 for both.
func (c *sessionCtx) internet(b *aggregate.Bucket, family int) inetSummary {
	var s inetSummary
	var loss, p95, jitter []float64
	for label, p := range b.Paths {
		v4, v6 := record.IsInetTarget(label), record.IsInet6Target(label)
		if !(v4 && family != 6 || v6 && family != 4) || p.Sent == 0 || !c.alive[label] {
			continue
		}
		s.Targets++
		if c.degraded(p) {
			s.Degraded++
		}
		loss = append(loss, p.LossPct)
		jitter = append(jitter, p.Jitter)
		if p.Recv > 0 {
			p95 = append(p95, p.P95)
		}
	}
	s.LossPct, s.P95, s.Jitter = aggregate.Median(loss), aggregate.Median(p95), aggregate.Median(jitter)
	return s
}

// symptoms lists what would hurt a call or stream in this bucket. A bucket with any symptom is bad.
func (c *sessionCtx) symptoms(b *aggregate.Bucket) []Evidence {
	th := c.th
	var ev []Evidence
	in := c.internet(b, 0)
	if in.Targets > 0 && in.LossPct > th.LossPct {
		ev = append(ev, Evidence{"internet_loss", fmt.Sprintf("Packet loss to the internet: %.1f%%", in.LossPct)})
	}
	if in.Targets > 0 && in.P95 > th.RTTP95Ms {
		ev = append(ev, Evidence{"internet_latency", fmt.Sprintf("Internet latency (95th percentile): %.0f ms", in.P95)})
	}
	if j := max(in.Jitter, b.STUN.Jitter); j > th.JitterMs {
		ev = append(ev, Evidence{"jitter", fmt.Sprintf("Jitter: %.0f ms", j)})
	}
	if c.stunOK && b.STUN.Sent > 0 && b.STUN.LossPct > th.LossPct {
		ev = append(ev, Evidence{"udp_loss", fmt.Sprintf("Call-like UDP packet loss: %.1f%%", b.STUN.LossPct)})
	}
	// Only the Windows resolver counts as a symptom: it is what apps actually use. Failures of
	// individual servers are evidence for the DNS rule, not problems on their own.
	if d := b.DNS[record.TSystem]; d != nil && d.Failed > 0 {
		ev = append(ev, Evidence{"dns_failure", "Name lookups failed"})
	}
	for _, host := range sortedKeys(b.HTTP) {
		if b.HTTP[host].Failed > 0 && c.httpOK[host] {
			ev = append(ev, Evidence{"http_failure", "Connection to " + host + " failed"})
			break
		}
	}
	if hasEvent(b, record.CWifi, record.NDisconnect) {
		ev = append(ev, Evidence{"wifi_disconnect", "Wi-Fi disconnected"})
	}
	if hasEvent(b, record.CUPnP, record.NWANReconnect) {
		ev = append(ev, Evidence{"wan_reconnect", "The router reconnected to the internet"})
	}
	return ev
}

func hasEvent(b *aggregate.Bucket, collector, name string) bool {
	for _, e := range b.Events {
		if e.Collector == collector && e.Name == name {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
