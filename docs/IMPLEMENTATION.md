# LANdlord implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build LANdlord, a Windows program that records a home connection for 48 hours and writes a self-contained `report.html` naming the most likely culprit for bad calls and streams.

**Architecture:** One Go binary. Collectors emit `record.Record`s into hourly gzip JSONL files in a session directory. The analysis pipeline (aggregate into 10 s buckets, diagnose with fixed rules, render the report) is pure Go and runs on any OS. Windows-specific data (Wi-Fi, ICMP, interface counters, power, event log) sits behind `internal/platform` interfaces with fakes for tests. A local web page and a tray icon form the UI; an optional elevated helper process adds admin-only data.

**Tech Stack:** Go (no cgo), `golang.org/x/sys/windows`, `golang.org/x/net`, `fyne.io/systray`, `github.com/miekg/dns`, `github.com/pion/stun/v3`, `github.com/huin/goupnp`, `github.com/pelletier/go-toml/v2`, uPlot 1.6.31 (vendored), GoReleaser, GitHub Actions.

**Spec:** [docs/DESIGN.md](DESIGN.md). Read it before starting any task. Where this plan and the spec disagree, this plan wins for implementation details and the "Deviations" list below says why.

**Level of detail:** Tasks 1 to 6 (foundation) contain complete code. From Task 7 on, tasks specify files, interfaces, behaviour, the tests to write and the platform details that are easy to get wrong; the code is written during implementation, test first, in the same style as Tasks 1 to 6.

## Global constraints

- Module path: `github.com/loehnertz/LANdlord`.
- `go.mod` declares `go 1.26` (raised from 1.25 because `golang.org/x/net` requires it). No cgo anywhere (`CGO_ENABLED=0` must build every target).
- Git branch is `master`. Commit after every task with a message in the form `<area>: <what>`, ending with the line `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`.
- Everything user-facing is in English.
- LANdlord never changes system settings. It only reads.
- No telemetry and no uploads. Outgoing connections go only to configured measurement targets, Cloudflare trace/speed endpoints and the local router.
- The report HTML must not reference any external URL (no CDN, no fonts, no images from the network).
- Default thresholds (copy exactly): loss 1 %, jitter 30 ms, RTT p95 150 ms, merge gap 30 s, mark window 2 min, gateway floor 30 ms, gateway factor 5, weak RSSI -70 dBm, weak receive ratio 0.3, retry 10 %, overlapping APs 4, router utilisation 0.8, laptop share 0.2, slow DNS 500 ms, standing RSSI -67 dBm.
- Live verdict defaults (copy exactly): likely needs 30 min awake, 1 min problem time, 50 % share; confident needs 2 h awake, 3 min problem time for the main culprit, 60 % share, 2 incidents, confidence medium or high; "no problems so far" needs 2 h awake and problem time below 0.5 %. The live diagnosis re-runs every 30 s on an incrementally updated aggregate.
- Default intervals: Wi-Fi 2 s, ping 1 s, traceroute 5 min, scan 10 min, STUN burst 50 pps for 10 s every 60 s, HTTP 60 s, DNS 30 s, UPnP 30 s, FRITZ!Box 60 s, public IP 30 min, MTU 60 min, speed test 60 min capped at 25 MB down and 10 MB up, speed test skipped when laptop traffic > 1 Mbit/s over the previous 30 s.
- Default duration 48 h. Data directory cap 200 MB.
- Local UI binds to `127.0.0.1` only, requires the `X-Landlord-Token` header and a `Host` of `127.0.0.1:<port>` or `localhost:<port>`, and accepts state changes only via POST.
- Router password is held in memory only.
- Windows-only files use the `_windows.go` suffix; their non-Windows counterparts use `//go:build !windows`.

## Deviations from the spec

- Storage cap: instead of rewriting old raw ping records into buckets, the writer switches to keeping every fifth ping echo record once the session directory exceeds the cap, and logs a `downsampled` event. Same effect on disk use, far less code, and the aggregate step already copes with fewer samples.
- Single-instance detection uses a `running.json` file plus a health check on the local UI instead of a named mutex, so it works the same on every OS.
- Sleep detection compares wall-clock time between 5 s ticks (a gap over 30 s is a sleep) instead of subscribing to power notifications. It needs no Windows API and catches lid-close suspends as well.
- Router identification drops the MAC vendor table (see the updated DESIGN.md): TR-064 and UPnP descriptions are authoritative.
- DOCSIS levels for cable FRITZ!Boxes moved to "Later" in DESIGN.md.
- Platform packages are `internal/platform/winplat` and `internal/platform/unixplat`, and the constructor for the current OS is `host.New()` in `internal/platform/host`. A `platform.Current()` inside `internal/platform` would create an import cycle, because the implementations import the interface package.

## Prerequisites

Go 1.25 or newer. `AGENTS.md` (with `CLAUDE.md` symlinked to it) at the repository root describes the conventions below for coding agents.

## File structure

```
cmd/landlord/main.go                 subcommand dispatch: run (default), report, simulate, version, --helper
internal/version/version.go          build-time version string
internal/record/record.go            Record, Kind, Sink, SinkFunc, Buffer, constructors
internal/record/names.go             collector, record name and target label constants
internal/config/config.go            Config, Thresholds, Duration, Default, Load
internal/store/session.go            Meta, Mark, Session, Create, Open, Latest, OpenOrResume
internal/store/writer.go             Writer (record.Sink), hourly gzip JSONL, downsampling
internal/store/reader.go             Session.ReadRecords
internal/aggregate/aggregate.go      Session, Bucket and stats types, Build
internal/aggregate/stats.go          percentile, jitter, median helpers
internal/sim/sim.go                  Gen, Scenario, Run
internal/sim/scenarios.go            built-in scenarios
internal/diagnose/types.go           Culprit, Confidence, Incident, Finding, Recommendation, Result
internal/diagnose/buckets.go         bad-bucket detection and path helpers
internal/diagnose/rules.go           per-bucket culprit rules
internal/diagnose/session.go         session-level passes: congestion, periodic spikes
internal/diagnose/incidents.go       merging, marks, shares, confidence
internal/diagnose/findings.go        standing findings
internal/diagnose/recommend.go       recommendations and country hints
internal/diagnose/analyze.go         Analyze entry point
internal/report/report.go            Render, Options, data encoding, redaction
internal/report/csv.go               bucket CSV (also embedded for the export button)
internal/report/template.html        page template
internal/report/assets/app.js        charts, heatmap, zoom, redaction toggle, CSV export
internal/report/assets/style.css     light/dark, print, narrow screens
internal/report/assets/uPlot.iife.min.js, uPlot.min.css, UPLOT-LICENSE
internal/platform/platform.go        interfaces, shared types, ErrUnsupported, ErrLocationDenied
internal/platform/wifiutil.go        channel/band from frequency, IE width parsing (pure Go)
internal/platform/fake.go            Fake platform for tests
internal/platform/windows/*.go       Windows implementation (package winplat)
internal/platform/unix/*.go          darwin/linux implementation (package unixplat)
internal/platform/current_windows.go, current_other.go   platform.Current()
internal/collect/supervisor.go       Supervisor, Collector, State
internal/collect/ping/ping.go        gateway, hop and internet pings
internal/collect/traceroute/traceroute.go   ICMP traceroute, hop discovery
internal/collect/wifi/wifi.go        link, counters, events, scans
internal/collect/system/system.go    interface counters, power, sleep, adapter info, keep-awake
internal/collect/dnscheck/dnscheck.go
internal/collect/httpcheck/httpcheck.go
internal/collect/stuncheck/stuncheck.go
internal/collect/publicip/publicip.go
internal/collect/speed/speed.go
internal/collect/mtu/mtu.go
internal/router/detect.go            identity detection
internal/router/upnp.go              IGD client and collector, tunnel detection
internal/router/fritzbox/digest.go   HTTP digest auth
internal/router/fritzbox/tr064.go    SOAP client, DSL collector, device log
internal/collect/eventlog/eventlog.go   wevtutil-based history (Windows), unavailable elsewhere
internal/collect/admin/admin.go      wlanreport, driver properties, TCP traceroute (helper only)
internal/ui/server.go                HTTP server, auth middleware, endpoints
internal/ui/page.html                status page
internal/ui/tray_windows.go, tray_other.go
internal/app/app.go                  Recorder: session lifecycle, wiring, finish
internal/app/instance.go             running.json single-instance handling
internal/app/helper.go               helper process entry and parent watch
tools/genicon/main.go                generates assets/landlord.ico
.github/workflows/ci.yml, release.yml
.goreleaser.yaml, .golangci.yml, Makefile, winres/winres.json
README.md, CONTRIBUTING.md, LICENSE, docs/USER-GUIDE.md
```

## Record conventions

Every collector and the aggregate step use these exact names. They are defined as constants in `internal/record/names.go` (Task 2).

| Collector | Kind | Name | Target | Values | Attrs |
|---|---|---|---|---|---|
| `ping` | metric | `echo` | `gateway`, `gateway6`, `hop1`..`hop3`, `inet:<ip>`, `inet6:<ip>` | `rtt_ms` or `lost`=1 | `ip` |
| `trace` | info | `route` | destination IP | `hops` (count) | `path` (comma-separated, `*` for no reply) |
| `trace` | event | `route_change` | destination IP | | `old`, `new` |
| `wifi` | metric | `link` | | `connected`, `rssi_dbm`, `quality_pct`, `rx_mbps`, `tx_mbps`, `channel`, `freq_mhz`, `retry_pct` | `ssid`, `bssid`, `band`, `phy` |
| `wifi` | metric | `scan` | | `total_aps`, `overlap_aps`, `width_mhz` | `networks` (JSON array) |
| `wifi` | event | `connect`, `disconnect`, `roam` | | `reason` | `reason_text` |
| `system` | metric | `iface` | interface name | `rx_bps`, `tx_bps`, `in_errors`, `out_errors`, `discards`, `link_mbps` | |
| `system` | metric | `power` | | `on_ac` | |
| `system` | event | `sleep` | | `seconds` | `from`, `to` (RFC 3339) |
| `system` | info | `adapter` | | | `name`, `description`, `mac`, `driver_version`, `driver_date`, `wireless` |
| `dns` | metric | `lookup` | `system`, `configured:<ip>`, `<ip>` | `ms` or `failed`=1 | `host`, `error` |
| `http` | metric | `fetch` | host | `connect_ms`, `tls_ms`, `ttfb_ms` or `failed`=1 | `error` |
| `stun` | metric | `burst` | server | `sent`, `received`, `loss_pct`, `jitter_ms`, `rtt_p50_ms` | |
| `stun` | info | `nat` | | | `mapping` (`endpoint-independent`, `endpoint-dependent`, `unknown`) |
| `speed` | metric | `test` | | `down_mbps`, `up_mbps`, `bloat_ms` | `grade`, `trigger` |
| `speed` | event | `skipped` | | `laptop_mbps` | `reason` |
| `public` | info | `public_ip` | | | `ip`, `loc` |
| `mtu` | metric | `pmtu` | destination | `mtu` | |
| `router` | info | `identity` | gateway IP | | `vendor`, `model`, `method`, `mac` |
| `upnp` | metric | `wan` | | `uptime_s`, `rx_bps`, `tx_bps`, `link_down_bps`, `link_up_bps` | `external_ip`, `status` |
| `upnp` | event | `wan_reconnect` | | `uptime_s` | |
| `upnp` | info | `tunnel` | | | `kind` (`cgnat`, `dslite`), `evidence` |
| `fritz` | metric | `dsl` | | `snr_down_db`, `snr_up_db`, `atten_down_db`, `atten_up_db`, `sync_down_kbps`, `sync_up_kbps`, `crc_delta`, `fec_delta`, `hec_delta` | |
| `fritz` | event | `resync` | | `sync_down_kbps` | |
| `fritz` | info | `device_log` | | | `log` |
| `eventlog` | info | `wlan_history` | | `disconnects_7d`, `connect_failures_7d`, `ncsi_changes_7d` | `daily` (JSON) |
| `admin` | info | `wlanreport` | | | `file` |
| `admin` | info | `driver` | | | registry values |
| `admin` | info | `tcp_route` | destination | `hops` | `path` |
| `landlord` | event | `start`, `stop`, `downsampled` | | | `version` |
| any | unavailable | collector name | | | `reason` |

Marks are stored in the session `meta.json`, not as records.

---

### Task 1: Toolchain and repository scaffold

**Files:**
- Create: `go.mod`, `.gitignore`, `LICENSE`, `Makefile`, `.golangci.yml`, `.github/workflows/ci.yml`, `README.md`
- Create: `internal/version/version.go`, `internal/version/version_test.go`
- Create: `cmd/landlord/main.go`

**Interfaces:**
- Consumes: nothing
- Produces: `version.Version string`, `version.Commit string` (set via `-ldflags -X`), `version.String() string`

- [ ] **Step 1: Check the Go toolchain**

Run: `go version`
Expected: `go1.25` or newer.

- [ ] **Step 2: Initialise the module**

Run: `go mod init github.com/loehnertz/LANdlord && go mod edit -go=1.25`
Expected: `go.mod` exists with `module github.com/loehnertz/LANdlord` and `go 1.25`.

- [ ] **Step 3: Write the failing test**

`internal/version/version_test.go`:

```go
package version

import "testing"

func TestString(t *testing.T) {
	Version, Commit = "1.2.3", ""
	if got := String(); got != "1.2.3" {
		t.Fatalf("String() = %q, want %q", got, "1.2.3")
	}
	Commit = "abc123"
	if got := String(); got != "1.2.3 (abc123)" {
		t.Fatalf("String() = %q, want %q", got, "1.2.3 (abc123)")
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./internal/version/`
Expected: FAIL, `undefined: Version`.

- [ ] **Step 5: Implement**

`internal/version/version.go`:

```go
// Package version holds the build version, set by the release build via -ldflags.
package version

var (
	Version = "dev"
	Commit  = ""
)

func String() string {
	if Commit == "" {
		return Version
	}
	return Version + " (" + Commit + ")"
}
```

`cmd/landlord/main.go` (extended in Task 12 and Task 28):

```go
package main

import (
	"fmt"
	"os"

	"github.com/loehnertz/LANdlord/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("landlord", version.String())
		return
	}
	fmt.Fprintln(os.Stderr, "usage: landlord version")
	os.Exit(2)
}
```

`.gitignore`:

```
/dist/
/bin/
*.exe
*.syso
/testdata/out/
.DS_Store
```

`LICENSE`: the standard MIT license text with `Copyright (c) 2026 Jakob Löhnertz`.

`Makefile`:

```make
.PHONY: test lint windows sim-report

test:
	go test ./...

lint:
	go vet ./...
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run

windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-H=windowsgui" -o bin/landlord.exe ./cmd/landlord

sim-report:
	go run ./cmd/landlord simulate -scenario weak_wifi -out testdata/out
```

`.golangci.yml`:

```yaml
version: "2"
linters:
  default: standard
  enable:
    - errorlint
    - misspell
    - unconvert
```

`.github/workflows/ci.yml`:

```yaml
name: ci
on:
  push:
    branches: [master]
  pull_request:
jobs:
  test:
    strategy:
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go vet ./...
      - run: go test ./...
        env:
          CGO_ENABLED: "0"
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run
  cross-build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...
      - run: GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build ./...
      - run: GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./...
```

`README.md` (expanded in Task 28):

```markdown
# LANdlord

Find out why your internet is bad: Wi-Fi, router, line, provider or DNS.
LANdlord records your connection on Windows for 48 hours and writes a single report naming the most likely culprit.

Status: under development. See [docs/DESIGN.md](docs/DESIGN.md).
```

- [ ] **Step 6: Run tests and build**

Run: `go test ./... && go run ./cmd/landlord version`
Expected: tests PASS, prints `landlord dev`.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "scaffold: module, CI, version package" -m "Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 2: Record type and naming conventions

**Files:**
- Create: `internal/record/record.go`, `internal/record/names.go`, `internal/record/record_test.go`

**Interfaces:**
- Consumes: nothing
- Produces:
  - `type Kind string`, constants `KindMetric`, `KindEvent`, `KindInfo`, `KindUnavailable`
  - `type Record struct { Time time.Time; Collector string; Kind Kind; Name string; Target string; Values map[string]float64; Attrs map[string]string }`
  - `type Sink interface { Emit(Record) }`, `type SinkFunc func(Record)`
  - `type Buffer struct` with `Emit(Record)`, `Records() []Record`, `Filter(collector, name string) []Record`
  - `func Metric(collector, name, target string, t time.Time, v map[string]float64) Record`
  - `func Event(collector, name string, t time.Time, attrs map[string]string) Record`
  - `func Info(collector, name, target string, t time.Time, attrs map[string]string) Record`
  - `func Unavailable(collector string, t time.Time, reason string) Record`
  - `func HopTarget(n int) string`, `func InetTarget(a netip.Addr) string`, `func IsInetTarget(t string) bool`, `func IsInet6Target(t string) bool`
  - All constants from the "Record conventions" table (`CPing`, `NEcho`, `TGateway`, ...)

- [ ] **Step 1: Write the failing test**

`internal/record/record_test.go`:

```go
package record

import (
	"encoding/json"
	"net/netip"
	"testing"
	"time"
)

func TestRecordJSONRoundTrip(t *testing.T) {
	ts := time.Date(2026, 9, 14, 18, 0, 0, 0, time.UTC)
	in := Metric(CPing, NEcho, TGateway, ts, map[string]float64{"rtt_ms": 3.5})
	in.Attrs = map[string]string{"ip": "192.168.178.1"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"t":"2026-09-14T18:00:00Z","c":"ping","k":"metric","n":"echo","tg":"gateway","v":{"rtt_ms":3.5},"a":{"ip":"192.168.178.1"}}`
	if string(b) != want {
		t.Fatalf("json = %s\nwant  %s", b, want)
	}
	var out Record
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Time.Equal(in.Time) || out.Values["rtt_ms"] != 3.5 || out.Target != TGateway {
		t.Fatalf("round trip mismatch: %+v", out)
	}
}

func TestTargets(t *testing.T) {
	v4 := InetTarget(netip.MustParseAddr("1.1.1.1"))
	v6 := InetTarget(netip.MustParseAddr("2606:4700:4700::1111"))
	if v4 != "inet:1.1.1.1" || v6 != "inet6:2606:4700:4700::1111" {
		t.Fatalf("got %q %q", v4, v6)
	}
	if !IsInetTarget(v4) || IsInetTarget(v6) || !IsInet6Target(v6) || IsInet6Target(TGateway) {
		t.Fatal("target classification wrong")
	}
	if HopTarget(2) != "hop2" {
		t.Fatal("hop target wrong")
	}
}

