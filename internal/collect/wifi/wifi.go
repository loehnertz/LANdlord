// Package wifi records the Wi-Fi link, radio counters, neighbouring networks and connection events.
package wifi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/loehnertz/LANdlord/internal/collect"
	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
)

const locationDeniedReason = "location access for desktop apps is turned off"

type Collector struct {
	p          platform.Platform
	linkEvery  time.Duration
	scanEvery  time.Duration
	scanWait   time.Duration
	onLocation func(denied bool)
	trigger    chan struct{}

	// Owned by the Run goroutine.
	prev     *platform.WifiCounters
	denied   bool
	scanning atomic.Bool
	lastLink atomic.Pointer[platform.WifiLink]
}

func New(p platform.Platform, linkEvery, scanEvery time.Duration, onLocationDenied func(bool)) *Collector {
	if linkEvery <= 0 {
		linkEvery = 2 * time.Second
	}
	if scanEvery <= 0 {
		scanEvery = 10 * time.Minute
	}
	if onLocationDenied == nil {
		onLocationDenied = func(bool) {}
	}
	return &Collector{p: p, linkEvery: linkEvery, scanEvery: scanEvery, scanWait: 4 * time.Second,
		onLocation: onLocationDenied, trigger: make(chan struct{}, 1)}
}

func (c *Collector) Name() string { return record.CWifi }

// Trigger requests an immediate link sample and scan.
func (c *Collector) Trigger() {
	select {
	case c.trigger <- struct{}{}:
	default:
	}
}

func (c *Collector) Run(ctx context.Context, sink record.Sink) error {
	w, err := c.p.OpenWifi()
	if err != nil {
		if errors.Is(err, platform.ErrUnsupported) {
			return fmt.Errorf("%w: %w", collect.ErrPermanent, err)
		}
		return err
	}
	// Per-run context: when this run ends (for example before a supervisor restart), the event
	// subscription and its forwarding goroutine end with it, before the handle is closed.
	runCtx, cancel := context.WithCancel(ctx)
	defer w.Close()
	defer cancel()
	ctx = runCtx
	if events, err := w.Events(ctx); err == nil {
		go forwardEvents(ctx, events, sink)
	}
	linkTick := time.NewTicker(c.linkEvery)
	defer linkTick.Stop()
	scanTick := time.NewTicker(c.scanEvery)
	defer scanTick.Stop()

	c.sampleLink(w, sink)
	c.startScan(ctx, w, sink)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-linkTick.C:
			c.sampleLink(w, sink)
		case <-scanTick.C:
			c.startScan(ctx, w, sink)
		case <-c.trigger:
			c.sampleLink(w, sink)
			c.startScan(ctx, w, sink)
		}
	}
}

func (c *Collector) sampleLink(w platform.Wifi, sink record.Sink) {
	now := time.Now()
	link, err := w.Link()
	if errors.Is(err, platform.ErrLocationDenied) {
		if !c.denied {
			c.denied = true
			sink.Emit(record.Unavailable(record.CWifi, now, locationDeniedReason))
			c.onLocation(true)
		}
		return
	}
	if err != nil {
		return
	}
	if c.denied {
		c.denied = false
		c.onLocation(false)
	}
	if !link.Connected {
		c.prev = nil
		c.lastLink.Store(nil)
		sink.Emit(record.Metric(record.CWifi, record.NLink, "", now, map[string]float64{"connected": 0}))
		return
	}
	c.lastLink.Store(&link)
	rssi := float64(link.RSSI)
	if link.RSSI == 0 {
		// Windows maps signal quality linearly between -100 dBm (0 %) and -50 dBm (100 %).
		rssi = float64(link.QualityPct)/2 - 100
	}
	values := map[string]float64{
		"connected":   1,
		"rssi_dbm":    rssi,
		"quality_pct": float64(link.QualityPct),
		"rx_mbps":     float64(link.RxKbps) / 1000,
		"tx_mbps":     float64(link.TxKbps) / 1000,
		"channel":     float64(platform.ChannelFromFreq(link.FreqMHz)),
		"freq_mhz":    float64(link.FreqMHz),
	}
	if cur, err := w.Counters(); err == nil {
		if c.prev != nil && cur.TxFrames > c.prev.TxFrames && cur.Retries >= c.prev.Retries {
			values["retry_pct"] = min(100, float64(cur.Retries-c.prev.Retries)/float64(cur.TxFrames-c.prev.TxFrames)*100)
		}
		c.prev = &cur
	}
	sink.Emit(record.Record{Time: now, Collector: record.CWifi, Kind: record.KindMetric, Name: record.NLink, Values: values,
		Attrs: map[string]string{"ssid": link.SSID, "bssid": link.BSSID.String(), "band": platform.BandFromFreq(link.FreqMHz), "phy": link.PHY}})
}

func (c *Collector) startScan(ctx context.Context, w platform.Wifi, sink record.Sink) {
	if !c.scanning.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer c.scanning.Store(false)
		c.scanOnce(ctx, w, sink)
	}()
}

type network struct {
	SSID    string `json:"ssid"`
	BSSID   string `json:"bssid"`
	RSSI    int    `json:"rssi"`
	Channel int    `json:"channel"`
	Band    string `json:"band"`
	Width   int    `json:"width"`
}

func (c *Collector) scanOnce(ctx context.Context, w platform.Wifi, sink record.Sink) {
	if err := w.Scan(); err != nil && !errors.Is(err, platform.ErrUnsupported) {
		return
	}
	if c.scanWait > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.scanWait):
		}
	}
	list, err := w.BSSList()
	if err != nil {
		return
	}
	var connected platform.BSS
	found := false
	if link := c.lastLink.Load(); link != nil {
		for _, b := range list {
			if strings.EqualFold(b.BSSID.String(), link.BSSID.String()) {
				connected, found = b, true
				break
			}
		}
		if !found && link.FreqMHz > 0 {
			connected, found = platform.BSS{BSSID: link.BSSID, FreqMHz: link.FreqMHz, WidthMHz: 20}, true
		}
	}
	overlap := 0
	nets := make([]network, 0, len(list))
	for _, b := range list {
		nets = append(nets, network{SSID: b.SSID, BSSID: b.BSSID.String(), RSSI: b.RSSI, Channel: platform.ChannelFromFreq(b.FreqMHz), Band: platform.BandFromFreq(b.FreqMHz), Width: b.WidthMHz})
		if found && b.BSSID.String() != connected.BSSID.String() && b.RSSI > -80 && platform.Overlaps(connected, b) {
			overlap++
		}
	}
	encoded, _ := json.Marshal(nets)
	values := map[string]float64{"total_aps": float64(len(list))}
	if found {
		values["overlap_aps"] = float64(overlap)
		values["width_mhz"] = float64(connected.WidthMHz)
	}
	sink.Emit(record.Record{Time: time.Now(), Collector: record.CWifi, Kind: record.KindMetric, Name: record.NScan, Values: values,
		Attrs: map[string]string{"networks": string(encoded)}})
}

func forwardEvents(ctx context.Context, events <-chan platform.WifiEvent, sink record.Sink) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			t := ev.Time
			if t.IsZero() {
				t = time.Now()
			}
			sink.Emit(record.Record{Time: t, Collector: record.CWifi, Kind: record.KindEvent, Name: ev.Name,
				Values: map[string]float64{"reason": float64(ev.Reason)}})
		}
	}
}
