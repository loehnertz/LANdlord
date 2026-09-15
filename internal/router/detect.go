// Package router identifies the home router and reads its WAN statistics.
package router

import (
	"context"
	"encoding/xml"
	"html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
)

type Identity struct {
	Vendor, Model, Method string
	FritzBox              bool
}

type deviceDescription struct {
	Device struct {
		Manufacturer string `xml:"manufacturer"`
		ModelName    string `xml:"modelName"`
		FriendlyName string `xml:"friendlyName"`
	} `xml:"device"`
}

// igdDevice is what SSDP discovery reports about an Internet Gateway Device.
type igdDevice struct {
	Manufacturer, Model string
	Host                string
}

// discoverIGDs is replaced in tests.
var discoverIGDs = discoverIGDsSSDP

var titlePattern = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// Detect identifies the router at gateway: TR-064 first (FRITZ!Box), then UPnP, then the web page title.
func Detect(ctx context.Context, client *http.Client, gateway netip.Addr) Identity {
	host := gateway.String()
	if gateway.Is6() {
		host = "[" + host + "]"
	}
	return detect(ctx, client, gateway, "http://"+host+":49000/tr64desc.xml", "http://"+host+"/")
}

func detect(ctx context.Context, client *http.Client, gateway netip.Addr, tr064URL, pageURL string) Identity {
	if desc, ok := fetchDescription(ctx, client, tr064URL); ok {
		id := Identity{Vendor: desc.Device.Manufacturer, Model: firstNonEmpty(desc.Device.ModelName, desc.Device.FriendlyName), Method: "tr064"}
		id.FritzBox = strings.Contains(strings.ToUpper(id.Vendor), "AVM")
		return id
	}
	for _, d := range discoverIGDs(ctx) {
		if d.Host == gateway.String() {
			return Identity{Vendor: d.Manufacturer, Model: d.Model, Method: "upnp"}
		}
	}
	if title := pageTitle(ctx, client, pageURL); title != "" {
		return Identity{Model: title, Method: "http"}
	}
	return Identity{Method: "unknown"}
}

func fetchDescription(ctx context.Context, client *http.Client, url string) (deviceDescription, bool) {
	var desc deviceDescription
	body, ok := get(ctx, client, url, 256<<10)
	if !ok || xml.Unmarshal(body, &desc) != nil || desc.Device.Manufacturer == "" {
		return desc, false
	}
	return desc, true
}

func pageTitle(ctx context.Context, client *http.Client, url string) string {
	body, ok := get(ctx, client, url, 256<<10)
	if !ok {
		return ""
	}
	m := titlePattern.FindSubmatch(body)
	if m == nil {
		return ""
	}
	title := strings.Join(strings.Fields(html.UnescapeString(string(m[1]))), " ")
	if len(title) > 80 {
		title = title[:80]
	}
	return title
}

func get(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	return body, err == nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func hostOf(rawURL string) string {
	rest := rawURL
	if _, after, ok := strings.Cut(rawURL, "://"); ok {
		rest = after
	}
	hostport, _, _ := strings.Cut(rest, "/")
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

// IdentityCollector records the router identity and reports it (for example to start the
// FRITZ!Box collector) whenever the gateway changes.
type IdentityCollector struct {
	p          platform.Platform
	client     *http.Client
	every      time.Duration
	onIdentity func(Identity, netip.Addr)
}

func NewIdentityCollector(p platform.Platform, onIdentity func(Identity, netip.Addr)) *IdentityCollector {
	if onIdentity == nil {
		onIdentity = func(Identity, netip.Addr) {}
	}
	return &IdentityCollector{p: p, client: &http.Client{Timeout: 5 * time.Second}, every: time.Minute, onIdentity: onIdentity}
}

func (c *IdentityCollector) Name() string { return record.CRouter }

func (c *IdentityCollector) Run(ctx context.Context, sink record.Sink) error {
	var last netip.Addr
	for {
		if route, err := c.p.DefaultRoute(); err == nil && route.Gateway4.IsValid() && route.Gateway4 != last {
			gw := route.Gateway4
			id := Detect(ctx, c.client, gw)
			if ctx.Err() != nil {
				return nil
			}
			attrs := map[string]string{"vendor": id.Vendor, "model": id.Model, "method": id.Method}
			if mac, err := c.p.NeighborMAC(gw); err == nil {
				attrs["mac"] = mac.String()
			}
			sink.Emit(record.Info(record.CRouter, record.NIdentity, gw.String(), time.Now(), attrs))
			c.onIdentity(id, gw)
			last = gw
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(c.every):
		}
	}
}