func TestBufferFilter(t *testing.T) {
	var b Buffer
	now := time.Now()
	b.Emit(Metric(CPing, NEcho, TGateway, now, nil))
	b.Emit(Event(CWifi, NRoam, now, nil))
	b.Emit(Unavailable(CFritz, now, "no password"))
	if got := len(b.Filter(CWifi, NRoam)); got != 1 {
		t.Fatalf("Filter roam = %d, want 1", got)
	}
	u := b.Filter(CFritz, "")
	if len(u) != 1 || u[0].Kind != KindUnavailable || u[0].Attrs["reason"] != "no password" {
		t.Fatalf("unavailable record wrong: %+v", u)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/record/`
Expected: FAIL, `undefined: Metric`.

- [ ] **Step 3: Implement**

`internal/record/record.go`:

```go
// Package record defines the measurement record every collector emits and
// every analysis step consumes.
package record

import (
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type Kind string

const (
	KindMetric      Kind = "metric"
	KindEvent       Kind = "event"
	KindInfo        Kind = "info"
	KindUnavailable Kind = "unavailable"
)

type Record struct {
	Time      time.Time          `json:"t"`
	Collector string             `json:"c"`
	Kind      Kind               `json:"k"`
	Name      string             `json:"n,omitempty"`
	Target    string             `json:"tg,omitempty"`
	Values    map[string]float64 `json:"v,omitempty"`
	Attrs     map[string]string  `json:"a,omitempty"`
}

type Sink interface {
	Emit(Record)
}

type SinkFunc func(Record)

func (f SinkFunc) Emit(r Record) { f(r) }

// Buffer is an in-memory Sink, mainly for tests.
type Buffer struct {
	mu   sync.Mutex
	recs []Record
}

func (b *Buffer) Emit(r Record) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.recs = append(b.recs, r)
}

func (b *Buffer) Records() []Record {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Record(nil), b.recs...)
}

// Filter returns records of the collector whose Name matches; an empty name matches all.
func (b *Buffer) Filter(collector, name string) []Record {
	var out []Record
	for _, r := range b.Records() {
		if r.Collector == collector && (name == "" || r.Name == name) {
			out = append(out, r)
		}
	}
	return out
}

func Metric(collector, name, target string, t time.Time, v map[string]float64) Record {
	return Record{Time: t, Collector: collector, Kind: KindMetric, Name: name, Target: target, Values: v}
}

func Event(collector, name string, t time.Time, attrs map[string]string) Record {
	return Record{Time: t, Collector: collector, Kind: KindEvent, Name: name, Attrs: attrs}
}

func Info(collector, name, target string, t time.Time, attrs map[string]string) Record {
	return Record{Time: t, Collector: collector, Kind: KindInfo, Name: name, Target: target, Attrs: attrs}
}

func Unavailable(collector string, t time.Time, reason string) Record {
	return Record{Time: t, Collector: collector, Kind: KindUnavailable, Name: collector, Attrs: map[string]string{"reason": reason}}
}

func HopTarget(n int) string { return fmt.Sprintf("hop%d", n) }

func InetTarget(a netip.Addr) string {
	if a.Is4() || a.Is4In6() {
		return "inet:" + a.Unmap().String()
	}
	return "inet6:" + a.String()
}

func IsInetTarget(t string) bool  { return strings.HasPrefix(t, "inet:") }
func IsInet6Target(t string) bool { return strings.HasPrefix(t, "inet6:") }
```

`internal/record/names.go`:

```go
package record

// Collector names.
const (
	CPing     = "ping"
	CTrace    = "trace"
	CWifi     = "wifi"
	CSystem   = "system"
	CDNS      = "dns"
	CHTTP     = "http"
	CSTUN     = "stun"
	CSpeed    = "speed"
	CPublic   = "public"
	CMTU      = "mtu"
	CRouter   = "router"
	CUPnP     = "upnp"
	CFritz    = "fritz"
	CEventLog = "eventlog"
	CAdmin    = "admin"
	CApp      = "landlord"
)

// Record names.
const (
	NEcho         = "echo"
	NRoute        = "route"
	NRouteChange  = "route_change"
	NLink         = "link"
	NScan         = "scan"
	NConnect      = "connect"
	NDisconnect   = "disconnect"
	NRoam         = "roam"
	NIface        = "iface"
	NPower        = "power"
	NSleep        = "sleep"
	NAdapter      = "adapter"
	NLookup       = "lookup"
	NFetch        = "fetch"
	NBurst        = "burst"
	NNAT          = "nat"
	NTest         = "test"
	NSkipped      = "skipped"
	NPublicIP     = "public_ip"
	NPMTU         = "pmtu"
	NIdentity     = "identity"
	NWAN          = "wan"
	NWANReconnect = "wan_reconnect"
	NTunnel       = "tunnel"
	NDSL          = "dsl"
	NResync       = "resync"
	NDeviceLog    = "device_log"
	NWlanHistory  = "wlan_history"
	NWlanReport   = "wlanreport"
	NDriver       = "driver"
	NTCPRoute     = "tcp_route"
	NStart        = "start"
	NStop         = "stop"
	NDownsampled  = "downsampled"
)

// Fixed target labels.
const (
	TGateway  = "gateway"
	TGateway6 = "gateway6"
	TSystem   = "system"
)
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/record/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/record
git commit -m "record: record type, sinks and naming conventions" -m "Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 3: Session store

**Files:**
- Create: `internal/store/session.go`, `internal/store/writer.go`, `internal/store/reader.go`, `internal/store/store_test.go`

**Interfaces:**
- Consumes: `record.Record`, `record.Event`, `record.CApp`, `record.CPing`, `record.NEcho`, `record.NDownsampled`
- Produces:
  - `type Mark struct { Time time.Time; Tag string }`
  - `type Meta struct { ID, Version string; Start time.Time; Duration time.Duration; TZOffsetSec int; Finished bool; FinishedAt time.Time; HelperAsked bool; ContractMbps float64; ReportPath string; Marks []Mark }` with `func (m Meta) End() time.Time` and `func (m Meta) Location() *time.Location`
  - `var ErrNotFound`
  - `func Create(root string, now time.Time, d time.Duration, version string) (*Session, error)`
  - `func Open(dir string) (*Session, error)`
  - `func Latest(root string) (*Session, error)`
  - `func OpenOrResume(root string, now time.Time, d time.Duration, version string) (s *Session, resumed bool, err error)`
  - `func (s *Session) Dir() string`, `Meta() Meta`, `Update(func(*Meta)) error`, `AddMark(Mark) error`
  - `func (s *Session) NewWriter(prefix string, maxBytes int64) *Writer`
  - `func (w *Writer) Emit(record.Record)`, `Flush() error`, `Run(ctx context.Context, every time.Duration)`, `Close() error`
  - `func (s *Session) ReadRecords(fn func(record.Record) error) error`

- [ ] **Step 1: Write the failing tests**

`internal/store/store_test.go`:

```go
package store

import (
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/record"
)

var t0 = time.Date(2026, 9, 14, 18, 30, 0, 0, time.UTC)

func readAll(t *testing.T, s *Session) []record.Record {
	t.Helper()
	var out []record.Record
	if err := s.ReadRecords(func(r record.Record) error { out = append(out, r); return nil }); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWriteFlushRead(t *testing.T) {
	s, err := Create(t.TempDir(), t0, 48*time.Hour, "test")
	if err != nil {
		t.Fatal(err)
	}
	w := s.NewWriter("records", 0)
	for i := range 3 {
		w.Emit(record.Metric(record.CPing, record.NEcho, record.TGateway, t0.Add(time.Duration(i)*time.Second), map[string]float64{"rtt_ms": float64(i)}))
	}
	// Records crossing into the next hour go to a second file.
	w.Emit(record.Metric(record.CPing, record.NEcho, record.TGateway, t0.Add(40*time.Minute), map[string]float64{"rtt_ms": 9}))
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	// Read while the writer is still open: the current gzip member has no footer yet.
	if got := len(readAll(t, s)); got != 4 {
		t.Fatalf("read %d records before close, want 4", got)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := len(readAll(t, s)); got != 4 {
		t.Fatalf("read %d records after close, want 4", got)
	}
}

func TestResumeAndMarks(t *testing.T) {
	root := t.TempDir()
	s1, resumed, err := OpenOrResume(root, t0, 48*time.Hour, "test")
	if err != nil || resumed {
		t.Fatalf("first open: resumed=%v err=%v", resumed, err)
	}
	if err := s1.AddMark(Mark{Time: t0.Add(time.Hour), Tag: "call"}); err != nil {
		t.Fatal(err)
	}
	w := s1.NewWriter("records", 0)
	w.Emit(record.Event(record.CApp, record.NStart, t0, nil))
	_ = w.Flush() // simulate a crash: no Close

	s2, resumed, err := OpenOrResume(root, t0.Add(3*time.Hour), 48*time.Hour, "test")
	if err != nil || !resumed || s2.Dir() != s1.Dir() {
		t.Fatalf("second open: resumed=%v dir=%s err=%v", resumed, s2.Dir(), err)
	}
	if m := s2.Meta(); len(m.Marks) != 1 || m.Marks[0].Tag != "call" {
		t.Fatalf("marks not persisted: %+v", m.Marks)
	}
	w2 := s2.NewWriter("records", 0)
	w2.Emit(record.Event(record.CApp, record.NStart, t0.Add(3*time.Hour), nil))
	if err := w2.Close(); err != nil {
		t.Fatal(err)
	}
	if got := len(readAll(t, s2)); got != 2 {
		t.Fatalf("read %d records across runs, want 2", got)
	}

	// Past the duration, or finished, starts a new session.
	if err := s2.Update(func(m *Meta) { m.Finished = true; m.FinishedAt = t0.Add(4 * time.Hour) }); err != nil {
		t.Fatal(err)
	}
	s3, resumed, err := OpenOrResume(root, t0.Add(5*time.Hour), 48*time.Hour, "test")
	if err != nil || resumed || s3.Dir() == s1.Dir() {
		t.Fatalf("after finish: resumed=%v err=%v", resumed, err)
	}
}

func TestDownsampleWhenOverCap(t *testing.T) {
	s, err := Create(t.TempDir(), t0, time.Hour, "test")
	if err != nil {
		t.Fatal(err)
	}
	w := s.NewWriter("records", 1) // 1 byte cap: exceeded after the first flush
	w.Emit(record.Event(record.CApp, record.NStart, t0, nil))
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		w.Emit(record.Metric(record.CPing, record.NEcho, record.TGateway, t0.Add(time.Duration(i+1)*time.Second), map[string]float64{"rtt_ms": 1}))
	}
	w.Emit(record.Event(record.CWifi, record.NRoam, t0.Add(20*time.Second), nil))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var echo, downsampled, roam int
	for _, r := range readAll(t, s) {
		switch r.Name {
		case record.NEcho:
			echo++
		case record.NDownsampled:
			downsampled++
		case record.NRoam:
			roam++
		}
	}
	if echo != 2 || downsampled != 1 || roam != 1 {
		t.Fatalf("echo=%d downsampled=%d roam=%d, want 2/1/1", echo, downsampled, roam)
	}
}

func TestMetaEndAndLocation(t *testing.T) {
	m := Meta{Start: t0, Duration: 2 * time.Hour, TZOffsetSec: 7200}
	if !m.End().Equal(t0.Add(2 * time.Hour)) {
		t.Fatal("End without finish wrong")
	}
	m.Finished, m.FinishedAt = true, t0.Add(time.Hour)
	if !m.End().Equal(t0.Add(time.Hour)) {
		t.Fatal("End with finish wrong")
	}
	if h := t0.In(m.Location()).Hour(); h != 20 {
		t.Fatalf("local hour = %d, want 20", h)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/`
Expected: FAIL, `undefined: Create`.

- [ ] **Step 3: Implement**

`internal/store/session.go`:

```go
// Package store persists a recording session: metadata plus hourly gzip JSONL record files.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const metaFile = "meta.json"

var ErrNotFound = errors.New("store: no session found")

type Mark struct {
	Time time.Time `json:"t"`
	Tag  string    `json:"tag,omitempty"`
}

type Meta struct {
	ID           string        `json:"id"`
	Version      string        `json:"version"`
	Start        time.Time     `json:"start"`
	Duration     time.Duration `json:"duration"`
	TZOffsetSec  int           `json:"tz_offset_sec"`
	Finished     bool          `json:"finished"`
	FinishedAt   time.Time     `json:"finished_at,omitzero"`
	HelperAsked  bool          `json:"helper_asked"`
	ContractMbps float64       `json:"contract_mbps,omitempty"`
	ReportPath   string        `json:"report_path,omitempty"`
	Marks        []Mark        `json:"marks,omitempty"`
}

// End is when the session stops (or stopped) recording.
func (m Meta) End() time.Time {
	if m.Finished {
		return m.FinishedAt
	}
	return m.Start.Add(m.Duration)
}

// Location is the UTC offset the session was recorded in, for local-time display and hour-of-day rules.
func (m Meta) Location() *time.Location {
	return time.FixedZone("session", m.TZOffsetSec)
}

type Session struct {
	dir  string
	mu   sync.Mutex
	meta Meta
}

func Create(root string, now time.Time, d time.Duration, version string) (*Session, error) {
	_, off := now.Zone()
	m := Meta{
		ID:          now.UTC().Format("20060102-150405"),
		Version:     version,
		Start:       now.UTC(),
		Duration:    d,
		TZOffsetSec: off,
	}
	dir := filepath.Join(root, "sessions", m.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}
	s := &Session{dir: dir, meta: m}
	return s, s.save()
}

func Open(dir string) (*Session, error) {
	b, err := os.ReadFile(filepath.Join(dir, metaFile))
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}
	s := &Session{dir: dir}
	if err := json.Unmarshal(b, &s.meta); err != nil {
		return nil, fmt.Errorf("parse %s: %w", metaFile, err)
	}
	return s, nil
}

func Latest(root string) (*Session, error) {
	entries, err := os.ReadDir(filepath.Join(root, "sessions"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	if len(ids) == 0 {
		return nil, ErrNotFound
	}
	sort.Strings(ids)
	return Open(filepath.Join(root, "sessions", ids[len(ids)-1]))
}

// OpenOrResume continues the latest session if it is unfinished and still within its duration.
func OpenOrResume(root string, now time.Time, d time.Duration, version string) (*Session, bool, error) {
	s, err := Latest(root)
	switch {
	case err == nil:
		if m := s.Meta(); !m.Finished && now.Before(m.Start.Add(m.Duration)) {
			return s, true, nil
		}
	case !errors.Is(err, ErrNotFound):
		return nil, false, err
	}
	s, err = Create(root, now, d, version)
	return s, false, err
}

func (s *Session) Dir() string { return s.dir }

func (s *Session) Meta() Meta {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.meta
	m.Marks = append([]Mark(nil), s.meta.Marks...)
	return m
}

func (s *Session) Update(fn func(*Meta)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.meta)
	return s.save()
}

func (s *Session) AddMark(m Mark) error {
	return s.Update(func(meta *Meta) { meta.Marks = append(meta.Marks, m) })
}

// save writes meta.json atomically. Callers hold s.mu (or own s exclusively).
func (s *Session) save() error {
	b, err := json.MarshalIndent(s.meta, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, metaFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
```

`internal/store/writer.go`:

```go
package store

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/loehnertz/LANdlord/internal/record"
)

// Writer appends records to hourly gzip JSONL files. Each Writer instance uses its own
// files (the name includes its creation time), so a resumed run never appends to a file
// a crashed run left truncated.
type Writer struct {
	dir      string
	prefix   string
	runID    int64
	maxBytes int64

	mu         sync.Mutex
	hour       string
	f          *os.File
	gz         *gzip.Writer
	enc        *json.Encoder
	downsample bool
	echoSeen   uint64
	err        error
}

func (s *Session) NewWriter(prefix string, maxBytes int64) *Writer {
	return &Writer{dir: s.dir, prefix: prefix, runID: time.Now().UnixNano(), maxBytes: maxBytes}
}

func (w *Writer) Emit(r record.Record) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.downsample && r.Collector == record.CPing && r.Name == record.NEcho {
		w.echoSeen++
		if w.echoSeen%5 != 0 {
			return
		}
	}
	w.encode(r)
}

func (w *Writer) encode(r record.Record) {
	if err := w.rotate(r.Time); err != nil {
		w.err = errors.Join(w.err, err)
		return
	}
	if err := w.enc.Encode(r); err != nil {
		w.err = errors.Join(w.err, err)
	}
}

func (w *Writer) rotate(t time.Time) error {
	h := t.UTC().Format("2006010215")
	if h == w.hour && w.gz != nil {
		return nil
	}
	if err := w.closeFile(); err != nil {
		return err
	}
	name := fmt.Sprintf("%s-%s-%d.jsonl.gz", w.prefix, h, w.runID)
	f, err := os.OpenFile(filepath.Join(w.dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	w.f, w.gz, w.hour = f, gzip.NewWriter(f), h
	w.enc = json.NewEncoder(w.gz)
	return nil
}

// Flush makes everything emitted so far readable and enforces the size cap.
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.gz != nil {
		if err := w.gz.Flush(); err != nil {
			w.err = errors.Join(w.err, err)
		}
		if err := w.f.Sync(); err != nil {
			w.err = errors.Join(w.err, err)
		}
	}
	if w.maxBytes > 0 && !w.downsample {
		if size, err := dirSize(w.dir); err == nil && size > w.maxBytes {
			w.downsample = true
			w.encode(record.Event(record.CApp, record.NDownsampled, time.Now(), nil))
			if w.gz != nil {
				_ = w.gz.Flush()
			}
		}
	}
	err := w.err
	w.err = nil
	return err
}

// Run flushes every interval until ctx is done, then closes the writer.
func (w *Writer) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = w.Close()
			return
		case <-t.C:
			_ = w.Flush()
		}
	}
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return errors.Join(w.err, w.closeFile())
}

func (w *Writer) closeFile() error {
	if w.gz == nil {
		return nil
	}
	err := errors.Join(w.gz.Close(), w.f.Close())
	w.gz, w.f, w.enc, w.hour = nil, nil, nil, ""
	return err
}

func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}
```

`internal/store/reader.go`:

```go
package store

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/loehnertz/LANdlord/internal/record"
)

// ReadRecords calls fn for every readable record in the session. Records are grouped
// by file, not globally sorted. Truncated files (after a crash or while a writer is
// still open) yield everything up to the truncation.
func (s *Session) ReadRecords(fn func(record.Record) error) error {
	files, err := filepath.Glob(filepath.Join(s.dir, "*.jsonl.gz"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, p := range files {
		if err := readFile(p, fn); err != nil {
			return err
		}
	}
	return nil
}

func readFile(path string, fn func(record.Record) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil // empty or header-only file
		}
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	sc := bufio.NewScanner(gz)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var r record.Record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, gzip.ErrChecksum) {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/store/`
Expected: PASS. If `TestWriteFlushRead` reads 0 records before close, check that `Flush` calls `gz.Flush()` (a sync flush makes the data decodable).

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "store: sessions, hourly gzip writer, crash-tolerant reader" -m "Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 4: Configuration

**Files:**
- Create: `internal/config/config.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing
- Produces:
  - `type Duration struct{ time.Duration }` implementing `encoding.TextMarshaler`/`TextUnmarshaler`
  - `type Thresholds struct { LossPct, JitterMs, RTTP95Ms float64; MergeGap, MarkWindow Duration; GatewayFloorMs, GatewayFactor, RSSIWeakDBm, RxRatioWeak, RetryPct float64; OverlapAPs int; RouterUtil, LaptopShare, DNSSlowMs, RSSIStandingDBm float64 }`
  - `type Config struct` (fields below)
  - `func Default() Config`
  - `func Load(path string) (Config, error)`

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/pelletier/go-toml/v2@latest`

- [ ] **Step 2: Write the failing test**

`internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	c := Default()
	if c.Duration.Duration != 48*time.Hour {
		t.Fatalf("duration = %v", c.Duration)
	}
	th := c.Thresholds
	if th.LossPct != 1 || th.JitterMs != 30 || th.RTTP95Ms != 150 || th.MergeGap.Duration != 30*time.Second ||
		th.MarkWindow.Duration != 2*time.Minute || th.GatewayFloorMs != 30 || th.GatewayFactor != 5 ||
		th.RSSIWeakDBm != -70 || th.RxRatioWeak != 0.3 || th.RetryPct != 10 || th.OverlapAPs != 4 ||
		th.RouterUtil != 0.8 || th.LaptopShare != 0.2 || th.DNSSlowMs != 500 || th.RSSIStandingDBm != -67 {
		t.Fatalf("threshold defaults wrong: %+v", th)
	}
	if th.LikelyMinAwake.Duration != 30*time.Minute || th.LikelyMinBad.Duration != time.Minute || th.LikelyMinShare != 0.5 ||
		th.ConfidentMinAwake.Duration != 2*time.Hour || th.ConfidentMinBad.Duration != 3*time.Minute || th.ConfidentMinShare != 0.6 ||
		th.ConfidentMinIncidents != 2 || th.QuietMinAwake.Duration != 2*time.Hour || th.QuietMaxProblemPct != 0.5 {
		t.Fatalf("live verdict defaults wrong: %+v", th)
	}
	if len(c.Targets.IPv4) != 3 || len(c.Targets.IPv6) != 3 || len(c.STUN.Servers) != 2 || len(c.HTTP.Targets) != 6 {
		t.Fatalf("target defaults wrong: %+v", c)
	}
	if !c.Speedtest.Enabled || c.Speedtest.Interval.Duration != time.Hour || c.Speedtest.MaxDownloadMB != 25 || c.Speedtest.MaxUploadMB != 10 {
		t.Fatalf("speedtest defaults wrong: %+v", c.Speedtest)
	}
	if !c.Helper.Enabled || !c.UI.OpenBrowser || c.MaxDataMB != 200 {
		t.Fatalf("misc defaults wrong: %+v", c)
	}
}

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Duration.Duration != 48*time.Hour {
		t.Fatal("expected defaults")
	}
}

func TestLoadOverridesAndKeepsOtherDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "landlord.toml")
	content := `
duration = "24h"
[speedtest]
interval = "30m"
[thresholds]
loss_pct = 2.5
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Duration.Duration != 24*time.Hour || c.Speedtest.Interval.Duration != 30*time.Minute || c.Thresholds.LossPct != 2.5 {
		t.Fatalf("overrides not applied: %+v", c)
	}
	if c.Thresholds.JitterMs != 30 || c.Speedtest.MaxDownloadMB != 25 {
		t.Fatal("unrelated defaults lost")
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "landlord.toml")
	if err := os.WriteFile(p, []byte("durtion = \"1h\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "durtion") {
		t.Fatalf("expected unknown key error, got %v", err)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/config/`
Expected: FAIL, `undefined: Default`.

- [ ] **Step 4: Implement**

`internal/config/config.go`:

```go
// Package config holds LANdlord's settings: built-in defaults, optionally overridden by landlord.toml.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type Duration struct{ time.Duration }

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

type Thresholds struct {
	LossPct         float64  `toml:"loss_pct"`
	JitterMs        float64  `toml:"jitter_ms"`
	RTTP95Ms        float64  `toml:"rtt_p95_ms"`
	MergeGap        Duration `toml:"merge_gap"`
	MarkWindow      Duration `toml:"mark_window"`
	GatewayFloorMs  float64  `toml:"gateway_floor_ms"`
	GatewayFactor   float64  `toml:"gateway_factor"`
	RSSIWeakDBm     float64  `toml:"rssi_weak_dbm"`
	RxRatioWeak     float64  `toml:"rx_ratio_weak"`
	RetryPct        float64  `toml:"retry_pct"`
	OverlapAPs      int      `toml:"overlap_aps"`
	RouterUtil      float64  `toml:"router_util"`
	LaptopShare     float64  `toml:"laptop_share"`
	DNSSlowMs       float64  `toml:"dns_slow_ms"`
	RSSIStandingDBm float64  `toml:"rssi_standing_dbm"`

	// Live verdict readiness.
	LikelyMinAwake        Duration `toml:"likely_min_awake"`
	LikelyMinBad          Duration `toml:"likely_min_bad"`
	LikelyMinShare        float64  `toml:"likely_min_share"`
	ConfidentMinAwake     Duration `toml:"confident_min_awake"`
	ConfidentMinBad       Duration `toml:"confident_min_bad"`
	ConfidentMinShare     float64  `toml:"confident_min_share"`
	ConfidentMinIncidents int      `toml:"confident_min_incidents"`
	QuietMinAwake         Duration `toml:"quiet_min_awake"`
	QuietMaxProblemPct    float64  `toml:"quiet_max_problem_pct"`
}

type Config struct {
	Duration Duration `toml:"duration"`
	Targets  struct {
		IPv4 []string `toml:"ipv4"`
		IPv6 []string `toml:"ipv6"`
	} `toml:"targets"`
	STUN struct {
		Servers []string `toml:"servers"`
	} `toml:"stun"`
	HTTP struct {
		Targets []string `toml:"targets"`
	} `toml:"http"`
	DNS struct {
		Hosts []string `toml:"hosts"`
	} `toml:"dns"`
	Speedtest struct {
		Enabled       bool     `toml:"enabled"`
		Interval      Duration `toml:"interval"`
		MaxDownloadMB int      `toml:"max_download_mb"`
		MaxUploadMB   int      `toml:"max_upload_mb"`
	} `toml:"speedtest"`
	Thresholds Thresholds `toml:"thresholds"`
	Helper     struct {
		Enabled bool `toml:"enabled"`
	} `toml:"helper"`
	UI struct {
		OpenBrowser bool `toml:"open_browser"`
	} `toml:"ui"`
	DataDir   string `toml:"data_dir"`
	MaxDataMB int    `toml:"max_data_mb"`
}

func Default() Config {
	var c Config
	c.Duration = Duration{48 * time.Hour}
	c.Targets.IPv4 = []string{"1.1.1.1", "8.8.8.8", "9.9.9.9"}
	c.Targets.IPv6 = []string{"2606:4700:4700::1111", "2001:4860:4860::8888", "2620:fe::fe"}
	c.STUN.Servers = []string{"stun.l.google.com:19302", "stun.cloudflare.com:3478"}
	c.HTTP.Targets = []string{"zoom.us", "teams.microsoft.com", "meet.google.com", "www.youtube.com", "www.netflix.com", "www.google.com"}
	c.DNS.Hosts = []string{"www.google.com", "www.wikipedia.org", "www.microsoft.com", "www.netflix.com", "zoom.us"}
	c.Speedtest.Enabled = true
	c.Speedtest.Interval = Duration{time.Hour}
	c.Speedtest.MaxDownloadMB = 25
	c.Speedtest.MaxUploadMB = 10
	c.Thresholds = Thresholds{
		LossPct: 1, JitterMs: 30, RTTP95Ms: 150,
		MergeGap: Duration{30 * time.Second}, MarkWindow: Duration{2 * time.Minute},
		GatewayFloorMs: 30, GatewayFactor: 5,
		RSSIWeakDBm: -70, RxRatioWeak: 0.3, RetryPct: 10, OverlapAPs: 4,
		RouterUtil: 0.8, LaptopShare: 0.2, DNSSlowMs: 500, RSSIStandingDBm: -67,
		LikelyMinAwake: Duration{30 * time.Minute}, LikelyMinBad: Duration{time.Minute}, LikelyMinShare: 0.5,
		ConfidentMinAwake: Duration{2 * time.Hour}, ConfidentMinBad: Duration{3 * time.Minute}, ConfidentMinShare: 0.6,
		ConfidentMinIncidents: 2,
		QuietMinAwake: Duration{2 * time.Hour}, QuietMaxProblemPct: 0.5,
	}
	c.Helper.Enabled = true
	c.UI.OpenBrowser = true
	c.MaxDataMB = 200
	return c
}

// Load returns Default() overridden by the TOML file at path. A missing file is not an error.
func Load(path string) (Config, error) {
	c := Default()
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	defer f.Close()
	dec := toml.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			return c, fmt.Errorf("%s: %s", path, strict.String())
		}
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/config/`
Expected: PASS. `StrictMissingError.String()` includes the offending key name `durtion`.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/config
git commit -m "config: defaults and landlord.toml loading" -m "Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 5: Aggregation into buckets

**Files:**
- Create: `internal/aggregate/stats.go`, `internal/aggregate/aggregate.go`, `internal/aggregate/aggregate_test.go`

**Interfaces:**
- Consumes: `record.*` (Task 2), `store.Meta`, `store.Meta.End()` (Task 3)
- Produces:
  - `const DefaultWidth = 10 * time.Second`
  - `func Percentile(sorted []float64, p float64) float64` (nearest rank), `func Median(vals []float64) float64`, `func Jitter(seq []float64) float64`
  - `type PathStats struct { Sent, Recv int; LossPct, P50, P95, Jitter float64 }`
  - `type WifiStats struct { Present, Connected, ScanKnown bool; RSSIMin, RSSIMean, RSSIMax, QualityPct, RxMbps, TxMbps, RetryPct float64; SSID, BSSID, Band string; Channel, OverlapAPs int }`
  - `type ResolverStats struct { Count, Failed int; MeanMs float64 }`
  - `type HTTPStats struct { Count, Failed int; ConnectMs, TTFBMs float64 }`
  - `type RouterStats struct { Present bool; RxBps, TxBps, LinkDownBps, LinkUpBps float64 }`
  - `type DSLStats struct { Present, Resync bool; SNRDownDB, CRCDelta float64 }`
  - `type Event struct { Time time.Time; Collector, Name string; Values map[string]float64; Attrs map[string]string }`
  - `type Bucket struct { Start time.Time; Paths map[string]*PathStats; STUN PathStats; Wifi WifiStats; DNS map[string]*ResolverStats; HTTP map[string]*HTTPStats; Router RouterStats; DSL DSLStats; LaptopRxBps, LaptopTxBps float64; PowerKnown, OnBattery, Asleep bool; Events []Event }`
  - `type SpeedTest struct { Time time.Time; DownMbps, UpMbps, BloatMs float64; Grade, Trigger string }`
  - `type Session struct { Meta store.Meta; Width time.Duration; Buckets []Bucket; Events []Event; Infos, Unavailable, Scans, PMTU []record.Record; SpeedTests []SpeedTest; Skipped []Event }`
  - `type Builder struct` with `func NewBuilder(meta store.Meta, width time.Duration) *Builder`, `func (b *Builder) Add(r record.Record)` and `func (b *Builder) Snapshot(meta store.Meta) *Session`. A Builder is not safe for concurrent use; callers serialise `Add` and `Snapshot`. Snapshots are independent copies: later `Add` calls never change a snapshot already returned.
  - `func Build(meta store.Meta, width time.Duration, read func(func(record.Record) error) error) (*Session, error)` (a Builder fed by `read`, then `Snapshot`)
  - `func (s *Session) Index(t time.Time) int`, `func (s *Session) LatestInfo(collector, name string) (record.Record, bool)`

- [ ] **Step 1: Write the failing tests**

`internal/aggregate/aggregate_test.go`:

```go
package aggregate

import (
	"math"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/record"
	"github.com/loehnertz/LANdlord/internal/store"
)

var t0 = time.Date(2026, 9, 14, 18, 0, 0, 0, time.UTC)

func build(t *testing.T, meta store.Meta, recs []record.Record) *Session {
	t.Helper()
	s, err := Build(meta, DefaultWidth, func(fn func(record.Record) error) error {
		for _, r := range recs {
			if err := fn(r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func echo(target string, at time.Duration, rtt float64) record.Record {
	return record.Metric(record.CPing, record.NEcho, target, t0.Add(at), map[string]float64{"rtt_ms": rtt})
}

func lost(target string, at time.Duration) record.Record {
	return record.Metric(record.CPing, record.NEcho, target, t0.Add(at), map[string]float64{"lost": 1})
}

func TestStatsHelpers(t *testing.T) {
	sorted := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9}
	if Percentile(sorted, 0.5) != 5 || Percentile(sorted, 0.95) != 9 || Percentile(nil, 0.5) != 0 {
		t.Fatal("percentile wrong")
	}
	if Median([]float64{9, 1, 5}) != 5 {
		t.Fatal("median wrong")
	}
	if Jitter([]float64{10, 20, 10, 20}) != 10 || Jitter([]float64{5}) != 0 {
		t.Fatal("jitter wrong")
	}
}

func TestPathStats(t *testing.T) {
	var recs []record.Record
	for i := 1; i <= 9; i++ {
		recs = append(recs, echo(record.TGateway, time.Duration(i-1)*time.Second, float64(i)))
	}
	recs = append(recs, lost(record.TGateway, 9500*time.Millisecond))
	s := build(t, store.Meta{Start: t0, Duration: time.Hour, Finished: true, FinishedAt: t0.Add(10 * time.Second)}, recs)
	if len(s.Buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(s.Buckets))
	}
	p := s.Buckets[0].Paths[record.TGateway]
	if p.Sent != 10 || p.Recv != 9 || p.LossPct != 10 || p.P50 != 5 || p.P95 != 9 || p.Jitter != 1 {
		t.Fatalf("path stats wrong: %+v", *p)
	}
}

func TestBucketRangeUnfinishedTrimsToLastRecord(t *testing.T) {
	recs := []record.Record{echo(record.TGateway, 0, 1), echo(record.TGateway, 25*time.Second, 1)}
	s := build(t, store.Meta{Start: t0, Duration: time.Hour}, recs)
	if len(s.Buckets) != 3 {
		t.Fatalf("buckets = %d, want 3", len(s.Buckets))
	}
	if !s.Buckets[2].Start.Equal(t0.Add(20 * time.Second)) {
		t.Fatalf("bucket 2 start = %v", s.Buckets[2].Start)
	}
	if s.Index(t0.Add(-time.Second)) != -1 || s.Index(t0.Add(time.Hour)) != -1 {
		t.Fatal("Index out of range should be -1")
	}
}

func TestWifiRouterStunDNSHTTP(t *testing.T) {
	recs := []record.Record{
		{Time: t0, Collector: record.CWifi, Kind: record.KindMetric, Name: record.NLink,
			Values: map[string]float64{"connected": 1, "rssi_dbm": -60, "quality_pct": 80, "rx_mbps": 300, "tx_mbps": 200, "channel": 36, "retry_pct": 4},
			Attrs:  map[string]string{"ssid": "home", "bssid": "aa:bb", "band": "5"}},
		{Time: t0.Add(2 * time.Second), Collector: record.CWifi, Kind: record.KindMetric, Name: record.NLink,
			Values: map[string]float64{"connected": 1, "rssi_dbm": -70, "quality_pct": 60, "rx_mbps": 100, "tx_mbps": 100, "channel": 36, "retry_pct": 6},
			Attrs:  map[string]string{"ssid": "home", "bssid": "aa:bb", "band": "5"}},
		record.Metric(record.CSTUN, record.NBurst, "stun.example:3478", t0.Add(time.Second), map[string]float64{"sent": 500, "received": 490, "jitter_ms": 12, "rtt_p50_ms": 30}),
		record.Metric(record.CDNS, record.NLookup, "configured:192.168.178.1", t0, map[string]float64{"ms": 40}),
		record.Metric(record.CDNS, record.NLookup, "configured:192.168.178.1", t0.Add(time.Second), map[string]float64{"failed": 1}),
		record.Metric(record.CHTTP, record.NFetch, "zoom.us", t0, map[string]float64{"connect_ms": 20, "tls_ms": 30, "ttfb_ms": 90}),
		record.Metric(record.CUPnP, record.NWAN, "", t0, map[string]float64{"rx_bps": 40e6, "tx_bps": 1e6, "link_down_bps": 50e6, "link_up_bps": 10e6}),
		record.Metric(record.CSystem, record.NIface, "Wi-Fi", t0, map[string]float64{"rx_bps": 2e6, "tx_bps": 1e5}),
		record.Metric(record.CSystem, record.NPower, "", t0, map[string]float64{"on_ac": 0}),
	}
	s := build(t, store.Meta{Start: t0, Duration: time.Hour, Finished: true, FinishedAt: t0.Add(10 * time.Second)}, recs)
	b := s.Buckets[0]
	w := b.Wifi
	if !w.Present || !w.Connected || w.RSSIMin != -70 || w.RSSIMax != -60 || w.RSSIMean != -65 || w.RxMbps != 200 || w.RetryPct != 5 || w.Band != "5" || w.Channel != 36 {
		t.Fatalf("wifi stats wrong: %+v", w)
	}
	if b.STUN.Sent != 500 || b.STUN.Recv != 490 || b.STUN.LossPct != 2 || b.STUN.Jitter != 12 || b.STUN.P50 != 30 {
		t.Fatalf("stun stats wrong: %+v", b.STUN)
	}
	d := b.DNS["configured:192.168.178.1"]
	if d.Count != 2 || d.Failed != 1 || d.MeanMs != 40 {
		t.Fatalf("dns stats wrong: %+v", *d)
	}
	if h := b.HTTP["zoom.us"]; h.Count != 1 || h.ConnectMs != 20 || h.TTFBMs != 90 {
		t.Fatalf("http stats wrong: %+v", *h)
	}
	if !b.Router.Present || b.Router.RxBps != 40e6 || b.Router.LinkDownBps != 50e6 {
		t.Fatalf("router stats wrong: %+v", b.Router)
	}
	if b.LaptopRxBps != 2e6 || !b.PowerKnown || !b.OnBattery {
		t.Fatalf("laptop/power wrong: rx=%v known=%v battery=%v", b.LaptopRxBps, b.PowerKnown, b.OnBattery)
	}
}

func TestCarryForwardSleepEventsInfos(t *testing.T) {
	meta := store.Meta{Start: t0, Duration: time.Hour, Finished: true, FinishedAt: t0.Add(60 * time.Second)}
	recs := []record.Record{
		record.Metric(record.CWifi, record.NScan, "", t0, map[string]float64{"overlap_aps": 5, "total_aps": 12}),
		record.Metric(record.CSystem, record.NPower, "", t0, map[string]float64{"on_ac": 1}),
		{Time: t0.Add(50 * time.Second), Collector: record.CSystem, Kind: record.KindEvent, Name: record.NSleep,
			Values: map[string]float64{"seconds": 30},
			Attrs:  map[string]string{"from": t0.Add(20 * time.Second).Format(time.RFC3339), "to": t0.Add(50 * time.Second).Format(time.RFC3339)}},
		record.Event(record.CWifi, record.NRoam, t0.Add(31*time.Second), nil),
		record.Info(record.CPublic, record.NPublicIP, "", t0.Add(-time.Hour), map[string]string{"ip": "1.2.3.4", "loc": "DE"}),
		record.Info(record.CPublic, record.NPublicIP, "", t0.Add(time.Second), map[string]string{"ip": "5.6.7.8", "loc": "DE"}),
		record.Unavailable(record.CFritz, t0, "no password"),
		record.Unavailable(record.CFritz, t0.Add(time.Second), "no password"),
		echo(record.TGateway, -5*time.Second, 1), // before start: ignored
	}
	s := build(t, meta, recs)
	if len(s.Buckets) != 6 {
		t.Fatalf("buckets = %d, want 6", len(s.Buckets))
	}
	if s.Buckets[3].Wifi.OverlapAPs != 5 || !s.Buckets[3].Wifi.ScanKnown {
		t.Fatal("scan overlap not carried forward")
	}
	if !s.Buckets[4].PowerKnown || s.Buckets[4].OnBattery {
		t.Fatal("power not carried forward")
	}
	for i, want := range []bool{false, false, true, true, true, false} {
		if s.Buckets[i].Asleep != want {
			t.Fatalf("bucket %d asleep = %v, want %v", i, s.Buckets[i].Asleep, want)
		}
	}
	if len(s.Buckets[3].Events) != 1 || s.Buckets[3].Events[0].Name != record.NRoam {
		t.Fatalf("roam not attached to bucket 3: %+v", s.Buckets[3].Events)
	}
	if _, ok := s.Buckets[0].Paths[record.TGateway]; ok {
		t.Fatal("record before start was aggregated")
	}
	if ip, ok := s.LatestInfo(record.CPublic, record.NPublicIP); !ok || ip.Attrs["ip"] != "5.6.7.8" {
		t.Fatalf("LatestInfo = %+v", ip)
	}
	if len(s.Unavailable) != 1 {
		t.Fatalf("unavailable not deduplicated: %d", len(s.Unavailable))
	}
	if math.IsNaN(s.Buckets[0].STUN.Jitter) {
		t.Fatal("NaN in empty stats")
	}
}

func TestBuilderSnapshotsAreIndependent(t *testing.T) {
	meta := store.Meta{Start: t0, Duration: time.Hour}
	b := NewBuilder(meta, DefaultWidth)
	b.Add(echo(record.TGateway, 0, 10))
	b.Add(record.Event(record.CWifi, record.NRoam, t0.Add(time.Second), nil))
	first := b.Snapshot(meta)

	b.Add(echo(record.TGateway, 2*time.Second, 30))
	b.Add(record.Event(record.CWifi, record.NRoam, t0.Add(3*time.Second), nil))
	b.Add(echo(record.TGateway, 15*time.Second, 5))
	second := b.Snapshot(meta)

	if len(first.Buckets) != 1 || first.Buckets[0].Paths[record.TGateway].Sent != 1 || first.Buckets[0].Paths[record.TGateway].P95 != 10 {
		t.Fatalf("first snapshot changed: %+v", first.Buckets[0].Paths[record.TGateway])
	}
	if len(first.Buckets[0].Events) != 1 || len(first.Events) != 1 {
		t.Fatal("first snapshot events changed")
	}
	if len(second.Buckets) != 2 || second.Buckets[0].Paths[record.TGateway].Sent != 2 || second.Buckets[0].Paths[record.TGateway].P95 != 30 {
		t.Fatalf("second snapshot wrong: buckets=%d", len(second.Buckets))
	}
	if len(second.Events) != 2 {
		t.Fatalf("second snapshot events = %d, want 2", len(second.Events))
	}
	// Records beyond the configured duration are dropped.
	b.Add(echo(record.TGateway, 2*time.Hour, 1))
	if n := len(b.Snapshot(meta).Buckets); n != 2 {
		t.Fatalf("record after end grew buckets to %d", n)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/aggregate/`
Expected: FAIL, `undefined: Build`.

- [ ] **Step 3: Implement**

`internal/aggregate/stats.go`:

```go
package aggregate

import (
	"math"
	"slices"
)

// Percentile uses the nearest-rank method on an ascending slice.
func Percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[max(0, min(idx, len(sorted)-1))]
}

func Median(vals []float64) float64 {
	s := slices.Clone(vals)
	slices.Sort(s)
	return Percentile(s, 0.5)
}

// Jitter is the mean absolute difference between consecutive samples.
func Jitter(seq []float64) float64 {
	if len(seq) < 2 {
		return 0
	}
	var sum float64
	for i := 1; i < len(seq); i++ {
		sum += math.Abs(seq[i] - seq[i-1])
	}
	return sum / float64(len(seq)-1)
}
```

`internal/aggregate/aggregate.go`:

```go
// Package aggregate turns raw records into fixed-width time buckets.
package aggregate

import (
	"math"
	"slices"
	"time"

	"github.com/loehnertz/LANdlord/internal/record"
	"github.com/loehnertz/LANdlord/internal/store"
)

const DefaultWidth = 10 * time.Second

type PathStats struct {
	Sent, Recv int
	LossPct    float64
	P50, P95   float64
	Jitter     float64
	rtts       []float64
}

type WifiStats struct {
	Present, Connected, ScanKnown bool
	RSSIMin, RSSIMean, RSSIMax    float64
	QualityPct                    float64
	RxMbps, TxMbps                float64
	RetryPct                      float64
	SSID, BSSID, Band             string
	Channel, OverlapAPs           int
}

type ResolverStats struct {
	Count, Failed int
	MeanMs        float64
}

type HTTPStats struct {
	Count, Failed     int
	ConnectMs, TTFBMs float64
}

type RouterStats struct {
	Present                             bool
	RxBps, TxBps, LinkDownBps, LinkUpBps float64
}

type DSLStats struct {
	Present, Resync     bool
	SNRDownDB, CRCDelta float64
}

type Event struct {
	Time            time.Time
	Collector, Name string
	Values          map[string]float64
	Attrs           map[string]string
}

type Bucket struct {
	Start                        time.Time
	Paths                        map[string]*PathStats
	STUN                         PathStats
	Wifi                         WifiStats
	DNS                          map[string]*ResolverStats
	HTTP                         map[string]*HTTPStats
	Router                       RouterStats
	DSL                          DSLStats
	LaptopRxBps, LaptopTxBps     float64
	PowerKnown, OnBattery, Asleep bool
	Events                       []Event
}

type SpeedTest struct {
	Time                      time.Time
	DownMbps, UpMbps, BloatMs float64
	Grade, Trigger            string
}

type Session struct {
	Meta        store.Meta
	Width       time.Duration
	Buckets     []Bucket
	Events      []Event
	Infos       []record.Record
	Unavailable []record.Record
	Scans       []record.Record
	PMTU        []record.Record
	SpeedTests  []SpeedTest
	Skipped     []Event
}

// accum holds running sums for one bucket until finalize.
type accum struct {
	rssiN, rssiSum, qualSum, rxSum, txSum float64
	retryN, retrySum                      float64
	laptopN, laptopRx, laptopTx           float64
	routerN, routerRx, routerTx           float64
	stunN, stunJitter, stunP50            float64
	powerN, powerAC                       float64
	dnsSum                                map[string]float64
	httpConn, httpTTFB                    map[string]float64
}

// Builder accumulates records incrementally. The live verdict feeds it every record as it
// is written; the final report builds a fresh one from disk.
type Builder struct {
	width           time.Duration
	limit           int // bucket count covered by the configured duration
	s               Session
	acc             []accum
	last            time.Time
	seenUnavailable map[string]bool
}

func NewBuilder(meta store.Meta, width time.Duration) *Builder {
	if width <= 0 {
		width = DefaultWidth
	}
	return &Builder{
		width:           width,
		limit:           bucketCount(meta.Start, meta.Start.Add(meta.Duration), width),
		s:               Session{Meta: meta, Width: width},
		seenUnavailable: map[string]bool{},
	}
}

func Build(meta store.Meta, width time.Duration, read func(func(record.Record) error) error) (*Session, error) {
	b := NewBuilder(meta, width)
	if err := read(func(r record.Record) error { b.Add(r); return nil }); err != nil {
		return nil, err
	}
	return b.Snapshot(meta), nil
}

func (b *Builder) Add(r record.Record) {
	s := &b.s
	switch r.Kind {
	case record.KindInfo:
		s.Infos = append(s.Infos, r)
		return
	case record.KindUnavailable:
		key := r.Collector + "\x00" + r.Attrs["reason"]
		if !b.seenUnavailable[key] {
			b.seenUnavailable[key] = true
			s.Unavailable = append(s.Unavailable, r)
		}
		return
	}
	if r.Time.Before(s.Meta.Start) {
		return
	}
	i := int(r.Time.Sub(s.Meta.Start) / b.width)
	if i >= b.limit {
		return
	}
	b.grow(i + 1)
	if r.Time.After(b.last) {
		b.last = r.Time
	}
	s.add(&s.Buckets[i], &b.acc[i], r)
}

func (b *Builder) grow(n int) {
	for len(b.s.Buckets) < n {
		b.s.Buckets = append(b.s.Buckets, emptyBucket(b.s.Meta.Start.Add(time.Duration(len(b.s.Buckets))*b.width)))
		b.acc = append(b.acc, accum{})
	}
}

// Snapshot finalises a copy of the current state. For a finished session it covers the
// whole recording; otherwise it ends with the bucket of the latest record.
func (b *Builder) Snapshot(meta store.Meta) *Session {
	n := 0
	switch {
	case meta.Finished:
		n = min(b.limit, bucketCount(meta.Start, meta.FinishedAt, b.width))
	case !b.last.IsZero():
		n = int(b.last.Sub(meta.Start)/b.width) + 1
	}
	b.grow(n)
	src := &b.s
	out := &Session{
		Meta:        meta,
		Width:       b.width,
		Buckets:     make([]Bucket, n),
		Events:      slices.Clone(src.Events),
		Infos:       slices.Clone(src.Infos),
		Unavailable: slices.Clone(src.Unavailable),
		Scans:       slices.Clone(src.Scans),
		PMTU:        slices.Clone(src.PMTU),
		SpeedTests:  slices.Clone(src.SpeedTests),
		Skipped:     slices.Clone(src.Skipped),
	}
	for i := range n {
		out.Buckets[i] = cloneBucket(src.Buckets[i])
		a := b.acc[i]
		finalize(&out.Buckets[i], &a)
	}
	out.carryForward()
	out.applySleep()
	slices.SortStableFunc(out.Events, func(x, y Event) int { return x.Time.Compare(y.Time) })
	slices.SortStableFunc(out.Infos, func(x, y record.Record) int { return x.Time.Compare(y.Time) })
	slices.SortStableFunc(out.SpeedTests, func(x, y SpeedTest) int { return x.Time.Compare(y.Time) })
	return out
}

func bucketCount(start, end time.Time, width time.Duration) int {
	return max(0, int(math.Ceil(float64(end.Sub(start))/float64(width))))
}

func emptyBucket(start time.Time) Bucket {
	return Bucket{Start: start, Paths: map[string]*PathStats{}, DNS: map[string]*ResolverStats{}, HTTP: map[string]*HTTPStats{}}
}

// cloneBucket deep-copies everything finalize and carryForward write to.
func cloneBucket(src Bucket) Bucket {
	dst := src
	dst.Paths = make(map[string]*PathStats, len(src.Paths))
	for k, p := range src.Paths {
		cp := *p
		dst.Paths[k] = &cp
	}
	dst.DNS = make(map[string]*ResolverStats, len(src.DNS))
	for k, r := range src.DNS {
		cp := *r
		dst.DNS[k] = &cp
	}
	dst.HTTP = make(map[string]*HTTPStats, len(src.HTTP))
	for k, h := range src.HTTP {
		cp := *h
		dst.HTTP[k] = &cp
	}
	dst.Events = slices.Clone(src.Events)
	return dst
}

// Index returns the bucket index for t, or -1 when t is outside the session.
func (s *Session) Index(t time.Time) int {
	if t.Before(s.Meta.Start) {
		return -1
	}
	i := int(t.Sub(s.Meta.Start) / s.Width)
	if i >= len(s.Buckets) {
		return -1
	}
	return i
}

// LatestInfo returns the most recent info record with the given collector and name.
func (s *Session) LatestInfo(collector, name string) (record.Record, bool) {
	var out record.Record
	found := false
	for _, r := range s.Infos {
		if r.Collector == collector && r.Name == name && (!found || !r.Time.Before(out.Time)) {
			out, found = r, true
		}
	}
	return out, found
}

func (s *Session) add(b *Bucket, a *accum, r record.Record) {
	v := r.Values
	if r.Kind == record.KindEvent {
		ev := Event{Time: r.Time, Collector: r.Collector, Name: r.Name, Values: v, Attrs: r.Attrs}
		b.Events = append(b.Events, ev)
		s.Events = append(s.Events, ev)
		switch {
		case r.Collector == record.CSpeed && r.Name == record.NSkipped:
			s.Skipped = append(s.Skipped, ev)
		case r.Collector == record.CFritz && r.Name == record.NResync:
			b.DSL.Resync = true
		}
		return
	}
	switch r.Collector + "/" + r.Name {
	case record.CPing + "/" + record.NEcho:
		p := b.Paths[r.Target]
		if p == nil {
			p = &PathStats{}
			b.Paths[r.Target] = p
		}
		p.Sent++
		if rtt, ok := v["rtt_ms"]; ok && v["lost"] == 0 {
			p.Recv++
			p.rtts = append(p.rtts, rtt)
		}
	case record.CSTUN + "/" + record.NBurst:
		b.STUN.Sent += int(v["sent"])
		b.STUN.Recv += int(v["received"])
		a.stunN++
		a.stunJitter += v["jitter_ms"]
		a.stunP50 += v["rtt_p50_ms"]
	case record.CWifi + "/" + record.NLink:
		w := &b.Wifi
		w.Present = true
		if v["connected"] != 1 {
			return
		}
		rssi := v["rssi_dbm"]
		if !w.Connected || rssi < w.RSSIMin {
			w.RSSIMin = rssi
		}
		if !w.Connected || rssi > w.RSSIMax {
			w.RSSIMax = rssi
		}
		w.Connected = true
		a.rssiN++
		a.rssiSum += rssi
		a.qualSum += v["quality_pct"]
		a.rxSum += v["rx_mbps"]
		a.txSum += v["tx_mbps"]
		if rp, ok := v["retry_pct"]; ok {
			a.retryN++
			a.retrySum += rp
		}
		w.Channel = int(v["channel"])
		w.SSID, w.BSSID, w.Band = r.Attrs["ssid"], r.Attrs["bssid"], r.Attrs["band"]
	case record.CWifi + "/" + record.NScan:
		b.Wifi.OverlapAPs = int(v["overlap_aps"])
		b.Wifi.ScanKnown = true
		s.Scans = append(s.Scans, r)
	case record.CSystem + "/" + record.NIface:
		a.laptopN++
		a.laptopRx += v["rx_bps"]
		a.laptopTx += v["tx_bps"]
	case record.CSystem + "/" + record.NPower:
		a.powerN++
		a.powerAC += v["on_ac"]
	case record.CDNS + "/" + record.NLookup:
		rs := b.DNS[r.Target]
		if rs == nil {
			rs = &ResolverStats{}
			b.DNS[r.Target] = rs
		}
		rs.Count++
		if v["failed"] == 1 {
			rs.Failed++
		} else {
			if a.dnsSum == nil {
				a.dnsSum = map[string]float64{}
			}
			a.dnsSum[r.Target] += v["ms"]
		}
	case record.CHTTP + "/" + record.NFetch:
		hs := b.HTTP[r.Target]
		if hs == nil {
			hs = &HTTPStats{}
			b.HTTP[r.Target] = hs
		}
		hs.Count++
		if v["failed"] == 1 {
			hs.Failed++
		} else {
			if a.httpConn == nil {
				a.httpConn, a.httpTTFB = map[string]float64{}, map[string]float64{}
			}
			a.httpConn[r.Target] += v["connect_ms"]
			a.httpTTFB[r.Target] += v["ttfb_ms"]
		}
	case record.CUPnP + "/" + record.NWAN:
		b.Router.Present = true
		a.routerN++
		a.routerRx += v["rx_bps"]
		a.routerTx += v["tx_bps"]
		if d := v["link_down_bps"]; d > 0 {
			b.Router.LinkDownBps = d
		}
		if u := v["link_up_bps"]; u > 0 {
			b.Router.LinkUpBps = u
		}
	case record.CFritz + "/" + record.NDSL:
		b.DSL.Present = true
		b.DSL.SNRDownDB = v["snr_down_db"]
		b.DSL.CRCDelta += v["crc_delta"]
	case record.CSpeed + "/" + record.NTest:
		s.SpeedTests = append(s.SpeedTests, SpeedTest{
			Time: r.Time, DownMbps: v["down_mbps"], UpMbps: v["up_mbps"], BloatMs: v["bloat_ms"],
			Grade: r.Attrs["grade"], Trigger: r.Attrs["trigger"],
		})
	case record.CMTU + "/" + record.NPMTU:
		s.PMTU = append(s.PMTU, r)
	}
}

func finalize(b *Bucket, a *accum) {
	for _, p := range b.Paths {
		if p.Sent > 0 {
			p.LossPct = float64(p.Sent-p.Recv) / float64(p.Sent) * 100
		}
		p.Jitter = Jitter(p.rtts)
		sorted := slices.Clone(p.rtts)
		slices.Sort(sorted)
		p.P50, p.P95 = Percentile(sorted, 0.5), Percentile(sorted, 0.95)
		p.rtts = nil
	}
	if b.STUN.Sent > 0 {
		b.STUN.LossPct = float64(b.STUN.Sent-b.STUN.Recv) / float64(b.STUN.Sent) * 100
	}
	if a.stunN > 0 {
		b.STUN.Jitter, b.STUN.P50 = a.stunJitter/a.stunN, a.stunP50/a.stunN
	}
	if a.rssiN > 0 {
		b.Wifi.RSSIMean = a.rssiSum / a.rssiN
		b.Wifi.QualityPct = a.qualSum / a.rssiN
		b.Wifi.RxMbps, b.Wifi.TxMbps = a.rxSum/a.rssiN, a.txSum/a.rssiN
	}
	if a.retryN > 0 {
		b.Wifi.RetryPct = a.retrySum / a.retryN
	}
	for target, rs := range b.DNS {
		if ok := rs.Count - rs.Failed; ok > 0 {
			rs.MeanMs = a.dnsSum[target] / float64(ok)
		}
	}
	for target, hs := range b.HTTP {
		if ok := hs.Count - hs.Failed; ok > 0 {
			hs.ConnectMs, hs.TTFBMs = a.httpConn[target]/float64(ok), a.httpTTFB[target]/float64(ok)
		}
	}
	if a.routerN > 0 {
		b.Router.RxBps, b.Router.TxBps = a.routerRx/a.routerN, a.routerTx/a.routerN
	}
	if a.laptopN > 0 {
		b.LaptopRxBps, b.LaptopTxBps = a.laptopRx/a.laptopN, a.laptopTx/a.laptopN
	}
	if a.powerN > 0 {
		b.PowerKnown, b.OnBattery = true, a.powerAC/a.powerN < 0.5
	}
}

// carryForward fills slow-changing values (scan results, WAN link rates, power) into later buckets.
func (s *Session) carryForward() {
	var overlap int
	var scanKnown, powerKnown, onBattery bool
	var linkDown, linkUp float64
	for i := range s.Buckets {
		b := &s.Buckets[i]
		if b.Wifi.ScanKnown {
			overlap, scanKnown = b.Wifi.OverlapAPs, true
		} else if scanKnown {
			b.Wifi.OverlapAPs, b.Wifi.ScanKnown = overlap, true
		}
		if b.PowerKnown {
			powerKnown, onBattery = true, b.OnBattery
		} else if powerKnown {
			b.PowerKnown, b.OnBattery = true, onBattery
		}
		if b.Router.LinkDownBps > 0 {
			linkDown, linkUp = b.Router.LinkDownBps, b.Router.LinkUpBps
		} else if b.Router.Present {
			b.Router.LinkDownBps, b.Router.LinkUpBps = linkDown, linkUp
		}
	}
}

func (s *Session) applySleep() {
	for _, ev := range s.Events {
		if ev.Collector != record.CSystem || ev.Name != record.NSleep {
			continue
		}
		from, err1 := time.Parse(time.RFC3339, ev.Attrs["from"])
		to, err2 := time.Parse(time.RFC3339, ev.Attrs["to"])
		if err1 != nil || err2 != nil {
			continue
		}
		for i := range s.Buckets {
			st := s.Buckets[i].Start
			if !st.Before(from) && st.Before(to) {
				s.Buckets[i].Asleep = true
			}
		}
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/aggregate/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/aggregate
git commit -m "aggregate: 10 s buckets with path, wifi, dns, http, router and power stats" -m "Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 6: Scenario simulator

The simulator produces realistic record streams for end-to-end tests and for developing the report without a Windows laptop. Diagnosis unit tests (Tasks 7 to 9) build buckets directly; the simulator is for whole-pipeline checks.

**Files:**
- Create: `internal/sim/sim.go`, `internal/sim/scenarios.go`, `internal/sim/sim_test.go`

**Interfaces:**
- Consumes: `record.*` (Task 2), `aggregate.Build` (Task 5, test only), `store.Meta` (Task 3, test only)
- Produces:
  - `type Condition struct { GatewayLossPct, GatewayRTTms, HopLossPct, HopRTTms, Hop2ExtraLossPct, InetLossPct, InetRTTms, Inet6LossPct, JitterMs, RSSI, RxMbps, RetryPct, RouterRxBps, LaptopRxBps float64; ConfiguredDNSFail, Roam bool; FritzCRC float64 }`
  - `type Scenario struct { Name, Description string; Duration time.Duration; Cond func(t time.Time, loc *time.Location) Condition; Extra func(g *Gen) }`
  - `type Gen struct { Start, End time.Time; Loc *time.Location; R *rand.Rand; Sink record.Sink }`
  - `func Names() []string`, `func Get(name string) (Scenario, bool)`
  - `func Run(sc Scenario, start time.Time, loc *time.Location, seed int64, sink record.Sink)`
  - Scenario names: `healthy`, `weak_wifi`, `access_line`, `evening_congestion`, `hop_ratelimit`, `periodic_scan`, `dns_trouble`, `ipv4_tunnel`, `lan_saturation`

- [ ] **Step 1: Write the failing test**

`internal/sim/sim_test.go`:

```go
package sim

import (
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/record"
	"github.com/loehnertz/LANdlord/internal/store"
)

func TestAllScenariosRegistered(t *testing.T) {
	want := []string{"access_line", "dns_trouble", "evening_congestion", "healthy", "hop_ratelimit", "ipv4_tunnel", "lan_saturation", "periodic_scan", "weak_wifi"}
	got := Names()
	if len(got) != len(want) {
		t.Fatalf("Names() = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names() = %v, want %v", got, want)
		}
	}
}

func TestWeakWifiProducesDegradedGatewayBuckets(t *testing.T) {
	sc, _ := Get("weak_wifi")
	start := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	var buf record.Buffer
	Run(sc, start, time.UTC, 1, &buf)
	recs := buf.Records()
	if len(recs) == 0 {
		t.Fatal("no records")
	}
	meta := store.Meta{Start: start, Duration: sc.Duration, Finished: true, FinishedAt: start.Add(sc.Duration)}
	s, err := aggregate.Build(meta, aggregate.DefaultWidth, func(fn func(record.Record) error) error {
		for _, r := range recs {
			if err := fn(r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var weak, lossy int
	for _, b := range s.Buckets {
		if b.Wifi.RSSIMin <= -75 {
			weak++
		}
		if p := b.Paths[record.TGateway]; p != nil && p.LossPct > 1 {
			lossy++
		}
	}
	if weak == 0 || lossy == 0 {
		t.Fatalf("weak=%d lossy=%d, expected both > 0", weak, lossy)
	}
	if _, ok := s.LatestInfo(record.CRouter, record.NIdentity); !ok {
		t.Fatal("router identity info missing")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sim/`
Expected: FAIL, `undefined: Names`.

- [ ] **Step 3: Implement**

`internal/sim/sim.go`:

```go
// Package sim generates synthetic record streams for tests and report development.
package sim

import (
	"encoding/json"
	"math/rand"
	"net/netip"
	"sort"
	"time"

	"github.com/loehnertz/LANdlord/internal/record"
)

// Condition describes the network at one instant. Zero values mean "healthy default".
type Condition struct {
	GatewayLossPct, GatewayRTTms float64
	HopLossPct, HopRTTms         float64
	Hop2ExtraLossPct             float64
	InetLossPct, InetRTTms       float64
	Inet6LossPct                 float64
	JitterMs                     float64
	RSSI, RxMbps, RetryPct       float64
	RouterRxBps, LaptopRxBps     float64
	ConfiguredDNSFail, Roam      bool
	FritzCRC                     float64
}

type Scenario struct {
	Name, Description string
	Duration          time.Duration
	Cond              func(t time.Time, loc *time.Location) Condition
	Extra             func(g *Gen)
}

type Gen struct {
	Start, End time.Time
	Loc        *time.Location
	R          *rand.Rand
	Sink       record.Sink
}

func (g *Gen) every(step time.Duration, fn func(t time.Time)) {
	for t := g.Start; t.Before(g.End); t = t.Add(step) {
		fn(t)
	}
}

var registry = map[string]Scenario{}

func register(sc Scenario) { registry[sc.Name] = sc }

func Names() []string {
	var out []string
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func Get(name string) (Scenario, bool) {
	sc, ok := registry[name]
	return sc, ok
}

var (
	gateway  = netip.MustParseAddr("192.168.178.1")
	hops     = []netip.Addr{netip.MustParseAddr("62.155.244.1"), netip.MustParseAddr("217.0.118.5"), netip.MustParseAddr("80.156.160.1")}
	inet4    = []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("9.9.9.9")}
	inet6    = []netip.Addr{netip.MustParseAddr("2606:4700:4700::1111"), netip.MustParseAddr("2001:4860:4860::8888")}
	httpHost = []string{"zoom.us", "teams.microsoft.com", "meet.google.com", "www.youtube.com", "www.netflix.com", "www.google.com"}
)

const pingStep = 2 * time.Second

// Run emits the scenario's records from start for its duration.
func Run(sc Scenario, start time.Time, loc *time.Location, seed int64, sink record.Sink) {
	g := &Gen{Start: start, End: start.Add(sc.Duration), Loc: loc, R: rand.New(rand.NewSource(seed)), Sink: sink}
	g.infos()
	cond := func(t time.Time) Condition { return withDefaults(sc.Cond(t, loc)) }
	g.every(pingStep, func(t time.Time) {
		c := cond(t)
		g.echo(record.TGateway, gateway, t, c.GatewayRTTms, c.GatewayLossPct, c.JitterMs/4)
		for i, h := range hops {
			loss := c.HopLossPct
			if i == 1 {
				loss += c.Hop2ExtraLossPct
			}
			g.echo(record.HopTarget(i+1), h, t, c.HopRTTms+float64(i)*2, loss, c.JitterMs/2)
		}
		for _, a := range inet4 {
			g.echo(record.InetTarget(a), a, t, c.InetRTTms, c.InetLossPct, c.JitterMs)
		}
		for _, a := range inet6 {
			g.echo(record.InetTarget(a), a, t, c.InetRTTms, c.Inet6LossPct, c.JitterMs)
		}
		g.Sink.Emit(record.Record{Time: t, Collector: record.CWifi, Kind: record.KindMetric, Name: record.NLink,
			Values: map[string]float64{"connected": 1, "rssi_dbm": c.RSSI + g.noise(2), "quality_pct": 2 * (c.RSSI + 100),
				"rx_mbps": c.RxMbps, "tx_mbps": c.RxMbps * 0.8, "channel": 36, "freq_mhz": 5180, "retry_pct": c.RetryPct},
			Attrs: map[string]string{"ssid": "FRITZ!Box 7590 XY", "bssid": "3c:a6:2f:00:00:01", "band": "5", "phy": "ax"}})
		g.Sink.Emit(record.Metric(record.CSystem, record.NIface, "Wi-Fi", t, map[string]float64{"rx_bps": c.LaptopRxBps, "tx_bps": c.LaptopRxBps / 5}))
		if c.Roam && t.Sub(g.Start)%time.Minute == 0 {
			g.Sink.Emit(record.Record{Time: t, Collector: record.CWifi, Kind: record.KindEvent, Name: record.NRoam, Values: map[string]float64{"reason": 0}})
		}
	})
	g.every(30*time.Second, func(t time.Time) {
		c := cond(t)
		g.Sink.Emit(record.Metric(record.CSystem, record.NPower, "", t, map[string]float64{"on_ac": 1}))
		g.Sink.Emit(record.Metric(record.CUPnP, record.NWAN, "", t, map[string]float64{
			"rx_bps": c.RouterRxBps, "tx_bps": c.RouterRxBps / 8, "link_down_bps": 50e6, "link_up_bps": 10e6, "uptime_s": t.Sub(g.Start).Seconds() + 86400}))
		for _, target := range []string{record.TSystem, "configured:192.168.178.1", "1.1.1.1"} {
			if c.ConfiguredDNSFail && target != "1.1.1.1" {
				g.Sink.Emit(record.Record{Time: t, Collector: record.CDNS, Kind: record.KindMetric, Name: record.NLookup, Target: target,
					Values: map[string]float64{"failed": 1}, Attrs: map[string]string{"host": "www.google.com", "error": "i/o timeout"}})
				continue
			}
			g.Sink.Emit(record.Metric(record.CDNS, record.NLookup, target, t, map[string]float64{"ms": 20 + g.noise(5)}))
		}
	})
	g.every(time.Minute, func(t time.Time) {
		c := cond(t)
		sent := 500.0
		received := sent * (1 - c.InetLossPct/100)
		g.Sink.Emit(record.Metric(record.CSTUN, record.NBurst, "stun.cloudflare.com:3478", t, map[string]float64{
			"sent": sent, "received": received, "loss_pct": c.InetLossPct, "jitter_ms": c.JitterMs, "rtt_p50_ms": c.InetRTTms}))
		for _, h := range httpHost {
			g.Sink.Emit(record.Metric(record.CHTTP, record.NFetch, h, t, map[string]float64{"connect_ms": c.InetRTTms, "tls_ms": c.InetRTTms * 1.5, "ttfb_ms": c.InetRTTms * 3}))
		}
		g.Sink.Emit(record.Metric(record.CFritz, record.NDSL, "", t, map[string]float64{
			"snr_down_db": 9, "snr_up_db": 8, "sync_down_kbps": 100000, "sync_up_kbps": 40000, "crc_delta": c.FritzCRC}))
	})
	g.every(10*time.Minute, func(t time.Time) {
		nets, _ := json.Marshal([]map[string]any{{"ssid": "neighbour", "rssi": -72, "channel": 40}})
		g.Sink.Emit(record.Record{Time: t, Collector: record.CWifi, Kind: record.KindMetric, Name: record.NScan,
			Values: map[string]float64{"total_aps": 9, "overlap_aps": 2, "width_mhz": 80}, Attrs: map[string]string{"networks": string(nets)}})
	})
	g.every(time.Hour, func(t time.Time) {
		c := cond(t)
		down := 50 * (1 - c.InetLossPct/20)
		g.Sink.Emit(record.Record{Time: t.Add(5 * time.Minute), Collector: record.CSpeed, Kind: record.KindMetric, Name: record.NTest,
			Values: map[string]float64{"down_mbps": max(down, 1), "up_mbps": 9, "bloat_ms": 12}, Attrs: map[string]string{"grade": "B", "trigger": "interval"}})
	})
	if sc.Extra != nil {
		sc.Extra(g)
	}
}

func withDefaults(c Condition) Condition {
	if c.GatewayRTTms == 0 {
		c.GatewayRTTms = 3
	}
	if c.HopRTTms == 0 {
		c.HopRTTms = 12
	}
	if c.InetRTTms == 0 {
		c.InetRTTms = 22
	}
	if c.JitterMs == 0 {
		c.JitterMs = 2
	}
	if c.RSSI == 0 {
		c.RSSI = -55
	}
	if c.RxMbps == 0 {
		c.RxMbps = 400
	}
	if c.RetryPct == 0 {
		c.RetryPct = 2
	}
	if c.RouterRxBps == 0 {
		c.RouterRxBps = 3e6
	}
	if c.LaptopRxBps == 0 {
		c.LaptopRxBps = 1.5e6
	}
	return c
}

func (g *Gen) noise(amplitude float64) float64 { return (g.R.Float64()*2 - 1) * amplitude }

func (g *Gen) echo(target string, ip netip.Addr, t time.Time, rtt, lossPct, jitter float64) {
	attrs := map[string]string{"ip": ip.String()}
	if g.R.Float64()*100 < lossPct {
		g.Sink.Emit(record.Record{Time: t, Collector: record.CPing, Kind: record.KindMetric, Name: record.NEcho, Target: target,
			Values: map[string]float64{"lost": 1}, Attrs: attrs})
		return
	}
	g.Sink.Emit(record.Record{Time: t, Collector: record.CPing, Kind: record.KindMetric, Name: record.NEcho, Target: target,
		Values: map[string]float64{"rtt_ms": max(0.3, rtt+g.noise(jitter))}, Attrs: attrs})
}

func (g *Gen) infos() {
	t := g.Start
	g.Sink.Emit(record.Info(record.CRouter, record.NIdentity, gateway.String(), t, map[string]string{"vendor": "AVM", "model": "FRITZ!Box 7590", "method": "tr064", "mac": "3c:a6:2f:00:00:01"}))
	g.Sink.Emit(record.Info(record.CPublic, record.NPublicIP, "", t, map[string]string{"ip": "203.0.113.7", "loc": "DE"}))
	g.Sink.Emit(record.Info(record.CSystem, record.NAdapter, "", t, map[string]string{"name": "Wi-Fi", "description": "Intel(R) Wi-Fi 6E AX211 160MHz", "driver_version": "23.60.0.10", "wireless": "true"}))
	g.Sink.Emit(record.Info(record.CTrace, record.NRoute, "1.1.1.1", t, map[string]string{"path": "192.168.178.1,62.155.244.1,217.0.118.5,80.156.160.1,1.1.1.1"}))
	g.Sink.Emit(record.Unavailable(record.CEventLog, t, "simulated session"))
}
```

`internal/sim/scenarios.go`:

```go
package sim

import (
	"time"

	"github.com/loehnertz/LANdlord/internal/record"
)

// Episodes use wall-clock minutes (for example minutes 0-2 and 30-32 of every hour), so they
// line up with the local-time hour boundaries the diagnosis reports.
func init() {
	register(Scenario{
		Name: "healthy", Description: "Everything fine for 6 hours", Duration: 6 * time.Hour,
		Cond: func(time.Time, *time.Location) Condition { return Condition{} },
	})

	register(Scenario{
		Name: "weak_wifi", Description: "Every 30 minutes the signal drops to -79 dBm for 3 minutes", Duration: 6 * time.Hour,
		Cond: func(t time.Time, _ *time.Location) Condition {
			if t.Minute()%30 >= 3 {
				return Condition{}
			}
			return Condition{RSSI: -79, RxMbps: 20, RetryPct: 25, GatewayLossPct: 6, GatewayRTTms: 45, InetLossPct: 15, InetRTTms: 170, Inet6LossPct: 15, JitterMs: 40, Roam: true}
		},
	})

	register(Scenario{
		Name: "access_line", Description: "Every 30 minutes the line degrades for 3 minutes, with CRC errors", Duration: 6 * time.Hour,
		Cond: func(t time.Time, _ *time.Location) Condition {
			if t.Minute()%30 >= 3 {
				return Condition{}
			}
			return Condition{HopLossPct: 15, HopRTTms: 160, InetLossPct: 15, Inet6LossPct: 15, InetRTTms: 170, JitterMs: 35, FritzCRC: 150}
		},
	})

	register(Scenario{
		Name: "evening_congestion", Description: "Latency beyond the router rises every evening from 19:00 to 23:00", Duration: 48 * time.Hour,
		Cond: func(t time.Time, loc *time.Location) Condition {
			if h := t.In(loc).Hour(); h < 19 || h >= 23 {
				return Condition{}
			}
			return Condition{HopRTTms: 150, InetRTTms: 190, JitterMs: 45, InetLossPct: 2, Inet6LossPct: 2, HopLossPct: 2}
		},
	})

	register(Scenario{
		Name: "hop_ratelimit", Description: "The second provider hop drops 40 % of pings but traffic is fine", Duration: 6 * time.Hour,
		Cond: func(time.Time, *time.Location) Condition { return Condition{Hop2ExtraLossPct: 40} },
	})

	register(Scenario{
		Name: "periodic_scan", Description: "Latency spikes to the router every 60 s with a strong signal", Duration: 6 * time.Hour,
		Cond: func(t time.Time, _ *time.Location) Condition {
			if t.Second() >= 2 {
				return Condition{}
			}
			return Condition{GatewayRTTms: 180, InetRTTms: 220, JitterMs: 60}
		},
	})

	register(Scenario{
		Name: "dns_trouble", Description: "Every 30 minutes the router's DNS fails for 2 minutes", Duration: 6 * time.Hour,
		Cond: func(t time.Time, _ *time.Location) Condition {
			return Condition{ConfiguredDNSFail: t.Minute()%30 < 2}
		},
	})

	register(Scenario{
		Name: "ipv4_tunnel", Description: "DS-Lite line where only IPv4 degrades", Duration: 6 * time.Hour,
		Cond: func(t time.Time, _ *time.Location) Condition {
			if t.Minute()%30 >= 3 {
				return Condition{}
			}
			return Condition{InetLossPct: 25, InetRTTms: 170, JitterMs: 35}
		},
		Extra: func(g *Gen) {
			g.Sink.Emit(record.Info(record.CUPnP, record.NTunnel, "", g.Start, map[string]string{"kind": "dslite", "evidence": "router has no public IPv4 address"}))
		},
	})

	register(Scenario{
		Name: "lan_saturation", Description: "Another device fills the line every 30 minutes for 5 minutes", Duration: 6 * time.Hour,
		Cond: func(t time.Time, _ *time.Location) Condition {
			if t.Minute()%30 >= 5 {
				return Condition{}
			}
			return Condition{RouterRxBps: 48e6, LaptopRxBps: 0.5e6, GatewayRTTms: 6, InetRTTms: 240, JitterMs: 50, InetLossPct: 1.5, Inet6LossPct: 1.5}
		},
	})
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/sim/`
Expected: PASS within a few seconds.

- [ ] **Step 5: Commit**

```bash
git add internal/sim
git commit -m "sim: scenario generator for end-to-end tests and report development" -m "Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 7: Diagnosis core (symptoms, incidents, marks, summary)

**Files:**
- Create: `internal/diagnose/types.go`, `internal/diagnose/buckets.go`, `internal/diagnose/incidents.go`, `internal/diagnose/testutil_test.go`, `internal/diagnose/incidents_test.go`, `internal/diagnose/buckets_test.go`

**Interfaces:**
- Consumes: `aggregate.Session`, `aggregate.Bucket`, `aggregate.PathStats`, `aggregate.Median`, `aggregate.Percentile` (Task 5); `config.Thresholds` (Task 4); `store.Mark` (Task 3); `record` constants (Task 2)
- Produces:
  - `type Culprit string` with constants `WifiSignal`, `WifiInterference`, `ClientDevice`, `LANSaturation`, `AccessLine`, `ISPCongestion`, `IPv4Tunnel`, `DNS`, `RemoteService`, `Unknown` (string values are the spec IDs), `var AllCulprits []Culprit` in that order, `func (Culprit) Title() string` (spec titles)
  - `type Confidence string` (`High`, `Medium`, `Low`); `confidenceFor(strongSignals int)`: 3+ high, 2 medium, else low
  - `type Evidence struct { Signal, Text string }`; package var `weakSignals` = `no_hops`, `later_hop`, `client` (shown, but not counted for confidence)
  - `type BucketVerdict struct { Bad bool; Culprit Culprit; Symptoms, Evidence []Evidence }`
  - `type Incident struct { Start, End time.Time; First, Last int; Culprit Culprit; Confidence Confidence; Shares map[Culprit]float64; RunnersUp []Culprit; Symptoms, Evidence []Evidence }` with `Duration()`
  - `type MarkResult struct { Time time.Time; Tag string; Incident int }` (`-1` when no incident)
  - `type Finding struct { ID, Title, Detail string }`, `type Group string` (`Tenant`, `Landlord`), `type Recommendation struct { Group Group; For, Text string }`
  - `type Result struct { Verdicts []BucketVerdict; Incidents []Incident; AwakeBuckets, BadBuckets int; ProblemPct float64; Shares map[Culprit]float64; Main Culprit; MainConfidence Confidence; Marks []MarkResult; PeriodicSpikes bool; BloatGrade, Country string; Findings []Finding; Recommendations []Recommendation; Live LiveVerdict }` (`LiveVerdict` added in Task 10)
  - unexported: `sessionCtx` (`th`, `gwMedian`, `bestRx`, `hops []string`, `tunnel`, `hasIPv6`), `newCtx(s, th)`, `gatewayPath(b)`, `(c) degraded(p)`, `(c) gatewayDegraded(b)`, `(c) internet(b, family int) inetSummary`, `(c) symptoms(b) []Evidence`, `hasEvent(b, collector, name)`, `sortedKeys(map)`, `buildIncidents(s, verdicts, th)`, `pickMain(counts)`, `matchMarks(marks, incidents, window)`, `summarize(*Result)`

**Behaviour:**
- `newCtx`: `gwMedian` = median gateway P50 over awake buckets with replies (gateway, else gateway6); `bestRx` = 90th percentile of connected `RxMbps`; `hops` = `hop1..hop3` labels that answered at least once, in order; `tunnel` = any `upnp/tunnel` info; `hasIPv6` = any `inet6:` target answered.
- `degraded(p)`: sent > 0 and (loss > `LossPct` or P95 > `RTTP95Ms` or jitter > `JitterMs`).
- `gatewayDegraded(b)`: loss > `LossPct` or P95 > max(`GatewayFloorMs`, `GatewayFactor` x `gwMedian`).
- `internet(b, family)`: over `inet:` (4), `inet6:` (6) or both (0) targets with sent > 0: count, degraded count, median loss, median P95 (targets with replies only), median jitter.
- `symptoms(b)`, in this order, each at most once: `internet_loss` (median loss over all targets > threshold), `internet_latency` (median P95), `jitter` (max of median ICMP jitter and STUN jitter), `udp_loss` (STUN loss), `dns_failure` (any resolver with failures, keys sorted), `http_failure` (any host with failures, keys sorted), `wifi_disconnect` event, `wan_reconnect` event. A bucket is bad when it is awake and has at least one symptom.
- `buildIncidents`: bad buckets separated by at most `MergeGap / Width` non-bad buckets join one incident. Main culprit = most buckets, ties by `AllCulprits` order, `Unknown` only when nothing else. Shares over the incident's bad buckets; runners-up > 0.25. Evidence deduplicated by signal from buckets of the main culprit, symptoms from all bad buckets. Confidence from the number of distinct non-weak evidence signals. `End` = last bucket start + width.
- `matchMarks`: first incident overlapping [mark - window, mark + window].
- `summarize`: bad and awake counts, `ProblemPct`, shares over all bad buckets, `Main` via `pickMain` (empty when no bad buckets), `MainConfidence` = confidence with the most bucket time among incidents of the main culprit, ties towards higher, `Low` if none.

**Tests:**
- `testutil_test.go`: `healthyBucket(start)` (gateway P50 3/P95 4, hops 1 to 3 P95 12, three `inet:` and two `inet6:` targets P95 25, STUN 500/500, Wi-Fi -55 dBm 400 Mbit/s retry 2 % band 5 channel 36 overlap 1, DNS `system`/`configured:192.168.178.1`/`1.1.1.1` 20 ms, HTTP `zoom.us` and `www.google.com` fine, router 3 Mbit/s of 50/10, laptop 1 Mbit/s, power on AC) and `newSession(n int, mutate func(i int, b *aggregate.Bucket))` (start 2026-09-14 18:00 UTC, width 10 s, finished).
- Symptoms: healthy bucket has none; median loss 8 % on all internet targets gives `internet_loss`; a single lossy internet target gives none; STUN jitter 45 ms gives `jitter`; configured DNS failure gives `dns_failure`; a `wan_reconnect` event gives that symptom.
- `gatewayDegraded` uses the session median: gateway P95 25 ms is fine with median 3 ms (limit 30), degraded with median 1 ms is still limited by the 30 ms floor, degraded at 40 ms.
- Incidents from hand-built verdicts: bad at 0 and 1 with a 3-bucket gap to 5 merges; a 4-bucket gap splits. Shares, runners-up, main culprit tie-break and `Unknown` losing to anything else.
- Confidence: three strong signals give high; two strong plus `later_hop` gives medium.
- Marks: a mark 90 s after an incident end matches with the 2 min window; a mark 3 min away gets `-1`.
- `summarize`: problem percentage, main confidence weighting.

- [ ] **Step 1:** Write `testutil_test.go`, `buckets_test.go` and `incidents_test.go`; run `go test ./internal/diagnose/` and confirm compilation failures.
- [ ] **Step 2:** Implement `types.go`, `buckets.go`, `incidents.go`.
- [ ] **Step 3:** Run `go test ./internal/diagnose/`; expected PASS.
- [ ] **Step 4:** Commit `diagnose: symptoms, incidents, marks and summary`.

---

### Task 8: Culprit rules

**Files:**
- Create: `internal/diagnose/rules.go`, `internal/diagnose/rules_test.go`

**Interfaces:**
- Consumes: everything from Task 7
- Produces: `func (c *sessionCtx) classify(b *aggregate.Bucket) (Culprit, []Evidence)`, helpers `firstHop`, `lanSaturated`, `dnsProblem`, `failedService`

**Behaviour (checked in this order; first match wins):**
1. If the gateway is degraded or the bucket has a Wi-Fi `disconnect`/`roam` event:
   - add evidence `gateway` ("Router ping: X% loss, p95 Y ms") and/or `wifi_event`.
   - Wi-Fi connected and (min RSSI < `RSSIWeakDBm`, or receive rate < `RxRatioWeak` x `bestRx`, or a Wi-Fi event): `WifiSignal` with `rssi`/`rx_rate` evidence. A Wi-Fi event without a connected link is also `WifiSignal`.
   - Wi-Fi connected and any of retry > `RetryPct`, scan known with overlap >= `OverlapAPs`, band `2.4`: `WifiInterference` with `retries`/`crowded`/`band_24` evidence.
   - `lanSaturated`: `LANSaturation`.
   - Otherwise `ClientDevice` with weak evidence `client` ("Signal and channel look fine, so the delay comes from this computer or the router itself"). Wired or Wi-Fi-less sessions end here too.
2. `lanSaturated(b)`: router present with a known downstream link rate, utilisation = max(rx/down, tx/up) > `RouterUtil`, and laptop rx+tx < `LaptopShare` of router rx+tx. Evidence `router_util`, `laptop_share`.
3. Tunnel known and IPv6 seen: IPv4 targets degraded >= min(2, IPv4 count), IPv6 targets present and none degraded: `IPv4Tunnel` with `ipv4_only` and `tunnel` evidence.
4. Internet degraded on >= min(2, target count) targets: `AccessLine` with `internet` evidence plus exactly one of `hop1` (first responding hop degraded), `later_hop` (first hop fine) or `no_hops` (no hop answers); extra evidence `wan_reconnect` event, `crc` (DSL CRC delta > 0), `resync`, `snr` (downstream SNR margin between 0 and 6 dB).
5. `dnsProblem`: a local resolver (`system` or `configured:*`) failed or averaged > `DNSSlowMs`, unless public resolvers were seen and all of them were also bad. Evidence `dns_local`, and `dns_public_ok` when a public resolver answered normally.
6. `failedService`: exactly one HTTP host failed while at least one other succeeded: `RemoteService` with `service` evidence.
7. `wan_reconnect` event alone: `AccessLine`.
8. `Unknown`.

**Tests (table-driven over `healthyBucket` with mutations, asserting culprit and evidence signals):**
- weak signal: gateway loss 8 %, internet loss 8 %, RSSI min -78 gives `WifiSignal` with `gateway`, `rssi`
- roam event with healthy gateway gives `WifiSignal` with `wifi_event`
- interference: gateway loss 8 %, internet loss 8 %, retry 25 %, overlap 6 gives `WifiInterference` with `gateway`, `retries`, `crowded`
- client: gateway P95 200, internet P95 220, Wi-Fi fine gives `ClientDevice`
- wired: Wi-Fi not present, gateway P95 200 gives `ClientDevice`
- LAN: router rx 45 of 50 Mbit/s, laptop 1 Mbit/s, internet P95 250 gives `LANSaturation`; the same with laptop 20 Mbit/s does not
- access line: hops and internet loss 6 %, DSL CRC 100 gives `AccessLine` with `internet`, `hop1`, `crc`
- later hop: hop 1 fine, hops 2 to 3 and internet loss 6 % give `AccessLine` with `later_hop`
- no hops: hops absent from session context gives `no_hops`
- tunnel: context tunnel and IPv6, IPv4 loss 8 %, IPv6 fine gives `IPv4Tunnel`; without tunnel info gives `AccessLine`
- DNS: configured resolver failed 2 of 3, `1.1.1.1` fine gives `DNS`; all resolvers failing does not
- remote: `zoom.us` failed, `www.google.com` fine gives `RemoteService`
- unknown: STUN loss 5 % only gives `Unknown`
- hop rate limiting: hop 2 loss 40 % only is not a bad bucket (assert `symptoms` is empty)

- [ ] **Step 1:** Write `rules_test.go`; confirm it fails to compile.
- [ ] **Step 2:** Implement `rules.go`.
- [ ] **Step 3:** `go test ./internal/diagnose/`; expected PASS.
- [ ] **Step 4:** Commit `diagnose: culprit rules`.

---

### Task 9: Session passes, findings, recommendations, `Analyze`

**Files:**
- Create: `internal/diagnose/session.go`, `internal/diagnose/findings.go`, `internal/diagnose/recommend.go`, `internal/diagnose/analyze.go`, `internal/diagnose/analyze_test.go`, `internal/diagnose/scenarios_test.go`

**Interfaces:**
- Consumes: Tasks 5 to 8, `sim.Get`, `sim.Run` (test only)
- Produces: `func Analyze(s *aggregate.Session, th config.Thresholds) Result`, `func BloatGrade(ms float64) string`

**Behaviour:**
- `Analyze`: for each awake bucket compute symptoms, classify bad ones; run `applyPeriodic`, then `applyCongestion`; build incidents; summarize; match marks (`s.Meta.Marks`); country from the latest `public/public_ip` info `loc`; bloat grade from the median `BloatMs` of speed tests (empty without tests); findings; recommendations.
- `applyPeriodic`: spikes = awake buckets whose gateway P95 > max(20, 3 x `gwMedian`). With at least 10 spikes and at least 60 % of consecutive spike intervals between 50 and 70 s, set `PeriodicSpikes`; bad spike buckets classified `ClientDevice` or `Unknown` become `ClientDevice` with evidence `periodic` ("Router ping spikes repeat about every 60 seconds, which matches Windows background Wi-Fi scanning").
- `applyCongestion`: local time via `Meta.Location()`. With at least 6 `AccessLine` buckets, at least one hour of awake buckets between 02:00 and 06:00 with under 1 % bad, and at least 60 % of `AccessLine` buckets between 18:00 and 24:00, those evening buckets become `ISPCongestion` with `evening` evidence.
- `BloatGrade`: < 5 A, < 30 B, < 60 C, < 200 D, < 400 E, otherwise F.
- Findings (IDs): `weak_signal` (median connected RSSI mean < `RSSIStandingDBm`), `band_24` (> 20 % of connected buckets on 2.4 GHz), `crowded_channel` (latest scan overlap >= `OverlapAPs`), `slow_speed` (`Meta.ContractMbps` > 0 and median download < 50 % of it), `bufferbloat` (grade D or worse), `mtu` (smallest path MTU < 1500), `tunnel` (kind from info), `slow_dns` (median local resolver time > 3 x median public), `wlan_history` (`disconnects_7d` > 0), `periodic_spikes`, `battery` (gateway-degraded share on battery > 2 x on AC with at least 1 h on battery), `unavailable:<collector>` for each unavailable record with its reason.
- Recommendations: texts for the main culprit first, then other culprits with share > 0.1, then findings, deduplicated by text, each with `Group` and `For`. Use these texts:
  - `wifi_signal`: tenant "Sit closer to the router, or open the doors between you and the router." / "Use a network cable for important video calls."; landlord "Add a mesh access point or repeater near the room where the problems happen." / "Move the router to a more central spot in the apartment."
  - `wifi_interference`: tenant "If the Wi-Fi offers a separate 5 GHz network, connect to that one."; landlord "Change the router's Wi-Fi channel to a less crowded one (see the neighbouring networks in the inspection details)." / "Turn on 5 GHz or 6 GHz on the router if it supports it."
  - `client_device`: tenant "Keep the laptop plugged in during calls and set Windows' power mode to Best performance." / "Install the latest Wi-Fi driver from the laptop manufacturer's website."; landlord "Check the Wi-Fi adapter's roaming and power-saving settings in Device Manager."
  - `lan_saturation`: tenant "Ask others in the home to pause large downloads, uploads or backups during calls."; landlord "Turn on QoS or Smart Queue Management on the router, or move to a faster plan."
  - `access_line`: tenant "Restart the router once and check whether the problems continue."; landlord "Contact the provider with this report: the problems start beyond the router, on the line or in the provider's network." / "Have the in-house wiring and the wall socket checked, especially in an old building."
  - `isp_congestion`: tenant "Schedule large downloads outside 18:00 to 24:00."; landlord "Report the evening congestion to the provider and ask for a line check, or consider another provider."
  - `ipv4_tunnel`: landlord "Ask the provider for a public IPv4 address (dual stack) instead of DS-Lite or CGNAT."
  - `dns`: tenant "Set a public DNS server such as 1.1.1.1 in the Windows network settings."; landlord "Change the router's DNS server to a public resolver such as 1.1.1.1 or 9.9.9.9."
  - `remote_service`: tenant "Only one service was affected. Check its status page before changing anything at home."
  - `unknown`: tenant "Keep pressing the button when something goes wrong. More marks make the diagnosis more precise."
  - findings reuse the matching culprit texts (`weak_signal` as `wifi_signal`, `band_24`/`crowded_channel` as `wifi_interference`, `tunnel` as `ipv4_tunnel`, `slow_dns` as `dns`, `battery`/`periodic_spikes` as `client_device`, `bufferbloat` as `lan_saturation`); `slow_speed`: landlord "Tell the provider the contracted speed isn't delivered, using the speed test table in this report."; `mtu`: landlord "Check the router's MTU setting; the path MTU is below 1500."
  - Country `DE` and (main culprit `access_line`/`isp_congestion` or `slow_speed` finding): landlord "In Germany, measure with the Bundesnetzagentur's Breitbandmessung desktop app. If the provider keeps delivering less than contracted, section 57 of the Telecommunications Act (TKG) lets you reduce the price or cancel early."

**Tests:**
- `analyze_test.go`: periodic pass on 40 minutes of buckets with a gateway spike every 6th bucket; congestion pass on a 48 h session with evening access-line buckets and clean nights, and the negative case with a dirty night; findings for weak signal, bloat grade D, unavailable collector, tunnel; the DE hint appears only for DE; asleep buckets are neither awake nor bad.
- `scenarios_test.go` (end to end through `sim.Run` into a `record.Buffer`, `aggregate.Build`, `Analyze`): `weak_wifi` gives main `wifi_signal`; `access_line` gives `access_line`; `periodic_scan` gives `client_device` with `PeriodicSpikes`; `dns_trouble` gives `dns`; `ipv4_tunnel` gives `ipv4_tunnel`; `lan_saturation` gives `lan_saturation`; `healthy` and `hop_ratelimit` have no incidents; `evening_congestion` gives `isp_congestion` and is skipped under `-short`.

- [ ] **Step 1:** Write both test files; confirm failures.
- [ ] **Step 2:** Implement the four files.
- [ ] **Step 3:** `go test ./internal/diagnose/`; expected PASS. If a scenario's main culprit is off, fix the rule or scenario numbers based on the bucket evidence, not by loosening the assertion.
- [ ] **Step 4:** Commit `diagnose: session passes, findings, recommendations`.

---

### Task 10: Live verdict

**Files:**
- Create: `internal/diagnose/live.go`, `internal/diagnose/live_test.go`
- Modify: `internal/diagnose/analyze.go` (set `Result.Live`)

**Interfaces:**
- Produces:
  - `type LiveState string`: `Collecting` (`collecting`), `NoProblems` (`no_problems`), `Likely` (`likely`), `Confident` (`confident`)
  - `type LiveVerdict struct { State LiveState; Culprit Culprit; Share float64; AwakeFor, ProblemFor time.Duration; Incidents int; Headline string; Caveats []string }`
  - `func liveVerdict(s *aggregate.Session, r *Result, th config.Thresholds) LiveVerdict`

**Behaviour:** exactly the table in DESIGN.md "Live verdict". Awake time = awake buckets x width; problem time = bad buckets x width; main-culprit problem time and incident count only count the main culprit. Check order: confident, likely, no problems, collecting. Headlines:
- confident: "Most likely cause: {Title}. You can finish now, or keep recording to confirm."
- likely: "Likely cause so far: {Title} (based on {n} problems in {awake, rounded to hours or minutes}). This can still change."
- no problems: "No problems measured so far."
- collecting: "Still collecting data. Keep using the laptop normally and press the button when something goes wrong."

Caveats: "Evening congestion can only be detected after recording through an evening and a night." while the session has less than one hour between 18:00 and 24:00 or less than one hour between 02:00 and 06:00; "{n} of your marks happened while the connection measured fine." when marks lack incidents; "Not measured: {collector list}." from unavailable records.

**Tests:** each state from constructed sessions (25 minutes with problems gives collecting; 45 minutes with 2 minutes of weak-signal problems gives likely; 3 hours with two weak-signal incidents of 2 minutes each and medium confidence gives confident; 3 hours clean gives no problems); the evening caveat disappears on a 48 h session; mark and unavailable caveats text.

- [ ] **Step 1:** Write `live_test.go`; confirm failure.
- [ ] **Step 2:** Implement and wire into `Analyze`.
- [ ] **Step 3:** `go test ./internal/diagnose/`; PASS.
- [ ] **Step 4:** Commit `diagnose: live verdict`.

---

### Task 11: Report renderer

**Files:**
- Create: `internal/report/report.go`, `internal/report/data.go`, `internal/report/csv.go`, `internal/report/template.html`, `internal/report/assets/app.js`, `internal/report/assets/style.css`, `internal/report/assets/uPlot.iife.min.js`, `internal/report/assets/uPlot.min.css`, `internal/report/assets/UPLOT-LICENSE`, `internal/report/report_test.go`

**Interfaces:**
- Consumes: `aggregate.Session`, `diagnose.Result`, `version.String()`
- Produces:
  - `type Options struct { Redact bool; GeneratedAt time.Time; Partial bool }`
  - `func Render(w io.Writer, s *aggregate.Session, r diagnose.Result, opts Options) error`
  - `func WriteCSV(w io.Writer, s *aggregate.Session, r diagnose.Result) error`

**Behaviour:**
- Vendor uPlot: `curl -fsSL -o internal/report/assets/uPlot.iife.min.js https://cdn.jsdelivr.net/npm/uplot@1.6.31/dist/uPlot.iife.min.js`, the same for `dist/uPlot.min.css` and `LICENSE`. Commit them. Everything is embedded with `//go:embed`.
- Server-side HTML via `html/template` for every text section (verdict, marks, culprit bars as CSS widths, recommendations in two columns "The tenant can fix this" / "Call the landlord", incident table, inspection details, appendix), so the report is readable without JavaScript. Times shown in the session's local offset.
- `data.go` builds the chart payload: `{"v":1,"start":<unix ms>,"width":10000,"tz":<offset s>,"series":{"gw_p95","gw_loss","hop1_p95","inet_p95","inet_loss","stun_jitter","stun_loss","rssi","rx_mbps","router_util","dns_ms","culprit"},"events":[{"t","label","kind"}],"incidents":[{"s","e","culprit","row"}],"culprits":[...],"marks":[...]}`. Missing values are `null`. `inet_*` are medians over internet targets, `dns_ms` the system resolver, `culprit` the index into `culprits` or `null`. JSON is gzipped and base64-encoded into `<script type="application/octet-stream" id="landlord-data">`; `app.js` decodes it with `DecompressionStream("gzip")`.
- `app.js` draws synchronised uPlot charts (`cursor.sync.key = "landlord"`, shared x scale, drag to zoom, double-click to reset), shades incidents in the culprit colour, draws event and mark markers, renders the hour-by-day heatmap, zooms on incident row click, toggles redaction (adds `redacted` class to `<body>`; elements with class `pii` get blurred), and exports CSV via a `Blob` link from the embedded CSV text.
- CSS: `prefers-color-scheme` light/dark, print styles (charts at full width, no buttons), single column below 700 px.
- `Redact: true` replaces SSIDs, BSSIDs, public/external IPs and neighbour network names with `[redacted]` before rendering, and removes the embedded wlanreport.
- `Partial: true` (used by the status page's "report so far") adds a banner "Recording still in progress".
- The wlanreport (if `admin/wlanreport` info exists) is embedded via `<iframe sandbox srcdoc="...">` in the appendix.

**Tests:**
- Render a `sim` `weak_wifi` session: output parses with `golang.org/x/net/html`; contains "Weak Wi-Fi signal"; contains no `http://` or `https://` URLs except inside the uPlot license comment (assert with a regexp over `src=`, `href=` and `url(`); the data script decodes (base64, gunzip, JSON) and `series.gw_p95` length equals the bucket count.
- `Redact: true` output doesn't contain the simulated SSID or `203.0.113.7`.
- `WriteCSV` header and row count.

- [ ] **Step 1:** Vendor uPlot; write `report_test.go`; confirm failure.
- [ ] **Step 2:** Implement Go side, template, CSS, JS.
- [ ] **Step 3:** `go test ./internal/report/`; PASS. Open a rendered file in a browser and check charts, zoom, heatmap, dark mode and a 400 px wide window.
- [ ] **Step 4:** Commit `report: self-contained HTML report`.

---

### Task 12: `simulate` and `report` subcommands

**Files:**
- Modify: `cmd/landlord/main.go`
- Create: `internal/app/offline.go`, `internal/app/offline_test.go`

**Interfaces:**
- Produces: `func Simulate(scenario, outDir string, seed int64, now time.Time) (reportPath string, err error)`, `func RebuildReport(sessionDir, outPath string, redact bool, th config.Thresholds) error`

**Behaviour:**
- `landlord simulate -scenario NAME -out DIR [-seed N]`: creates a session under `DIR/data`, writes the scenario's records through a `store.Writer`, marks the session finished at start + duration, adds two marks during problem episodes (at 30 % and 60 % of the duration), analyzes and writes `DIR/report-NAME.html`. Prints the path. Unknown scenario lists `sim.Names()`.
- `landlord report [-redact] [-out FILE] SESSION_DIR`: reads the stored session and writes the report (default `SESSION_DIR/report.html`).
- `landlord version` unchanged. Subcommands use their own `flag.FlagSet`.

**Tests:** `Simulate("access_line", tmp, 1, t0)` writes a report containing "Access line"; `RebuildReport` on that session with `redact=true` succeeds.

- [ ] Steps: test, implement, `go test ./... && go run ./cmd/landlord simulate -scenario weak_wifi -out testdata/out`, open the report, commit `cmd: simulate and report subcommands`.

---

### Task 13: Platform interfaces, fake and Wi-Fi helpers

**Files:**
- Create: `internal/platform/platform.go`, `internal/platform/wifiutil.go`, `internal/platform/wifiutil_test.go`, `internal/platform/fake.go`, `internal/platform/current_windows.go`, `internal/platform/current_other.go`
- Create (stubs filled in by later tasks): `internal/platform/windows/platform.go` (package `winplat`, `//go:build windows`), `internal/platform/unix/platform.go` (package `unixplat`, `//go:build !windows`)

**Interfaces (exact):**

```go
var ErrUnsupported = errors.New("not supported on this platform")
var ErrLocationDenied = errors.New("location access for desktop apps is turned off")

type EchoStatus int // EchoOK, EchoTimeout, EchoTTLExpired, EchoUnreachable, EchoTooBig, EchoError
type EchoRequest struct { Dst netip.Addr; TTL, Size int; DontFragment bool; Timeout time.Duration }
type EchoReply struct { From netip.Addr; RTT time.Duration; Status EchoStatus }
type Pinger interface { Echo(ctx context.Context, req EchoRequest) (EchoReply, error); Close() error }

type Route struct { InterfaceName, Description string; InterfaceIndex int; InterfaceGUID string; MAC net.HardwareAddr; Gateway4, Gateway6, Local4 netip.Addr; DNSServers []netip.Addr; Wireless bool; LinkMbps float64 }
type Counters struct { RxBytes, TxBytes, InErrors, OutErrors, InDiscards, OutDiscards uint64; LinkBps uint64 }
type PowerStatus struct { Known, OnAC bool }
type AdapterInfo struct { DriverVersion, DriverDate string }

type WifiLink struct { Connected bool; SSID string; BSSID net.HardwareAddr; PHY string; QualityPct, RSSI int; RxKbps, TxKbps uint32; FreqMHz int }
type WifiCounters struct { TxFrames, RxFrames, Retries, Failures, FCSErrors uint64 }
type BSS struct { SSID string; BSSID net.HardwareAddr; RSSI, FreqMHz, WidthMHz int }
type WifiEvent struct { Time time.Time; Name string; Reason uint32 } // Name: record.NConnect/NDisconnect/NRoam
type Wifi interface {
	Link() (WifiLink, error)
	Counters() (WifiCounters, error)
	Scan() error
	BSSList() ([]BSS, error)
	Events(ctx context.Context) (<-chan WifiEvent, error)
	Close() error
}

type Platform interface {
	NewPinger(family int) (Pinger, error)
	DefaultRoute() (Route, error)
	NeighborMAC(ip netip.Addr) (net.HardwareAddr, error)
	InterfaceCounters(index int) (Counters, error)
	Adapter(route Route) (AdapterInfo, error)
	Power() (PowerStatus, error)
	KeepAwake(ctx context.Context) error // blocks until ctx is done
	OpenWifi() (Wifi, error)
	IsElevated() bool
	RelaunchElevated(args []string) error
	OpenURL(url string) error
	RevealFile(path string) error
	DocumentsDir() (string, error)
	DataDir() (string, error)
}

func Current() Platform // current_windows.go returns winplat.New(); current_other.go returns unixplat.New()
```

`wifiutil.go` (pure Go): `ChannelFromFreq(mhz int) int` (2412 to 2472 step 5 = channels 1 to 13, 2484 = 14, 5 GHz `(f-5000)/5`, 6 GHz `(f-5950)/5`), `BandFromFreq(mhz) string` (`2.4`, `5`, `6`), `Overlaps(a, b BSS) bool` (2.4 GHz: channel distance < 5; 5/6 GHz: frequency ranges `[f-w/2, f+w/2]` intersect, width defaulting to 20), `WidthFromIEs(ies []byte) int` (walk TLVs; HE Operation is extension element 255 with ext ID 36: 6 GHz operation info channel width; VHT Operation ID 192 byte 0: 1 means 80, 2 means 160, 3 means 80+80; HT Operation ID 61 byte 1 bit 2 (secondary channel offset non-zero) means 40; otherwise 20), `SSIDFromBytes`.

`fake.go`: `type Fake struct` with exported fields for each return value plus `EchoFunc func(EchoRequest) EchoReply`, `WifiFake` implementing `Wifi` with a settable `LinkValue`, `CountersValue`, `BSSValue`, and an `EventsCh`.

**Tests:** channel and band conversions for 2412, 2437, 2484, 5180, 5745, 5955; overlap on 2.4 GHz channels 1/4 (yes) and 1/6 (no), 5 GHz 80 MHz on 36 overlaps 20 MHz on 48; `WidthFromIEs` for crafted HT-40, VHT-80, VHT-160 and plain IEs.

- [ ] Steps: tests, implement, `go test ./internal/platform/ && GOOS=windows go build ./...`, commit `platform: interfaces, fake and Wi-Fi helpers`.

---

### Task 14: ICMP and route discovery; ping collector

**Files:**
- Create: `internal/platform/windows/icmp.go`, `internal/platform/windows/route.go`, `internal/platform/windows/icmp_api_test.go` (`//go:build windows && windowsapi`)
- Create: `internal/platform/unix/icmp.go`, `internal/platform/unix/route.go`, `internal/platform/unix/icmp_test.go`
- Create: `internal/collect/ping/ping.go`, `internal/collect/ping/ping_test.go`

**Windows details:**
- `iphlpapi.dll`: `IcmpCreateFile`, `Icmp6CreateFile`, `IcmpSendEcho2`, `Icmp6SendEcho2`, `IcmpCloseHandle`, `SendARP`. One ICMP handle per `Pinger`; each ping goroutine owns its own `Pinger`.
- `IP_OPTION_INFORMATION` on 64-bit: `{Ttl, Tos, Flags, OptionsSize uint8; _ [4]byte; OptionsData uintptr}`; `Flags = 2` (`IP_FLAG_DF`) for don't-fragment.
- `ICMP_ECHO_REPLY` on 64-bit: `{Address uint32; Status uint32; RoundTripTime uint32; DataSize uint16; Reserved uint16; Data uintptr; Options IP_OPTION_INFORMATION}`. Reply buffer size: `unsafe.Sizeof(reply) + size + 8`.
- The destination is an `IPAddr` (`uint32` from the 4 address bytes in little-endian memory order, i.e. `binary.LittleEndian.Uint32(a.As4()[:])`).
- `IcmpSendEcho2` returns 0 for timeouts and TTL expiry; still read `Status` and `Address` from the buffer when `Address != 0`. Status codes: `IP_SUCCESS 0`, `IP_DEST_HOST_UNREACHABLE 11003`, `IP_PACKET_TOO_BIG 11009`, `IP_REQ_TIMED_OUT 11010`, `IP_TTL_EXPIRED_TRANSIT 11013`.
- `RoundTripTime` has 1 ms resolution. Measure RTT with `time.Since` around the call instead and use the API value only as a floor check.
- `ICMPV6_ECHO_REPLY`: `IPV6_ADDRESS_EX` is packed (26 bytes: port uint16, flowinfo uint32, addr [8]uint16, scope uint32), so `Status` is at offset 28 and `RoundTripTime` at 32. Read these with `binary.LittleEndian` from the buffer rather than a Go struct. Verify with the `windowsapi` test pinging `::1`.
- `DefaultRoute`: `windows.GetAdaptersAddresses(AF_UNSPEC, GAA_FLAG_INCLUDE_GATEWAYS, ...)`; pick the adapter with `OperStatus == IfOperStatusUp`, a gateway, and the lowest `Ipv4Metric`. `IfType == 71` (`IF_TYPE_IEEE80211`) means wireless. DNS servers from `FirstDnsServerAddress`, GUID from `AdapterName`, link speed from `ReceiveLinkSpeed`.
- `NeighborMAC`: `SendARP(dst, 0, &mac[0], &len)` with a `[8]byte` buffer.

**Unix details:** `golang.org/x/net/icmp` with `icmp.ListenPacket("udp4", "0.0.0.0")` (unprivileged ICMP; works on macOS, and on Linux when `net.ipv4.ping_group_range` allows it; otherwise return `ErrUnsupported`); TTL via `ipv4.NewPacketConn(c).SetTTL`; don't-fragment is unsupported on macOS (return `ErrUnsupported`). Default route: macOS parses `route -n get default` (`gateway:`, `interface:`); Linux parses `/proc/net/route`. Neighbor MAC: `arp -n <ip>` on macOS, `/proc/net/arp` on Linux.

**Ping collector:**
- `type Config struct { Targets4, Targets6 []netip.Addr; Interval, Timeout time.Duration }`; `func New(p platform.Platform, cfg Config, hops *HopSet) *Collector` implementing `collect.Collector` (`Name() "ping"`).
- `type HopSet struct` (mutex-guarded `[]netip.Addr` for hop1..hop3) with `Set([]netip.Addr)` and `Get() []netip.Addr`; the traceroute collector (Task 16) updates it.
- Every `Interval`, per target (gateway from `DefaultRoute` refreshed every 30 s, hops from `HopSet`, internet targets), one goroutine sends one echo with `Timeout = 1 s` and emits `ping/echo` with `rtt_ms` or `lost`. Internet targets start staggered by `Interval / len(targets)`. When the gateway IP changes, emit `router/identity`-independent info is not needed here; just switch targets.

**Tests:** collector with `platform.Fake` whose `EchoFunc` returns timeouts for one target: emitted records per target, `lost` vs `rtt_ms`, target labels `gateway`, `hop1`, `inet:1.1.1.1`, `inet6:...`; stops on context cancel. Unix pinger test pings `127.0.0.1` and skips when `ListenPacket` fails. Windows API test pings `127.0.0.1`, `::1`, and `8.8.8.8` with TTL 1 (expects `EchoTTLExpired` or timeout).

- [ ] Steps: tests, implement, `go test ./... && GOOS=windows go vet ./...`, commit `ping: ICMP on Windows and unix, route discovery, ping collector`.

---

### Task 15: Collector supervisor

**Files:**
- Create: `internal/collect/supervisor.go`, `internal/collect/supervisor_test.go`

**Interfaces:**
- Produces:
  - `type Collector interface { Name() string; Run(ctx context.Context, sink record.Sink) error }`
  - `var ErrPermanent = errors.New("collector cannot run")`; collectors wrap it (`fmt.Errorf("%w: reason", collect.ErrPermanent)`) when retrying is pointless
  - `type State struct { Name, Status, LastError string; Restarts int }` (Status: `running`, `backoff`, `unavailable`, `stopped`)
  - `type Supervisor struct`; `func NewSupervisor(sink record.Sink, now func() time.Time) *Supervisor`; `Add(Collector)`; `Run(ctx)` (blocks until all collectors return after cancel); `States() []State`

**Behaviour:** each collector in its own goroutine; panics recovered and treated as errors; a nil return while ctx is live counts as an error; `ErrPermanent` emits one `record.Unavailable(name, now, reason)` and stops that collector; other errors restart with backoff 1 s, 2 s, 4 s ... capped at 5 min, reset after 10 min of healthy running.

**Tests:** permanent error emits exactly one unavailable record; a panicking collector restarts (use a short injectable base backoff); cancel stops everything; `States()` reflects statuses.

- [ ] Steps: tests, implement, commit `collect: supervisor with backoff and unavailable handling`.

---

### Task 16: Traceroute collector

**Files:**
- Create: `internal/collect/traceroute/traceroute.go`, `internal/collect/traceroute/traceroute_test.go`

**Interfaces:**
- Produces: `func New(p platform.Platform, dests []netip.Addr, hops *ping.HopSet, interval time.Duration) *Collector`; `func (c *Collector) Trigger()` (non-blocking, used by marks); `func Trace(ctx, pinger, dst, maxHops int) []netip.Addr` (invalid `netip.Addr{}` for no reply)

**Behaviour:** TTL 1..30, 3 probes per TTL until one replies, 1 s timeout, stop at `EchoOK`. Emits `trace/route` info (`path` comma-separated, `*` for no reply) per destination, and `trace/route_change` when the path differs from the previous trace for that destination (ignoring `*` differences). Updates `HopSet` from the first destination's trace: the first three responding hops after the gateway that are not private addresses (`netip.Addr.IsPrivate()`, and 100.64.0.0/10 counts as private). Runs every `interval` and on `Trigger()`.

**Tests:** fake platform answering TTL n with a scripted address: path, hop set skips gateway and private hops, route change detection ignores `*`.

- [ ] Steps: tests, implement, commit `traceroute: ICMP traceroute and hop discovery`.

---

### Task 17: Wi-Fi on Windows; Wi-Fi collector

**Files:**
- Create: `internal/platform/windows/wlan.go`, `internal/platform/windows/wlan_types.go`, `internal/platform/windows/wlan_api_test.go` (`windows && windowsapi`)
- Create: `internal/platform/unix/wifi.go` (returns `ErrUnsupported`)
- Create: `internal/collect/wifi/wifi.go`, `internal/collect/wifi/wifi_test.go`

**Windows details (`wlanapi.dll`):**
- `WlanOpenHandle(2, 0, &negotiated, &handle)`, `WlanEnumInterfaces`, `WlanQueryInterface`, `WlanScan`, `WlanGetNetworkBssList`, `WlanRegisterNotification`, `WlanFreeMemory`, `WlanCloseHandle`.
- Opcodes: `wlan_intf_opcode_current_connection = 7`, `wlan_intf_opcode_statistics = 0x10000101`, `wlan_intf_opcode_rssi = 0x10000102`.
- `WLAN_INTERFACE_INFO`: `{InterfaceGuid windows.GUID; Description [256]uint16; State uint32}`; list header `{NumberOfItems, Index uint32}` followed by entries.
- `WLAN_CONNECTION_ATTRIBUTES`: `{State, Mode uint32; ProfileName [256]uint16; Assoc WLAN_ASSOCIATION_ATTRIBUTES; Security WLAN_SECURITY_ATTRIBUTES}`; `WLAN_ASSOCIATION_ATTRIBUTES`: `{SSID DOT11_SSID{Length uint32; SSID [32]byte}; BssType uint32; BSSID [6]byte; PhyType uint32; PhyIndex uint32; SignalQuality uint32; RxRate uint32; TxRate uint32}` (rates in kbit/s; Go's alignment matches C).
- `WLAN_STATISTICS`: three `uint64`, two `WLAN_MAC_FRAME_STATISTICS` (12 x `uint64`), `NumberOfPhys uint32` (+4 padding), then `WLAN_PHY_FRAME_STATISTICS` entries (18 x `uint64`: TransmittedFrameCount, MulticastTransmittedFrameCount, FailedCount, RetryCount, MultipleRetryCount, MaxTXLifetimeExceededCount, TransmittedFragmentCount, RTSSuccessCount, RTSFailureCount, ACKFailureCount, ReceivedFrameCount, MulticastReceivedFrameCount, PromiscuousReceivedFrameCount, MaxRXLifetimeExceededCount, FrameDuplicateCount, ReceivedFragmentCount, PromiscuousReceivedFragmentCount, FCSErrorCount). Sum all PHYs.
- `WLAN_BSS_LIST`: `{TotalSize, NumberOfItems uint32}` then `WLAN_BSS_ENTRY` entries of 360 bytes: SSID (0..36), PhyId (36), BSSID (40..46), BssType (48), PhyType (52), Rssi int32 (56), LinkQuality (60), InRegDomain uint8 (64), BeaconPeriod uint16 (66), Timestamp uint64 (72), HostTimestamp uint64 (80), CapabilityInformation uint16 (88), ChCenterFrequency uint32 in kHz (92), WLAN_RATE_SET (96..352), IeOffset uint32 (352), IeSize uint32 (356). IEs start at entry offset + `IeOffset`. Add a compile-time size assertion (`var _ [360]byte = [unsafe.Sizeof(wlanBssEntry{})]byte{}`).
- Frequency of the current connection: find the connected BSSID in the BSS list (refreshed every 60 s), fallback 0.
- Notifications: `WlanRegisterNotification(h, WLAN_NOTIFICATION_SOURCE_ACM|WLAN_NOTIFICATION_SOURCE_MSM (0x08|0x10), 0, windows.NewCallback(cb), 0, 0, nil)`. `WLAN_NOTIFICATION_DATA`: `{Source, Code uint32; InterfaceGuid GUID; DataSize uint32; Data uintptr}`. ACM codes: 10 connection_complete (connect), 21 disconnected (disconnect; reason at offset 560 of `WLAN_CONNECTION_NOTIFICATION_DATA`). MSM codes: 5 roaming_start, 6 roaming_end (roam on 6). Send to a buffered channel, dropping when full. Unregister by calling it again with source 0 on close.
- Windows 11 24H2 and later: `WlanQueryInterface(current_connection)` and `WlanGetNetworkBssList` return `ERROR_ACCESS_DENIED` (5) when location access for desktop apps is off. Map that to `platform.ErrLocationDenied`.

**Wi-Fi collector:**
- `func New(p platform.Platform, linkEvery, scanEvery time.Duration, onLocationDenied func(bool)) *Collector`; `Trigger()` for an immediate snapshot and scan on marks.
- Every 2 s: `wifi/link` with `connected`, `rssi_dbm`, `quality_pct`, `rx_mbps`, `tx_mbps`, `channel`, `freq_mhz`, `retry_pct` (retry delta / transmitted delta x 100 since the previous sample, omitted on the first sample or when no frames were sent) and attrs `ssid`, `bssid`, `band`, `phy`.
- Every 10 min: `Scan()`, wait 4 s, `BSSList()`; emit `wifi/scan` with `total_aps`, `overlap_aps` (networks other than the connected BSSID overlapping it with RSSI > -80), `width_mhz` of the connected BSS and `networks` JSON (`ssid`, `bssid`, `rssi`, `channel`, `band`, `width`).
- Events from `Events(ctx)` become `wifi/connect|disconnect|roam` with `reason`.
- `ErrLocationDenied`: emit `record.Unavailable("wifi", ..., "location access for desktop apps is turned off")` once, call `onLocationDenied(true)`, keep polling every 30 s and resume (and call `onLocationDenied(false)`) when access is granted. `ErrUnsupported` or no wireless interface: `ErrPermanent`.

**Tests (fake Wi-Fi):** link records and retry percentage from two counter samples; scan overlap counting and JSON; location denied emits one unavailable record and recovers; event mapping.

- [ ] Steps: tests, implement, `GOOS=windows go vet ./...`, run the `windowsapi` test on a Windows machine with Wi-Fi when available (otherwise note it for Task 30), commit `wifi: wlanapi bindings and Wi-Fi collector`.

---

### Task 18: System collector and keep-awake

**Files:**
- Create: `internal/platform/windows/system.go`, `internal/platform/unix/system.go`, `internal/collect/system/system.go`, `internal/collect/system/system_test.go`

**Windows details:** interface counters via `windows.GetIfEntry2Ex(MibIfEntryNormal, &row)` with `row.InterfaceIndex` set (fields `InOctets`, `OutOctets`, `InErrors`, `OutErrors`, `InDiscards`, `OutDiscards`, `ReceiveLinkSpeed`). Power via `kernel32!GetSystemPowerStatus` (`{ACLineStatus, BatteryFlag, BatteryLifePercent, SystemStatusFlag uint8; BatteryLifeTime, BatteryFullLifeTime uint32}`; `ACLineStatus` 1 = AC, 255 = unknown). `KeepAwake`: start a goroutine, `runtime.LockOSThread()`, call `kernel32!SetThreadExecutionState(ES_CONTINUOUS|ES_SYSTEM_REQUIRED)` (`0x80000000|0x1`), block on ctx, then call it with `ES_CONTINUOUS` only; the thread lock matters because the state belongs to the calling OS thread. Adapter driver version and date: registry `HKLM\SYSTEM\CurrentControlSet\Control\Class\{4d36e972-e325-11ce-bfc1-08002be10318}\NNNN` where `NetCfgInstanceId` equals the adapter GUID; values `DriverVersion`, `DriverDate`.

**Unix details:** Linux counters from `/sys/class/net/<name>/statistics/*`; macOS returns `ErrUnsupported` for counters, power and keep-awake.

**Collector:** `func New(p platform.Platform, every time.Duration) *Collector` (`Name() "system"`). Every 2 s `system/iface` with rates from counter deltas and error/discard deltas; every 30 s `system/power`; once `system/adapter` info. Sleep detection: a 5 s ticker compares wall-clock time with the previous tick; a gap > 30 s emits `system/sleep` with `seconds`, `from`, `to`. Runs `KeepAwake` in the background and ignores `ErrUnsupported`. Exposes `LaptopMbps(window time.Duration) float64` (average of rx+tx over recent samples) for the speed test skip rule.

**Tests:** rates from fake counters; sleep gap detection with an injected clock; `LaptopMbps` averaging.

- [ ] Steps: tests, implement, commit `system: interface counters, power, sleep detection, keep-awake`.

---

### Task 19: DNS collector

**Files:** `internal/collect/dnscheck/dnscheck.go`, `internal/collect/dnscheck/dnscheck_test.go`

**Behaviour:** `go get github.com/miekg/dns`. `func New(p platform.Platform, hosts []string, every time.Duration) *Collector`. Every 30 s, pick the next host (round robin) and resolve it via: the Windows resolver (`net.DefaultResolver.LookupHost`, target `system`), each configured DNS server from `DefaultRoute().DNSServers` (target `configured:<ip>`), `1.1.1.1` and `8.8.8.8` (targets `1.1.1.1`, `8.8.8.8`), using `dns.Client{Timeout: 3 * time.Second}` with an `A` query and `RecursionDesired`. Emit `dns/lookup` with `ms`, or `failed` and `error` (timeout, SERVFAIL, NXDOMAIN for a host that should exist, network error).

**Tests:** a local `dns.Server` on `127.0.0.1:0` answering one name and returning SERVFAIL for another; the collector pointed at it via an injectable resolver list.

- [ ] Steps: tests, implement, commit `dnscheck: resolver timing and failures`.

---

### Task 20: HTTP collector

**Files:** `internal/collect/httpcheck/httpcheck.go`, `internal/collect/httpcheck/httpcheck_test.go`

**Behaviour:** `func New(hosts []string, every time.Duration) *Collector`. Every 60 s, for each host, `GET https://<host>/` with a fresh `http.Transport{DisableKeepAlives: true}`, 10 s timeout, `httptrace` for `ConnectStart/ConnectDone`, `TLSHandshakeStart/Done`, `GotFirstResponseByte`; read at most 64 KB of the body, close. Emit `http/fetch` with `connect_ms`, `tls_ms`, `ttfb_ms` or `failed` with `error`. Any HTTP status counts as success (the network worked). User agent `LANdlord/<version> (+https://github.com/loehnertz/LANdlord)`. The URL builder is injectable so tests use `httptest.NewTLSServer`.

**Tests:** success timings are positive; a closed port gives `failed`.

- [ ] Steps: tests, implement, commit `httpcheck: connect, TLS and first-byte timing`.

---

### Task 21: STUN collector

**Files:** `internal/collect/stuncheck/stuncheck.go`, `internal/collect/stuncheck/stuncheck_test.go`

**Behaviour:** `go get github.com/pion/stun/v3`. `func New(servers []string, every time.Duration, rate int, burst time.Duration) *Collector`. Every 60 s, alternate servers; resolve, open one UDP socket, send `rate` binding requests per second for `burst` (50 x 10 s), each with a new transaction ID; a reader goroutine matches responses by transaction ID and records RTT; wait 2 s after the last send. Emit `stun/burst` with `sent`, `received`, `loss_pct`, `jitter_ms` (`aggregate.Jitter` over RTTs in send order), `rtt_p50_ms`. Once at start: send one request to each of the first two servers from the same socket and compare `XOR-MAPPED-ADDRESS` ports; emit `stun/nat` info `endpoint-independent`, `endpoint-dependent` or `unknown`.

**Tests:** a local STUN responder (UDP server decoding with `stun.Message.Decode` and replying with `stun.XORMappedAddress`) dropping every 10th request: `loss_pct` about 10, jitter computed; NAT detection with two local responders reporting the same or different ports.

- [ ] Steps: tests, implement, commit `stuncheck: call-like UDP loss and jitter`.

---

### Task 22: Public IP and speed test with bufferbloat

**Files:** `internal/collect/publicip/publicip.go`, `internal/collect/publicip/publicip_test.go`, `internal/collect/speed/speed.go`, `internal/collect/speed/speed_test.go`

**Public IP:** `func New(url string, every time.Duration) *Collector` with default URL `https://www.cloudflare.com/cdn-cgi/trace`. Parse `key=value` lines; emit `public/public_ip` info with `ip` and `loc` every 30 min. Test with `httptest`.

**Speed test:**
- `type Config struct { Interval time.Duration; MaxDownloadBytes, MaxUploadBytes int64; DownURL, UpURL string; SkipAboveMbps float64 }` with defaults `https://speed.cloudflare.com/__down?bytes=%d`, `https://speed.cloudflare.com/__up`, skip above 1 Mbit/s.
- `func New(p platform.Platform, cfg Config, laptopMbps func(time.Duration) float64) *Collector`; `Trigger()` for marks (a smaller test: 10 MB down, 2 MB up).
- Before a test: if `laptopMbps(30*time.Second) > SkipAboveMbps`, emit `speed/skipped` with `laptop_mbps` and `reason` and try again in 5 minutes.
- Bufferbloat: ping the gateway and `1.1.1.1` every 200 ms for 5 s before the test (baseline) and during the download and upload; `bloat_ms` = median RTT under load minus median baseline RTT (max over the two targets, floored at 0); `grade` via `diagnose.BloatGrade`.
- Download: stream and count bytes, Mbit/s over the time after the first byte. Upload: POST a body of zero bytes of the configured size, Mbit/s over the request duration.
- Emit `speed/test` with `down_mbps`, `up_mbps`, `bloat_ms`, attrs `grade`, `trigger` (`interval` or `mark`).

**Tests:** `httptest` servers serving and accepting bytes; skip path; bloat computation with a fake pinger returning higher RTTs during transfer.

- [ ] Steps: tests, implement, commit `speed: capped speed test with bufferbloat grade; public IP`.

---

### Task 23: Path MTU collector

**Files:** `internal/collect/mtu/mtu.go`, `internal/collect/mtu/mtu_test.go`

**Behaviour:** `func New(p platform.Platform, dst netip.Addr, every time.Duration) *Collector`. Binary search on the ICMP payload size between 1172 and 1472 with `DontFragment` (3 attempts per size, success means any `EchoOK`; `EchoTooBig` or timeouts mean too big); MTU = payload + 28. Emit `mtu/pmtu` with `mtu`. `ErrUnsupported` from the pinger (macOS) becomes `ErrPermanent`.

**Tests:** fake pinger that accepts up to payload 1452 gives MTU 1480.

- [ ] Steps: tests, implement, commit `mtu: path MTU discovery`.

---

### Task 24: Router detection, UPnP IGD, tunnel detection

**Files:** `internal/router/detect.go`, `internal/router/detect_test.go`, `internal/router/upnp.go`, `internal/router/upnp_test.go`

**Detection:** `func Detect(ctx, client *http.Client, gateway netip.Addr, mac net.HardwareAddr) Identity` with `type Identity struct { Vendor, Model, Method string; FritzBox bool }`: GET `http://<gw>:49000/tr64desc.xml` (FRITZ!Box when `manufacturer` contains `AVM`; model from `modelName`/`friendlyName`, method `tr064`); otherwise the UPnP IGD root description from SSDP (manufacturer, modelName, method `upnp`); otherwise the `<title>` of `http://<gw>/` (method `http`); otherwise `unknown`. The collector wrapper emits `router/identity` info once and again when the gateway changes.

**UPnP collector:** `go get github.com/huin/goupnp`. Discover with `internetgateway2.NewWANCommonInterfaceConfig1ClientsCtx` plus `NewWANIPConnection1ClientsCtx`/`NewWANIPConnection2ClientsCtx`/`NewWANPPPConnection1ClientsCtx` (check the exact generated method names and return types with `go doc github.com/huin/goupnp/dcps/internetgateway2`). Every 30 s: `GetCommonLinkProperties` (link rates), `GetTotalBytesReceived`/`GetTotalBytesSent` (rates from deltas; counters may be 32-bit, so treat a decrease as a wrap at 2^32 when the previous value was below 2^32, otherwise as a reset), `GetStatusInfo` (uptime; an uptime lower than the previous one emits `upnp/wan_reconnect`), `GetExternalIPAddress`. Emit `upnp/wan`. No IGD found within 10 s: `ErrPermanent` with reason "router doesn't offer UPnP".

**Tunnel detection** (function `DetectTunnel(externalIP string, publicIP string, v6Seen bool) (kind, evidence string)`, called whenever either IP changes): external IP in `100.64.0.0/10` gives `cgnat`; external IP empty or private while IPv6 works gives `dslite`; external IP differs from the Cloudflare-reported public IP gives `cgnat`. Emit `upnp/tunnel` info once per kind.

**Tests:** `httptest` servers serving a FRITZ!Box `tr64desc.xml` fixture and an HTML title; counter wrap and reconnect logic on a fake IGD interface; tunnel detection table.

- [ ] Steps: tests, implement, commit `router: detection, UPnP WAN stats, tunnel detection`.

---

### Task 25: FRITZ!Box TR-064 plugin

**Files:** `internal/router/fritzbox/digest.go`, `internal/router/fritzbox/digest_test.go`, `internal/router/fritzbox/tr064.go`, `internal/router/fritzbox/tr064_test.go`, `internal/router/fritzbox/testdata/*.xml`

**Behaviour:**
- `digest.go`: HTTP digest auth (RFC 7616 with MD5 and `qop=auth`): on 401 parse `WWW-Authenticate`, compute `HA1 = MD5(user:realm:pass)`, `HA2 = MD5(POST:uri)`, response `MD5(HA1:nonce:nc:cnonce:auth:HA2)`, retry once.
- `tr064.go`: `type Client struct { Base string; User, Password string; HTTP *http.Client }`; `func (c *Client) Call(ctx, controlURL, serviceType, action string) (map[string]string, error)` posting a SOAP envelope with header `SOAPAction: "<serviceType>#<action>"` and parsing the response body's child elements into a map. Control URLs from `tr64desc.xml` by service type, with fallbacks `/upnp/control/wandslifconfig1` and `/upnp/control/deviceinfo`.
- Collector: `func New(gateway netip.Addr, creds func() (user, pass string, ok bool), every time.Duration) *Collector`. Without credentials emit `unavailable` "router password not entered" once and poll `creds` every 10 s. Every 60 s: `urn:dslforum-org:service:WANDSLInterfaceConfig:1#GetInfo` (`NewUpstreamNoiseMargin`/`NewDownstreamNoiseMargin` and attenuation in 0.1 dB units, `NewUpstreamCurrRate`/`NewDownstreamCurrRate` in kbit/s) and `#GetStatisticsTotal` (`NewCRCErrors`, `NewFECErrors`, `NewHECErrors`; deltas). Emit `fritz/dsl`. A downstream sync rate change of more than 1 % emits `fritz/resync`. Once per hour `urn:dslforum-org:service:DeviceInfo:1#GetDeviceLog` emits `fritz/device_log` info with the last 200 lines. 401 after digest emits `unavailable` "router password rejected" and waits for new credentials. A cable model without `WANDSLInterfaceConfig` emits `unavailable` "no DSL line statistics on this router".

**Tests:** digest response against a known vector; SOAP parsing against fixtures; `httptest` server requiring digest auth and returning fixtures; CRC delta and resync detection.

- [ ] Steps: tests, implement, commit `fritzbox: TR-064 DSL statistics and device log`.

---

### Task 26: Event log history, admin extras, elevated helper

**Files:** `internal/collect/eventlog/eventlog_windows.go`, `internal/collect/eventlog/eventlog_other.go`, `internal/collect/eventlog/parse.go`, `internal/collect/eventlog/parse_test.go`, `internal/collect/admin/admin_windows.go`, `internal/collect/admin/admin_other.go`, `internal/collect/admin/tcptrace_windows.go`, `internal/app/helper.go`, `internal/platform/windows/elevate.go`

**Event log:** run `wevtutil qe <log> /q:"*[System[TimeCreated[timediff(@SystemTime) <= 604800000]]]" /f:xml /c:2000` with `SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}` for `Microsoft-Windows-WLAN-AutoConfig/Operational` (IDs 8003 disconnect, 8002 connect failure), `Microsoft-Windows-NCSI/Operational` (4042 capability change). Output has no root element; wrap in `<Events>` before `encoding/xml` parsing (`Event/System/EventID`, `Event/System/TimeCreated/@SystemTime`). Emit `eventlog/wlan_history` with the 7-day counts and `daily` JSON. Access denied: emit unavailable "event log needs admin rights" so the helper retries it. Parsing is pure Go and tested with a fixture on every OS.

**Admin extras (helper only):** `netsh wlan show wlanreport` (hidden window), copy `%ProgramData%\Microsoft\Windows\WlanReport\wlan-report-latest.html` into the session directory as `wlanreport.html`, emit `admin/wlanreport` info with `file`. Driver properties: read all string and DWORD values of the adapter's class registry key (Task 18 lookup), emit `admin/driver` info. TCP traceroute (best effort): raw ICMP socket (`windows.Socket(AF_INET, SOCK_RAW, IPPROTO_ICMP)`, bound to the local IPv4 address, `SIO_RCVALL` not needed) listening for type 11 replies; for TTL 1..20 open a TCP socket with `net.Dialer.Control` setting `IP_TTL` and a unique local port, dial `1.1.1.1:443` with a 1 s timeout, match the quoted TCP source port in the ICMP payload (20-byte outer IP header, 8-byte ICMP header, quoted IP header of IHL x 4 bytes, then the source port). Emit `admin/tcp_route` every 15 minutes. If it gets no hop replies at all in three consecutive runs, emit unavailable "TCP traceroute not supported here" and stop.

**Helper process:**
- `internal/platform/windows/elevate.go`: `IsElevated` via `windows.GetCurrentProcessToken().IsElevated()`; `RelaunchElevated(args)` via `windows.ShellExecute(0, "runas", exe, args, "", SW_HIDE)`; `ERROR_CANCELLED` (1223) returns `ErrDeclined`.
- `landlord --helper --session DIR --parent PID`: opens the session, writes with `store.NewWriter("helper", cap)`, runs event log and admin collectors under a supervisor, and exits when the parent exits (`OpenProcess(SYNCHRONIZE)` + `WaitForSingleObject` in a goroutine).
- Non-Windows builds: `admin_other.go` and `eventlog_other.go` return `ErrPermanent` "Windows only".

**Tests:** event log XML parsing fixture; TCP traceroute quoted-port parsing on a crafted packet; helper argument parsing.

- [ ] Steps: tests, implement, `GOOS=windows go vet ./...`, commit `admin: event log history, wlanreport, driver properties, TCP traceroute, helper`.

---

### Task 27: Local UI server and status page

**Files:** `internal/ui/server.go`, `internal/ui/server_test.go`, `internal/ui/page.html`

**Interfaces:**
- `type Status struct { Recording bool; StartedAt, EndsAt time.Time; Remaining time.Duration; Marks int; Live diagnose.LiveVerdict; Collectors []collect.State; LocationDenied, HelperRunning bool; RouterNeedsPassword bool; ReportPath string }`
- `type Controller interface { Status() Status; Mark(tag string) error; Finish() (reportPath string, err error); SetRouterCredentials(user, pass string); SetContractMbps(mbps float64) error; ReportSoFar(w io.Writer) error; OpenLocationSettings() error }`
- `func New(c Controller) (*Server, error)` (random 32-byte hex token), `func (s *Server) Start(ctx) (url string, err error)` (listen `127.0.0.1:0`, returns `http://127.0.0.1:<port>/#<token>`), `func (s *Server) Token() string`

**Endpoints:** `GET /` (page, no token needed; the token is read from the URL fragment and kept in JS memory), `GET /api/status`, `POST /api/mark` (`{"tag":"call"}`), `POST /api/finish`, `POST /api/router` (`{"user","password"}`), `POST /api/contract` (`{"mbps"}`), `GET /api/report-so-far` (HTML), `POST /api/location-settings`, `GET /healthz`. Middleware: `Host` must be `127.0.0.1:<port>` or `localhost:<port>`; every `/api/` and `/healthz` request needs `X-Landlord-Token` (for `report-so-far`, a `?token=` query parameter is accepted so it can open in a new tab); state changes only via POST; responses set `Cache-Control: no-store` and `X-Frame-Options: DENY`.

**Page:** big red "It's bad right now!" button with optional tag chips (Call, Streaming, Browsing), a toast "Marked. Thanks!"; status line with remaining time; the live verdict card (headline, share bar, caveats) refreshed every 5 s; location-access banner with a button when `LocationDenied`; collapsible "Router password (FRITZ!Box only)" and "Contracted download speed" forms; "See report so far" link; "Finish now and create report" with a confirm dialog; after finishing, show where the report was saved. Plain HTML, CSS and JS, no external resources, readable at 400 px wide, light and dark.

**Tests:** wrong Host gives 403; missing or wrong token gives 401; GET on a POST endpoint gives 405; mark and finish call the controller; status JSON includes the live verdict.

- [ ] Steps: tests, implement, check the page in a browser against a fake controller (`go test -run TestManualPage -manual` helper that serves until interrupted, skipped by default), commit `ui: local status page with live verdict`.

---

### Task 28: App lifecycle, tray, single instance

**Files:** `internal/app/app.go`, `internal/app/app_test.go`, `internal/app/instance.go`, `internal/app/instance_test.go`, `internal/app/collectors.go`, `internal/ui/tray_windows.go`, `internal/ui/tray_other.go`, `internal/ui/icon.ico` (from Task 29's generator; commit a generated copy here), `cmd/landlord/main.go`

**Behaviour:**
- `app.Run(ctx, cfg config.Config, plat platform.Platform, opts RunOptions) error` where `RunOptions{ NoHelper, NoBrowser bool; DataDir string }`:
  1. Data dir from config or `plat.DataDir()`; `instance.go` reads `running.json` (`{"url","token","pid"}`); if `GET /healthz` with the token answers 200, open the URL and return.
  2. `store.OpenOrResume`; emit `landlord/start` (with `version`).
  3. Sink tee: every record goes to the `store.Writer` (flushed every 5 s) and, under a mutex, to an `aggregate.Builder`. On resume, feed the Builder from `ReadRecords` first.
  4. Every 30 s (and after each mark) snapshot the Builder and run `diagnose.Analyze`; keep the latest `Result` for `Status()`.
  5. Build collectors (`collectors.go`): ping, traceroute, Wi-Fi, system, DNS, HTTP, STUN, public IP, speed, MTU, router identity, UPnP, FRITZ!Box when `Identity.FritzBox`, event log (unelevated attempt). Run them under the supervisor.
  6. Start the UI server; write `running.json`; open the browser unless disabled.
  7. Helper: on Windows, if `cfg.Helper.Enabled`, not elevated, and `!Meta.HelperAsked`, set `HelperAsked` and call `RelaunchElevated(["--helper", "--session", dir, "--parent", pid])`; a declined prompt is fine.
  8. `Mark(tag)`: `store.AddMark`, trigger traceroute, Wi-Fi snapshot and speed test.
  9. Finish (UI button, tray, or duration reached): stop collectors, close the writer, set `Finished`/`FinishedAt`, build from disk, analyze, render to `Documents\LANdlord\<yyyy-mm-dd_hhmm>\report.html` (`plat.DocumentsDir()`), save `ReportPath`, reveal the file, remove `running.json`, keep the UI up for 10 minutes showing the report path, then exit. A session already past its duration at startup is finished immediately.
- Tray (`tray_windows.go`, `fyne.io/systray`): `systray.Run` on the main goroutine (`runtime.LockOSThread` in `init`); menu items "Open window", "Mark a problem", "Finish and create report", "Quit without report" (confirm via the status page). `tray_other.go`: `Run(ctx, onReady)` just blocks on ctx.
- `cmd/landlord/main.go`: no args or `run` starts `app.Run`; flags `-config` (default `landlord.toml` next to the executable), `-duration`, `-no-helper`, `-no-browser`, `-data-dir`; `--helper` dispatches to `app.RunHelper`; `simulate`, `report`, `version` as before. With `-H=windowsgui` there is no console, so errors at startup are shown with `MessageBoxW` on Windows (`internal/platform/windows/msgbox.go`) and printed to stderr elsewhere.

**Tests:** `instance_test.go` (stale `running.json` ignored when health check fails); `app_test.go` runs `app.Run` with `platform.Fake`, a 30 s duration, `NoBrowser`, fake collectors injected through an option, calls `Mark` and waits for finish: a report file exists, meta is finished, marks are stored, and the live verdict was computed at least once.

- [ ] Steps: tests, implement, `go test ./... && make windows`, commit `app: session lifecycle, live verdict, tray, single instance`.

---

### Task 29: Packaging, release and documentation

**Files:** `tools/genicon/main.go`, `winres/winres.json`, `.goreleaser.yaml`, `.github/workflows/release.yml`, `README.md`, `CONTRIBUTING.md`, `docs/USER-GUIDE.md`, `Makefile`

**Behaviour:**
- `tools/genicon`: draws a simple 256x256 house with a Wi-Fi arc using `image/draw`, encodes PNG, and writes an ICO file containing PNG entries at 16, 32, 48 and 256 px to `internal/ui/icon.ico` and `winres/icon.png`.
- `winres/winres.json` for `github.com/tc-hib/go-winres`: icon, version info (`ProductName LANdlord`, `FileDescription LANdlord connection inspector`), manifest with `asInvoker`, `dpiAwareness per monitor v2`, supported OS Windows 10/11. `make windows` runs `go run github.com/tc-hib/go-winres@latest make --in winres/winres.json --out cmd/landlord/rsrc` first (produces `rsrc_windows_amd64.syso`/`arm64`, git-ignored).
- `.goreleaser.yaml` (version 2): before hook runs go-winres; builds `landlord` for windows amd64/arm64 with `-H=windowsgui -s -w -X .../version.Version={{.Version}} -X .../version.Commit={{.ShortCommit}}`, and darwin/linux amd64/arm64 without `-H`; zip for Windows, tar.gz otherwise; `checksums.txt`; release notes include the SmartScreen "More info" then "Run anyway" instructions.
- `release.yml`: on tags `v*`, `goreleaser/goreleaser-action` with `GITHUB_TOKEN`.
- `README.md`: what it does, a screenshot of a simulated report (`docs/screenshot.png`), quick start for non-technical users (download, run, press the button, send the report), what it measures, privacy (no telemetry, full list of outgoing connections), admin and location access explained, redaction, building from source, simulate command, license.
- `CONTRIBUTING.md`: layout, tests, the record conventions contract, how to add a scenario, a router plugin, or a country hint.
- `docs/USER-GUIDE.md`: a one-page guide for the person running it, with the SmartScreen and location access steps.

- [ ] Steps: implement, `make windows`, `goreleaser release --snapshot --clean` locally (see https://goreleaser.com/install/), commit `release: icon, resources, GoReleaser, docs`.

---

### Task 30: Verification on real Windows

- [ ] Push to `origin master` and confirm CI is green on all three OSes, including the Windows cross-build.
- [ ] On a Windows 11 laptop on Wi-Fi: run `go test -tags windowsapi ./internal/platform/windows/...`; all API tests pass.
- [ ] Run `landlord.exe` unelevated, decline the admin prompt: status page opens, Wi-Fi data appears (or the location banner shows and clears after enabling location access), live verdict progresses from collecting.
- [ ] Run again with admin accepted: wlanreport and event log history appear in "report so far".
- [ ] Press the button three times, enter a wrong then a right router password (if a FRITZ!Box is available), close the browser tab, reopen via the tray.
- [ ] Put the laptop to sleep for 5 minutes, wake it, confirm the sleep gap is shaded.
- [ ] Record for 24 hours; check CPU below 1 % and memory below 100 MB in Task Manager; finish; the report opens from Explorer, renders offline, and looks right on a phone after sending it via a messenger.
- [ ] Fix anything found, with tests where possible, then tag `v0.1.0`.
