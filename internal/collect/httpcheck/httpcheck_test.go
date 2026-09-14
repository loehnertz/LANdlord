package httpcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/record"
)

func TestFetch(t *testing.T) {
	var userAgent string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.UserAgent()
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusForbidden) // any status means the network worked
		_, _ = w.Write([]byte(strings.Repeat("x", 100_000)))
	}))
	defer srv.Close()

	c := New([]string{"example.test"}, time.Hour)
	c.tlsConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig
	c.url = func(string) string { return srv.URL }
	var buf record.Buffer
	c.fetch(context.Background(), "example.test", &buf)

	c.timeout = 200 * time.Millisecond
	c.url = func(string) string { return "https://127.0.0.1:1/" }
	c.fetch(context.Background(), "down.test", &buf)

	recs := buf.Filter(record.CHTTP, record.NFetch)
	if len(recs) != 2 {
		t.Fatalf("records = %d", len(recs))
	}
	ok := recs[0].Values
	if recs[0].Target != "example.test" || ok["connect_ms"] <= 0 || ok["tls_ms"] <= 0 || ok["ttfb_ms"] < 5 {
		t.Fatalf("success values = %v", ok)
	}
	if !strings.HasPrefix(userAgent, "LANdlord/") {
		t.Fatalf("user agent = %q", userAgent)
	}
	if recs[1].Values["failed"] != 1 || recs[1].Attrs["error"] == "" {
		t.Fatalf("failure record = %+v", recs[1])
	}
}
