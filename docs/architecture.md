# Architecture

A single static Go binary implementing the whole Pwnagotchi daemon: no
Python runtime, no subprocess bridge, one process. This document describes
how the pieces fit together; for per-subsystem porting status see
`docs/feature-matrix.md`, for plugins specifically see
`docs/plugin-compatibility-matrix.md` and `docs/plugin-development.md`,
and for every intentional Python↔Go divergence see
`docs/known-differences.md`.

## Process shape

One process, one `main()` (`cmd/pwnagotchi/main.go`), a handful of
long-lived goroutines:

- the main-mode loop (`internal/cli.RunAutoMode`/`RunManualMode`) driving
  `internal/agent`'s recon/associate/deauth/epoch cycle,
- one bounded, serial event-queue goroutine per loaded plugin
  (`internal/pluginmanager`),
- a background render thread for the resolved display driver
  (`internal/ui/display`), if a real display initializes,
- an `internal/mesh` peer-advertising poller,
- a bettercap websocket event-consumer goroutine (`internal/bettercap`),
- the `internal/web` HTTP server (`net/http`, not a separate process).

Panics inside any one plugin's event handler are recovered and counted,
never crashing the daemon or another plugin — see
`internal/pluginmanager/manager_test.go`.

## Startup sequence (`cmd/pwnagotchi/main.go`'s `run()`)

In order, mirroring real `cli.py`'s own literal sequence except where Go's
static construction order forces a difference (documented inline in the
source and in `docs/known-differences.md`):

1. Parse CLI args (`internal/cli`); handle `plugins <subcmd>`,
   `--version`, `--donate`, `--check-update` and exit early if matched.
2. `config.LoadConfig` — embedded `defaults.toml`, boot-config migration,
   `config.toml` merge, `conf.d/*.toml` drop-ins, display-type alias
   normalization. Handle `--print-config` and exit early if matched.
3. `fs.SetupMounts` — zram/tmpfs RAM-disk mounts per `fs.memory.mounts.*`.
4. `logging.SetupLogging`.
5. Construct the native plugin manager (`pluginmanager.New`) — it must
   exist before the display/agent are built, since constructing those is
   what starts emitting the events plugins react to. Its
   `CapabilitiesFor` closure captures the view/agent variables *by
   reference*; they're still zero-valued at this point and get filled in
   by steps 6 and 8 below, which is safe because no plugin's `OnLoad` runs
   until step 9.
6. `display.New` — try the real rendering pipeline
   (`hw.NewDriver` → `view.New`) first; fall back to
   `cli.NewHeadlessView` (log-only) if the resolved driver's
   `Initialize()` fails (no physical display, or `ui.display.enabled=false`)
   — both are legitimate operating modes, never a stub.
7. `unit.SetName` — hostname vs. `main.name` check; **reboots the host for
   real** if they differ, exactly like Python. Handle `--clear` and exit
   early if matched.
8. `identity.NewKeyPair`, then `agent.New` (wires the real bettercap
   client, view, epoch/automata state).
9. `registerNativePlugins` + `pluginmanager.Manager.LoadAll` — every
   bundled plugin whose `config['main']['plugins'][name].enabled` is true
   gets `OnLoad` called, then the automatic `loaded`→`config_changed`
   event pair.
10. Register the `SIGUSR1` mode-aware restart handler.
11. `web.New` + `Start()` — the HTTP server, always started (a no-op
    internally if `ui.web.enabled` is false, matching Python).
12. Enter `cli.RunAutoMode` or `cli.RunManualMode` — this is the main loop;
    it runs until the process is killed.

## Package map

