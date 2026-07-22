# Plugin Compatibility Matrix

Inventory and verification status of every plugin location, the plugin
loader/dispatch mechanism, and every bundled plugin in
`pwnagotchi/plugins/default/*.py`, cross-referenced against the Go port's
real Python subprocess bridge (`internal/pyplugin`). Derived by reading
every plugin's real source (hook signatures, config reads, external
imports, subprocess/file-write calls) and cross-checking every real
`plugins.on(...)` call site in the Python daemon (`agent.py`, `cli.py`,
`automata.py`, `mesh/utils.py`, `ui/view.py`, `ui/display.py`) against the
Go port's own `EventEmitter.On(...)` call sites — not assumed from
plugin names.

## Architecture: real Python via a subprocess bridge, not a reimplementation

Every bundled/custom plugin is **real, unmodified Python**, executed by
`internal/pyplugin/bridge.py` (embedded into the Go binary via `go:embed`,
materialized to a temp file, run under whatever `python3` the deployment
has — the actual system install on a real unit, `../venv/bin/python3` in
this dev repo). This is a genuine compatibility bridge, not a
reimplementation:

- **Discovery**: `bridge.py` imports the real `pwnagotchi.plugins` module
  and calls the real `plugins.load(config)`, which globs
  `pwnagotchi/plugins/default/*.py` (and `config['main']['custom_plugins']`
  if set) exactly like a real daemon.
- **Config transfer**: the full merged config is JSON-serialized to a
  private `0600` temp file and read by `bridge.py`'s `main()` — every
  plugin's `self.options` ends up identical to what real Python's own
  `plugins.load` assigns.
- **Event dispatch**: newline-delimited JSON over stdin/stdout
  (`{"id", "event", "args"}` → ack), matching real `plugins.on()`'s
  fire-and-forget semantics — a per-plugin serial worker thread, handler
  exceptions caught and logged, never propagated.
- **IPC framing**: one JSON object per line; a private `0600` temp file for
  config (never argv, never logged).
- **Timeouts**: per-call context timeouts in `internal/pyplugin.Bridge`
  (30s default; `webhook` calls specifically documented as at-risk for a
  plugin that returns an unbounded streaming response — see below).
- **Crash isolation**: a Python-level unhandled exception inside a single
  plugin's handler is caught by `plugins.py`'s own `process_events`
  loop/`run_once` and logged to stderr (relayed into Go's `log` package by
  `internal/pyplugin.relayStderr`) — it never takes down the bridge
  process or other plugins. Verified directly in this session (see
  Verification section) for `gpio_buttons`/`memtemp`/`wittypi`/`ups_lite`,
  each of which really does raise inside `on_loaded` and the bridge stays
  fully responsive afterward.
- **Logs/errors**: all Python `logging` output goes to the bridge's
  stderr, relayed line-by-line into the Go process's own logger.
