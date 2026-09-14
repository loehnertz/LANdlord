//go:build windows

package winplat

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/loehnertz/LANdlord/internal/platform"
)

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	procIcmpCreateFile  = iphlpapi.NewProc("IcmpCreateFile")
	procIcmp6CreateFile = iphlpapi.NewProc("Icmp6CreateFile")
	procIcmpSendEcho2   = iphlpapi.NewProc("IcmpSendEcho2")
	procIcmp6SendEcho2  = iphlpapi.NewProc("Icmp6SendEcho2")
	procIcmpCloseHandle = iphlpapi.NewProc("IcmpCloseHandle")
	procSendARP         = iphlpapi.NewProc("SendARP")
)

// IP_OPTION_INFORMATION; Go's alignment matches the 64-bit C layout.
type ipOptionInformation struct {
	TTL, TOS, Flags, OptionsSize uint8
	OptionsData                  uintptr
}

// ICMP_ECHO_REPLY (64-bit).
type icmpEchoReply struct {
	Address       uint32
	Status        uint32
	RoundTripTime uint32
	DataSize      uint16
	Reserved      uint16
	Data          uintptr
	Options       ipOptionInformation
}

// sockaddr_in6.
type sockaddrIn6 struct {
	Family   uint16
	Port     uint16
	Flowinfo uint32
	Addr     [16]byte
	ScopeID  uint32
}

const (
	ipFlagDF              = 0x2
	ipDestNetUnreachable  = 11002
	ipDestHostUnreachable = 11003
	ipDestProtUnreachable = 11004
	ipDestPortUnreachable = 11005
	ipPacketTooBig        = 11009
	ipReqTimedOut         = 11010
	ipTTLExpiredTransit   = 11013
	ipTTLExpiredReassem   = 11014
)

type pinger struct {
	h      windows.Handle
	family int
}

func (*Platform) NewPinger(family int) (platform.Pinger, error) {
	proc := procIcmpCreateFile
	if family == 6 {
		proc = procIcmp6CreateFile
	}
	if err := proc.Find(); err != nil {
		return nil, err
	}
	r, _, err := proc.Call()
	if h := windows.Handle(r); h != windows.InvalidHandle {
		return &pinger{h: h, family: family}, nil
	}
	return nil, err
}

func (p *pinger) Close() error {
	r, _, err := procIcmpCloseHandle.Call(uintptr(p.h))
	if r == 0 {
		return err
	}
	return nil
}

func (p *pinger) Echo(ctx context.Context, req platform.EchoRequest) (platform.EchoReply, error) {
	if err := ctx.Err(); err != nil {
		return platform.EchoReply{}, err
	}
	size := req.Size
	if size <= 0 {
		size = 32
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = time.Second
	}
	data := make([]byte, size)
	for i := range data {
		data[i] = byte('a' + i%23)
	}
	opts := ipOptionInformation{TTL: 128}
	if req.TTL > 0 {
		opts.TTL = uint8(min(req.TTL, 255))
	}
	if req.DontFragment {
		opts.Flags = ipFlagDF
	}
	reply := make([]byte, 128+size)
	if p.family == 6 {
		return p.echo6(req, data, &opts, reply, timeout)
	}

	dst := req.Dst.Unmap().As4()
	start := time.Now()
	n, _, callErr := procIcmpSendEcho2.Call(
		uintptr(p.h), 0, 0, 0,
		uintptr(binary.LittleEndian.Uint32(dst[:])),
		uintptr(unsafe.Pointer(&data[0])), uintptr(size),
		uintptr(unsafe.Pointer(&opts)),
		uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)),
		uintptr(timeout.Milliseconds()),
	)
	rtt := time.Since(start)
	r := (*icmpEchoReply)(unsafe.Pointer(&reply[0]))
	var from [4]byte
	binary.LittleEndian.PutUint32(from[:], r.Address)
	fromAddr := netip.AddrFrom4(from)
	if n == 0 {
		// Timeouts and TTL expiry return 0; the reply buffer may still carry the responder.
		if r.Address != 0 && r.Status != 0 {
			return statusReply(r.Status, fromAddr, rtt), nil
		}
		var errno windows.Errno
		if errors.As(callErr, &errno) && isIPStatus(uint32(errno)) {
			return statusReply(uint32(errno), netip.Addr{}, rtt), nil
		}
		return platform.EchoReply{Status: platform.EchoError}, callErr
	}
	return statusReply(r.Status, fromAddr, rtt), nil
}

