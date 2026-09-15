package fritzbox

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/loehnertz/LANdlord/internal/collect"
	"github.com/loehnertz/LANdlord/internal/record"
)

const (
	dslService        = "urn:dslforum-org:service:WANDSLInterfaceConfig:1"
	deviceInfoService = "urn:dslforum-org:service:DeviceInfo:1"
)

var fallbackControlURLs = map[string]string{
	dslService:        "/upnp/control/wandslifconfig1",
	deviceInfoService: "/upnp/control/deviceinfo",
}

var ErrUnauthorized = errors.New("router password rejected")

type SOAPFault struct {
	Code        int
	Description string
}

func (f *SOAPFault) Error() string { return fmt.Sprintf("TR-064 error %d: %s", f.Code, f.Description) }

type httpStatusError int

func (e httpStatusError) Error() string { return "TR-064 HTTP status " + strconv.Itoa(int(e)) }

// Client calls TR-064 actions with HTTP digest authentication.
type Client struct {
	Base           string // for example http://192.168.178.1:49000
	User, Password string
	HTTP           *http.Client

	mu          sync.Mutex
	challenge   *challenge
	nc          int
	controlURLs map[string]string
}

type serviceDesc struct {
	ServiceType string `xml:"serviceType"`
	ControlURL  string `xml:"controlURL"`
}

type deviceDesc struct {
	Services []serviceDesc `xml:"serviceList>service"`
	Devices  []deviceDesc  `xml:"deviceList>device"`
}

func (d deviceDesc) collect(into map[string]string) {
	for _, s := range d.Services {
		into[s.ServiceType] = s.ControlURL
	}
	for _, sub := range d.Devices {
		sub.collect(into)
	}
}

func (c *Client) controlURL(ctx context.Context, serviceType string) string {
	c.mu.Lock()
	urls := c.controlURLs
	c.mu.Unlock()
	if urls == nil {
		urls = map[string]string{}
		if req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/tr64desc.xml", nil); err == nil {
			if resp, err := c.HTTP.Do(req); err == nil {
				var root struct {
					Device deviceDesc `xml:"device"`
				}
				if resp.StatusCode == http.StatusOK && xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&root) == nil {
					root.Device.collect(urls)
				}
				resp.Body.Close()
			}
		}
		c.mu.Lock()
		c.controlURLs = urls
		c.mu.Unlock()
	}
	if u, ok := urls[serviceType]; ok {
		return u
	}
	return fallbackControlURLs[serviceType]
}

