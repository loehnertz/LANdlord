package system

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
)

func TestSystemCollector(t *testing.T) {
	var calls atomic.Uint64
	fake := &platform.Fake{
		RouteValue: platform.Route{InterfaceName: "Wi-Fi", InterfaceIndex: 7, Description: "Intel Wi-Fi 6E", MAC: net.HardwareAddr{1, 2, 3, 4, 5, 6}, Wireless: true},
		CountersFunc: func(index int) (platform.Counters, error) {
			n := calls.Add(1)
			return platform.Counters{RxBytes: n * 125_000, TxBytes: n * 12_500, InErrors: n, LinkBps: 866_700_000}, nil
		},
		AdapterValue: platform.AdapterInfo{DriverVersion: "23.60.0.10", DriverDate: "2026-05-01"},
		PowerValue:   platform.PowerStatus{Known: true, OnAC: false},
	}
	c := New(fake, 20*time.Millisecond)
	var buf record.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = c.Run(ctx, &buf) })
	time.Sleep(200 * time.Millisecond)

	if mbps := c.LaptopMbps(time.Minute); mbps <= 0 {
		t.Fatalf("LaptopMbps = %v", mbps)
	}
	cancel()
	wg.Wait()

	ifaces := buf.Filter(record.CSystem, record.NIface)
	if len(ifaces) < 3 {
		t.Fatalf("iface samples = %d", len(ifaces))
	}
	v := ifaces[0].Values
	if v["rx_bps"] <= 0 || v["tx_bps"] <= 0 || v["in_errors"] != 1 || v["link_mbps"] != 866.7 || ifaces[0].Target != "Wi-Fi" {
		t.Fatalf("iface values = %v target=%s", v, ifaces[0].Target)
	}
	adapter := buf.Filter(record.CSystem, record.NAdapter)
	if len(adapter) != 1 || adapter[0].Attrs["driver_version"] != "23.60.0.10" || adapter[0].Attrs["wireless"] != "true" {
		t.Fatalf("adapter = %+v", adapter)
	}
	power := buf.Filter(record.CSystem, record.NPower)
	if len(power) == 0 || power[0].Values["on_ac"] != 0 {
		t.Fatalf("power = %+v", power)
	}
}

func TestSleepDetection(t *testing.T) {
	c := New(&platform.Fake{}, 0)
	var buf record.Buffer
	t0 := time.Date(2026, 9, 14, 22, 0, 0, 0, time.UTC)
	c.checkSleep(t0, &buf)
	c.checkSleep(t0.Add(5*time.Second), &buf)
	c.checkSleep(t0.Add(25*time.Minute), &buf)
	events := buf.Filter(record.CSystem, record.NSleep)
	if len(events) != 1 {
		t.Fatalf("sleep events = %d", len(events))
	}
	e := events[0]
	if e.Values["seconds"] != (25*time.Minute-5*time.Second).Seconds() || e.Attrs["from"] != "2026-09-14T22:00:05Z" || e.Attrs["to"] != "2026-09-14T22:25:00Z" {
		t.Fatalf("sleep event = %+v", e)
	}
}
