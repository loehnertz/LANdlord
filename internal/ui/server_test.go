package ui

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fakeController struct {
	mu       sync.Mutex
	marks    []string
	finished bool
	user     string
	pass     string
	mbps     float64
}

func (f *fakeController) Status() Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Status{Recording: !f.finished, Marks: len(f.marks), Live: LiveView{State: "likely", Headline: "Likely cause so far: Weak Wi-Fi signal"}}
}
func (f *fakeController) Mark(tag string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.marks = append(f.marks, tag)
	return nil
}
func (f *fakeController) Finish() (string, error) {
	f.finished = true
	return `C:\Users\x\Documents\LANdlord\report.html`, nil
}
func (f *fakeController) SetRouterCredentials(user, pass string) { f.user, f.pass = user, pass }
func (f *fakeController) SetContractMbps(mbps float64) error {
	if mbps < 0 {
		return errors.New("invalid speed")
	}
	f.mbps = mbps
	return nil
}
func (f *fakeController) ReportSoFar(w io.Writer) error {
	_, err := io.WriteString(w, "<html>report</html>")
	return err
}
func (f *fakeController) OpenLocationSettings() error { return nil }

func newTestServer(t *testing.T) (*Server, *fakeController, http.Handler) {
	t.Helper()
	fc := &fakeController{}
	s, err := New(fc)
	if err != nil {
		t.Fatal(err)
	}
	s.port = 45678
	return s, fc, s.Handler()
}

func do(h http.Handler, method, target, host, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = host
	if token != "" {
		req.Header.Set("X-Landlord-Token", token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSecurityChecks(t *testing.T) {
	s, _, h := newTestServer(t)
	host := "127.0.0.1:45678"
	if rec := do(h, "GET", "/api/status", "evil.example:45678", s.Token(), ""); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong host: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/status", host, "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/status", host, "nope", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/mark", host, s.Token(), ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET on POST endpoint: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/report-so-far?token="+s.Token(), host, "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("report with wrong query token: %d", rec.Code)
	}
	page := do(h, "GET", "/", "localhost:45678", "", "")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "It's bad right now!") || page.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("page: %d %v", page.Code, page.Header())
	}
	if !strings.HasSuffix(s.URL(), "/?t="+s.Token()) {
		t.Fatalf("URL = %s", s.URL())
	}
}

func TestEndpoints(t *testing.T) {
	s, fc, h := newTestServer(t)
	host := "127.0.0.1:45678"
	if rec := do(h, "POST", "/api/mark", host, s.Token(), `{"tag":"call"}`); rec.Code != http.StatusOK {
		t.Fatalf("mark: %d %s", rec.Code, rec.Body)
	}
	var st Status
	rec := do(h, "GET", "/api/status", host, s.Token(), "")
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || st.Marks != 1 || st.Live.State != "likely" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	do(h, "POST", "/api/router", host, s.Token(), `{"user":"","password":"secret"}`)
	if fc.pass != "secret" {
		t.Fatal("router credentials not passed on")
	}
	if rec := do(h, "POST", "/api/contract", host, s.Token(), `{"mbps":-1}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid contract: %d", rec.Code)
	}
	rec = do(h, "GET", "/api/report-so-far", host, s.Token(), "")
	if rec.Code != http.StatusOK || rec.Body.String() != "<html>report</html>" {
		t.Fatalf("report so far: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "POST", "/api/finish", host, s.Token(), "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "report.html") || !fc.finished {
		t.Fatalf("finish: %d %s", rec.Code, rec.Body)
	}
	if fc.marks[0] != "call" {
		t.Fatalf("marks = %v", fc.marks)
	}
}
