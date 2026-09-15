//go:build windows

package admin

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/loehnertz/LANdlord/internal/collect/traceroute"
	"github.com/loehnertz/LANdlord/internal/record"
)

// tcpTraceTarget is traced on port 443, the port calls and websites use.
var tcpTraceTarget = netip.MustParseAddr("1.1.1.1")

const (
	tcpTracePort     = 443
	tcpTraceEvery    = 15 * time.Minute
	tcpTraceMaxHops  = 30
	tcpTraceAttempts = 2
	tcpTraceSilent   = 5
)

// traceLoop runs a TCP traceroute every 15 minutes. It needs admin rights for the raw ICMP socket.
func (c *Collector) traceLoop(ctx context.Context, sink record.Sink) {
	for {
		if route, err := c.p.DefaultRoute(); err == nil && route.Local4.IsValid() {
			tctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			path, err := tcpTrace(tctx, route.Local4, tcpTraceTarget, tcpTracePort, time.Second)
			cancel()
			if err == nil && len(path) > 0 {
				sink.Emit(record.Record{Time: time.Now(), Collector: record.CAdmin, Kind: record.KindInfo, Name: record.NTCPRoute,
					Target: tcpTraceTarget.String(), Values: map[string]float64{"hops": float64(len(path))},
					Attrs: map[string]string{"path": traceroute.FormatPath(path)}})
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(tcpTraceEvery):
		}
	}
}

// tcpTrace sends TCP connection attempts with increasing TTL and reads the routers' ICMP
// "time exceeded" answers from a raw socket, matched by the attempt's source port.
func tcpTrace(ctx context.Context, local, dst netip.Addr, port uint16, timeout time.Duration) ([]netip.Addr, error) {
	raw, err := windows.Socket(windows.AF_INET, windows.SOCK_RAW, windows.IPPROTO_ICMP)
	if err != nil {
		return nil, err
	}
	var closeOnce sync.Once
	closeRaw := func() { closeOnce.Do(func() { _ = windows.Closesocket(raw) }) }
	defer closeRaw()
	if err := windows.Bind(raw, &windows.SockaddrInet4{Addr: local.As4()}); err != nil {
		return nil, err
	}
	_ = windows.SetsockoptInt(raw, windows.SOL_SOCKET, windows.SO_RCVTIMEO, 250)

	var mu sync.Mutex
	responders := map[uint16]netip.Addr{}
	readerDone := make(chan struct{})
	stop := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 1500)
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, from, err := windows.Recvfrom(raw, buf, 0)
			if err != nil {
				continue // timeout, or the socket was closed
			}
			sa, ok := from.(*windows.SockaddrInet4)
			if !ok {
				continue
			}
			if srcPort, ok := matchICMPReply(buf[:n], dst, port); ok {
				mu.Lock()
				responders[srcPort] = netip.AddrFrom4(sa.Addr)
				mu.Unlock()
			}
		}
	}()
	defer func() {
		close(stop)
		closeRaw()
		<-readerDone
	}()

	basePort := 33434 + rand.IntN(20000)
	target := netip.AddrPortFrom(dst, port).String()
	var path []netip.Addr
	silent := 0
	for ttl := 1; ttl <= tcpTraceMaxHops && silent < tcpTraceSilent; ttl++ {
		var hop netip.Addr
		reached := false
		for attempt := 0; attempt < tcpTraceAttempts && !hop.IsValid() && !reached; attempt++ {
			srcPort := uint16(basePort + ttl*tcpTraceAttempts + attempt)
			dialer := net.Dialer{
				Timeout:   timeout,
				LocalAddr: &net.TCPAddr{IP: local.AsSlice(), Port: int(srcPort)},
				Control: func(_, _ string, c syscall.RawConn) error {
					var serr error
					err := c.Control(func(fd uintptr) {
						serr = windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, windows.IP_TTL, ttl)
					})
					return errors.Join(err, serr)
				},
			}
			conn, err := dialer.DialContext(ctx, "tcp4", target)
			if ctx.Err() != nil {
				return path, ctx.Err()
			}
			if err == nil {
				_ = conn.Close()
				hop, reached = dst, true
				break
			}
			if errors.Is(err, windows.WSAECONNREFUSED) || errors.Is(err, syscall.ECONNREFUSED) {
				hop, reached = dst, true // the destination answered with a reset
				break
			}
			deadline := time.Now().Add(300 * time.Millisecond)
			for time.Now().Before(deadline) {
				mu.Lock()
				addr, ok := responders[srcPort]
				mu.Unlock()
				if ok {
					hop = addr
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
		path = append(path, hop)
		if reached {
			break
		}
		if hop.IsValid() {
			silent = 0
		} else {
			silent++
		}
	}
	return path, nil
}
