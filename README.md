# LANdlord

LANdlord finds out why your internet keeps letting you down. It records the connection of a Windows laptop for up to two days, while you use it normally, and then writes one report that names the most likely culprit: the Wi-Fi signal, a crowded Wi-Fi channel, the laptop itself, someone else filling the line, the line or the provider, evening congestion, an IPv4 tunnel, DNS, or a single online service.

It's made for the situation where the person with the bad connection isn't technical and the person who could help isn't there. They run LANdlord, press a big button whenever a call stutters, and send you the report.

## How it works

LANdlord measures every layer between the laptop and the internet at the same time and looks for the lowest one that breaks first:

- Wi-Fi signal strength, link speed, retransmissions, roaming and neighbouring networks
- ping to the router, to the provider's first routers and to several internet hosts, over IPv4 and IPv6
- UDP loss and jitter in a pattern similar to a video call (using public STUN servers)
- DNS lookups through Windows, the router and public resolvers
- connection and response times for Zoom, Teams, Meet, YouTube, Netflix and Google
- an hourly, capped speed test with a bufferbloat grade
- the router's own traffic counters and reconnects via UPnP, and line statistics on FRITZ!Box routers (with the router password): DSL noise margin and errors, or DOCSIS signal levels and errors on cable models
- Windows' Wi-Fi history from the week before, its Wi-Fi report, and a TCP traceroute on port 443, when admin rights are granted

While recording, the status page already shows a likely cause once there is enough data. The full recording is optional.

## Quick start

1. Download the latest `landlord_..._windows_amd64.zip` from [Releases](https://github.com/loehnertz/LANdlord/releases) and unzip it.
2. Double-click `landlord.exe`. Windows warns about an unknown publisher because the program isn't code-signed yet: click "More info", then "Run anyway".
3. Answer the admin prompt with "Yes" for extra data (or "No"; it still works).
4. A page opens in the browser. Press "It's bad right now!" whenever something goes wrong.
5. After 48 hours, or when you click "Finish now", the report is saved in `Documents\LANdlord` and the folder opens. Send `report.html` to whoever helps you.

The [user guide](docs/USER-GUIDE.md) walks through every step, including the Windows location setting that Wi-Fi details need.

## Privacy

LANdlord has no telemetry and uploads nothing. It only reads system information and changes no settings. It connects to:

- your router (ping, DNS, UPnP, and TR-064 on FRITZ!Box routers)
- the provider routers on the way to the internet (ping)
- 1.1.1.1, 8.8.8.8, 9.9.9.9 and their IPv6 counterparts (ping and DNS)
- stun.l.google.com and stun.cloudflare.com (STUN)
- zoom.us, teams.microsoft.com, meet.google.com, www.youtube.com, www.netflix.com, www.google.com (one small HTTPS request each per minute)
- www.cloudflare.com and speed.cloudflare.com (public IP address and speed test)

All of these can be changed in `landlord.toml`. The router password is kept in memory only.

Reports contain network names, IP addresses and router details. Use "Hide private details" in the report, or `landlord report -redact SESSION_DIR`, before sharing one publicly.

## Other platforms

Windows 10 and 11 are fully supported. The macOS and Linux builds run in a limited mode without Wi-Fi details, which is mostly useful for development.

## Building from source

Requires Go 1.26 or newer.

```bash
go test ./...
make windows          # bin/landlord.exe with icon and version info
go run ./cmd/landlord simulate -scenario weak_wifi -out testdata/out
```

`simulate` generates a realistic fake recording and its report on any OS; `go run ./cmd/landlord simulate -scenario nope` lists all scenarios. See [CONTRIBUTING.md](CONTRIBUTING.md) and [docs/DESIGN.md](docs/DESIGN.md).

## Configuration

Everything works without configuration. To change the recording length, measurement targets or thresholds, put a `landlord.toml` next to `landlord.exe`, for example:

```toml
duration = "24h"

[speedtest]
enabled = false

[thresholds]
loss_pct = 2
```

The keys and defaults are in [internal/config/config.go](internal/config/config.go).

## License

MIT
