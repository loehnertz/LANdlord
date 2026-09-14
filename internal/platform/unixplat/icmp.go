//go:build !windows

package unixplat

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"github.com/loehnertz/LANdlord/internal/platform"
)

// pinger uses unprivileged ICMP datagram sockets (macOS, and Linux when ping_group_range allows it).
type pinger struct {
	mu     sync.Mutex
	family int
	conn   *icmp.PacketConn
	id     int
	seq    int
}

func (*Platform) NewPinger(family int) (platform.Pinger, error) {
	network, addr := "udp4", "0.0.0.0"
	if family == 6 {
		network, addr = "udp6", "::"
	}
	c, err := icmp.ListenPacket(network, addr)
	if err != nil {
		return nil, fmt.Errorf("%w: unprivileged ICMP: %v", platform.ErrUnsupported, err)
	}
	return &pinger{family: family, conn: c, id: os.Getpid() & 0xffff}, nil
}

func (p *pinger) Close() error { return p.conn.Close() }

func (p *pinger) Echo(ctx context.Context, req platform.EchoRequest) (platform.EchoReply, error) {
	if req.DontFragment {
		return platform.EchoReply{}, platform.ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return platform.EchoReply{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	size, timeout := req.Size, req.Timeout
	if size <= 0 {
		size = 32
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = 64
	}
	var msgType icmp.Type = ipv4.ICMPTypeEcho
	proto := 1
	if p.family == 6 {
		msgType, proto = ipv6.ICMPTypeEchoRequest, 58
		_ = p.conn.IPv6PacketConn().SetHopLimit(ttl)
	} else {
		_ = p.conn.IPv4PacketConn().SetTTL(ttl)
	}
	p.seq = (p.seq + 1) & 0xffff
	msg, err := (&icmp.Message{Type: msgType, Body: &icmp.Echo{ID: p.id, Seq: p.seq, Data: make([]byte, size)}}).Marshal(nil)
	if err != nil {
		return platform.EchoReply{}, err
	}
	start := time.Now()
	dst := &net.UDPAddr{IP: req.Dst.AsSlice(), Zone: req.Dst.Zone()}
	if _, err := p.conn.WriteTo(msg, dst); err != nil {
		return platform.EchoReply{Status: platform.EchoError}, err
	}
	if err := p.conn.SetReadDeadline(start.Add(timeout)); err != nil {
		return platform.EchoReply{}, err
	}
	buf := make([]byte, 1500)
	for {
		n, from, err := p.conn.ReadFrom(buf)
		if err != nil {
			var nerr net.Error
			if errors.As(err, &nerr) && nerr.Timeout() {
				return platform.EchoReply{Status: platform.EchoTimeout}, nil
			}
			return platform.EchoReply{Status: platform.EchoError}, err
		}
		m, err := icmp.ParseMessage(proto, buf[:n])
		if err != nil {
			continue
		}
		reply := platform.EchoReply{From: addrOf(from), RTT: time.Since(start)}
		switch m.Type {
		case ipv4.ICMPTypeEchoReply, ipv6.ICMPTypeEchoReply:
			// Linux rewrites the echo ID on datagram sockets, so match on the sequence number.
			if e, ok := m.Body.(*icmp.Echo); ok && e.Seq == p.seq {
				reply.Status = platform.EchoOK
				return reply, nil
			}
		case ipv4.ICMPTypeTimeExceeded, ipv6.ICMPTypeTimeExceeded:
			reply.Status = platform.EchoTTLExpired
			return reply, nil
		case ipv4.ICMPTypeDestinationUnreachable, ipv6.ICMPTypeDestinationUnreachable:
			reply.Status = platform.EchoUnreachable
			return reply, nil
		case ipv6.ICMPTypePacketTooBig:
			reply.Status = platform.EchoTooBig
			return reply, nil
		}
	}
}

func addrOf(a net.Addr) netip.Addr {
	var ip net.IP
	switch v := a.(type) {
	case *net.UDPAddr:
		ip = v.IP
	case *net.IPAddr:
		ip = v.IP
	}
	addr, _ := netip.AddrFromSlice(ip)
	return addr.Unmap()
}
