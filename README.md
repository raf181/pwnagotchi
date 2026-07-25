# Pwnagotchi (Go)

This is a Go implementation of [Pwnagotchi](https://pwnagotchi.org/) — the
daemon, web UI, plugin system, and all 23 bundled plugins are native Go,
built as a single static binary with no Python runtime dependency.

[Pwnagotchi](https://pwnagotchi.org/) is a Raspberry Pi leveraging
[bettercap](https://www.bettercap.org/) that survives from its
surrounding Wi-Fi environment to maximize the crackable WPA key material
it captures (either passively, or by performing authentication and
association attacks). This material is collected as PCAP files containing
any form of handshake supported by [hashcat](https://hashcat.net/hashcat/),
including [PMKIDs](https://www.evilsocket.net/2019/02/13/Pwning-WiFi-networks-with-bettercap-and-the-PMKID-client-less-attack/),
full and half WPA handshakes.

Multiple units within close physical proximity can "talk" to each other,
advertising their presence to each other by broadcasting custom
information elements using a parasite protocol
[@evilsocket](https://x.com/evilsocket) built on top of the existing
dot11 standard.

## Layout

- `cmd/pwnagotchi` — CLI entry point.
- `internal/config` — TOML/YAML config loading, merging, defaults.
- `internal/cli` — argument parsing.
- `internal/identity` — RSA identity keypair management.
- `internal/automata` — state machine.
- `internal/epoch` — epoch/session bookkeeping.
- `internal/logging` — logging setup.
- `internal/voice` — personality voice lines (translated via embedded
  gettext `.mo` catalogs; editable `.po` sources live in `locale-src/`).
- `internal/bettercap` — bettercap REST API client.
- `internal/grid` — pwngrid API client.
- `internal/mesh` — peer discovery.
- `internal/agent` — main orchestration loop.
- `internal/ui` — display state/rendering; `internal/ui/hw` — display
  drivers; `internal/web` — web UI.
- `internal/pluginmanager` — the native plugin manager every bundled
  plugin registers with: lifecycle, event dispatch, typed capabilities,
  panic isolation.
- `internal/plugins/native/` — all 23 bundled plugins, each its own
  package (memtemp, cache, switcher, wpa-sec, grid, wigle, bt-tether,
  session-stats, auto-tune, and the rest).
- `internal/pluginrpc` — the Go-only third-party plugin distribution
  system: versioned manifests, checksum-verified separately-compiled Go
  executables, and a bounded RPC protocol — see `docs/plugin-development.md`.
- `internal/wifiparse` — pure-Go PCAP/802.11 field extraction (BSSID,
  ESSID, encryption, channel, RSSI) for the `grid`/`wigle` plugins.
- `docs/` — `architecture.md` (how the pieces fit together),
  `migration-ledger.md` (evidence-based porting log), `plugin-development.md`
  (native plugin API), `feature-matrix.md`/`plugin-compatibility-matrix.md`/
  `known-differences.md` (per-subsystem status and Python↔Go divergences).
- `deploy/` — Raspberry Pi image build pipeline (pi-gen stages, systemd
  units, kernel/Nexmon pinning).
- `tests/`, `testdata/` — Go tests and fixtures.

## Building

```sh
go build ./...
go vet ./...
go test ./...
go test -race ./...

# Cross-compile for the Raspberry Pi target:
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o pwnagotchi-go ./cmd/pwnagotchi
```

Or via the Makefile: `make build`, `make fmt`, `make vet`, `make test`, `make race`.

## Writing a plugin

See [`docs/plugin-development.md`](docs/plugin-development.md) for the
native Go plugin API (bundled/compiled-in) and
[`internal/pluginrpc`](internal/pluginrpc) for the third-party
distribution system (versioned manifests + checksum-verified executables).
Existing Python plugins are not compatible with this daemon — the Python
plugin bridge has been fully removed.

## Status and known gaps

See [`docs/migration-ledger.md`](docs/migration-ledger.md) for the
authoritative, evidence-based record of what's ported, what's
intentionally deferred, and why. Two items are deliberately out of scope
as of this writing (by explicit request, not oversight): the ~94 physical
display driver ports and the SPI/I2C/GPIO/PWM hardware bus abstraction
work. The `pwnagotchi/` Python source tree at the repository root is kept
only for that reason — it is not run, imported, or required by this Go
daemon in any way.

## Links

| &nbsp;    | Official Links                                           |
|-----------|----------------------------------------------------------|
| Website   | [pwnagotchi.org](https://pwnagotchi.org/)                  |
| Chat      | [discord](https://discord.gg/PGgnzFbz4M) |
| Subreddit | [r/pwnagotchi](https://www.reddit.com/r/pwnagotchi/)     |

## License

`pwnagotchi` created by [@evilsocket](https://x.com/evilsocket) and
updated by [us](https://github.com/jayofelony/pwnagotchi/graphs/contributors).
It is released under the GPL3 license.