// Call runs a TR-064 action without arguments and returns the response values by element name.
func (c *Client) Call(ctx context.Context, serviceType, action string) (map[string]string, error) {
	path := c.controlURL(ctx, serviceType)
	if path == "" {
		return nil, httpStatusError(http.StatusNotFound)
	}
	body := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:%s xmlns:u="%s"></u:%s></s:Body></s:Envelope>`, action, serviceType, action)
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+path, strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
		req.Header.Set("SOAPAction", serviceType+"#"+action)
		c.mu.Lock()
		if c.challenge != nil {
			c.nc++
			req.Header.Set("Authorization", authorization(*c.challenge, c.User, c.Password, http.MethodPost, path, c.nc, newCnonce()))
		}
		c.mu.Unlock()
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode == http.StatusUnauthorized {
			ch, ok := parseChallenge(resp.Header.Get("WWW-Authenticate"))
			if !ok || attempt == 1 {
				return nil, ErrUnauthorized
			}
			c.mu.Lock()
			c.challenge, c.nc = &ch, 0
			c.mu.Unlock()
			continue
		}
		if fault := parseFault(data); fault != nil {
			if fault.Code == 606 {
				return nil, ErrUnauthorized
			}
			return nil, fault
		}
		if resp.StatusCode != http.StatusOK {
			return nil, httpStatusError(resp.StatusCode)
		}
		return parseResponse(data, action+"Response")
	}
	return nil, ErrUnauthorized
}

// parseResponse collects the text of every child element of the <actionResponse> element.
func parseResponse(data []byte, element string) (map[string]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	out := map[string]string{}
	inside := false
	var current string
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case t.Name.Local == element:
				inside = true
			case inside:
				current = t.Name.Local
				text.Reset()
			}
		case xml.CharData:
			if current != "" {
				text.Write(t)
			}
		case xml.EndElement:
			switch {
			case t.Name.Local == element:
				return out, nil
			case inside && t.Name.Local == current:
				out[current] = text.String()
				current = ""
			}
		}
	}
	if !inside {
		return nil, fmt.Errorf("response has no %s element", element)
	}
	return out, nil
}

func parseFault(data []byte) *SOAPFault {
	if !bytes.Contains(data, []byte("Fault")) {
		return nil
	}
	values, err := parseResponse(data, "UPnPError")
	if err != nil {
		return &SOAPFault{Description: "unknown SOAP fault"}
	}
	code, _ := strconv.Atoi(values["errorCode"])
	return &SOAPFault{Code: code, Description: values["errorDescription"]}
}

func isMissingService(err error) bool {
	var status httpStatusError
	var fault *SOAPFault
	return errors.As(err, &status) && status == http.StatusNotFound || errors.As(err, &fault) && fault.Code == 401
}

// Collector records DSL statistics, or DOCSIS values on cable models, once the router password
// is entered on the status page.
type Collector struct {
	base     string
	webBase  string
	creds    func() (user, pass string, ok bool)
	every    time.Duration
	logEvery time.Duration
	poll     time.Duration
}

func New(gateway netip.Addr, creds func() (user, pass string, ok bool), every time.Duration) *Collector {
	if every <= 0 {
		every = time.Minute
	}
	return &Collector{base: "http://" + gateway.String() + ":49000", webBase: "http://" + gateway.String(), creds: creds, every: every, logEvery: time.Hour, poll: 10 * time.Second}
}

func (c *Collector) Name() string { return record.CFritz }

func (c *Collector) wait(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (c *Collector) Run(ctx context.Context, sink record.Sink) error {
	client := &Client{Base: c.base, HTTP: &http.Client{Timeout: 10 * time.Second}}
	var announced, rejected string
	var prevStats map[string]float64
	var prevSync float64
	var lastLog time.Time
	for {
		user, pass, ok := c.creds()
		if !ok {
			if announced != "missing" {
				announced = "missing"
				sink.Emit(record.Unavailable(record.CFritz, time.Now(), "router password not entered"))
			}
			if !c.wait(ctx, c.poll) {
				return nil
			}
			continue
		}
		key := user + "\x00" + pass
		if key == rejected {
			if !c.wait(ctx, c.poll) {
				return nil
			}
			continue
		}
		if client.User != user || client.Password != pass {
			client.User, client.Password = user, pass
			client.mu.Lock()
			client.challenge = nil
			client.mu.Unlock()
		}

		info, err := client.Call(ctx, dslService, "GetInfo")
		if ctx.Err() != nil {
			return nil
		}
		switch {
		case errors.Is(err, ErrUnauthorized):
			rejected, announced = key, "rejected"
			sink.Emit(record.Unavailable(record.CFritz, time.Now(), "router password rejected"))
			continue
		case isMissingService(err):
			// Cable models have no DSL service over TR-064; their channel values are in the web interface.
			return c.runCable(ctx, sink)
		case err != nil:
			if !c.wait(ctx, c.every) {
				return nil
			}
			continue
		}
		announced = ""
		now := time.Now()
		values := map[string]float64{
			"snr_down_db":    tenths(info["NewDownstreamNoiseMargin"]),
			"snr_up_db":      tenths(info["NewUpstreamNoiseMargin"]),
			"atten_down_db":  tenths(info["NewDownstreamAttenuation"]),
			"atten_up_db":    tenths(info["NewUpstreamAttenuation"]),
			"sync_down_kbps": parseNumber(info["NewDownstreamCurrRate"]),
			"sync_up_kbps":   parseNumber(info["NewUpstreamCurrRate"]),
		}
		if stats, err := client.Call(ctx, dslService, "GetStatisticsTotal"); err == nil {
			cur := map[string]float64{"crc": parseNumber(stats["NewCRCErrors"]), "fec": parseNumber(stats["NewFECErrors"]), "hec": parseNumber(stats["NewHECErrors"])}
			if prevStats != nil {
				for _, k := range []string{"crc", "fec", "hec"} {
					values[k+"_delta"] = max(0, cur[k]-prevStats[k])
				}
			}
			prevStats = cur
		}
		if sync := values["sync_down_kbps"]; prevSync > 0 && sync > 0 && abs(sync-prevSync)/prevSync > 0.01 {
			sink.Emit(record.Record{Time: now, Collector: record.CFritz, Kind: record.KindEvent, Name: record.NResync,
				Values: map[string]float64{"sync_down_kbps": sync}})
		}
		prevSync = values["sync_down_kbps"]
		sink.Emit(record.Metric(record.CFritz, record.NDSL, "", now, values))

		if time.Since(lastLog) >= c.logEvery {
			if log, err := client.Call(ctx, deviceInfoService, "GetDeviceLog"); err == nil && log["NewDeviceLog"] != "" {
				sink.Emit(record.Info(record.CFritz, record.NDeviceLog, "", now, map[string]string{"log": lastLines(log["NewDeviceLog"], 200)}))
			}
			lastLog = time.Now()
		}
		if !c.wait(ctx, c.every) {
			return nil
		}
	}
}

// runCable records DOCSIS channel values from the web interface of a cable FRITZ!Box.
func (c *Collector) runCable(ctx context.Context, sink record.Sink) error {
	web := &WebClient{Base: c.webBase, HTTP: &http.Client{Timeout: 15 * time.Second}}
	var rejected string
	var prev *DOCSISInfo
	for {
		user, pass, ok := c.creds()
		if !ok || user+"\x00"+pass == rejected {
			if !c.wait(ctx, c.poll) {
				return nil
			}
			continue
		}
		info, err := web.DocInfo(ctx, user, pass)
		if ctx.Err() != nil {
			return nil
		}
		switch {
		case errors.Is(err, ErrUnauthorized):
			rejected = user + "\x00" + pass
			sink.Emit(record.Unavailable(record.CFritz, time.Now(), "router password rejected"))
			continue
		case errors.Is(err, errNoDOCSIS), isMissingService(err):
			return fmt.Errorf("%w: no line statistics on this router", collect.ErrPermanent)
		case err != nil:
			if !c.wait(ctx, c.every) {
				return nil
			}
			continue
		}
		values := map[string]float64{
			"ds_channels":       float64(info.DSChannels),
			"us_channels":       float64(info.USChannels),
			"ds_power_min_dbmv": info.DSPowerMin,
			"ds_power_max_dbmv": info.DSPowerMax,
			"us_power_max_dbmv": info.USPowerMax,
			"ds_mer_min_db":     info.DSMERMin,
		}
		if prev != nil {
			values["corr_errors_delta"] = max(0, info.CorrErrors-prev.CorrErrors)
			values["noncorr_errors_delta"] = max(0, info.NonCorrErrors-prev.NonCorrErrors)
		}
		prev = &info
		sink.Emit(record.Metric(record.CFritz, record.NDOCSIS, "", time.Now(), values))
		if !c.wait(ctx, c.every) {
			return nil
		}
	}
}

func parseNumber(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func tenths(s string) float64 { return parseNumber(s) / 10 }

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[:n] // the FRITZ!Box lists the newest entries first
	}
	return strings.Join(lines, "\n")
}
