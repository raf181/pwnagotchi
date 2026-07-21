# pwnagotchi (Go)

**This is now the primary implementation.** It's a behavioral port of the
[pwnagotchi](https://github.com/jayofelony/pwnagotchi) Python daemon to Go,
targeting the closest possible 1:1 compatibility with the Python reference
implementation that remains at the repository root (`../pwnagotchi`) —
kept only as the differential-test oracle and as the runtime for bundled
plugins (see `internal/pyplugin`), not as something to run directly or add
features to anymore.

The Python implementation is the **behavioral oracle**. It is never modified by this
port. Every difference between the two is either fixed (Go made to match Python) or
recorded in `docs/known-differences.md` with a rationale.

## Layout

- `cmd/pwnagotchi` — CLI entry point, mirrors `pwnagotchi.cli:pwnagotchi_cli`.
- `internal/config` — TOML/YAML config loading, merging, defaults (`pwnagotchi/utils.py`).
- `internal/cli` — argument parsing (`pwnagotchi/cli.py`).
- `internal/identity` — RSA identity keypair management (`pwnagotchi/identity.py`).
- `internal/automata` — state machine (`pwnagotchi/automata.py`).
- `internal/epoch` — epoch/session bookkeeping (`pwnagotchi/epoch.py`).
- `internal/logging` — logging setup (`pwnagotchi/log.py`).
- `internal/voice` — personality voice lines (`pwnagotchi/voice.py`).
- `internal/bettercap` — bettercap REST API client (`pwnagotchi/bettercap.py`).
- `internal/grid` — pwngrid API client (`pwnagotchi/grid.py`).
- `internal/mesh` — peer discovery (`pwnagotchi/mesh/`).
- `internal/agent` — main orchestration loop (`pwnagotchi/agent.py`).
- `internal/ui` — display state/rendering (`pwnagotchi/ui/`), `internal/ui/hw` — display
  drivers (`pwnagotchi/ui/hw/`), `internal/ui/web` — web UI (`pwnagotchi/ui/web/`).
- `internal/plugins` — plugin loader (`pwnagotchi/plugins/`).
- `internal/pybridge` — subprocess/IPC bridge used to run unported Python plugins.
- `internal/fs` — overlay filesystem management (`pwnagotchi/fs/`).
- `docs/` — analysis, feature matrix, baseline, differences, final report.
- `tests/`, `testdata/` — Go tests and fixtures, including Python-vs-Go differential tests.

## Status

See `docs/feature-matrix.md` for the authoritative per-module port status, and
`docs/final-port-report.md` for the current overall state.

## Building

```
cd go-port
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

## Compatibility tests

```
make compatibility-test
```

Runs the Python reference (from `../venv`) and the Go build side by side and diffs
their behavior for CLI, config, serialization, and other surfaces covered so far.