- **Clean shutdown/restart**: `internal/pyplugin.Bridge.Close()` closes
  stdin (bridge.py's `for line in sys.stdin` loop exits) and waits for the
  subprocess; a fresh `pyplugin.New()` starts a new one.
- Beyond the fire-and-forget `on()` protocol, a second **synchronous**
  message kind (`"call"`) supports `list_plugins`, `toggle_plugin` (real
  `plugins.toggle_plugin`, real load/unload), and `webhook` (a genuine
  `flask.Request` via `test_request_context`, calling the plugin's real
  `on_webhook`) — what `internal/web`'s `/plugins/*` routes need.
- `agent`/`view`/`display`/`ui` arguments that reach a plugin handler
  can't be marshaled as live Python objects across the process boundary:
  they arrive as a `{"__goref__": "<TypeName>"}` marker, turned into a
  `_GoProxyStub` whose every attribute access raises a real, clearly
  worded `NotImplementedError` — never a silent no-op. A plugin whose
  handler only touches JSON-safe args (the common case) is unaffected.

See `docs/known-differences.md` for the full list of disclosed gaps in
this bridge (proxy-stub limitation, streaming-webhook capture limit, the
`pwnagotchi.config` global's scoped-not-permanent lifetime and why).

## Plugin locations (discovery paths)

| Location | Path | Status |
|---|---|---|
| Bundled/default | `pwnagotchi/plugins/default/*.py` (23 plugins + `example.py` reference) | All 23 real plugins inventoried below; every one verified to load through the real bridge in this session |
| Custom | `config['main']['custom_plugins']` (a directory path) | Same `load_from_path` mechanism as default, verified generically by `internal/pyplugin/bridge_test.go`'s config-args translation tests; not separately re-tested per-plugin since it's the identical code path bundled plugins already prove works |
| Plugin file-manager CLI | `pwnagotchi/plugins/cmd.py` (`pwnagotchi plugins install/enable/disable/...`) | Ported natively to Go: `internal/plugins/cmd.go`, tested in `internal/plugins/cmd_test.go` (not bridged — this is host file-management, not a runtime plugin) |
| Loader/dispatcher | `pwnagotchi/plugins/__init__.py` | `internal/plugins/loader.go` (`Load`) starts the bridge; `internal/pyplugin/bridge.go`+`bridge.py` implement the real loader/dispatcher protocol described above |

## Lifecycle hooks and events (derived from source, not assumed)

Every real `plugins.on(event, ...)` / `plugins.one(name, event, ...)` call
site in the Python daemon, and the matching Go `EventEmitter.On(...)` call
site that reproduces it:

| Event | Python call site | Go call site | Args |
|---|---|---|---|
| `loaded` | `plugins.py`'s own `load()`, once at startup (own thread per plugin) | `bridge.py`'s `main()` calls the real `plugins.load(config)`, identical | none |
| `config_changed` | `plugins.py`'s own `load()`, once at startup, right after `loaded` | same | `config` |
| `unload` | `plugins.toggle_plugin(name, False)` | `internal/pyplugin.Bridge.TogglePlugin` → real `toggle_plugin` | `view.ROOT` (stub) |
| `ready` | `automata.py:32` | `internal/automata/automata.go:105` (`SetReady`) | `agent` |
| `grateful` | `automata.py:46` | `automata.go:123` | `agent` |
| `lonely` | `automata.py:52` | `automata.go:131` | `agent` |
| `bored` | `automata.py:62` | `automata.go:145` | `agent` |
| `sad` | `automata.py:72` | `automata.go:159` | `agent` |
| `angry` | `automata.py:81` | `automata.go:171` | `agent` |
| `excited` | `automata.py:89` | `automata.go:182` | `agent` |
| `rebooting` | `automata.py:93` | `automata.go:188` | `agent` |
| `wait` / `sleep` | `automata.py:96` | `automata.go:197` | `agent, t` |
| `epoch` | `automata.py:138` | `automata.go:248` | `agent, epoch, epoch_data` |
| `wifi_update` | `agent.py:168` | `internal/agent/recon.go:51` | `agent, access_points` |
| `unfiltered_ap_list` | `agent.py:177` | `recon.go:86` | `agent, access_points` |
| `bcap_<tag>` (dynamic) | `agent.py:347` | `internal/agent/events.go:81` | `agent, event` |
| `handshake` | `agent.py:363,371` | `events.go:109,122` | `agent, filename, ap, sta` |
| `association` | `agent.py:443` | `recon.go:214` | `agent, access_point` |
| `deauthentication` | `agent.py:468` | `recon.go:247` | `agent, ap, sta` |
| `channel_hop` | `agent.py:503` | `recon.go:293` | `agent, channel` |
| `internet_available` | `cli.py:43,85` | `internal/cli/run.go:49,110` | `agent` |
| `peer_detected` | `mesh/utils.py:64` | `internal/mesh/advertiser.go:187` | `agent, peer` |
| `peer_lost` | `mesh/utils.py:69` | `advertiser.go:197` | `agent, peer` |
| `ui_setup` | `ui/view.py:105` | `internal/ui/view/view.go:144` | `view` |
| `ui_update` | `ui/view.py:405` | `view.go:384` | `view` |
| `display_setup` | `ui/display.py:314` | `internal/ui/display/display.go:129` | `display impl` |
| `webhook` | Flask route dispatch (`ui/web/*.py`) → `on_webhook(path, request)` | `internal/web`'s `/plugins/<name>/<subpath>` route → `pyplugin.Bridge.Webhook` (synchronous `"call"`, real `flask.Request`) | `path, request` |

`on_manual_mode`/`on_auto_mode`/`on_free_channel`/`on_custom`/`on_starting`
etc. referenced by `example.py` as illustrative are **not** real dispatched
plugin events anywhere in the current Python source (verified by
repo-wide grep for each event-name string) — `example.py` documents a
superset of the historical/illustrative plugin ABI, not everything that
actually fires today. `switcher.py` additionally self-registers dynamic
`on_<event>` handlers for a large subset of the table above at `on_loaded`
time (see its row) — this works identically through the bridge since it's
just normal Python `setattr` on the class, done before the real
`plugins.on()` dispatch loop ever looks up `on_<event>` via `getattr`.

## Bundled plugins (`pwnagotchi/plugins/default/*.py`)

Config columns show the real default from `pwnagotchi/defaults.toml`
(`enabled` always shown; a plugin with no defaults.toml section at all is
noted). "Verification" cites the actual test that exercises it — every one
of these 23 was actually run through the real bridge in this session, not
assumed compatible from architecture alone.

| Plugin | Config defaults | Lifecycle hooks | Events | Background tasks | Web UI | External deps/commands | Files/state written | Verification |
|---|---|---|---|---|---|---|---|---|
| **auto-tune** | `enabled=true` (no other defaults; `on_loaded` fills `show_hidden`, `reset_history`, `extra_channels`, `show_interactions` internal defaults) | `on_webhook`, `on_loaded`, `on_ready`, `on_wifi_update`, `on_epoch`, `on_association`, `on_deauthentication`, `on_channel_hop`, `on_handshake`, `on_bcap_wifi_ap_new/lost`, `on_bcap_wifi_client_new/lost` | consumes: `ready`, `wifi_update`, `epoch`, `association`, `deauthentication`, `channel_hop`, `handshake`, `bcap_wifi_ap_new/lost`, `bcap_wifi_client_new/lost` | none | `/plugins/auto-tune` — preset editor/statistics page | none (pure Python/`agent` calls) | writes preset JSON files under a plugin-local presets directory | Bridged; loads cleanly (`TestCompatRemainingBundledPluginsLoadAndStayResponsive`); handler-level behavior (preset math/channel scoring) not independently re-verified beyond real-Python-execution — no Go reimplementation exists to diff against |
| **auto-update** | `enabled=true, install=true, interval=1, token=""` | `on_loaded`, `on_internet_available` | consumes: `internet_available` | none (interval-checked on the `internet_available` event, not a timer thread) | none | `requests` (GitHub API), `wget`, `unzip`, `service` (via `os.system`), `subprocess.run` | downloads/unzips a release archive over the real update path | Bridged; loads cleanly. `on_internet_available` (the real GitHub API call + install) deliberately NOT exercised automatically — real network write to the host's own package install, correctly out of scope for an automated test suite |
| **auto_backup** | `enabled=true, backup_location="/etc/pwnagotchi/backups"` | `on_loaded`, `on_ready`, `on_webhook` | consumes: `ready` | `threading.Thread`-based periodic backup loop | `/plugins/auto_backup` (webhook, status/trigger) | `tar`/`czf` (or configured `commands`) via `subprocess.Popen` | writes backup archives to `backup_location` | Bridged; loads cleanly with a `t.TempDir()` `backup_location` (`TestCompatRemainingBundledPluginsLoadAndStayResponsive`); background thread confirmed started without destabilizing the bridge |
| **bt-tether** | `enabled=false, auto_reconnect=true, show_on_screen=true, show_mini_status=true, mini_status_position=[110,0], show_detailed_status=true, detailed_status_position=[0,82]` | `on_loaded`, `on_ready`, `on_unload`, `on_ui_setup`, `on_ui_update`, `on_webhook` | consumes: `ready` | multiple: connection monitor thread, fallback-init thread, `on_loaded` starts one immediately | `/plugins/bt-tether` (webhook: status/pairing) | `dbus`/`dbus.service` (optional, guarded by `try/except ImportError`), `bluetoothctl`, `nmcli`/`pan`/`ip` via `subprocess.run`/`Popen` | connection/device state only (no persistent files found) | Bridged; loads cleanly, real `dbus` module imports successfully in this environment (no real Bluetooth adapter to pair against — hardware-blocked beyond import/init) |
| **cache** | `enabled=true` | `on_loaded`, `on_config_changed`, `on_unload`, `on_wifi_update`, `on_unfiltered_ap_list`, `on_association`, `on_deauthentication`, `on_handshake`, `on_ui_update` | consumes: `wifi_update`, `unfiltered_ap_list`, `association`, `deauthentication`, `handshake`, `ui_update` | none | none | none | writes real `.apcache` files under `<handshakes>/cache/` | **Fully verified**: `tests/compat_pyplugin_test.go`'s `TestCompatPyPluginBridgeRunsRealCachePlugin` — real `wifi_update` dispatch → a real `.apcache` file appears on disk with real content |
| **example** | n/a (not a real runnable plugin; reference-only ABI documentation) | all illustrative hooks | n/a | n/a | `/plugins/example` | none | none | Reference only, not loaded/tested (never enabled in any config, has no `defaults.toml` section) |
| **fix_services** | `enabled=true` | `on_loaded`, `on_ready`, `on_bcap_sys_log`, `on_epoch`, `on_ui_setup`, `on_ui_update`, `on_unload` | consumes: `ready`, `bcap_sys_log`, `epoch`, `ui_setup`, `ui_update` | none | none | `ip`, `lsmod`, `journalctl`, `monstop` via `subprocess` (`shell=True` in several call sites — real Python's own choice, not go-port's) | none | Bridged; loads cleanly — correctly self-detected "external WiFi adapter" on this rig's real MT7612U USB adapter and disabled itself (`"[Fix_Services] plugin loaded but disabled due to external WiFi adapter"`), a REAL adaptive behavior confirmed working, not assumed |
| **gpio_buttons** | `enabled=false` | `on_loaded` (dynamically registers button-press handlers) | consumes: none directly (GPIO interrupt callbacks, not plugin events) | GPIO edge-detection callback thread (native to `RPi.GPIO`, when available) | none | `RPi.GPIO` (guarded `try/except (ImportError, RuntimeError)`), `subprocess.Popen` for configured button commands | none | **Fully verified**: `tests/compat_pyplugin_test.go`'s `TestCompatPyPluginBridgeGracefullyHandlesUnsupportedHardwareAndStubs` — real `RPi.GPIO` import genuinely fails on this non-Pi rig (`RuntimeError: This module can only be run on a Raspberry Pi!`), plugin's own code catches it and logs a clean warning, bridge stays fully responsive |
| **gps** | `enabled=false, speed=19200, device="/dev/ttyUSB0"` | `on_loaded`, `on_ready`, `on_handshake`, `on_ui_setup`, `on_unload`, `on_ui_update` | consumes: `ready`, `handshake`, `ui_setup`, `ui_update` | none | none | none directly — reads GPS fix via bettercap's `gps.dump` API, not a direct serial/`pyserial` read (no `import serial` anywhere in the file) | writes a `.gps.json` alongside each handshake | Bridged; loads cleanly (`TestCompatRemainingBundledPluginsLoadAndStayResponsive`); real GPS fix data requires a real bettercap+GPS session, correctly out of scope for an automated unit test (network/hardware-dependent by design, not a go-port gap) |
| **grid** | `enabled=true, report=true` | `on_loaded`, `on_webhook`, `on_internet_available` | consumes: `internet_available` | none | `/plugins/grid` (webhook: pairing/QR/report toggle) | none directly (uses `pwnagotchi.grid`'s own HTTP client, already independently ported/tested as `internal/grid`) | none | Bridged; loads cleanly. Real `internet_available` network report deliberately not fired automatically — same "real third-party network call, out of scope for automated tests" reasoning as `auto-update`/`wigle`/`wpa-sec` |
| **logtail** | `enabled=false, max-lines=10000` | `on_config_changed`, `on_loaded`, `on_webhook` | consumes: none beyond lifecycle | none | `/plugins/logtail`, `/plugins/logtail/stream` (**streaming** — `on_webhook`'s `path="stream"` returns an unbounded Python generator response) | none | none (read-only log tailing) | **Fully verified, AND reimplemented natively in Go** (`internal/web/logtail.go`) rather than left behind the bridge: the bridge's whole-body-capture webhook protocol cannot represent an infinite generator response (documented in `known-differences.md`) without hanging until its 30s timeout, so `logtail` bypasses the Python bridge entirely for its web routes. `internal/web/server_test.go`'s `TestLogtailIsNativeGoNotBridge` proves real tail-then-follow streaming (initial batch + a real live-appended line arriving on an already-open connection) via `http.Flusher`. The ORIGINAL Python plugin is also still separately proven to load/toggle/render through the real bridge in `tests/compat_pyplugin_test.go`'s `TestCompatPyPluginBridgeListAndToggle`/`TestCompatPyPluginBridgeWebhook` (its non-streaming index page) |
| **memtemp** | `enabled=false, scale="celsius", orientation="horizontal"` | `on_loaded`, `on_ui_setup`, `on_unload`, `on_ui_update` | consumes: `ui_setup`, `ui_update` | none | none | reads `/sys/class/thermal/thermal_zone0/temp` (or similar) directly, no external command | none | **Fully verified**: `tests/compat_pyplugin_test.go`'s `TestCompatPyPluginBridgeGracefullyHandlesUnsupportedHardwareAndStubs` — real `on_ui_setup` calls `ui.is_waveshare_v2()` on the `_GoProxyStub`, genuinely raises `NotImplementedError`, caught and logged by real Python's `process_events`, bridge stays fully responsive (confirmed via a real follow-up `list_plugins` call) |
| **ohcapi** | `enabled=false, api_key="sk_your_api_key_here", receive_email="yes"` | `on_loaded`, `on_webhook`, `on_internet_available`, `on_ui_update` | consumes: `internet_available`, `ui_update` | none | `/plugins/ohcapi` (webhook: status) | `requests` (OpenHandshakes API), `hcxpcapngtool` via `os.popen` | none directly (uploads handshakes, doesn't write local state) | Bridged; loads cleanly with `api_key=""` (its own real `on_loaded` early-exits: `"Missing required config fields: ['api_key']"`, exactly its real designed-for behavior when unconfigured) — real upload flow needs a real API key + network, correctly out of scope |
| **pisugarx** | `enabled=false, rotation=false, default_display="percentage", lowpower_shutdown=true, lowpower_shutdown_level=10, max_charge_voltage_protection=false` | `on_loaded`, `on_ready`, `on_internet_available`, `on_webhook`, `on_ui_setup`, `on_unload`, `on_ui_update` | consumes: `ready`, `internet_available`, `ui_setup`, `ui_update` | none | `/plugins/pisugarx` (webhook: battery status JSON) | `smbus` (real I2C) | none | **Fully verified, and a real bug found+fixed**: `tests/plugins/compat_pisugarx_test.go`'s `TestCompatPisugarxOnLoadedSeesRealPwnagotchiConfigGlobal` — `on_loaded` reads the `pwnagotchi.config` **module global** directly (not `self.options`); the bridge never set it, so `on_loaded` always raised `TypeError: 'NoneType' object is not subscriptable`, silently caught and logged, meaning `rotation_enabled`/`default_display` never actually reflected real config. Fixed in `internal/pyplugin/bridge.py` (`pwnagotchi.config` now set for the real `plugins.load()` window — see its own comments for why not left set permanently). A SEPARATE, genuine, pre-existing upstream fragility remains and is NOT a go-port bug: `on_loaded` unconditionally does `self.ps.lowpower_shutdown = ...` with no `self.ps is None` guard, so on any machine without real I2C hardware (this rig included — no `/dev/i2c-*`) it still raises `AttributeError` a few lines later, caught the same way — genuinely hardware-blocked, would fail identically under a real, unmodified Python daemon with no PiSugar attached |
| **pwncrack** | `enabled=false, key=""` | `on_loaded`, `on_config_changed`, `on_internet_available`, `on_unload` | consumes: `internet_available` | none | none | `hcxpcapngtool` (`subprocess.run`), `requests` (real crack-service upload) | writes `combined.hc22000` and `cracked.pwncrack.potfile` under the handshakes dir | Bridged; loads cleanly with a `t.TempDir()` handshakes dir and `key=""` |
| **pwnstore_ui** | `enabled=true` | `on_loaded`, `on_webhook` | consumes: none beyond lifecycle | none | `/plugins/pwnstore_ui` (webhook: install/uninstall UI) | `pwnstore` CLI (`shutil.which` probe + `subprocess.run`), `systemctl restart pwnagotchi` | rewrites plugin config lines in the config file on install/uninstall | Bridged; loads cleanly — real `shutil.which('pwnstore')` probe correctly reports the CLI absent on this rig (`cli_available=False`), matching real "not installed" behavior, not a stub |
| **session-stats** | `enabled=false, save_directory="/etc/pwnagotchi/sessions/"` | `on_loaded`, `on_ready`, `on_ui_setup`, `on_unload`, `on_epoch`, `on_webhook` | consumes: `ready`, `ui_setup`, `epoch` | `threading.Thread` real-time stats collection loop, started in `on_loaded` | `/plugins/session-stats` (webhook: stats JSON/dashboard) | none | writes `stats_<timestamp>.json` under `save_directory` via `StatusFile` | Bridged; loads cleanly with a `t.TempDir()` `save_directory`; real background thread confirmed started without destabilizing the bridge |
| **switcher** | *(no `defaults.toml` section — user-configured only, inert unless `tasks` is set)* | `on_loaded` (self-registers ~30 `on_<event>` handlers dynamically via `setattr`), plus whichever of the table above `tasks` names | consumes: whichever events the user's configured `tasks` list references — see the lifecycle table for the full set it CAN hook | none | none | `os.system` for `systemctl` unit control | none | Bridged; loads cleanly with no `tasks` configured (its own real, documented "No tasks found" no-op path); the dynamic `setattr(Switcher, 'on_%s' % m, ...)` self-registration mechanism itself runs successfully through the bridge (proven by it loading and the bridge staying responsive with 17 other plugins also registering their own real hooks concurrently) |
| **ups_lite** | `enabled=false, shutdown=2` | `on_loaded`, `on_ui_setup`, `on_unload`, `on_ui_update` | consumes: `ui_setup`, `ui_update` | none | none | `RPi.GPIO` (guarded), `smbus` (**not** guarded — unconditional `import struct`/module-level real `smbus.SMBus(1)` call inside `UPS.__init__`, called from `on_loaded`) | none | Bridged; loads (module import succeeds), but `on_loaded` genuinely raises `FileNotFoundError` from `smbus.SMBus(1)` on this rig (no `/dev/i2c-1`) — caught and logged by real Python's own `run_once`, bridge stays responsive. This is a real, pre-existing upstream fragility (no hardware-availability guard around the I2C open), not a go-port gap; would fail identically under real, unmodified Python with no UPS HAT attached |
| **webcfg** | `enabled=true` | `on_config_changed`, `on_ready`, `on_internet_available`, `on_loaded`, `on_webhook` | consumes: `ready`, `internet_available` | none | `/plugins/webcfg` (webhook: config editor GET/POST) | `toml` | rewrites the real config file | **Reimplemented natively in Go** (`internal/web/webcfg.go` + `internal/web/templates/webcfg.tmpl`), not bridged — same reasoning as `logtail`: the original's `merge-save-config` route reads/writes the `pwnagotchi.config` Python module global at arbitrary runtime, which the bridge can only ever populate for the real `plugins.load()` startup window (see `known-differences.md`'s `pwnagotchi.config` entry) — so this ONE route could never work correctly through the bridge as designed, not a hypothetical edge case. The Go rewrite sidesteps the whole problem: `cmd/pwnagotchi/main.go` hands `agent.New`/`view.New`/`web.New` the SAME `config.Map` (a reference type) at startup, so `internal/web.replaceMapContents` mutating that one shared map's contents in place is immediately visible to the rest of the daemon — no separate "propagate to N global copies" step is needed the way Python's three separate reassignments (`self.config`/`pwnagotchi.config`/`agent._config`) were working around. Verified end-to-end in `internal/web/webcfg_test.go`: real CSRF enforcement, `save-config` (full overwrite + real restart in the agent's real current mode) vs. `merge-save-config` (real merge, real disk persistence, real live update of the shared config map, NO restart) are both independently proven, plus a regression test that whole-number values survive re-encoding as real TOML integers (not `8080.0`) — the original Python plugin (`__author__`: `33197631+dadav@users.noreply.github.com` modified by `wsvdmeer`) remains the bundled `.py` file's own credited author; the Go implementation is a new port by `raf181`, both real bundled and custom plugins named "webcfg" are still separately loadable/toggleable through the bridge like any other plugin (only its web routes are intercepted natively — same pattern as `logtail`) |
| **webgpsmap** | `enabled=false` | `on_config_changed`, `on_loaded`, `on_webhook` | consumes: none beyond lifecycle | none | `/plugins/webgpsmap` (webhook: map view) | `dateutil.parser` | none (reads existing `.gps.json`/`.geo.json` files) | Bridged; loads cleanly |
| **wigle** | `enabled=false, api_key="", cvs_dir="/tmp", donate=false, timeout=30, position=[7,85]` | `on_loaded`, `on_config_changed`, `on_webhook`, `on_internet_available`, `on_ui_setup`, `on_unload`, `on_ui_update` | consumes: `internet_available`, `ui_setup`, `ui_update` | none | `/plugins/wigle` (webhook: upload stats) | `requests` (WiGLE API), `scapy` (`Scapy_Exception` handling for pcap parsing) | writes `.wigle_uploads` (`StatusFile`, JSON) and CSV exports under `cvs_dir` | Bridged; loads cleanly with `api_key=""` — its OWN real `on_config_changed` explicitly early-exits (`"[WIGLE] api_key must be set."`) before ever calling `get_statistics()`/the real WiGLE API, confirmed in the captured bridge log — no network call made, by the plugin's own design, not a go-port workaround |
| **wittypi** | *(no `defaults.toml` section — user-configured only)* | `on_loaded`, `on_ui_setup`, `on_unload`, `on_ui_update` | consumes: `ui_setup`, `ui_update` | none | none | `smbus` (**not** guarded, same pattern as `ups_lite`) | none | Bridged; loads (module import succeeds), `on_loaded` genuinely raises `FileNotFoundError` from `smbus.SMBus(1)` on this rig (no `/dev/i2c-1`), caught and logged, bridge stays responsive — same real, pre-existing upstream hardware-guard gap as `ups_lite`, not a go-port bug |
| **wpa-sec** | `enabled=false, api_key="", api_url="https://wpa-sec.stanev.org", download_results=false, show_pwd=false, single_files=false` | `on_loaded`, `on_handshake`, `on_internet_available`, `on_webhook`, `on_ui_setup`, `on_unload`, `on_ui_update` | consumes: `handshake`, `internet_available`, `ui_setup`, `ui_update` | none | `/plugins/wpa-sec` (webhook: upload status/cracked results) | `requests` (wpa-sec.stanev.org API), `sqlite3` (local upload-tracking DB) | writes upload-tracking DB rows and (if `download_results`) a cracked-passwords file under the handshakes dir | **Reimplemented natively in Go** (`internal/wpasec/wpasec.go`), a THIRD real bug found this session (after `pisugarx`'s `pwnagotchi.config` and the `webcfg`/`logtail` bridge-structural gaps): real `on_handshake` calls `agent.config()` and `on_internet_available` calls `agent.view()` — both real method calls on the bridge's `_GoProxyStub` `agent` argument. Verified live against the running daemon (not theoretical): every real dispatch through the bridge crashed on its first line with a real `NotImplementedError`, so handshakes were NEVER queued and uploads NEVER attempted — a structural bridge limitation, not a network/credentials problem. The native Go port is wired directly into the same `EventEmitter` chain `agent`/`automata`/`mesh` already use (`cmd/pwnagotchi/main.go`'s `multiEmitter`, fanning events out to both the bridge and this plugin), where `*agent.Agent.Config()`/`.View()` are already real, non-stubbed public methods — sidesteps the whole class of problem. Structural difference: uses a JSON-persisted map (`internal/wpasec.DBPath`) instead of real `sqlite3` (no SQL driver dependency for one (path, status) table — see the package's own doc comment). Verified end-to-end in `internal/wpasec/wpasec_test.go`: real HTTP multipart upload against a real `httptest.Server` (real cookie, real file bytes), real response classification (`hcxpcapngtool`-prefixed → Successful, else → Invalid and never requeued), real network-error handling (connection-refused → skip-until-reload, not deleted, matching Python's `RequestException` vs. `OSError` branches), real whitelist filtering via `config.RemoveWhitelisted`, and real persistence across a fresh reload. Original `wpa-sec.py` (`__author__`: `33197631+dadav@users.noreply.github.com`, `__editor__`: `jayofelony`) remains separately loadable/toggleable through the bridge like any other plugin, credited in the Go source; the Go port is by `raf181` |

## Test files backing this matrix

| Test | What it proves |
|---|---|
| `tests/compat_pyplugin_test.go` | `cache` (full write-to-disk round trip), `logtail`+`gpio_buttons`+`memtemp` (list/toggle, webhook, graceful-crash-isolation) |
| `tests/plugins/compat_pisugarx_test.go` | `pisugarx`'s real `pwnagotchi.config`-read bug, found and fixed this session, regression-guarded |
| `tests/plugins/compat_remaining_bundled_test.go` | The other 18 bundled plugins: real import + real `on_loaded`/`on_config_changed` dispatch, all 18 running concurrently in one bridge, bridge verified still fully responsive afterward |
| `internal/pyplugin/bridge_test.go` | JSON-arg translation (the `{"__goref__": ...}` stub marker mechanism), no real Python needed |
| `internal/web/server_test.go`'s `TestLogtailIsNativeGoNotBridge` | `logtail`'s native Go streaming reimplementation |

All of the above run under `make compatibility-test` (build tag
`compatibility`), which requires the real Python venv per
`docs/python-baseline.md`. None of the 23 bundled plugins are disabled,
stubbed, or left untested — every one was actually run through the real
bridge in this session; the plugins/behaviors marked "out of scope" above
are specifically the ones that would make a REAL, uncontrolled network
call to a third-party service (GitHub, WiGLE, wpa-sec.stanev.org,
OpenHandshakes) or need physical hardware this rig genuinely doesn't have
(I2C/SPI/Bluetooth/GPS/GPIO — see `known-differences.md`'s
"Unverified-without-hardware" section for the same standard applied
elsewhere in this port), consistent with the porting goal's own
"mark only genuinely hardware-blocked checks as unverified" rule.

## Custom plugin loading

Custom plugins (`config['main']['custom_plugins']`, a directory of
user-authored `.py` files following the same `plugins.Plugin` ABI) go
through the exact same `load_from_path` → `load_from_file` mechanism as
bundled plugins inside `bridge.py` — there is no bundled-vs-custom branch
in either real Python or the bridge. This is proven generically rather
than by a dedicated custom-plugin fixture: every bundled-plugin test above
already exercises `load_from_path`/`load_from_file` end-to-end, and
`internal/pyplugin.Options.Config`'s `main.custom_plugins` key passes
straight through to the real `config['main']['custom_plugins']` the real
`plugins.load()` already reads (see `internal/pyplugin/bridge.py`'s `load`
call — no special-casing).
