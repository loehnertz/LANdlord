// Package stuncheck measures UDP loss and jitter in a pattern similar to a video call, using
// public STUN servers so no server of our own is needed.
package stuncheck

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/pion/stun/v3"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/record"
)

type Collector struct {
	servers []string
	every   time.Duration
	rate    int
	burst   time.Duration
	wait    time.Duration
	next    int
	addrs   map[string]*net.UDPAddr
	natDone bool
}

func New(servers []string, every time.Duration, rate int, burst time.Duration) *Collector {
	if every <= 0 {
		every = time.Minute
	}
	if rate <= 0 {
		rate = 50
	}
	if burst <= 0 {
		burst = 10 * time.Second
	}
	return &Collector{servers: servers, every: every, rate: rate, burst: burst, wait: 2 * time.Second, addrs: map[string]*net.UDPAddr{}}
}

func (c *Collector) Name() string { return record.CSTUN }

func (c *Collector) Run(ctx context.Context, sink record.Sink) error {
	if len(c.servers) == 0 {
		return errors.New("no STUN servers configured")
	}
	tick := time.NewTicker(c.every)
	defer tick.Stop()
	for {
		if !c.natDone && len(c.servers) >= 2 {
			c.detectNAT(ctx, sink)
		}
		server := c.servers[c.next%len(c.servers)]
		c.next++
		c.burstOnce(ctx, server, sink)
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

// resolve caches addresses, so a DNS outage doesn't turn into fake UDP loss.
func (c *Collector) resolve(server string) *net.UDPAddr {
	if addr, err := net.ResolveUDPAddr("udp4", server); err == nil {
		c.addrs[server] = addr
		return addr
	}
	return c.addrs[server]
}

func (c *Collector) burstOnce(ctx context.Context, server string, sink record.Sink) {
	raddr := c.resolve(server)
	if raddr == nil {
		return
	}
	conn, err := net.DialUDP("udp4", nil, raddr)
	if err != nil {
		return
	}

	var mu sync.Mutex
	sentAt := map[[stun.TransactionIDSize]byte]time.Time{}
	rtts := map[[stun.TransactionIDSize]byte]float64{}
	var readers sync.WaitGroup
	readers.Go(func() {
		buf := make([]byte, 1500)
		for {
			n, err := conn.Read(buf)
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if err != nil {
				// A connected UDP socket reports ICMP errors (for example "connection refused") on read; keep listening.
				continue
			}
			m := &stun.Message{Raw: append([]byte(nil), buf[:n]...)}
			if m.Decode() != nil {
				continue
			}
			mu.Lock()
			if t, ok := sentAt[m.TransactionID]; ok {
				if _, dup := rtts[m.TransactionID]; !dup {
					rtts[m.TransactionID] = float64(time.Since(t).Microseconds()) / 1000
				}
			}
			mu.Unlock()
		}
	})

	start := time.Now()
	count := int(c.burst.Seconds() * float64(c.rate))
	interval := time.Second / time.Duration(c.rate)
	order := make([][stun.TransactionIDSize]byte, 0, count)
	tick := time.NewTicker(interval)
	for range count {
		msg, err := stun.Build(stun.TransactionID, stun.BindingRequest)
		if err != nil {
			continue
		}
		mu.Lock()
		sentAt[msg.TransactionID] = time.Now()
		mu.Unlock()
		order = append(order, msg.TransactionID)
		_, _ = conn.Write(msg.Raw)
		select {
		case <-ctx.Done():
			tick.Stop()
			conn.Close()
			readers.Wait()
			return
		case <-tick.C:
		}
	}
	tick.Stop()
	select {
	case <-ctx.Done():
	case <-time.After(c.wait):
	}
	conn.Close()
	readers.Wait()
	if ctx.Err() != nil || len(order) == 0 {
		return
	}

	mu.Lock()
	defer mu.Unlock()
	seq := make([]float64, 0, len(rtts))
	for _, id := range order {
		if rtt, ok := rtts[id]; ok {
			seq = append(seq, rtt)
		}
	}
	sent := float64(len(order))
	received := float64(len(seq))
	values := map[string]float64{
		"sent":      sent,
		"received":  received,
		"loss_pct":  (sent - received) / sent * 100,
		"jitter_ms": aggregate.Jitter(seq),
	}
	if len(seq) > 0 {
		values["rtt_p50_ms"] = aggregate.Median(seq)
	}
	sink.Emit(record.Metric(record.CSTUN, record.NBurst, server, start, values))
}

// detectNAT compares the public port two servers see for the same local socket.
func (c *Collector) detectNAT(ctx context.Context, sink record.Sink) {
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return
	}
	defer conn.Close()
	ports := map[string]int{}
	for _, server := range c.servers[:2] {
		raddr := c.resolve(server)
		if raddr == nil {
			continue
		}
		msg, err := stun.Build(stun.TransactionID, stun.BindingRequest)
		if err != nil {
			continue
		}
		for range 3 {
			if ctx.Err() != nil {
				return
			}
			_, _ = conn.WriteToUDP(msg.Raw, raddr)
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			buf := make([]byte, 1500)
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				continue
			}
			resp := &stun.Message{Raw: buf[:n]}
			var xor stun.XORMappedAddress
			if resp.Decode() == nil && resp.TransactionID == msg.TransactionID && xor.GetFrom(resp) == nil {
				ports[server] = xor.Port
				break
			}
		}
	}
	if ctx.Err() != nil {
		return
	}
	c.natDone = true
	mapping := "unknown"
	if len(ports) == 2 {
		p0, p1 := ports[c.servers[0]], ports[c.servers[1]]
		if p0 == p1 {
			mapping = "endpoint-independent"
		} else {
			mapping = "endpoint-dependent"
		}
	}
	sink.Emit(record.Info(record.CSTUN, record.NNAT, "", time.Now(), map[string]string{"mapping": mapping}))
}
