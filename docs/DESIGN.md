# LANdlord design

Status: approved, 2026-09-14. Implementation plan: [IMPLEMENTATION.md](IMPLEMENTATION.md)

LANdlord is a Windows program that records a home internet connection for a day or two and then writes a single HTML report naming the most likely cause of bad video calls and stuttering streams. It is meant for people who aren't technical (run it, press a button when things are bad, send the report to someone who is technical) and it is released as open source under the MIT license.

The typical case: video calls stutter and streams drop quality, on and off, with no obvious pattern. Nobody on site can tell whether the Wi-Fi, the router, the building's wiring, the provider or something further out is at fault, and the person who could help isn't there to troubleshoot.

## Goals

- Tell apart the layers that can cause the problem: Wi-Fi signal, Wi-Fi interference, the laptop itself, the router or shared usage, the access line and provider, provider congestion, IPv4 tunnelling (DS-Lite/CGNAT), DNS, and single remote services.
- Catch intermittent problems by recording for 48 hours by default while the laptop is used normally.
- Show a provisional verdict on the status page as soon as the data supports one, so the full 48 hours is optional. Recording continues and the verdict can change as more data arrives.
- Tie the measurements to what the user actually experienced, via an "It's bad right now!" button.
- Produce one self-contained `report.html` that works offline, on a phone, and can be sent via a messenger.
- Require nothing beyond double-clicking an `.exe`. Admin rights are optional.
- Work for anyone, anywhere. Router brands, countries and providers are detected and handled as optional extras, never assumed.

## Non-goals for v1

- Uploading results, remote live dashboards or any backend.
- Real Wi-Fi collectors on macOS and Linux. Those platforms build and run in a limited mode that is useful for development and for internet-side measurements.
- An Android app. A Wi-Fi walk test on Android is a candidate for later (see "Later").
- Changing any system setting. LANdlord only reads.
- Packet capture (Npcap or similar).

## Prior art

Existing tools cover parts of this, but none found combine the Wi-Fi layer, hop-by-hop path data and router statistics over days with an automatic verdict aimed at non-technical users, for free.

