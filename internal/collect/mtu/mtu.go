// Package mtu discovers the largest packet that reaches the internet without fragmentation.
package mtu

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/loehnertz/LANdlord/internal/collect"
	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
)

const (
	headerBytes = 28 // IPv4 (20) + ICMP (8)
	minPayload  = 1172
	maxPayload  = 1472
)

var errTooSmall = errors.New("path MTU below 1200 bytes")

type Collector struct {
	p     platform.Platform
	dst   netip.Addr
	every time.Duration
}

func New(p platform.Platform, dst netip.Addr, every time.Duration) *Collector {
	if every <= 0 {
		every = time.Hour
	}
	return &Collector{p: p, dst: dst, every: every}
}

func (c *Collector) Name() string { return record.CMTU }

func (c *Collector) Run(ctx context.Context, sink record.Sink) error {
	pg, err := c.p.NewPinger(4)
	if err != nil {
		if errors.Is(err, platform.ErrUnsupported) {
			return fmt.Errorf("%w: %v", collect.ErrPermanent, err)
		}
		return err
	}
	defer pg.Close()
	for {
		mtu, err := Discover(ctx, pg, c.dst)
		switch {
		case errors.Is(err, platform.ErrUnsupported):
			return fmt.Errorf("%w: don't-fragment pings are not supported here", collect.ErrPermanent)
		case err == nil:
			sink.Emit(record.Metric(record.CMTU, record.NPMTU, c.dst.String(), time.Now(), map[string]float64{"mtu": float64(mtu)}))
		case errors.Is(err, errTooSmall):
			sink.Emit(record.Metric(record.CMTU, record.NPMTU, c.dst.String(), time.Now(), map[string]float64{"mtu": minPayload + headerBytes - 1}))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(c.every):
		}
	}
}

// Discover binary-searches the largest don't-fragment echo payload that gets an answer.
func Discover(ctx context.Context, pg platform.Pinger, dst netip.Addr) (int, error) {
	fits := func(size int) (bool, error) {
		for range 3 {
			reply, err := pg.Echo(ctx, platform.EchoRequest{Dst: dst, Size: size, DontFragment: true, Timeout: time.Second})
			if err != nil {
				if errors.Is(err, platform.ErrUnsupported) || ctx.Err() != nil {
					return false, err
				}
				continue
			}
			if reply.Status == platform.EchoOK {
				return true, nil
			}
			if reply.Status == platform.EchoTooBig {
				return false, nil
			}
		}
		return false, nil
	}
	if ok, err := fits(maxPayload); err != nil {
		return 0, err
	} else if ok {
		return maxPayload + headerBytes, nil
	}
	if ok, err := fits(minPayload); err != nil {
		return 0, err
	} else if !ok {
		return 0, errTooSmall
	}
	lo, hi := minPayload, maxPayload-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		ok, err := fits(mid)
		if err != nil {
			return 0, err
		}
		if ok {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + headerBytes, nil
}
