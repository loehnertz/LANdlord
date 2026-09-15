package router

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/collect"
	"github.com/loehnertz/LANdlord/internal/record"
)

const fritzDesc = `<?xml version="1.0"?>
<root xmlns="urn:dslforum-org:device-1-0">
 <device>
  <deviceType>urn:dslforum-org:device:InternetGatewayDevice:1</deviceType>
  <friendlyName>FRITZ!Box 7590</friendlyName>
  <manufacturer>AVM</manufacturer>
  <modelName>FRITZ!Box 7590</modelName>
 </device>
</root>`

var gw = netip.MustParseAddr("192.168.178.1")

func TestDetect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/tr64desc.xml", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(fritzDesc)) })
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><head><title>\n  Vodafone &amp; Station\n</title></head></html>"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := srv.Client()

	id := detect(context.Background(), client, gw, srv.URL+"/tr64desc.xml", srv.URL+"/")
	if !id.FritzBox || id.Vendor != "AVM" || id.Model != "FRITZ!Box 7590" || id.Method != "tr064" {
		t.Fatalf("tr064 identity = %+v", id)
	}

	discoverIGDs = func(context.Context) []igdDevice {
		return []igdDevice{{Manufacturer: "Other", Model: "Elsewhere", Host: "10.0.0.1"}, {Manufacturer: "Sagemcom", Model: "F@st 3896", Host: gw.String()}}
	}
	defer func() { discoverIGDs = discoverIGDsSSDP }()
	id = detect(context.Background(), client, gw, srv.URL+"/missing.xml", srv.URL+"/")
	if id.Method != "upnp" || id.Vendor != "Sagemcom" || id.FritzBox {
		t.Fatalf("upnp identity = %+v", id)
	}

	discoverIGDs = func(context.Context) []igdDevice { return nil }
	id = detect(context.Background(), client, gw, srv.URL+"/missing.xml", srv.URL+"/")
	if id.Method != "http" || id.Model != "Vodafone & Station" {
		t.Fatalf("http identity = %+v", id)
	}
	if got := hostOf("http://192.168.178.1:49000/x"); got != "192.168.178.1" {
		t.Fatalf("hostOf = %q", got)
	}
}

func TestCounterDelta(t *testing.T) {
	for _, tc := range []struct{ prev, cur, want uint64 }{
		{100, 250, 150},
		{1<<32 - 10, 5, 15},
		{1 << 40, 5, 0},
	} {
		if got := counterDelta(tc.prev, tc.cur); got != tc.want {
			t.Fatalf("counterDelta(%d, %d) = %d, want %d", tc.prev, tc.cur, got, tc.want)
		}
	}
}

func TestDetectTunnel(t *testing.T) {
	for _, tc := range []struct {
		ext, public string
		v6          bool
		want        string
	}{
		{"100.72.1.2", "", false, "cgnat"},
		{"0.0.0.0", "", true, "dslite"},
		{"192.168.0.2", "", true, "dslite"},
		{"10.0.0.2", "", false, "cgnat"},
		{"203.0.113.7", "198.51.100.9", false, "cgnat"},
		{"203.0.113.7", "203.0.113.7", true, ""},
		{"", "203.0.113.7", true, ""},
	} {
		if kind, _ := DetectTunnel(tc.ext, tc.public, tc.v6); kind != tc.want {
			t.Fatalf("DetectTunnel(%q, %q, %v) = %q, want %q", tc.ext, tc.public, tc.v6, kind, tc.want)
		}
	}
}

type scriptedIGD struct{ stats []WANStats }

func (s *scriptedIGD) Stats(context.Context) (WANStats, error) {
	if len(s.stats) == 0 {
		return WANStats{}, errors.New("done")
	}
	st := s.stats[0]
	s.stats = s.stats[1:]
	return st, nil
}

func TestUPnPEmit(t *testing.T) {
	c := NewUPnPCollector(time.Second, func() string { return "203.0.113.7" }, func() bool { return true })
	var buf record.Buffer
	t0 := time.Date(2026, 9, 14, 20, 0, 0, 0, time.UTC)
	c.emit(WANStats{LinkDownBps: 100e6, LinkUpBps: 40e6, RxBytes: 1000, TxBytes: 500, Uptime: 5000, ExternalIP: "0.0.0.0"}, t0, &buf)
	c.emit(WANStats{LinkDownBps: 100e6, LinkUpBps: 40e6, RxBytes: 1000 + 3_750_000, TxBytes: 500 + 375_000, Uptime: 20, ExternalIP: "0.0.0.0"}, t0.Add(30*time.Second), &buf)

	wans := buf.Filter(record.CUPnP, record.NWAN)
	if len(wans) != 2 {
		t.Fatalf("wan records = %d", len(wans))
	}
	if _, ok := wans[0].Values["rx_bps"]; ok {
		t.Fatal("first sample should have no rate")
	}
	if v := wans[1].Values; v["rx_bps"] != 1e6 || v["tx_bps"] != 1e5 || v["link_down_bps"] != 100e6 {
		t.Fatalf("rates = %v", v)
	}
	if len(buf.Filter(record.CUPnP, record.NWANReconnect)) != 1 {
		t.Fatal("reconnect not detected")
	}
	if tunnels := buf.Filter(record.CUPnP, record.NTunnel); len(tunnels) != 1 || tunnels[0].Attrs["kind"] != "dslite" {
		t.Fatalf("tunnel records = %+v", tunnels)
	}
}

func TestUPnPWithoutGatewayIsPermanent(t *testing.T) {
	c := NewUPnPCollector(time.Second, nil, nil)
	c.discover = func(context.Context) (IGD, error) { return nil, errors.New("none") }
	if err := c.Run(context.Background(), &record.Buffer{}); !errors.Is(err, collect.ErrPermanent) {
		t.Fatalf("expected permanent error, got %v", err)
	}
	c.discover = func(context.Context) (IGD, error) { return &scriptedIGD{stats: []WANStats{{Uptime: 1}}}, nil }
	c.every = time.Millisecond
	err := c.Run(context.Background(), &record.Buffer{})
	if err == nil || errors.Is(err, collect.ErrPermanent) {
		t.Fatalf("repeated stats failures should be a restartable error, got %v", err)
	}
}
