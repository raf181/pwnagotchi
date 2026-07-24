# Plugin Compatibility Matrix

Inventory and verification status of every bundled plugin, now that all 23
+ `example` are native Go implementations on `internal/pluginmanager`
(see `docs/plugin-development.md` for the plugin API itself and
`docs/migration-ledger.md` for how each one got here). The Python
subprocess bridge this document used to describe has been deleted
entirely — there is no fallback to real Python for any bundled plugin.

## Architecture: native Go plugins on `internal/pluginmanager`

Every bundled plugin is a real Go package under `internal/plugins/native/`
(three — `logtail`, `webcfg`, `wpa-sec` — live in `internal/web` and
`internal/wpasec` instead; see their rows below for why), registered with
`internal/pluginmanager.Manager` from `cmd/pwnagotchi/main.go`'s
`registerNativePlugins`:

- **Lifecycle**: `Manager.LoadAll` calls each registered plugin's `OnLoad`
  in dependency order if its `config['main']['plugins'][name].enabled` is
  true, then auto-delivers `loaded` then `config_changed` events —
  mirroring Python's `plugins.load()` firing the same two events in the
  same order. `OnUnload` runs on a runtime disable (web UI toggle) or
  daemon shutdown.
- **Event dispatch**: each loaded plugin gets its own bounded, serial event
  queue and goroutine (mirrors Python's one-`PluginEventQueue`-thread-per-
  plugin model). A panic inside a plugin's `HandleEvent` is recovered and
  counted (`Manager.List()`'s `Status.Panics`), never taking down another
  plugin or the daemon — verified by real, injected-panic tests in
  `internal/pluginmanager/manager_test.go`, not just claimed by design.
- **Capabilities**: `OnLoad` receives a typed `Capabilities` struct (narrow
  interfaces for Agent/View/Bettercap/Grid/State/Exec/HTTPClient/Clock/
  GPIO/I2C/SPI/Web/System/Log) instead of live `agent`/`view`/`display`
  Python objects — there is no proxy-stub/RPC-marshaling boundary at all
  anymore, because the plugin and the daemon are the same process. A
  plugin that calls `Capabilities.Agent.Config()`/`.View()` gets the real
  thing directly; the entire class of "argument becomes an inert stub
  across a process boundary" bugs this document used to track (see
  `docs/known-differences.md`'s old `wpa-sec`/bridge entries) cannot occur
  in the current architecture.
- **Webhooks**: `WebhookHandler.OnWebhook`/`RouteRegistrar.RegisterRoutes`
  receive a real `*http.Request` on the real `internal/web` mux, in-process
  — no whole-body-capture RPC step, so a plugin can stream an unbounded
  response (`internal/web/logtail.go`'s `/plugins/logtail/stream` does
  exactly this via a real `http.Flusher`).
- **Third-party/out-of-process plugins**: a separate system,
  `internal/pluginrpc`, exists for plugins distributed as separately
  compiled Go executables (versioned manifest, checksum verification,
  bounded newline-delimited-JSON RPC, crash/hang detection) — see
  `docs/plugin-development.md`'s "Registering your plugin" section.
  `pwnagotchi plugins install <name>` for a legacy Python `.py` plugin
  fails with a clear migration message (`internal/plugins/cmd.go`'s
  `legacyPythonPluginMessage`) rather than silently doing nothing.

## Event dispatch (daemon call sites, native plugin side)

Every event a native plugin's `HandleEvent(event string, args []interface{})`
can receive, and where the daemon fires it. Names match Python's `on_<event>`
minus the `on_` prefix (see `docs/plugin-development.md`).

| Event | Fired from | Args |
|---|---|---|
| `loaded` | `pluginmanager.Manager.LoadAll`, automatically right after `OnLoad` succeeds | none |
| `config_changed` | `pluginmanager.Manager.LoadAll`, automatically right after `loaded` | full `config.Map` |
| `ready` | `internal/automata` | `agent` |
| `grateful` / `lonely` / `bored` / `sad` / `angry` / `excited` / `rebooting` | `internal/automata` | `agent` |
| `wait` / `sleep` | `internal/automata` | `agent, t` |
| `epoch` | `internal/automata` | `agent, epoch, epoch_data` |
| `wifi_update` | `internal/agent/recon.go` | `agent, []agent.AP` |
| `unfiltered_ap_list` | `internal/agent/recon.go` | `agent, []interface{}` |
| `bcap_<tag>` (dynamic) | `internal/agent/events.go` | `agent, map[string]interface{}` (raw bettercap event) |
| `handshake` | `internal/agent/events.go` | `agent, filename, ap, sta` (ap/sta sometimes plain BSSID strings — see `docs/plugin-development.md`) |
| `association` | `internal/agent/recon.go` | `agent, ap` |
| `deauthentication` | `internal/agent/recon.go` | `agent, ap, sta` |
| `channel_hop` | `internal/agent/recon.go` | `agent, channel` |
| `internet_available` | `internal/cli/run.go` | `agent` |
| `peer_detected` / `peer_lost` | `internal/mesh/advertiser.go` | `agent, peer` |
| `ui_setup` | not a separate event — a native plugin adds its UI elements directly inside `OnLoad` (see `docs/plugin-development.md`) | n/a |
| `ui_update` | `internal/ui/view/view.go` | none (read `Capabilities.View`) |
| `display_setup` | `internal/ui/display/display.go` | n/a |
| webhook | HTTP request to `/plugins/<name>/<subpath>` | real `*http.Request`, in-process |

`switcher`'s Go port self-registers for a large subset of this table based
on its own `tasks` config, the same as Python's dynamic `setattr` mechanism
— see its row below.

## Bundled plugins (`internal/plugins/native/*` unless noted)

"Verification" is the plugin's own Go test file — every plugin below has
one; behavior is proven by real Go tests against injected fakes
(`CommandRunner`/`HTTPClient`/`Clock`/GPIO/I2C), not by re-running the
original Python.

| Plugin | Package | `enabled` default | Capabilities implemented | Verification |
|---|---|---|---|---|
| **auto-tune** | `autotune` | `true` | Loader, EventHandler, WebhookHandler | `auto_tune_test.go` (16 tests) |
| **auto_backup** | `autobackup` | `true` | Loader, EventHandler, WebhookHandler | `auto_backup_test.go` (13 tests) |
| **auto-update** | `autoupdate` | `true` | Loader, Unloader, EventHandler | `auto_update_test.go` (9 tests) |
| **bt-tether** | `bttether` | `false` | Loader, Unloader, EventHandler, WebhookHandler | `bt_tether_test.go` (21 tests) — real `bluetoothctl` argv discovery/pairing, a single targeted `dbus-send` NAP call, real `ip`/`dhclient` bring-up parsing; no live scan-progress streaming or interactive pairing-agent passkey confirmation (`Capabilities.Exec` is request/response, not a persistent session) |
| **cache** | `cache` | `true` | Loader, Unloader, EventHandler | `cache_test.go` (13 tests) — writes real `.apcache` files under `<handshakes>/cache/` |
| **example** | `example` | n/a (reference only, no `defaults.toml` section) | every optional interface, minimal logic | `example_test.go` (8 tests) — the "what does a plugin skeleton look like" reference cited by `docs/plugin-development.md` |
| **fix_services** | `fixservices` | `true` | Loader, EventHandler | `fix_services_test.go` (16 tests) |
| **gpio_buttons** | `gpiobuttons` | `false` | Loader, Unloader | `gpio_buttons_test.go` (8 tests) — real GPIO line capability, injectable in tests; `Capabilities.GPIO` is unbacked by a real Linux implementation on non-Pi hardware (see `docs/known-differences.md`'s bus-abstraction note) |
| **gps** | `gps` | `false` | Loader, Unloader, EventHandler | `gps_test.go` (10 tests) — writes `.gps.json` alongside each handshake; see `wigle`'s row for a disclosed cross-plugin gap (Accuracy/Updated fields) |
| **grid** | `gridplugin` (import alias; package `grid`) | `true` | Loader, EventHandler, WebhookHandler | `grid_test.go` (11 tests) — constructor-injected real `*grid.Client` + a `grid.SessionSummary` closure reading `agent.LastSession`, a documented divergence from the generic `Capabilities.GridCapability` shape (see the package's own doc comment) |
| **memtemp** | `memtemp` | `false` | Loader, Unloader, EventHandler | `memtemp_test.go` (10 tests) |
| **ohcapi** | `ohcapi` | `false` | Loader, EventHandler, WebhookHandler | `ohcapi_test.go` (12 tests) |
| **pisugarx** | `pisugarx` | `false` | Loader, Unloader, EventHandler, WebhookHandler | `pisugarx_test.go` (19 tests) — real I2C capability, unbacked without real hardware (same bus-abstraction caveat as `gpio_buttons`) |
| **pwncrack** | `pwncrack` | `false` | Loader, Unloader, EventHandler | `pwncrack_test.go` (6 tests) |
| **pwnstore_ui** | `pwnstoreui` | `true` | Loader, WebhookHandler | `pwnstore_ui_test.go` (12 tests) — real embedded store UI, real store-JSON fetch, real `config.toml` rewrite, real backgrounded `systemctl restart`; install/uninstall of a third-party store entry honestly returns `{"success": false}` rather than fabricating success (there is no bundled Python `pwnstore` installer to shell out to anymore) |
| **session-stats** | `sessionstats` | `false` | Loader, Unloader, EventHandler, WebhookHandler | `session_stats_test.go` (17 tests) |
| **switcher** | `switcher` | *(no `defaults.toml` section — user-configured only)* | Loader, EventHandler | `switcher_test.go` (6 tests) — real argv process execution (`Capabilities.Exec`) and real reboot (`Capabilities.System`) for its `commands`/task-scheduling behavior; `commands` is the one config field in this whole port that is deliberately treated as a shell program string, not argv (documented exception — see `docs/plugin-development.md`) |
| **ups_lite** | `upslite` | `false` | Loader, Unloader, EventHandler | `ups_lite_test.go` (9 tests) — real I2C capability, same hardware caveat as `pisugarx` |
| **webgpsmap** | `webgpsmap` | `false` | Loader, EventHandler, WebhookHandler, RouteRegistrar | `webgpsmap_test.go` (15 tests) |
| **wigle** | `wigle` | `false` | Loader, Unloader, EventHandler, WebhookHandler | `wigle_test.go` (17 tests) — uses `internal/wifiparse` (pure-Go PCAP/802.11 parsing) + `cache.ReadAPCache`; disclosed cross-plugin gap: `gps`'s `.gps.json` only carries Latitude/Longitude/Altitude today, not Accuracy/Updated, so `wigle` defaults missing Accuracy to 0 and missing/unparseable Updated to file mtime — a disclosed, non-security-relevant improvement over Python's uncaught `KeyError` on the same missing fields |
| **wittypi** | `wittypi` | *(no `defaults.toml` section — user-configured only)* | Loader, Unloader, EventHandler | `wittypi_test.go` (8 tests) — same I2C hardware caveat as `pisugarx`/`ups_lite` |

## Plugins reimplemented outside `internal/plugins/native` (structural reasons, not bridge limitations)

These three were pulled out of the generic bridge path early because their
own features needed something the bridge specifically couldn't do; now
that the bridge is gone entirely, they remain separate because their real
implementation genuinely lives closer to the subsystem they extend, not
because of any remaining limitation:

| Plugin | Where | `enabled` default | Verification | Why it's separate |
|---|---|---|---|---|
| **logtail** | `internal/web/logtail.go` | `false` | `internal/web/server_test.go` (`TestLogtailIsNativeGoNotBridge` and related) | Streaming tail-then-follow webhook (`/plugins/logtail/stream`) needs a real `http.Flusher` on the live request/response, which only `internal/web` itself can wire directly |
| **webcfg** | `internal/web/webcfg.go` | `true` | `internal/web/webcfg_test.go` (6 tests) | Its "merge and save without a restart" feature needs to mutate the *same* shared `config.Map` reference the rest of the daemon reads — `cmd/pwnagotchi/main.go` hands `agent.New`/`view.New`/`web.New` that one shared map at startup, so `internal/web.replaceMapContents` mutating it in place is immediately visible everywhere, with no separate propagate-to-N-copies step |
| **wpa-sec** | `internal/wpasec/wpasec.go` | `false` | `internal/wpasec/wpasec_test.go` (8 tests) | Wired directly into the same `EventEmitter` chain `agent`/`automata`/`mesh` already use, where `*agent.Agent.Config()`/`.View()` are real, non-stubbed methods; uses a JSON-persisted map instead of sqlite3 (no cgo/SQL-driver dependency for one `(path, status)` table), with a one-time `migrateLegacyDB()` import of any real prior Python install's `.wpa_sec_db` via the pure-Go, no-cgo `modernc.org/sqlite` driver — see `docs/known-differences.md` |

## Custom plugin loading

There is no bundled-vs-custom distinction for native plugins the way
Python had `load_from_path`/`load_from_file` — a native Go plugin must be
compiled into the daemon (see `docs/plugin-development.md`'s "Registering
your plugin" section) or distributed as a separately compiled, manifest-
described executable through `internal/pluginrpc`. There is no equivalent
of dropping a bare `.py` file into a custom-plugins directory and having it
picked up at next startup.
