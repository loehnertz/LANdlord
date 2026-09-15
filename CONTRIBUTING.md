# Contributing

Thanks for helping. Issues and pull requests are welcome.

## Getting started

You need Go 1.26 or newer. Most of the code (aggregation, diagnosis, report, UI) runs and is tested on any OS; only the Windows collectors need Windows.

```bash
go test ./...
go run ./cmd/landlord simulate -scenario weak_wifi -out testdata/out
```

Open the generated report in a browser to see changes to the report or the diagnosis.

Code that calls Windows APIs has extra tests behind a build tag. Run them on a Windows machine (ideally a laptop on Wi-Fi):

```powershell
go test -v -tags windowsapi ./internal/platform/winplat/
```

## How the pieces fit

- Collectors in `internal/collect/*` and `internal/router/*` emit `record.Record`s. They reach the operating system only through `internal/platform`.
- `internal/store` writes records to hourly gzip files in the session directory.
- `internal/aggregate` turns records into 10-second buckets, `internal/diagnose` finds problems and their causes, and `internal/report` renders the HTML report.
- `internal/app` runs a recording: collectors, the live verdict, the local status page (`internal/ui`) and the elevated helper.

[docs/DESIGN.md](docs/DESIGN.md) explains the reasoning; [AGENTS.md](AGENTS.md) lists the conventions.

## The record contract

Collector names, record names and target labels are constants in `internal/record/names.go`, and the table in `docs/IMPLEMENTATION.md` ("Record conventions") says which values each record carries. `internal/aggregate` depends on exactly these names, so change both sides together.

## Common contributions

**A new diagnosis scenario.** Add it to `internal/sim/scenarios.go`, add the expected culprit to `TestScenarios` in `internal/diagnose/scenarios_test.go`, and adjust the rules in `internal/diagnose/rules.go` if needed. Thresholds belong in `internal/config`, not in the rules.

**A router plugin.** Follow `internal/router/fritzbox`: detect the router in `internal/router/detect.go`, write a collector that emits records under its own collector name, and start it from `Recorder.onRouterIdentity` in `internal/app/app.go`.

**A country hint.** Add the text in `internal/diagnose/recommend.go` next to the German one, keyed on the country code from Cloudflare's trace.

## Style

- Write the failing test first.
- Keep user-facing text plain and short; the people reading it aren't network engineers.
- LANdlord never changes system settings and never uploads data. Keep it that way.
