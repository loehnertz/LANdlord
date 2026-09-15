// Package ui serves the local status page on 127.0.0.1 and provides the tray icon.
package ui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

//go:embed page.html
var pageHTML []byte

type LiveView struct {
	State          string   `json:"state"`
	Headline       string   `json:"headline"`
	Culprit        string   `json:"culprit"`
	SharePct       float64  `json:"share_pct"`
	Caveats        []string `json:"caveats"`
	AwakeMinutes   float64  `json:"awake_minutes"`
	ProblemMinutes float64  `json:"problem_minutes"`
}

type CollectorView struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	LastError string `json:"last_error,omitempty"`
}

type Status struct {
	Recording           bool            `json:"recording"`
	Finishing           bool            `json:"finishing"`
	StartedAt           time.Time       `json:"started_at"`
	EndsAt              time.Time       `json:"ends_at"`
	RemainingSeconds    int64           `json:"remaining_seconds"`
	Marks               int             `json:"marks"`
	Live                LiveView        `json:"live"`
	Collectors          []CollectorView `json:"collectors"`
	LocationDenied      bool            `json:"location_denied"`
	HelperRunning       bool            `json:"helper_running"`
	RouterIsFritzBox    bool            `json:"router_is_fritzbox"`
	RouterNeedsPassword bool            `json:"router_needs_password"`
	ContractMbps        float64         `json:"contract_mbps"`
	ReportPath          string          `json:"report_path"`
	Error               string          `json:"error,omitempty"`
}

type Controller interface {
	Status() Status
	Mark(tag string) error
	Finish() (reportPath string, err error)
	SetRouterCredentials(user, pass string)
	SetContractMbps(mbps float64) error
	ReportSoFar(w io.Writer) error
	OpenLocationSettings() error
}

type Server struct {
	c     Controller
	token string
	port  int
	srv   *http.Server
}

func New(c Controller) (*Server, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return &Server{c: c, token: hex.EncodeToString(b)}, nil
}

func (s *Server) Token() string { return s.token }

// URL is the address to open in the browser. The token is a query parameter because Windows'
// ShellExecute doesn't reliably keep URL fragments; the page removes it from the address bar.
func (s *Server) URL() string { return fmt.Sprintf("http://127.0.0.1:%d/?t=%s", s.port, s.token) }

// BaseURL is the address without the token, for health checks.
func (s *Server) BaseURL() string { return fmt.Sprintf("http://127.0.0.1:%d", s.port) }

// Start listens on a random local port and serves until ctx is done.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.port = ln.Addr().(*net.TCPAddr).Port
	s.srv = &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(sctx)
	}()
	return nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.page)
	mux.HandleFunc("/healthz", s.api(http.MethodGet, false, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/status", s.api(http.MethodGet, false, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.c.Status())
	}))
	mux.HandleFunc("/api/mark", s.api(http.MethodPost, false, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tag string `json:"tag"`
		}
		if !readJSON(w, r, &body) {
			return
		}
		if len(body.Tag) > 40 {
			body.Tag = body.Tag[:40]
		}
		if err := s.c.Mark(body.Tag); err != nil {
			httpError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/finish", s.api(http.MethodPost, false, func(w http.ResponseWriter, r *http.Request) {
		path, err := s.c.Finish()
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]string{"report_path": path})
	}))
	mux.HandleFunc("/api/router", s.api(http.MethodPost, false, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			User     string `json:"user"`
			Password string `json:"password"`
		}
		if !readJSON(w, r, &body) {
			return
		}
		s.c.SetRouterCredentials(body.User, body.Password)
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/contract", s.api(http.MethodPost, false, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Mbps float64 `json:"mbps"`
		}
		if !readJSON(w, r, &body) {
			return
		}
		if err := s.c.SetContractMbps(body.Mbps); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/location-settings", s.api(http.MethodPost, false, func(w http.ResponseWriter, r *http.Request) {
		if err := s.c.OpenLocationSettings(); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/report-so-far", s.api(http.MethodGet, false, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := s.c.ReportSoFar(w); err != nil {
			httpError(w, http.StatusInternalServerError, err)
		}
	}))
	return s.guard(mux)
}

// guard rejects requests whose Host isn't this server, which blocks DNS rebinding.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != fmt.Sprintf("127.0.0.1:%d", s.port) && r.Host != fmt.Sprintf("localhost:%d", s.port) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) api(method string, queryToken bool, fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Landlord-Token")
		if token == "" && queryToken {
			token = r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != method {
			w.Header().Set("Allow", method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		fn(w, r)
	}
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; img-src data: blob:; frame-src 'self'")
	_, _ = w.Write(pageHTML)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		httpError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