| Tool | Overlap | Gap |
|---|---|---|
| [PingPlotter](https://www.pingplotter.com/) | Hop-by-hop latency and loss over time, remote agents in PingPlotter Cloud | Commercial; the user interprets the graphs; no router line statistics or rule-based verdict |
| [ThousandEyes Endpoint Agent](https://docs.thousandeyes.com/product-documentation/global-vantage-points/endpoint-agents) | Wi-Fi metrics every 5 s, path from Wi-Fi to ISP to service | Enterprise product, not available to home users |
| [ICSI Netalyzr](http://netalyzr.icsi.berkeley.edu/) | Broad one-shot connectivity diagnosis | Shut down in March 2019; one-shot, so it can't catch intermittent problems |
| [ricklancee/netmonitor](https://github.com/ricklancee/netmonitor) | Long-running latency, jitter, loss, DNS, TTFB, speed and traceroute with dashboards | No Wi-Fi layer, no router data, no attribution |
| [edwinofsakh/network-quality-monitor](https://github.com/edwinofsakh/network-quality-monitor), [FutureSolutionDev/internet-monitor](https://github.com/FutureSolutionDev/internet-monitor), [jaclu/packet_loss-script](https://github.com/jaclu/packet_loss-script) | Long-running ping or loss monitoring | Single-layer, manual interpretation |
| MTR / WinMTR, `netsh wlan show wlanreport` | Individual building blocks | Manual, short-lived, need expertise |

## User flow

1. The user downloads `landlord.exe` from GitHub Releases and double-clicks it. SmartScreen warns about an unsigned binary; the README and release notes explain "More info" and "Run anyway".
2. The main process starts without elevation and asks once, through the Windows admin prompt, whether it may start the elevated helper for extra measurements. Declining is fine; the report lists what was skipped.
3. The default browser opens the local status page. It shows a large "It's bad right now!" button, the remaining time ("Recording, 47:58 left"), the number of marks so far, the live verdict (see "Live verdict"), and a "Finish now and create report" button. Collapsible sections offer an optional router password field and an optional "contracted speed" field.
4. A tray icon offers "Open window", "Mark a problem" and "Finish". Closing the browser tab does not stop recording.
5. On Windows 11 24H2 and later, reading Wi-Fi details requires location access for desktop apps. If Wi-Fi calls return access denied, the status page shows a short guide with a button that opens `ms-settings:privacy-location`.
6. When the duration ends or the user clicks "Finish", LANdlord writes `Documents\LANdlord\<yyyy-mm-dd_hhmm>\report.html` and opens Explorer with that file selected.
7. If the laptop restarts, starting `landlord.exe` again resumes the running session as long as it is younger than its configured duration. A second instance started while one is running only opens the existing status page.

Button presses store a timestamp and an optional tag (call, streaming, browsing, other). Each press also triggers an immediate traceroute, a Wi-Fi snapshot and a short speed test.

## Measurements

Intervals below are defaults and can be changed in the config. All Windows APIs listed work for a normal user unless marked "admin".

### Wi-Fi and the laptop

| Metric | Source | Interval |
|---|---|---|
| RSSI (dBm), signal quality (%) | `WlanQueryInterface` (`wlan_intf_opcode_rssi`, `wlan_intf_opcode_current_connection`) | 2 s |
| SSID, BSSID, PHY type, band, channel, receive and transmit rate | same | 2 s |
| Channel width | Information elements (HT/VHT/HE operation) from `WlanGetNetworkBssList` for the connected BSSID | 60 s |
| MAC-layer retry, failure and FCS error counters | `WlanQueryInterface` (`wlan_intf_opcode_statistics`) | 2 s |
| Connect, disconnect and roaming events with reason codes | `WlanRegisterNotification` | live |
| Neighbouring networks: count and signal per channel, overlap with the current channel | `WlanScan` followed by `WlanGetNetworkBssList` | 10 min; the scan itself is logged because it can cause a latency spike |
| Adapter byte counters, errors, discards, link speed | `GetIfEntry2` | 2 s |
| Adapter model, driver version and date | `GetAdaptersAddresses`, registry | once |
| AC or battery power | `GetSystemPowerStatus` | 30 s |
| Sleep and resume, VPN and other active adapters | power notifications, adapter list | live |
| Wi-Fi disconnect history before the recording | Event Log `Microsoft-Windows-WLAN-AutoConfig/Operational`; tried unelevated, retried by the helper | once |

Windows does not expose the noise floor, so there is no SNR on the Wi-Fi side. Retry counters and receive rate are the substitute for "the signal is there but the channel is bad".

### Home network and router

| Metric | Source | Interval |
|---|---|---|
| Ping to the default gateway (RTT, loss, jitter), IPv4 and IPv6 | `IcmpSendEcho2`, `Icmp6SendEcho2` | 1 s |
| Router identification | TR-064 description at `http://<gateway>:49000/tr64desc.xml` (FRITZ!Box), UPnP IGD device description (manufacturer, model), HTTP title of the gateway page, gateway MAC for display | once and on gateway change |
| Gateway MAC or IP changes, DHCP lease changes, configured DNS servers | `GetIpNetTable2`, `GetAdaptersAddresses` | 30 s |
| WAN uptime, external IP, WAN link rates, total bytes through the router | UPnP IGD (`WANCommonInterfaceConfig`, `WANIPConnection`) via SSDP; best effort | 30 s |
| FRITZ!Box: DSL noise margin, attenuation, CRC/FEC/HEC errors, sync rates, resyncs (sync rate changes); router event log | TR-064 on port 49000 with digest auth; needs the router password entered on the status page | 60 s |

The router plugin interface is written so other brands can be added later. v1 ships only the FRITZ!Box plugin.

### Path beyond the router

| Metric | Source | Interval |
|---|---|---|
| Traceroute to 1.1.1.1 and 8.8.8.8 (hops, responders, route changes) | `IcmpSendEcho2` with increasing TTL | 5 min and on mark |
| Ping to the first three responding hops after the gateway | `IcmpSendEcho2` | 1 s |
| Ping to internet targets (default 1.1.1.1, 8.8.8.8, 9.9.9.9, plus IPv6 equivalents) | `IcmpSendEcho2`, `Icmp6SendEcho2` | 1 s, staggered |
| CGNAT and DS-Lite detection | WAN IP in 100.64.0.0/10 (UPnP), no public IPv4 on the router, public IPv4 from Cloudflare's `/cdn-cgi/trace` differs from the router's WAN IP | once and on change |
| Public IP and country code | `https://www.cloudflare.com/cdn-cgi/trace` (`ip`, `loc`) | 30 min |
| UDP loss and jitter in a call-like pattern | STUN binding requests (`pion/stun`) at 50 packets/s for 10 s to `stun.l.google.com:19302` and `stun.cloudflare.com:3478` | every 60 s |
| NAT mapping behaviour | STUN | once |
| TCP connect, TLS handshake and time to first byte for zoom.us, teams.microsoft.com, meet.google.com, youtube.com, netflix.com, google.com | `net/http` with `httptrace` | 60 s |
| DNS resolve time and failures via the configured resolvers, 1.1.1.1, 8.8.8.8 and the Windows resolver | `miekg/dns`, `net.Resolver` | 30 s |
| Path MTU | ICMP echo with don't-fragment and a binary search on size | 60 min |
| Download and upload speed | Cloudflare speed endpoints (`speed.cloudflare.com/__down`, `__up`), capped at 25 MB down and 10 MB up per test | 60 min and on mark |
| Bufferbloat grade | Gateway and internet ping RTT during the speed test compared with the minute before | with each speed test |

Speed tests are skipped when the laptop itself sent or received more than 1 Mbit/s on average in the previous 30 s, so they don't disrupt a call. The skip is logged.

### Admin extras (elevated helper)

| Extra | Notes |
|---|---|
| `netsh wlan show wlanreport` | Runs once; the generated HTML (last 3 days of Wi-Fi sessions and disconnect reasons) is embedded in the report inside a sandboxed iframe |
| Full Event Log access | WLAN-AutoConfig, DHCP client and NCSI connectivity events for the last 7 days |
| TCP/443 traceroute | Per-socket TTL on a normal TCP connect plus a raw ICMP listener for time-exceeded replies. Best effort: if it proves unreliable on Windows during implementation, it is dropped and the ICMP traceroute remains |
| Wi-Fi driver advanced properties | Roaming aggressiveness, power saving, preferred band, read from the adapter's registry key; read only |

The helper is a second `landlord.exe --helper` process started with `ShellExecute` and the `runas` verb. It writes records into the same session directory through the store package, exits when the parent process exits, and never opens a browser. Keeping the browser and tray in the unelevated process avoids running a browser as admin.

## Diagnosis

Diagnosis runs over stored data, both at the end of a session and on demand for the "report so far" view. It is plain Go code with no machine learning and fixed, documented thresholds.

### Buckets

Raw records are aggregated into 10 s buckets. Per target a bucket holds sent and received counts, loss, RTT p50 and p95, and jitter (mean absolute difference of consecutive RTTs). Wi-Fi fields hold the minimum, mean and maximum RSSI, mean receive and transmit rate, retry deltas and the current BSSID and channel. Events are attached to the bucket they fall into. A 48 h session has about 17,300 buckets.

### Bad buckets and incidents

A bucket is bad if any of these holds for the internet side (median over the internet targets that responded):

| Condition | Default threshold |
|---|---|
| Loss (ICMP or STUN) | > 1 % |
| Jitter | > 30 ms |
| RTT p95 | > 150 ms |
| DNS or TCP/HTTP failures | any |
| Wi-Fi disconnect or WAN reconnect | any |

The loss, jitter and latency limits follow ITU-T G.114 and the published network requirements for Zoom and Microsoft Teams. Bad buckets separated by at most 30 s are merged into one incident. Each user mark opens a window of plus and minus 2 minutes; a mark with no incident inside that window is reported as "no measurable problem at this time".

### Culprit rules

Each bad bucket is checked from the lowest layer upwards. The first culprit whose rule matches gets the bucket. An incident's culprit is the one that owns most of its bucket time; runners-up above 25 % are listed too.

The gateway counts as degraded when loss > 1 % or RTT p95 > max(30 ms, 5 x the session's median gateway RTT).

| ID | Culprit | Rule |
|---|---|---|
| `wifi_signal` | Weak Wi-Fi signal | Gateway degraded, and RSSI < -70 dBm or receive rate < 30 % of the session's best, or a disconnect/roam in the bucket |
| `wifi_interference` | Wi-Fi interference or crowded channel | Gateway degraded, signal fine, and at least one of: MAC retry rate > 10 %, 4 or more neighbouring networks on overlapping channels above -80 dBm, band is 2.4 GHz |
| `client_device` | The laptop itself | Gateway degraded, signal fine, retries low, channel quiet; or gateway RTT spikes recur with a period of 55 to 65 s (background scanning); or degradation only on battery power |
| `lan_saturation` | Router overloaded or line shared | Gateway fine or mildly degraded, internet bad, and UPnP router throughput > 80 % of the WAN link rate while the laptop's own traffic is < 20 % of that; or bufferbloat grade D or worse |
| `access_line` | Access line, building wiring or provider access | Gateway fine, and all responding provider hops and at least two internet targets degraded in the same bucket; supporting evidence: WAN reconnects, FRITZ!Box CRC growth, resyncs, noise margin < 6 dB |
| `isp_congestion` | Provider congestion | Would be `access_line`, and at least 60 % of such bucket time falls between 18:00 and 24:00 local time while 02:00 to 06:00 is clean; re-evaluated at session level |
| `ipv4_tunnel` | IPv4 tunnel (DS-Lite/CGNAT) | IPv4 targets degraded while IPv6 to the same operators is fine, with DS-Lite or CGNAT detected |
| `dns` | DNS | Pings and TCP connects fine, but lookups via the configured resolver fail or take > 500 ms while 1.1.1.1 or 8.8.8.8 answer normally |
| `remote_service` | A single remote service | Only one target or HTTP service degraded, everything else fine |
| `unknown` | Unexplained | None of the above matched |

A provider hop's loss or RTT spike only counts if it continues to all later responding hops and to at least two internet targets. Routers often rate-limit ICMP addressed to themselves, so a lossy hop with clean hops behind it is ignored.

Confidence per incident: high when three or more independent signals agree (for example gateway loss, low RSSI and a roam event), medium for two, low for one.

### Standing findings

These are reported even without incidents:

- Median RSSI below -67 dBm, more than 20 % of the time on 2.4 GHz, or a crowded channel.
- Median download below 50 % of the contracted speed, if the user entered it.
- Bufferbloat grade (A to F, by RTT increase under load: < 5 ms, < 30 ms, < 60 ms, < 200 ms, < 400 ms, above).
- Path MTU below 1500 and any black-holing observed during the MTU probe.
- DS-Lite or CGNAT detected.
- Configured resolver median more than 3 x slower than 1.1.1.1.
- Wi-Fi disconnects from the pre-recording history (Event Log, wlanreport).
- Collectors that could not run, with the reason.

### Recommendations

Each culprit and standing finding maps to text in two groups, "The tenant can fix this" (things the user can do alone) and "Call the landlord" (hardware, provider or someone technical). Examples: move closer to the router or remove obstacles, prefer the 5 GHz network, use a cable for calls, set the adapter's power plan to maximum performance; add a mesh node or repeater, replace the router, change the Wi-Fi channel, change the DNS server, file a complaint with the provider using the report as evidence.

Country-specific hints appear only when the Cloudflare trace reports a matching country. v1 ships one: for `DE`, a pointer to the Bundesnetzagentur Breitbandmessung desktop app and the right to reduce payment under TKG section 57 when the contracted speed is not delivered.

### Live verdict

While recording, the app keeps an in-memory aggregate that is updated with every record as it is written, and re-runs the diagnosis every 30 seconds. Aggregation is incremental because re-reading two days of records every half minute would be too slow. The status page shows one of four states:

| State | Condition (defaults) | Status page text |
|---|---|---|
| Collecting | Less than 30 minutes of awake recording, or less than 1 minute of problem time | "Still collecting data. Keep using the laptop normally and press the button when something goes wrong." |
| No problems so far | At least 2 hours awake and problem time below 0.5 % | "No problems measured so far." Plus, if any marks had no incident: "Your marks happened while the connection measured fine." |
| Likely cause | At least 30 minutes awake, at least 1 minute of problem time, and the main culprit has at least 50 % of problem time | "Likely cause so far: Weak Wi-Fi signal (based on 4 problems in 3 hours). This can still change." |
| Confident | At least 2 hours awake, at least 3 minutes of problem time for the main culprit, at least 60 % share, at least 2 separate incidents with that culprit, and confidence medium or high | "Most likely cause: Weak Wi-Fi signal. You can finish now, or keep recording to confirm." |

Caveats are listed under the verdict when they apply: evening congestion can't be detected until the recording covers an evening and a night; marks without a matching incident are counted; missing collectors (for example no admin rights or no location access) are named. The thresholds are configurable under `[thresholds]`. The "Finish now" button is always available, and the final report uses the same diagnosis on all stored data.

## Architecture

```
landlord.exe (unelevated)                     landlord.exe --helper (elevated, optional)
  collectors --records--> store <--records--    admin collectors
                            |
                        aggregate -> diagnose -> report -> report.html
                            |
                   ui: 127.0.0.1 page + tray
```

### Packages

| Package | Responsibility |
|---|---|
| `cmd/landlord` | Flag parsing and subcommand dispatch |
| `internal/app` | Single-instance check, helper launch, session lifecycle, wiring collectors, UI and report, shutdown |
| `internal/record` | The `Record` type, `Sink` interface and record naming conventions shared by all packages |
| `internal/config` | Defaults, command-line flags and optional `landlord.toml` next to the executable |
| `internal/platform` | Interfaces for Wi-Fi, ICMP, interface counters, power, keep-awake and event logs. Windows implementations use `golang.org/x/sys/windows`; darwin and linux implementations cover ICMP and interface counters and return "unsupported" for the rest; fakes for tests |
| `internal/collect` | One file per collector, all implementing `Collector` |
| `internal/router` | Router detection and plugins (`fritzbox` in v1), UPnP IGD client |
| `internal/store` | Session directory, record encoding, hourly gzip JSONL files, session metadata and resume |
| `internal/aggregate` | Records to 10 s buckets |
| `internal/diagnose` | Incidents, culprit rules, standing findings, recommendations |
| `internal/report` | HTML template, embedded CSS, JS and uPlot, data embedding, redaction |
| `internal/ui` | Local HTTP server, status page, tray icon |
| `internal/sim` | Scenario-driven fake record generator for developing the UI, report and rules on any OS |

### Collector contract

```go
type Collector interface {
    Name() string
    Run(ctx context.Context, sink Sink) error
}

type Sink interface {
    Emit(Record)
}

type Record struct {
    Time      time.Time          `json:"t"`
    Collector string             `json:"c"`
    Kind      string             `json:"k"` // metric | event | info | unavailable
    Name      string             `json:"n,omitempty"` // e.g. echo, link, disconnect, mark
    Target    string             `json:"tg,omitempty"`
    Values    map[string]float64 `json:"v,omitempty"`
    Attrs     map[string]string  `json:"a,omitempty"`
}
```

A supervisor runs each collector in its own goroutine, recovers panics, and restarts failed collectors with exponential backoff capped at 5 minutes. A collector that cannot work at all (no Wi-Fi adapter, no UPnP, helper declined) emits one `unavailable` record with a reason and stops.

### Storage

Raw data lives in `%LOCALAPPDATA%\LANdlord\sessions\<session-id>\`: `meta.json` (start time, duration, version, config snapshot, marks) and `records-<yyyymmddhh>.jsonl.gz`, one file per hour. Writes are buffered and flushed every 5 s; after a crash at most the last few seconds and a truncated final line are lost, and the reader skips a truncated line. The data directory is capped at 200 MB; when exceeded, raw ping records older than 24 h are replaced by their buckets. The report goes to `Documents\LANdlord\` so it is easy to find.

### Local UI

The HTTP server binds to `127.0.0.1` on a random port. The browser is opened with a random 32-byte token in the URL; the page keeps it in memory and sends it as an `X-Landlord-Token` header on every request. The server rejects requests without the token, with a `Host` header other than `127.0.0.1:<port>` or `localhost:<port>`, and state-changing requests that aren't POST. This blocks other websites and DNS rebinding from pressing buttons or reading data. The router password is held in memory only and never written to disk or the report.

The tray icon uses `fyne.io/systray`, which needs no cgo on Windows. The executable is built with `-H=windowsgui`, so no console window appears.

While recording, `SetThreadExecutionState(ES_CONTINUOUS | ES_SYSTEM_REQUIRED)` keeps the laptop from sleeping. It is cleared on exit. Closing the lid can still suspend the laptop depending on power settings; sleep and resume are recorded and the gap is shaded in the report.

### Resource use

ICMP at 1 s to about eight targets, STUN bursts of 500 small packets per minute, one HTTP round per minute and an hourly capped speed test. Expected CPU use is well under 1 % and memory under 100 MB; both are checked during implementation with a 24 h run.

## Report

`report.html` is a single file with no external requests. It embeds CSS, a small JS app, uPlot (MIT) and the bucket data as gzip + base64, decompressed in the browser with `DecompressionStream`. Expected size for 48 h is 1 to 3 MB. It supports light and dark mode, narrow screens and printing.

The report uses the name's theme: it is an inspection report, suspects are culprits, and repairs are split between tenant and landlord.

Layout from top to bottom:

1. Verdict: main culprit with confidence, one plain sentence of explanation, recording period, and the share of time with problems.
2. The user's marks: how many fell into incidents and under which culprit, and how many had no measurable problem.
3. Culprit share of problem time as a bar chart.
4. Recommendations in the two groups, "The tenant can fix this" and "Call the landlord".
5. Timeline of synchronised uPlot charts sharing one time axis and cursor, zoomable: latency and loss per layer (gateway, provider hops, internet), STUN jitter and loss, RSSI with receive rate and band or channel changes, router throughput if available, DNS time. Event markers show disconnects, roams, WAN reconnects, speed tests, sleep gaps and marks. Incidents are shaded in their culprit's colour.
6. Heatmap of problem minutes by hour of day and day.
7. Incident table with time, duration, culprit, confidence and evidence. Clicking a row zooms the timeline.
8. Inspection details: neighbouring networks per channel, speed test table, bufferbloat grade, path MTU, IPv4 vs IPv6, CGNAT/DS-Lite, DNS resolver comparison, router details and line statistics.
9. Appendix: OS, adapter and driver, collector availability with reasons, thresholds used, the embedded wlanreport, and a CSV export of the buckets.

A redaction toggle in the report hides SSIDs, BSSIDs, public IPs and neighbouring network names. `landlord report --redact <session>` writes a redacted file directly, for sharing in GitHub issues.

## Configuration

Defaults work without any configuration. `landlord.toml` next to the executable, or flags, can override:

| Key | Default |
|---|---|
| `duration` | `48h` |
| `targets.ipv4`, `targets.ipv6` | 1.1.1.1, 8.8.8.8, 9.9.9.9 and IPv6 equivalents |
| `stun.servers` | `stun.l.google.com:19302`, `stun.cloudflare.com:3478` |
| `http.targets` | zoom.us, teams.microsoft.com, meet.google.com, youtube.com, netflix.com, google.com |
| `speedtest.interval`, `speedtest.max_download_mb`, `speedtest.max_upload_mb`, `speedtest.enabled` | `60m`, `25`, `10`, `true` |
| `thresholds.*` | The values in the diagnosis section |
| `helper.enabled` | `true` (asks for elevation) |
| `ui.open_browser` | `true` |

Subcommands for technical users: `landlord` (record), `landlord report [--redact] <session>` (rebuild a report from stored data), `landlord simulate <scenario>` (generate a fake session).

## Platform support

| Platform | v1 status |
|---|---|
| Windows 10 21H2+ and Windows 11 (amd64, arm64) | Full |
| macOS, Linux | Builds and runs; ICMP, traceroute, DNS, HTTP, STUN, speed test, UPnP and FRITZ!Box work; Wi-Fi, power and event log collectors report "unsupported". The report states the limited mode |

## Privacy and security

- No telemetry, no uploads, no accounts.
- Outgoing connections go only to the configured measurement targets: ICMP targets, STUN servers, HTTP targets, DNS resolvers, Cloudflare trace and speed endpoints, and the local router. The README lists them.
- The router password stays in memory.
- Reports contain network names and IPs; the README explains the redaction option.
- Release binaries come with SHA256 checksums, built by GitHub Actions from tagged commits.

## Testing

- `diagnose`: table-driven tests over synthetic bucket timelines, one or more per culprit rule, plus traps such as a lossy provider hop with clean hops behind it (must not become `access_line`), evening-only degradation (must become `isp_congestion`) and 60 s periodic gateway spikes (must become `client_device`).
- `aggregate`: percentile, jitter and loss calculations; bucket boundaries; sleep gaps.
- `store`: round trip, truncated final line, resume after restart, size cap.
- `collect` and `router`: parsers tested against captured fixtures (BSS information elements, TR-064 SOAP responses, SSDP descriptions, Cloudflare trace output).
- `platform`: fakes for all interfaces. Windows-only tests that call real APIs are behind a build tag and skip gracefully when there is no Wi-Fi adapter, as on CI runners.
- `ui`: token, Host header and method checks.
- `report`: the generated HTML parses, contains no external URLs, and the embedded data decodes to the expected bucket count.
- `sim`: scenarios from `internal/sim/scenarios/` feed end-to-end tests from records to report.
- Manual check before the first release: a 24 h run on a real Windows 11 laptop on Wi-Fi.

## Repository, build and release

```
cmd/landlord/
internal/{config,platform,collect,router,store,aggregate,diagnose,report,ui,sim}/
docs/                      design, screenshots, guide for non-technical users
.github/workflows/         test (windows, macos, linux), lint, release
.goreleaser.yaml
LICENSE  README.md  CONTRIBUTING.md
```

- Language: Go, latest stable release at implementation time, no cgo.
- Module path: `github.com/loehnertz/LANdlord`, matching the repository name exactly (Go module paths are case-sensitive). The binary is still called `landlord`.
- Main dependencies: `golang.org/x/sys`, `fyne.io/systray`, `github.com/miekg/dns`, `github.com/pion/stun/v3`, `github.com/huin/goupnp`, `github.com/pelletier/go-toml/v2`; uPlot vendored under `internal/report/assets`.
- Build: `GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui"`, with `go-winres` for icon, version info and an `asInvoker` manifest.
- CI: `go test ./...` on Windows, macOS and Linux, `go vet`, `golangci-lint`.
- Release: GoReleaser on tag push publishes Windows amd64 and arm64 zips, macOS and Linux archives, and checksums. Binaries are not code-signed in v1.
- License: MIT.

## Later

- Android Wi-Fi walk test (signal per room) to help place the router or a mesh node.
- Guided 15-minute test: next to the router, in the problem room, on a cable.
- Optional upload to a small self-hosted endpoint for live remote viewing.
- Real Wi-Fi collectors for macOS and Linux.
- Router plugins beyond FRITZ!Box.
- DOCSIS signal levels for FRITZ!Box cable models (needs the web UI login flow and `data.lua`, not TR-064).
- Code signing.

## Decisions

| Decision | Choice | Reason |
|---|---|---|
| Platform | Native Windows program in Go | A browser can't ping, traceroute or read Wi-Fi data; Android throttles scans and stops background work |
| Result delivery | Local `report.html` sent by the user | No backend to build or host |
| Session style | 48 h recording with a mark button | Intermittent problems need long recordings; marks link measurements to experience |
| UI | Local web page plus tray icon | Pure Go, cross-compiles from macOS, same rendering code as the report |
| Admin | Optional elevated helper | Extra data without making admin a requirement or running the browser elevated |
| Language | English everywhere | Open source audience |
| Name | LANdlord | Old building, blame the landlord's wiring |
| License | MIT | Permissive, common for Go tools |