| Package | Responsibility |
|---|---|
| `internal/config` | TOML/YAML loading, merging, defaults, display-type alias table, version comparison |
| `internal/identity` | RSA keypair generation/loading (via the external `pwngrid` binary), PSS signing, fingerprinting |
| `internal/fs` | Atomic file writes, zram/tmpfs RAM-disk mount management |
| `internal/logging` | Log setup, Python-matching line format, size-based rotation |
| `internal/voice` | Personality flavor text; embeds all 184 gettext `.mo` catalogs via `go:embed` |
| `internal/epoch` | Per-epoch counters, the `[epoch N] ...` log-line serialization contract |
| `internal/automata` | Mood/state-machine mixin logic (bored/sad/angry/excited/grateful/...) |
| `internal/bettercap` | Bettercap REST + websocket event client |
| `internal/grid` | pwngrid-peer REST client (mesh identity/inbox/advertise) |
| `internal/mesh` | Peer model, RFC3339 parsing, peer-advertising background poller |
| `internal/wifiparse` | Pure-Go pcap/RadioTap/802.11 field extraction (no scapy, no cgo) |
| `internal/agent` | Central orchestrator: recon, associate/deauth, channel hop, recovery data, handshake events |
| `internal/session` | Last-session log parsing (`LastSession`) |
| `internal/unit` | Hostname/uptime/mem/CPU/temperature telemetry, reboot/shutdown/restart |
| `internal/ui/state`, `faces`, `fonts`, `components` | Widget state, face strings, embedded DejaVu fonts, drawable primitives |
| `internal/ui/view` | Canvas composition, mood/event render methods, the render-callback hook the web UI's frame cache attaches to |
| `internal/ui/display` | Ties `view` to a resolved `hw.Driver`, background render thread, `on_frame` hook |
| `internal/ui/hw` | Display driver registry/interface. `DummyDisplay` is real; the ~92 real e-ink/OLED/LCD driver *implementations* are the disclosed, out-of-scope gap — see "Status and known gaps" below |
| `internal/web` | `net/http` server: index/UI-frame/theme/shutdown-reboot-restart/inbox routes, CSRF, Basic auth, the native `logtail`/`webcfg` plugin implementations |
| `internal/wpasec` | Native `wpa-sec` plugin (handshake upload to wpa-sec.stanev.org), including one-time legacy sqlite DB migration |
| `internal/pluginmanager` | The plugin runtime: `Plugin`/`Loader`/`Unloader`/`EventHandler`/`WebhookHandler`/`RouteRegistrar` interfaces, per-plugin queues, panic isolation, typed `Capabilities` |
| `internal/plugins/native/*` | The 20 bundled plugins that live directly on `pluginmanager` (3 more — `logtail`, `webcfg`, `wpa-sec` — live under `internal/web`/`internal/wpasec` for structural reasons; see `docs/plugin-compatibility-matrix.md`) |
| `internal/plugins` | The `pwnagotchi plugins <subcommand>` CLI (search/list/install/uninstall/upgrade/enable/disable/edit), now built on `pluginrpc` manifests instead of Python `.py` files |
| `internal/pluginrpc` | Third-party plugin distribution: versioned TOML manifests, checksum verification, a bounded newline-delimited-JSON RPC protocol, crash/hang detection for out-of-process plugins |
| `internal/pluginhost` | Small adapters folding pre-existing non-plugin-shaped code (`logtail`/`webcfg`'s bespoke routes) into `Manager.List()` bookkeeping |

## Plugins: two distribution paths

1. **Bundled** — compiled into this binary, registered once in
   `cmd/pwnagotchi/main.go`'s `registerNativePlugins`. This is how all 23
   original bundled plugins + `example` work today. See
   `docs/plugin-development.md`.
2. **Third-party** — a separately compiled Go executable, described by a
   versioned TOML manifest (name/version/target os-arch/sha256/declared
   capabilities), fetched from a JSON repository index, checksum-verified
   before every spawn, and driven over a bounded RPC protocol
   (`internal/pluginrpc`). `pluginrpc.RemotePlugin` implements the exact
   same `pluginmanager.Plugin`/`Loader`/`Unloader`/`EventHandler`
   interfaces a bundled plugin does, so the manager treats both uniformly.

There is no third path: a bare Python `.py` file is never loaded, and
`pwnagotchi plugins install <name>` for one fails with a clear migration
message rather than silently doing nothing (see
`internal/plugins/cmd.go`'s `legacyPythonPluginMessage`).

## Status and known gaps

Every package above is **Ported** or (for the display-driver registry)
**Interface-only** per `docs/feature-matrix.md`'s legend — except the ~92
real SPI/I2C/GPIO display driver *implementations* themselves and the
Go/SPI/I2C/GPIO/PWM hardware bus abstraction generalization work, which
are explicitly out of scope for the current phase of this migration by
user request, not an oversight. The `pwnagotchi/` Python source tree
remains on disk for exactly this reason (the display code depends on
parts of it) — it is not run, imported, or required by this Go daemon.
See the root `README.md`'s "Status and known gaps" section and
`docs/migration-ledger.md` for the full, evidence-based accounting.
