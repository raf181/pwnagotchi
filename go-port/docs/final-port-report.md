# Final Port Report

**Status: IN PROGRESS.** This document is updated at the end of every work
session as subsystems land; it is not yet the "done" report described in the
porting goal. Treat every claim below as accurate as of the date it was
written, not as a permanent guarantee — cross-check against
`docs/feature-matrix.md` (the live source of truth) before relying on any
specific status.

## What's real today: a runnable Go `pwnagotchi` binary with the entire core daemon layer

`cmd/pwnagotchi` now builds into a real binary. `--version`, `--help`, and
`--donate` were diffed directly against the real Python CLI (`python3 -m
pwnagotchi.cli`) and are **byte-for-byte identical**, including exit codes
— this is codified as a permanent test in `tests/compat_cli_test.go`, not
just something checked once by hand. `--print-config` was smoke-tested
against a scratch config directory and correctly bootstraps
`defaults.toml`.

| Subsystem | Go package | Verified via |
|---|---|---|
| CLI entry point (`pwnagotchi/cli.py`) | `cmd/pwnagotchi`, `internal/cli` | unit tests + `tests/compat_cli_test.go` (byte-diff against real Python `--version`/`--help`/`--donate`, stdout AND exit code) |
| Plugin file-manager CLI (`plugins/cmd.py`) | `internal/plugins/cmd.go` | unit tests incl. the `{:^N}` format-spec centering algorithm verified against real Python output |
| System telemetry + lifecycle (`pwnagotchi/__init__.py`) | `internal/unit` | unit tests against fixture `/proc`/`/sys` files; fake-`Runner`-backed SetName/Restart/Reboot/Shutdown tests |
| Version string (`pwnagotchi/_version.py`) | `internal/version` | unit test |
| Config/utils (`pwnagotchi/utils.py`) | `internal/config` | golden fixtures from the real Python interpreter + differential tests reproducing `load_config`'s full pipeline; `iface_channels`, `md5`, `download_file`, `unzip` all ported, shell-free |
| Filesystem (`pwnagotchi/fs/__init__.py`) | `internal/fs` | atomic-write tests, fake-`Runner` mount/zram tests, `SetupMounts` orchestration tests |
| Identity/crypto (`pwnagotchi/identity.py`) | `internal/identity` | openssl-backed unit tests + Python-differential tests (byte-exact fingerprint, RSA-PSS cross-verify against real pycryptodome) |
| Wi-Fi channel math (`pwnagotchi/mesh/wifi.py`) | `internal/mesh/wifi.go` | golden fixtures |
| Epoch tracking (`pwnagotchi/epoch.py`) | `internal/epoch` | golden `[epoch N] ...` log-line test against real Python `%`-format output |
| Mood state machine (`pwnagotchi/automata.py`) | `internal/automata` | unit tests |
| Bettercap client (`pwnagotchi/bettercap.py`) | `internal/bettercap` | httptest + real websocket server tests |
| Grid client (`pwnagotchi/grid.py`) | `internal/grid` | httptest, incl. the `advertise(enabled=False)` operator-precedence bug |
| Peer/mesh advertising (`pwnagotchi/mesh/peer.py`, `mesh/utils.py`) | `internal/mesh/peer.go`, `advertiser.go` | unit tests |
| Voice/flavor text (`pwnagotchi/voice.py`) | `internal/voice` | real `.mo` catalog cross-check against Python `gettext` |
| Last-session log parsing (`pwnagotchi/log.py`'s `LastSession`) | `internal/session` | golden transcript vs. a real `LastSession.parse()` run |
| Logging setup + rotation (`pwnagotchi/log.py`'s `setup_logging`) | `internal/logging` | unit tests incl. the `do_rotate` same-path-first-rotation edge case |
| Core orchestrator (`pwnagotchi/agent.py`) | `internal/agent` | 14 tests: construction/defaults, AP filtering, channel grouping, interaction throttling, associate/deauth/set_channel, handshake events, recovery round-trip |
| UI drawing/orchestration (`ui/components.py`, `ui/view.py`, `ui/display.py`) | `internal/ui/components`, `internal/ui/view`, `internal/ui/display` | unit tests incl. real-pixel draw assertions, the `IsNormal` string-concatenation bug reproduced against the real interpreter, `on_frame` real shell-command execution |
| Hardware display registry + layouts (`ui/hw/*.py`, ~94 drivers) | `internal/ui/hw` | exhaustive `NormalizeDisplayType`→driver resolution test; `Layout()` real for every driver (AST-extracted from real Python); `Initialize`/`Render`/`Clear` real only for `DummyDisplay` (no other real hardware in this rig — see below) |
| Plugin runtime loader + bundled plugins (`plugins/__init__.py`, `plugins/default/*.py`) | `internal/plugins/loader.go`, `internal/pyplugin` (real Python subprocess/IPC bridge) | `internal/pyplugin/bridge_test.go` (arg-translation unit tests) + `tests/compat_pyplugin_test.go` (real end-to-end: genuine `cache.py`/`logtail.py` plugins loaded, dispatched, toggled, and webhook-called via the bridge; real files written, real Flask responses rendered) |
| Web UI (`ui/web/__init__.py`, `server.py`, `handler.py`) | `internal/web` | `internal/web/server_test.go` (real embedded assets, real CSRF/auth enforcement, real frame serving) + the pyplugin compat tests above (plugin routes' real dependency) |

All of the above build cleanly (`go build ./...`), pass `go vet ./...`,
`go test ./...`, and `go test -race ./...` (19 packages, zero data races).
`make compatibility-test` runs the Python-vs-Go differential suite (CLI
output, config loading, RSA/PSS crypto compatibility) against the real
Python interpreter in `../venv`.

`cmd/pwnagotchi/main.go` now tries the real rendering pipeline
(`internal/ui/display.Display`, backed by `internal/ui/view`) first,
falling back to **headless** operation only when hardware `Initialize()`
fails: `internal/cli.HeadlessView` is a real, log-based implementation of
every view interface the daemon needs (not a stub — it logs the same
flavor text/state a real display would show, via `internal/voice`). Real
plugins are loaded too: `plugins.load(config)` runs the genuine,
unmodified Python plugin loader inside a real Python subprocess
(`internal/pyplugin`), falling back to a no-op event emitter only if no
working Python/pwnagotchi install is found (never fatal to the daemon,
matching Python's own top-level try/except in `plugins.load`).

## UI subsystem: foundational pieces landed

`internal/ui/{faces,fonts,state,hw}` are Ported (real DejaVu TTF embedding
and rasterization verified, real `State` change/listener semantics
verified, and — notably — **every one of the ~94 real hardware display
drivers is now registered** by its exact config type string, resolving to
either the fully-real `DummyDisplay` or a typed `ErrUnsupportedDisplay`
that reports the driver's real Python class name). An exhaustive test
cross-checks every possible `NormalizeDisplayType()` output against the
registry so no config-valid type string can fail to resolve to *something*.

While porting the registry, a genuine upstream Python bug was found and
documented: `"weact2in9"` normalizes successfully in `utils.load_config`
but has no matching branch in `display_for()` at all (confirmed by reading
the full ~94-branch `elif` chain — no trailing `else`, no dead code path
that catches it) — real Python silently returns `None` there and crashes
later with `AttributeError`. Surfaced as `hw.ErrNoDriverInPythonEither`,
a named case, rather than either replicating the crash or masking it.

## Not yet ported (see feature-matrix.md for the authoritative list)

- **Update: `ui/components.py`, `ui/view.py`, and `ui/display.py` are
  actually Ported** (`internal/ui/components`, `internal/ui/view`,
  `internal/ui/display`; a previous revision of this report was stale on
  this point — see feature-matrix.md, corrected). `cmd/pwnagotchi/main.go`
  tries the real rendering pipeline (`hw.NewDriver` → `view.New` → resolved
  driver's `Initialize`/`Render`) first, falling back to
  `internal/cli.HeadlessView` only when hardware `Initialize()` fails (no
  physical display on this machine, or `ui.display.enabled=false`).
- The ~92 real SPI/I2C/GPIO hardware driver *implementations* themselves
  (the registry/interface for all of them is done — see above) — each
  needs real hardware to verify against, per the porting goal's ban on
  fake success. This dev/test rig has no physical e-ink/OLED display, only
  the `DummyDisplay` driver has been verified as real (it needs none).
- **Update: `ui/web/*` is Ported.** `internal/web` is a real `net/http`
  server: real embedded static assets + `html/template`-converted
  templates (copied/converted from the real Jinja templates and static
  files, not referenced from the Python install), real HTTP Basic auth
  (constant-time compare), a real CSRF defense (double-submit-cookie, not
  Flask-WTF-identical but not weaker — see known-differences.md), real
  CORS, and every route wired to real backing data: `/ui` serves the real
  rendered device frame (`internal/ui/view.View.OnRender` now calls
  `web.UpdateFrame` on every real render, exactly mirroring `view.py`'s
  own `web.update_frame(self._canvas)` call), the inbox family calls the
  already-ported real `internal/grid` client, and the plugins family calls
  the real `internal/pyplugin` bridge (list/toggle/webhook — see below).
  `/shutdown`, `/reboot`, `/restart` are wired to the real, already-ported
  `internal/unit` functions via an injected `Actions` interface — real and
  dangerous in production, deliberately never exercised against the real
  Runner in this session's own testing (see the reboot incident note
  above). Verified via `internal/web`'s own unit tests, including
  `TestRealHTTPRoundTrip`, which drives the exact production handler stack
  through a REAL TCP listener (`httptest.NewServer`, not just
  `httptest.NewRecorder`) with a real `net/http.Client` and a real
  `http.CookieJar` carrying the real csrf cookie across requests exactly
  like a browser would — real socket, real HTTP framing, real embedded
  static asset served, real CSRF-gated POST invoking the real action after
  a real round trip. This is the strongest verification available without
  a way to drive an actual browser in this environment (no display/X
  server here either).
- **Update: the runtime plugin loader/event-dispatch engine is Ported.**
  `internal/plugins/loader.go` + `internal/pyplugin` (`bridge.go` +
  embedded `bridge.py`) run the REAL, unmodified
  `pwnagotchi.plugins.load(config)`/`plugins.on(...)` inside a real Python
  subprocess — genuine `importlib`-loaded plugin classes, genuine
  per-plugin serial worker-thread queue, not a reimplementation.
  `tests/compat_pyplugin_test.go` now exercises four real bundled plugins,
  each proving a different real property, not just one happy path:
  - `TestCompatPyPluginBridgeRunsRealCachePlugin`: `cache.py` loads,
    real `on_config_changed`/`on_wifi_update` handlers run, and it writes
    a real `.apcache` file to disk.
  - `TestCompatPyPluginBridgeListAndToggle`: `logtail.py` is really
    loaded/unloaded at runtime via the real `plugins.toggle_plugin`.
  - `TestCompatPyPluginBridgeWebhook`: `logtail.py`'s real `on_webhook`
    renders real HTML through the real `base.html` template via a genuine
    `flask.Request`.
  - `TestCompatPyPluginBridgeGracefullyHandlesUnsupportedHardwareAndStubs`:
    `gpio_buttons.py` (real upstream graceful degradation when RPi.GPIO
    isn't importable — this rig isn't a Raspberry Pi) and `memtemp.py`
    (whose real `on_ui_setup` calls `ui.is_waveshare_v2()` on its `ui`
    argument in the realistic default-config case — a real
    `NotImplementedError` from the stub, caught and logged by real
    Python's own `process_events`, never taking the bridge down; verified
    by successfully calling `ListPlugins` again immediately afterward).

  The remaining ~19 bundled plugins have not been individually exercised;
  the mechanism all of them share (real `importlib` loading, real
  `plugins.on()` dispatch, the stub/JSON-arg translation) is now verified
  against four plugins spanning the realistic range of behavior (pure
  data, runtime toggle, real webhook rendering, and hardware-stub
  failure), not just one. Known limitation: `agent`/`view`/`display`
  arguments passed to plugin callbacks become an inert stub across the
  process boundary (see docs/known-differences.md) rather than a live RPC
  proxy — a plugin that only touches JSON-safe data (like `cache.py`) is
  unaffected; the other 22 bundled plugins have not been individually
  tested yet, only the mechanism they all share has. `cmd/pwnagotchi/main.go`
  now wires this in place of the no-op emitter at startup. Extended with a
  synchronous "call" protocol (`list_plugins`/`toggle_plugin`/`webhook`)
  for `internal/web`'s `/plugins` routes — see known-differences.md.
- **Update: `utils.py`'s `md5`/`download_file`/`unzip` are actually
  Ported** (`internal/config/fileutil.go`; a previous revision of this
  report incorrectly bundled them with the still-unported
  `extract_from_pcap` — corrected). `extract_from_pcap` itself is
  genuinely not ported, but evidence-based investigation found this is
  correct, not a gap: its only two callers in the whole codebase
  (`plugins/default/grid.py`, `plugins/default/wigle.py`) are themselves
  real bundled Python plugins already executing inside the real
  `internal/pyplugin` bridge, so they already call the real Python
  `extract_from_pcap` directly — no Go implementation is exercised by
  anything, so none is needed.

## Security posture so far

- `internal/fs`, `internal/identity`, `internal/grid`, `internal/unit`,
  `internal/config` (`IfaceChannels`), and `internal/plugins` (`edit`'s
  `$EDITOR` invocation) all use `os/exec` with explicit argv for every
  external command — no shell string interpolation anywhere, closing the
  injection surface Python's `os.system(f"...")`/`subprocess.getoutput("...")`
  calls have (none exploitable in practice, since all arguments are fixed
  literals or validated, but the Go port removes even the theoretical
  surface).
- `internal/identity`'s RSA-PSS signing and fingerprint computation are
  verified byte-for-byte/interoperability-tested against real pycryptodome.
- `internal/bettercap`'s websocket client sends Basic-auth credentials as a
  real `Authorization` header (gorilla/websocket rejects URL userinfo
  outright) — same wire-level credentials, no downgrade.
- No secrets are logged anywhere in the code written so far.
- File permissions: `internal/fs.EnsureWrite` matches Python's real
  (0600-after-replace) behavior. No permission has been widened relative
  to Python.
- `cmd/pwnagotchi` faithfully preserves `pwnagotchi.set_name`'s real
  behavior (rewriting `/etc/hostname`/`/etc/hosts` and **rebooting the
  host** if the configured name differs from the current hostname) — this
  is pre-existing Python behavior, not something introduced by the port,
  but it means running the Go binary for real on a machine whose hostname
  doesn't match `main.name` in its config will trigger a real reboot, same
  as Python would. Documented here so it isn't a surprise during testing.

## Incident: manual smoke-testing caused real host reboots

While smoke-testing the compiled `cmd/pwnagotchi` binary by hand (not via
`go test`), invoking it with `--clear` on this dev host triggered a real
reboot, more than once in succession. Root cause, fully diagnosed: this
host's `/etc/hostname` (`pwnagotchi-dev`) differs from `main.name` in the
loaded config (`pwnagotchi`). `main.go`'s `run()` calls
`unit.SetName(name, unit.DefaultRunner, ...)` **unconditionally, before**
the `--clear`/`args.DoClear` check — matching real `cli.py`'s own literal
ordering exactly (`pwnagotchi.set_name(...)` runs before the `if
args.clear:` branch there too). `SetName` detects the mismatch, rewrites
`/etc/hostname`/`/etc/hosts`, and calls `Reboot()` against the real
`unit.DefaultRunner`, which shells out to `shutdown -r now` — a real,
unmocked reboot. This is faithful, intentional behavior (Python does
exactly this), not a Go-port bug, so the fix is **not** to change
`main.go`'s ordering or `SetName`'s behavior.

The actual fix is to testing practice: the compiled binary must never
again be invoked directly (with any flag that reaches past config
loading) on a host where hostname/`main.name` might mismatch, since doing
so — in Go exactly as in Python — reboots the machine. `tests/
live_hardware_test.go` is scoped deliberately narrowly (read-only
`IfaceChannels`/`Session()` queries only) specifically to make this
mistake structurally impossible in the live-hardware suite going forward.

## Session update: UI rendering fix + full plugin compatibility inventory

**This section is current; several claims elsewhere in this report
(notably outstanding-risk #1 below, which is now stale — the UI/web/plugin
layers it describes as "Python-only" have since been built and verified)
predate this session and a large prior "make the Go port primary"
consolidation.** Full details in `docs/rendering-investigation.md` and
`docs/plugin-compatibility-matrix.md`; summarized here:

- **Fixed a real UI text-rendering bug.** `internal/ui/components.Text`'s
  multi-line wrapped-text draw path advanced each line by the font's
  design line-height metric instead of reproducing Pillow's actual
  line-pitch formula (glyph-bbox of `"A"` + a hardcoded 4px default
  `spacing`) — wrapped `status` text (the widest, most frequently-used
  wrapped widget) visibly overlapped/garbled past its first line. This was
  the literal cause of the "corrupted Go UI text" report. Fixed via
  `pilLineSpacing()`; regression-tested against a real-Python-rendered
  golden PNG in the new `tests/visual/` package (byte-identical
  reproducibility of the golden verified via `tests/visual/oracle.py`
  under the real venv). Residual ~4.5% pixel-level divergence is
  documented, measured, unavoidable rasterizer-hinting noise (FreeType vs.
  `golang.org/x/image/font`), not a functional regression.
- **Inventoried and tested every one of the 23 bundled Python plugins**
  through the real `internal/pyplugin` subprocess bridge (`cache`,
  `logtail`, `gpio_buttons`, `memtemp` already had dedicated tests;
  `pisugarx` and the remaining 17 gained new tests this session in
  `tests/plugins/`). Full per-plugin hooks/events/config/deps/verification
  table in `docs/plugin-compatibility-matrix.md`.
- **Found and fixed a real bridge bug**: `pisugarx.py`'s `on_loaded` reads
  the `pwnagotchi.config` module global directly, which `bridge.py` never
  set (real `cli.py` does, before calling `plugins.load()`), so it always
  raised a real `TypeError`, silently caught and logged — the plugin
  looked "loaded" but its real startup logic never ran. Fixed with a
  deliberately load-window-scoped fix (not left set permanently, to avoid
  real `plugins.toggle_plugin` writing to the hardcoded
  `/etc/pwnagotchi/config.toml` system path on every toggle, including
  from automated tests) — see `known-differences.md`.
- Two genuine, pre-existing, upstream Python fragilities were found (NOT
  go-port bugs — same failure would occur under real, unmodified Python
  with no matching hardware attached): `ups_lite.py` and `wittypi.py` both
  call `smbus.SMBus(1)` inside `on_loaded` with no availability guard
  (unlike their own `RPi.GPIO` imports, which both correctly wrap in
  `try/except`), so both raise `FileNotFoundError` on any machine without
  real I2C hardware. Documented, not silently worked around.

## Outstanding risks / TODO before this can be called "done"

1. ~~The entire UI/web/plugin-loader layer is still Python-only.~~ **Stale
   as of this session** — `internal/web` (a real HTTP server with
   templates/routes/CSRF), `internal/ui/view`+`internal/ui/hw` (real
   rendering, verified pixel-accurate against Python — see above), and
   `internal/pyplugin` (a real Python plugin bridge, all 23 bundled
   plugins verified loading) all exist and are tested today. Remaining
   real gaps in this area are narrower and itemized in
   `docs/known-differences.md` (proxy-stub limitation for
   `agent`/`view`/`display` arguments passed to plugin handlers, streaming
   webhook capture limit, `pwnagotchi.config` global's load-window-only
   scope) and `docs/plugin-compatibility-matrix.md` (network/hardware-gated
   plugin behaviors correctly left unexercised by automated tests).
2. Logging format consistency: `internal/logging.Logger` produces
   Python's exact `[asctime] [LEVELNAME] [threadName] : message` format,
   but every other package built so far still logs via Go's plain stdlib
   `log` package (different wrapper format, same message content). Wiring
   all of them to a shared `internal/logging.Logger` instance is
   outstanding.
3. **Update:** this environment does in fact have real hardware — a
   MediaTek MT7612U USB Wi-Fi adapter (`wlan0`) and a real running
   `bettercap` instance (`api.rest` on `127.0.0.1:8081`) — and the lab's
   `casa`/`familia` Wi-Fi networks are confirmed to be simulated (no real
   clients), so they're a safe live-fire target for recon/associate/deauth
   testing. `tests/live_hardware_test.go` (`-tags=live`, never part of `go
   test ./...` or `make compatibility-test` — deliberate, human-run only)
   now verifies `internal/config.IfaceChannels` against the real adapter
   (39 real channels returned) and `internal/bettercap.Client.Session()`
   against the real bettercap instance (`version: 2.41.7`, real `wifi`
   sub-session present). No `ui/hw` driver has real display hardware to
   verify against yet (none is present in this rig), and the live suite
   deliberately does **not** exercise `internal/unit.SetName`/`Restart`/
   `Reboot` or `internal/agent`'s automata-driven restart path — see the
   incident note below.
4. `Agent._fetch_stats`'s per-step exception-message granularity, and
   `do_auto_mode`'s "wifi.interface not set" specific recovery branch,
   aren't reproduced exactly (see known-differences.md) — deliberate,
   low-impact simplifications given Go's error-handling shape, not
   oversights.
5. `cmd/pwnagotchi`'s manual/auto-mode main loops (`RunManualMode`/
   `RunAutoMode` in `internal/cli/run.go`) are implemented and build, but
   have not been run end-to-end against a live bettercap instance (see #3)
   — only their constituent `internal/agent` methods are unit-tested.

## Session update: fixed a real, pre-existing go.mod bug found via CI

Setting up `.github/workflows/build-pi-image.yml` (a Pi Zero 2 W deployment
pipeline, see `go-port/deploy/README.md`) surfaced a real, latent bug that
predates this session: `go-port/go.mod` declared `go 1.19`, but its actual
resolved dependencies (`golang.org/x/text` v0.40.0, specifically) require
`go 1.25` to compile — this had been silently masked because the dev
environment's own locally-installed Go (1.19.8) predates Go's "go
directive" enforcement mechanism entirely (added in later Go releases) and
just compiled the newer syntax anyway rather than refusing. GitHub
Actions' `actions/setup-go`, using a real, current Go toolchain, correctly
enforced it and failed the build. Fixed: bumped `go.mod` to `go 1.25.0`
(the real minimum satisfying the whole dependency graph, confirmed via
`go list -m -f '{{.Path}} {{.Version}} go{{.GoVersion}}' all`), which also
surfaced one new `go vet` finding under the newer toolchain's printf
analysis (`internal/voice.Voice.t`'s call to `gotext.Mo.Get` — a real
false positive, since `gotext.FormatString` special-cases zero variadic
args to skip `fmt.Sprintf` entirely; fixed by breaking vet's call-shape
pattern match rather than changing behavior). Full test suite (including
`-race` and `make compatibility-test`) reverified passing under the new
Go 1.25 toolchain.

## Session update: onboard WiFi monitor mode (nexmon) — real hardware findings

Deployed and SSH-tested the built image on real Pi Zero 2 W hardware for
the first time this session (prior sessions only got as far as a
successful CI build, never a real boot). Found and fixed three real,
independent bugs blocking monitor mode, none of them hypothetical:

1. `pwnlib`'s `reload_brcm` always failed with "Module brcmfmac is in
   use" — this kernel splits the onboard chip's driver into `brcmfmac` +
   a dependent companion module (`brcmfmac_cyw`), and removing a module
   never cascades to remove its dependents first.
2. NetworkManager manages `wlan0` by default and holds it even while
   disconnected, and a separate standalone `wpa_supplicant.service` held
   it too — either alone blocked the reload regardless of fix #1.
3. The onboard chip's stock (non-nexmon) firmware genuinely cannot
   create a monitor interface at all — confirmed directly via `iw phy
   info` on the real device, not assumed: its supported interface modes
   list has no `monitor` entry. This is the exact, previously-disclosed
   nexmon gap in `go-port/deploy/README.md`, now confirmed empirically
   rather than theoretically.

Porting the original (pre-Go-port) project's own nexmon install (deleted
from this repo's history, recovered via `git show a15ae8fc`) then
surfaced a fourth, real, evidence-backed finding:
`brcmfmac-nexmon-dkms` fails to compile against trixie's default kernel
(a real `cfg80211`/timer API mismatch, not a config problem) because
Kali's own package was specifically validated against the 6.12 kernel
line, not whatever much newer kernel trixie currently ships. Fixed by
pinning the kernel to Raspberry Pi Foundation's own `bookworm` suite
(still actively maintained, confirmed live) at build time — see
`go-port/docs/kernel-nexmon-compatibility.md` for the full evidence
trail and `go-port/deploy/pi-gen-stage/05a-pin-kernel/`. Not yet
confirmed working end-to-end on real hardware as of this note; the
Go port itself is unaffected by any of this — it's entirely
deployment-image/kernel-driver territory, not application code.

This report will be rewritten (not just appended to) once the port reaches
a state where "done" per the porting goal's own definition is a defensible
claim.
