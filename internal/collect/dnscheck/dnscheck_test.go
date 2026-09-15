package dnscheck

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
)

func startServer(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	srv := &dns.Server{PacketConn: pc, NotifyStartedFunc: func() { close(started) }, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		resp := new(dns.Msg)
		resp.SetReply(req)
		if req.Question[0].Name == "ok.example." {
			rr, _ := dns.NewRR("ok.example. 60 IN A 192.0.2.1")
			resp.Answer = append(resp.Answer, rr)
		} else {
			resp.Rcode = dns.RcodeServerFailure
		}
		_ = w.WriteMsg(resp)
	})}
	go func() { _ = srv.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String()
}

func TestMeasure(t *testing.T) {
	addr := startServer(t)
	c := New(&platform.Fake{}, []string{"ok.example"}, time.Hour)
	var buf record.Buffer
	r := resolver{label: "configured:127.0.0.1", addr: addr}
	c.measure(context.Background(), r, "ok.example", &buf)
	c.measure(context.Background(), r, "broken.example", &buf)
	c.timeout = 100 * time.Millisecond
	c.measure(context.Background(), resolver{label: "dead", addr: "127.0.0.1:1"}, "ok.example", &buf)

	recs := buf.Filter(record.CDNS, record.NLookup)
	if len(recs) != 3 {
		t.Fatalf("records = %d", len(recs))
	}
	if recs[0].Values["ms"] <= 0 || recs[0].Target != "configured:127.0.0.1" || recs[0].Attrs["host"] != "ok.example" {
		t.Fatalf("success record = %+v", recs[0])
	}
	if recs[1].Values["failed"] != 1 || recs[1].Attrs["error"] != "DNS answer SERVFAIL" {
		t.Fatalf("servfail record = %+v", recs[1])
	}
	if recs[2].Values["failed"] != 1 {
		t.Fatalf("unreachable resolver should fail: %+v", recs[2])
	}
}

func TestDefaultResolvers(t *testing.T) {
	fake := &platform.Fake{RouteValue: platform.Route{DNSServers: []netip.Addr{netip.MustParseAddr("192.168.178.1"), netip.MustParseAddr("fec0:0:0:ffff::1"), netip.MustParseAddr("1.1.1.1")}}}
	got := New(fake, nil, 0).defaultResolvers()
	var labels []string
	for _, r := range got {
		labels = append(labels, r.label)
	}
	want := []string{"system", "configured:192.168.178.1", "configured:1.1.1.1", "8.8.8.8"}
	if len(labels) != len(want) {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("labels = %v, want %v", labels, want)
		}
	}
}

func TestRoundRotatesHosts(t *testing.T) {
	addr := startServer(t)
	c := New(&platform.Fake{}, []string{"ok.example", "other.example"}, time.Hour)
	c.resolvers = func() []resolver { return []resolver{{label: "local", addr: addr}} }
	var buf record.Buffer
	c.round(context.Background(), &buf)
	c.round(context.Background(), &buf)
	recs := buf.Filter(record.CDNS, record.NLookup)
	if len(recs) != 2 || recs[0].Attrs["host"] != "ok.example" || recs[1].Attrs["host"] != "other.example" {
		t.Fatalf("records = %+v", recs)
	}
}
