package router

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/huin/goupnp"
	"github.com/huin/goupnp/dcps/internetgateway2"

	"github.com/loehnertz/LANdlord/internal/collect"
	"github.com/loehnertz/LANdlord/internal/record"
)

type WANStats struct {
	LinkDownBps, LinkUpBps uint64
	RxBytes, TxBytes       uint64
	Uptime                 uint64
	ExternalIP, Status     string
}

type IGD interface {
	Stats(ctx context.Context) (WANStats, error)
}

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

func discoverIGDsSSDP(ctx context.Context) []igdDevice {
	var out []igdDevice
	for _, urn := range []string{"urn:schemas-upnp-org:device:InternetGatewayDevice:2", "urn:schemas-upnp-org:device:InternetGatewayDevice:1"} {
		devices, err := goupnp.DiscoverDevicesCtx(ctx, urn)
		if err != nil {
			continue
		}
		for _, d := range devices {
			if d.Err != nil || d.Root == nil || d.Location == nil {
				continue
			}
			out = append(out, igdDevice{Manufacturer: d.Root.Device.Manufacturer, Model: d.Root.Device.ModelName, Host: d.Location.Hostname()})
		}
	}
	return out
}

type connection interface {
	GetStatusInfoCtx(ctx context.Context) (status string, lastError string, uptime uint32, err error)
	GetExternalIPAddressCtx(ctx context.Context) (string, error)
}

type goupnpIGD struct {
	common *internetgateway2.WANCommonInterfaceConfig1
	conn   connection
}

// DiscoverIGD finds the first UPnP Internet Gateway Device that exposes WAN statistics.
func DiscoverIGD(ctx context.Context) (IGD, error) {
	commons, _, err := internetgateway2.NewWANCommonInterfaceConfig1ClientsCtx(ctx)
	if err != nil {
		return nil, err
	}
	if len(commons) == 0 {
		return nil, errors.New("no UPnP internet gateway found")
	}
	g := &goupnpIGD{common: commons[0]}
	if c, _, err := internetgateway2.NewWANIPConnection2ClientsCtx(ctx); err == nil && len(c) > 0 {
		g.conn = c[0]
	} else if c, _, err := internetgateway2.NewWANIPConnection1ClientsCtx(ctx); err == nil && len(c) > 0 {
		g.conn = c[0]
	} else if c, _, err := internetgateway2.NewWANPPPConnection1ClientsCtx(ctx); err == nil && len(c) > 0 {
		g.conn = c[0]
	}
	return g, nil
}

func (g *goupnpIGD) Stats(ctx context.Context) (WANStats, error) {
	var st WANStats
	_, up, down, _, err := g.common.GetCommonLinkPropertiesCtx(ctx)
	if err != nil {
		return st, err
	}
	st.LinkUpBps, st.LinkDownBps = uint64(up), uint64(down)
	if rx, err := g.common.GetTotalBytesReceivedCtx(ctx); err == nil {
		st.RxBytes = uint64(rx)
	}
	if tx, err := g.common.GetTotalBytesSentCtx(ctx); err == nil {
		st.TxBytes = uint64(tx)
	}
	if g.conn != nil {
		if status, _, uptime, err := g.conn.GetStatusInfoCtx(ctx); err == nil {
			st.Status, st.Uptime = status, uint64(uptime)
		}
		if ip, err := g.conn.GetExternalIPAddressCtx(ctx); err == nil {
			st.ExternalIP = ip
		}
	}
	return st, nil
}

// counterDelta handles UPnP byte counters that are only 32 bits wide and wrap around.
func counterDelta(prev, cur uint64) uint64 {
	switch {
	case cur >= prev:
		return cur - prev
	case prev < 1<<32:
		return cur + (1<<32 - prev)
	default:
		return 0 // counter reset
	}
}

// DetectTunnel recognises CGNAT and DS-Lite from the router's WAN address, the address websites
// see, and whether IPv6 works. It returns an empty kind when there is no evidence.
func DetectTunnel(externalIP, publicIP string, ipv6Works bool) (kind, evidence string) {
	ext, err := netip.ParseAddr(externalIP)
	switch {
	case err != nil:
		return "", ""
	case cgnatPrefix.Contains(ext):
		return "cgnat", "the router's internet address is in the shared 100.64.0.0/10 range"
	case (ext.IsUnspecified() || ext.IsPrivate()) && ipv6Works:
		return "dslite", "the router has no public IPv4 address, while IPv6 works"
	case ext.IsPrivate():
		return "cgnat", "the router's internet address is a private address"
	case publicIP != "" && ext.String() != publicIP:
		return "cgnat", "the router's internet address differs from the address websites see"
	}
	return "", ""
}

type UPnPCollector struct {
	discover  func(context.Context) (IGD, error)
	every     time.Duration
	publicIP  func() string
	ipv6Works func() bool

	prev       *WANStats
	prevAt     time.Time
	tunnelSent map[string]bool
}

func NewUPnPCollector(every time.Duration, publicIP func() string, ipv6Works func() bool) *UPnPCollector {
	if every <= 0 {
		every = 30 * time.Second
	}
	if publicIP == nil {
		publicIP = func() string { return "" }
	}
	if ipv6Works == nil {
		ipv6Works = func() bool { return false }
	}
	return &UPnPCollector{discover: DiscoverIGD, every: every, publicIP: publicIP, ipv6Works: ipv6Works, tunnelSent: map[string]bool{}}
}

func (c *UPnPCollector) Name() string { return record.CUPnP }

func (c *UPnPCollector) Run(ctx context.Context, sink record.Sink) error {
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	igd, err := c.discover(dctx)
	cancel()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: the router doesn't offer UPnP statistics", collect.ErrPermanent)
	}
	failures := 0
	for {
		st, err := igd.Stats(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			if failures++; failures >= 10 {
				return fmt.Errorf("router statistics failed repeatedly: %w", err)
			}
		} else {
			failures = 0
			c.emit(st, time.Now(), sink)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(c.every):
		}
	}
}

func (c *UPnPCollector) emit(st WANStats, now time.Time, sink record.Sink) {
	values := map[string]float64{
		"uptime_s":      float64(st.Uptime),
		"link_down_bps": float64(st.LinkDownBps),
		"link_up_bps":   float64(st.LinkUpBps),
	}
	if c.prev != nil {
		if secs := now.Sub(c.prevAt).Seconds(); secs > 0 {
			values["rx_bps"] = float64(counterDelta(c.prev.RxBytes, st.RxBytes)) * 8 / secs
			values["tx_bps"] = float64(counterDelta(c.prev.TxBytes, st.TxBytes)) * 8 / secs
		}
		if st.Uptime > 0 && st.Uptime < c.prev.Uptime {
			sink.Emit(record.Record{Time: now, Collector: record.CUPnP, Kind: record.KindEvent, Name: record.NWANReconnect,
				Values: map[string]float64{"uptime_s": float64(st.Uptime)}})
		}
	}
	sink.Emit(record.Record{Time: now, Collector: record.CUPnP, Kind: record.KindMetric, Name: record.NWAN, Values: values,
		Attrs: map[string]string{"external_ip": st.ExternalIP, "status": st.Status}})
	if kind, evidence := DetectTunnel(st.ExternalIP, c.publicIP(), c.ipv6Works()); kind != "" && !c.tunnelSent[kind] {
		c.tunnelSent[kind] = true
		sink.Emit(record.Info(record.CUPnP, record.NTunnel, "", now, map[string]string{"kind": kind, "evidence": evidence}))
	}
	c.prev, c.prevAt = &st, now
}
