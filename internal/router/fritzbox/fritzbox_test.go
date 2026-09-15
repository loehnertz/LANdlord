package fritzbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/collect"
	"github.com/loehnertz/LANdlord/internal/record"
)

func TestDigestRFC2617Vector(t *testing.T) {
	ch := challenge{realm: "testrealm@host.com", nonce: "dcd98b7102dd2f0e8b11d0f600bfb0c093", qop: "auth,auth-int"}
	got := digestResponse(ch, "Mufasa", "Circle Of Life", "GET", "/dir/index.html", 1, "0a4f113b")
	if got != "6629fae49393a05397450978507c4ef1" {
		t.Fatalf("digest response = %s", got)
	}
	parsed, ok := parseChallenge(`Digest realm="F!Box SOAP-Auth", nonce="9D5A7D1F5E8E3C0A", algorithm=MD5, qop="auth"`)
	if !ok || parsed.realm != "F!Box SOAP-Auth" || parsed.nonce != "9D5A7D1F5E8E3C0A" || parsed.qop != "auth" {
		t.Fatalf("challenge = %+v", parsed)
	}
}

const tr64desc = `<?xml version="1.0"?>
<root xmlns="urn:dslforum-org:device-1-0"><device><manufacturer>AVM</manufacturer>
<serviceList><service><serviceType>urn:dslforum-org:service:DeviceInfo:1</serviceType><controlURL>/upnp/control/deviceinfo</controlURL></service></serviceList>
<deviceList><device><deviceList><device><serviceList>
<service><serviceType>urn:dslforum-org:service:WANDSLInterfaceConfig:1</serviceType><controlURL>/upnp/control/wandslifconfig1</controlURL></service>
</serviceList></device></deviceList></device></deviceList></device></root>`

func envelope(inner string) string {
	return `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body>` + inner + `</s:Body></s:Envelope>`
}

