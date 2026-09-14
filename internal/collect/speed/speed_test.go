package speed

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
)

func TestSpeedTestWithBufferbloat(t *testing.T) {
	var busy atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/down", func(w http.ResponseWriter, r *http.Request) {
		busy.Store(true)
		defer busy.Store(false)
		n, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
		chunk := make([]byte, 32<<10)
		for sent := 0; sent < n; sent += len(chunk) {
			_, _ = w.Write(chunk[:min(len(chunk), n-sent)])
			time.Sleep(10 * time.Millisecond)
		}
	})
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		busy.Store(true)
		defer busy.Store(false)
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(100 * time.Millisecond)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	fake := &platform.Fake{
		RouteValue: platform.Route{Gateway4: netip.MustParseAddr("192.168.178.1")},
		EchoFunc: func(req platform.EchoRequest) (platform.EchoReply, error) {
			rtt := 5 * time.Millisecond
			if busy.Load() {
				rtt = 60 * time.Millisecond
			}
			return platform.EchoReply{From: req.Dst, RTT: rtt, Status: platform.EchoOK}, nil
		},
	}
	cfg := DefaultConfig()
	cfg.DownURL = srv.URL + "/down?bytes=%d"
	cfg.UpURL = srv.URL + "/up"
	cfg.BaselineFor = 150 * time.Millisecond
	cfg.PingEvery = 10 * time.Millisecond
	c := New(fake, cfg, nil)

	var buf record.Buffer
	if !c.test(context.Background(), "interval", 512<<10, 256<<10, &buf) {
		t.Fatal("test reported failure")
	}
	tests := buf.Filter(record.CSpeed, record.NTest)
	if len(tests) != 1 {
		t.Fatalf("tests = %d", len(tests))
	}
	v := tests[0].Values
	if v["down_mbps"] <= 0 || v["up_mbps"] <= 0 || v["bloat_ms"] < 40 || tests[0].Attrs["grade"] != "C" || tests[0].Attrs["trigger"] != "interval" {
		t.Fatalf("test = %v %v", v, tests[0].Attrs)
	}
}

func TestSkipWhenLaptopBusy(t *testing.T) {
	c := New(&platform.Fake{}, DefaultConfig(), func(time.Duration) float64 { return 5 })
	var buf record.Buffer
	if c.test(context.Background(), "interval", 1, 1, &buf) {
		t.Fatal("busy laptop should skip")
	}
	skipped := buf.Filter(record.CSpeed, record.NSkipped)
	if len(skipped) != 1 || skipped[0].Values["laptop_mbps"] != 5 {
		t.Fatalf("skipped = %+v", skipped)
	}
	if len(buf.Filter(record.CSpeed, record.NTest)) != 0 {
		t.Fatal(fmt.Sprint("unexpected test record"))
	}
}
