// Package platform defines everything LANdlord needs from the operating system. Implementations
// live in the winplat and unixplat packages; host.New picks the one for the current OS.
package platform

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"time"
)

var (
	ErrUnsupported    = errors.New("not supported on this platform")
	ErrLocationDenied = errors.New("location access for desktop apps is turned off")
	ErrDeclined       = errors.New("the admin prompt was declined")
)

type EchoStatus int

const (
	EchoOK EchoStatus = iota
	EchoTimeout
	EchoTTLExpired
	EchoUnreachable
	EchoTooBig
	EchoError
)

type EchoRequest struct {
	Dst          netip.Addr
	TTL, Size    int
	DontFragment bool
	Timeout      time.Duration
}

type EchoReply struct {
	From   netip.Addr
	RTT    time.Duration
	Status EchoStatus
}

type Pinger interface {
	Echo(ctx context.Context, req EchoRequest) (EchoReply, error)
	Close() error
}

type Route struct {
	InterfaceName, Description string
	InterfaceIndex             int
	InterfaceGUID              string
	MAC                        net.HardwareAddr
	Gateway4, Gateway6, Local4 netip.Addr
	DNSServers                 []netip.Addr
	Wireless                   bool
	LinkMbps                   float64
}

type Counters struct {
	RxBytes, TxBytes, InErrors, OutErrors, InDiscards, OutDiscards uint64
	LinkBps                                                        uint64
}

type PowerStatus struct {
	Known, OnAC bool
}

type AdapterInfo struct {
	DriverVersion, DriverDate string
	// Properties holds the adapter's advanced driver settings where the platform exposes them.
	Properties map[string]string
}

type WifiLink struct {
	Connected      bool
	SSID           string
	BSSID          net.HardwareAddr
	PHY            string
	QualityPct     int
	RSSI           int
	RxKbps, TxKbps uint32
	FreqMHz        int
}

type WifiCounters struct {
	TxFrames, RxFrames, Retries, Failures, FCSErrors uint64
}

type BSS struct {
	SSID                    string
	BSSID                   net.HardwareAddr
	RSSI, FreqMHz, WidthMHz int
}

type WifiEvent struct {
	Time   time.Time
	Name   string // record.NConnect, record.NDisconnect or record.NRoam
	Reason uint32
}

type Wifi interface {
	Link() (WifiLink, error)
	Counters() (WifiCounters, error)
	Scan() error
	BSSList() ([]BSS, error)
	Events(ctx context.Context) (<-chan WifiEvent, error)
	Close() error
}

type Platform interface {
	// NewPinger returns an ICMP pinger for family 4 or 6.
	NewPinger(family int) (Pinger, error)
	DefaultRoute() (Route, error)
	NeighborMAC(ip netip.Addr) (net.HardwareAddr, error)
	InterfaceCounters(index int) (Counters, error)
	Adapter(route Route) (AdapterInfo, error)
	Power() (PowerStatus, error)
	// KeepAwake prevents system sleep until ctx is done.
	KeepAwake(ctx context.Context) error
	OpenWifi() (Wifi, error)
	IsElevated() bool
	RelaunchElevated(args []string) error
	OpenURL(url string) error
	RevealFile(path string) error
	DocumentsDir() (string, error)
	DataDir() (string, error)
}