// fakeFritz serves TR-064 with digest auth; counters and sync rate change on every call.
func fakeFritz(t *testing.T, password string, dsl bool) *httptest.Server {
	t.Helper()
	var calls atomic.Int32
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tr64desc.xml" {
			_, _ = io.WriteString(w, tr64desc)
			return
		}
		ch := challenge{realm: "F!Box SOAP-Auth", nonce: "ABCDEF0123456789", qop: "auth"}
		auth := r.Header.Get("Authorization")
		mu.Lock()
		valid := false
		if auth != "" {
			params := map[string]string{}
			for _, p := range splitParams(strings.TrimPrefix(auth, "Digest ")) {
				k, v, _ := strings.Cut(p, "=")
				params[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
			}
			var nc int
			_, _ = fmt.Sscanf(params["nc"], "%x", &nc)
			valid = params["response"] == digestResponse(ch, params["username"], password, r.Method, params["uri"], nc, params["cnonce"])
		}
		mu.Unlock()
		if !valid {
			w.Header().Set("WWW-Authenticate", `Digest realm="F!Box SOAP-Auth", nonce="ABCDEF0123456789", algorithm=MD5, qop="auth"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		action := r.Header.Get("SOAPAction")
		n := calls.Add(1)
		switch {
		case !dsl && strings.HasPrefix(action, dslService):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, envelope(`<s:Fault><detail><UPnPError xmlns="urn:dslforum-org:control-1-0"><errorCode>401</errorCode><errorDescription>Invalid Action</errorDescription></UPnPError></detail></s:Fault>`))
		case strings.HasSuffix(action, "#GetInfo"):
			sync := 100000
			if n > 4 {
				sync = 90000
			}
			_, _ = fmt.Fprintf(w, envelope(`<u:GetInfoResponse xmlns:u="%s"><NewStatus>Up</NewStatus><NewUpstreamCurrRate>40000</NewUpstreamCurrRate><NewDownstreamCurrRate>%d</NewDownstreamCurrRate><NewUpstreamNoiseMargin>80</NewUpstreamNoiseMargin><NewDownstreamNoiseMargin>95</NewDownstreamNoiseMargin><NewUpstreamAttenuation>120</NewUpstreamAttenuation><NewDownstreamAttenuation>180</NewDownstreamAttenuation></u:GetInfoResponse>`), dslService, sync)
		case strings.HasSuffix(action, "#GetStatisticsTotal"):
			_, _ = fmt.Fprintf(w, envelope(`<u:GetStatisticsTotalResponse xmlns:u="%s"><NewCRCErrors>%d</NewCRCErrors><NewFECErrors>%d</NewFECErrors><NewHECErrors>0</NewHECErrors></u:GetStatisticsTotalResponse>`), dslService, 10*n, 100*n)
		case strings.HasSuffix(action, "#GetDeviceLog"):
			_, _ = io.WriteString(w, envelope(`<u:GetDeviceLogResponse xmlns:u="`+deviceInfoService+`"><NewDeviceLog>15.09.26 08:00:01 DSL synchronised
15.09.26 07:59:40 DSL line lost</NewDeviceLog></u:GetDeviceLogResponse>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestClientCall(t *testing.T) {
	srv := fakeFritz(t, "secret", true)
	defer srv.Close()
	c := &Client{Base: srv.URL, User: "admin", Password: "secret", HTTP: srv.Client()}
	info, err := c.Call(context.Background(), dslService, "GetInfo")
	if err != nil || info["NewDownstreamNoiseMargin"] != "95" || info["NewStatus"] != "Up" {
		t.Fatalf("GetInfo = %v, %v", info, err)
	}
	bad := &Client{Base: srv.URL, User: "admin", Password: "wrong", HTTP: srv.Client()}
	if _, err := bad.Call(context.Background(), dslService, "GetInfo"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong password error = %v", err)
	}
}

func runCollector(t *testing.T, srv *httptest.Server, creds func() (string, string, bool), until func(*record.Buffer) bool) (*record.Buffer, error) {
	t.Helper()
	c := &Collector{base: srv.URL, creds: creds, every: 5 * time.Millisecond, logEvery: time.Hour, poll: 5 * time.Millisecond}
	var buf record.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, &buf) }()
	deadline := time.Now().Add(3 * time.Second)
	for !until(&buf) {
		select {
		case err := <-done:
			cancel()
			return &buf, err
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	return &buf, <-done
}

func TestCollectorDSL(t *testing.T) {
	srv := fakeFritz(t, "secret", true)
	defer srv.Close()
	var entered atomic.Bool
	creds := func() (string, string, bool) { return "", "secret", entered.Load() }
	go func() { time.Sleep(30 * time.Millisecond); entered.Store(true) }()

	buf, err := runCollector(t, srv, creds, func(b *record.Buffer) bool {
		return len(b.Filter(record.CFritz, record.NResync)) > 0 && len(b.Filter(record.CFritz, record.NDSL)) >= 3
	})
	if err != nil {
		t.Fatal(err)
	}
	if u := buf.Filter(record.CFritz, record.CFritz); len(u) != 1 || u[0].Attrs["reason"] != "router password not entered" {
		t.Fatalf("unavailable records = %+v", u)
	}
	dsl := buf.Filter(record.CFritz, record.NDSL)
	if v := dsl[0].Values; v["snr_down_db"] != 9.5 || v["atten_down_db"] != 18 || v["sync_up_kbps"] != 40000 {
		t.Fatalf("first dsl values = %v", v)
	}
	// The fake server's counters grow with every call, so only the ratio is fixed.
	if v := dsl[1].Values; v["crc_delta"] <= 0 || v["fec_delta"] != 10*v["crc_delta"] {
		t.Fatalf("second dsl deltas = %v", v)
	}
	if logs := buf.Filter(record.CFritz, record.NDeviceLog); len(logs) != 1 || !strings.Contains(logs[0].Attrs["log"], "DSL line lost") {
		t.Fatalf("device log = %+v", logs)
	}
}

func TestCollectorWrongPasswordAndCableRouter(t *testing.T) {
	srv := fakeFritz(t, "secret", true)
	defer srv.Close()
	buf, err := runCollector(t, srv, func() (string, string, bool) { return "", "wrong", true }, func(b *record.Buffer) bool {
		return len(b.Filter(record.CFritz, record.CFritz)) > 0
	})
	if err != nil {
		t.Fatal(err)
	}
	if u := buf.Filter(record.CFritz, record.CFritz); u[0].Attrs["reason"] != "router password rejected" {
		t.Fatalf("unavailable = %+v", u)
	}

	cable := fakeFritz(t, "secret", false)
	defer cable.Close()
	_, err = runCollector(t, cable, func() (string, string, bool) { return "", "secret", true }, func(*record.Buffer) bool { return false })
	if !errors.Is(err, collect.ErrPermanent) || !strings.Contains(err.Error(), "no DSL line statistics") {
		t.Fatalf("cable router error = %v", err)
	}
}