func (p *pinger) echo6(req platform.EchoRequest, data []byte, opts *ipOptionInformation, reply []byte, timeout time.Duration) (platform.EchoReply, error) {
	src := sockaddrIn6{Family: windows.AF_INET6}
	dst := sockaddrIn6{Family: windows.AF_INET6, Addr: req.Dst.As16()}
	if zone := req.Dst.Zone(); zone != "" {
		if id, err := strconv.Atoi(zone); err == nil {
			dst.ScopeID = uint32(id)
		} else if ifc, err := net.InterfaceByName(zone); err == nil {
			dst.ScopeID = uint32(ifc.Index)
		}
	}
	start := time.Now()
	n, _, callErr := procIcmp6SendEcho2.Call(
		uintptr(p.h), 0, 0, 0,
		uintptr(unsafe.Pointer(&src)), uintptr(unsafe.Pointer(&dst)),
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)),
		uintptr(unsafe.Pointer(opts)),
		uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)),
		uintptr(timeout.Milliseconds()),
	)
	rtt := time.Since(start)
	// ICMPV6_ECHO_REPLY: packed IPV6_ADDRESS_EX (port 2, flowinfo 4, address 16, scope 4) padded to 28, then Status.
	var from [16]byte
	copy(from[:], reply[6:22])
	status := binary.LittleEndian.Uint32(reply[28:32])
	fromAddr := netip.AddrFrom16(from)
	if n == 0 {
		if status != 0 && fromAddr != (netip.Addr{}) && !fromAddr.IsUnspecified() {
			return statusReply(status, fromAddr, rtt), nil
		}
		var errno windows.Errno
		if errors.As(callErr, &errno) && isIPStatus(uint32(errno)) {
			return statusReply(uint32(errno), netip.Addr{}, rtt), nil
		}
		return platform.EchoReply{Status: platform.EchoError}, callErr
	}
	return statusReply(status, fromAddr, rtt), nil
}

func isIPStatus(code uint32) bool { return code >= 11000 && code <= 11050 }

func statusReply(status uint32, from netip.Addr, rtt time.Duration) platform.EchoReply {
	r := platform.EchoReply{From: from, RTT: rtt}
	switch status {
	case 0:
		r.Status = platform.EchoOK
	case ipReqTimedOut:
		r.Status = platform.EchoTimeout
	case ipTTLExpiredTransit, ipTTLExpiredReassem:
		r.Status = platform.EchoTTLExpired
	case ipPacketTooBig:
		r.Status = platform.EchoTooBig
	case ipDestNetUnreachable, ipDestHostUnreachable, ipDestProtUnreachable, ipDestPortUnreachable:
		r.Status = platform.EchoUnreachable
	default:
		r.Status = platform.EchoError
	}
	return r
}

func (*Platform) NeighborMAC(ip netip.Addr) (net.HardwareAddr, error) {
	if !ip.Is4() && !ip.Is4In6() {
		return nil, platform.ErrUnsupported
	}
	dst := ip.Unmap().As4()
	var mac [8]byte
	length := uint32(len(mac))
	r, _, _ := procSendARP.Call(uintptr(binary.LittleEndian.Uint32(dst[:])), 0, uintptr(unsafe.Pointer(&mac[0])), uintptr(unsafe.Pointer(&length)))
	if r != 0 {
		return nil, windows.Errno(r)
	}
	return net.HardwareAddr(mac[:min(length, 6)]), nil
}
