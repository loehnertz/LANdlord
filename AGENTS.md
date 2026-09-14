# AGENTS.md

LANdlord records a home internet connection on Windows for 48 hours and writes a self-contained `report.html` that names the most likely cause of bad calls and streams: Wi-Fi signal, Wi-Fi interference, the laptop, the router or shared usage, the access line, provider congestion, an IPv4 tunnel, DNS or a single remote service. Written in Go without cgo, released under MIT.

## Read first

- [docs/DESIGN.md](docs/DESIGN.md) is the approved spec.
- [docs/IMPLEMENTATION.md](docs/IMPLEMENTATION.md) is the task-by-task plan. Work through the tasks in order and tick the checkboxes as steps are done. Where the plan and the spec disagree, the plan's "Deviations" section explains why.

## Commands

```bash
go test ./...                 # all tests, any OS
make lint                     # go vet + golangci-lint
make windows                  # cross-compile bin/landlord.exe from macOS or Linux
go run ./cmd/landlord simulate -scenario weak_wifi -out testdata/out   # fake session + report on any OS
```

Requires Go 1.26 or newer.

## Layout

- `cmd/landlord`: subcommand dispatch only.
- `internal/record`: the `Record` type and all collector, record and target name constants.
- `internal/store`: session directory, hourly gzip JSONL writer, crash-tolerant reader.
- `internal/aggregate`, `internal/diagnose`, `internal/report`: the analysis pipeline. Pure Go, runs and is tested on every OS.
- `internal/platform`: interfaces for everything OS-specific, with Windows and unix implementations and a fake for tests.
- `internal/collect/*`, `internal/router/*`: collectors. Each emits records through a `record.Sink`.
- `internal/ui`, `internal/app`: local status page, tray icon, session lifecycle, elevated helper.
- `internal/sim`: scenario generator used for end-to-end tests and report development.

## Conventions

- Module path `github.com/loehnertz/LANdlord`; the binary is `landlord`.
- Branch `master`. One commit per plan task, message `<area>: <what>`.
- Write the failing test first. Windows API tests use the build constraint `//go:build windows && windowsapi` and skip when no Wi-Fi adapter is present, so CI runners pass.
- Windows-only code lives in `_windows.go` files. Other platforms get a counterpart with `//go:build !windows`. Analysis code never imports `internal/platform/windows`.
- Record collector names, record names and target labels come only from `internal/record/names.go`. The "Record conventions" table in the plan is the contract between collectors and `aggregate`; change both together.
- Diagnosis thresholds come from `config.Thresholds`. Don't hardcode threshold numbers in rules.
- LANdlord only reads system state and never changes settings. No telemetry, no uploads. The report HTML must not reference external URLs.
- All user-facing text is English and written for people who aren't technical: short sentences, no jargon without an explanation.
- Code that touches Windows APIs can only be verified on Windows: the `windows-latest` CI runner for API calls, a real laptop on Wi-Fi for Wi-Fi data.
