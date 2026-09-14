package publicip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/loehnertz/LANdlord/internal/record"
)

const trace = `fl=123f45
h=www.cloudflare.com
ip=203.0.113.7
ts=1789400000.123
visit_scheme=https
loc=DE
tls=TLSv1.3
`

func TestCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(trace)) }))
	defer srv.Close()
	var changes []string
	c := New(srv.URL, 0, func(ip string) { changes = append(changes, ip) })
	var buf record.Buffer
	if !c.check(context.Background(), &buf) || !c.check(context.Background(), &buf) {
		t.Fatal("check failed")
	}
	recs := buf.Filter(record.CPublic, record.NPublicIP)
	if len(recs) != 2 || recs[0].Attrs["ip"] != "203.0.113.7" || recs[0].Attrs["loc"] != "DE" || c.IP() != "203.0.113.7" {
		t.Fatalf("records = %+v", recs)
	}
	if len(changes) != 1 {
		t.Fatalf("onIP calls = %v", changes)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("nothing useful")) }))
	defer bad.Close()
	if New(bad.URL, 0, nil).check(context.Background(), &buf) {
		t.Fatal("response without ip should fail")
	}
}
