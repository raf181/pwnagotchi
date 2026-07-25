# Writing a native Go plugin

This document describes how to write a **bundled** (compiled into the
daemon) native Go plugin for the Go-only Pwnagotchi port, replacing the
Python plugin API (`pwnagotchi.plugins.Plugin`) entirely for new plugin
development. It reflects `internal/pluginmanager` as actually implemented —
every interface and method named below is real, current code (see the
"Source of truth" list at the end if you want to verify something
yourself).

If you're looking for the plugin *distribution* story (installing a
third-party plugin without recompiling the daemon), see the "Registering
your plugin" section below — a manifest/checksum/RPC-based system now
exists at `internal/pluginrpc`.

## Why there's no more Python bridge for new plugins

Earlier in this migration, unported bundled plugins ran for real inside a
Python subprocess bridge (`internal/pyplugin`), so existing Python plugin
code kept working while the port caught up. That bridge — along with the
Python plugin loader it supervised — has since been removed entirely, once
all 23 bundled plugins + `example` were ported natively (see
`docs/migration-ledger.md`): it is not a channel for plugins at all
anymore, ported or otherwise. A plugin that only exists as a `.py` file
will not load, with no fallback — `pwnagotchi plugins install`/`enable`
reject a legacy Python plugin name with a message pointing back at this
document (see `internal/plugins/cmd.go`'s `legacyPythonPluginMessage`). If
you maintain an existing Python plugin, the path forward is to port it to
the shape described below — the reference plugins listed in "Learn by
example" were all migrated this same way and are a good starting point to
copy.

## The minimum: `Plugin`

Every native plugin implements exactly two methods:

```go
type Plugin interface {
    Name() string
    Metadata() Metadata
}
```

`Metadata` mirrors the class attributes a Python plugin used to expose:

```go
type Metadata struct {
    Version     string
    Author      string
    License     string
    Description string
    HasWebhook  bool // set true if you implement WebhookHandler (below)
}
```

That's it — a plugin with no background work and no web surface really is
a two-method struct. Everything else below is an *optional* interface the
plugin manager type-asserts for at load/unload/event/webhook time, so you
only implement what your plugin actually needs.

## Lifecycle: `Loader` and `Unloader`

```go
type Loader interface {
    OnLoad(Capabilities) error
}

type Unloader interface {
    OnUnload() error
}
```

`OnLoad` is where a plugin reads its config, stores whichever
`Capabilities` fields it needs, and sets up any real UI elements — see
"OnLoad already covers ui_setup" below. Returning an error marks the
plugin's load failed (logged, never fatal to the daemon — the same
tolerance real Python's `plugins.load()` has for one plugin raising in
`on_loaded`).

`OnUnload` is where you tear down whatever `OnLoad` set up: remove UI
elements you added, stop background goroutines, close files/sockets.
Called on a runtime disable (the web UI's plugin toggle) and on daemon
shutdown.

**There is no separate `unload` *event*.** Python dispatched
`on_unload(ui)` through the same generic event mechanism as everything
else; here it's its own lifecycle method with a clear call site
(`Manager.Unload`), not something you'd also see arrive through
`HandleEvent`.

## Events: one method, not twenty

```go
type EventHandler interface {
    HandleEvent(event string, args []interface{})
}
```

Python plugins implemented one `on_<event>` method per event
(`on_bored`, `on_sad`, `on_wifi_update`, ... — the bundled `example`
plugin alone had over twenty). A native Go plugin implements exactly one
method and switches on the event name:

```go
func (p *Plugin) HandleEvent(event string, args []interface{}) {
    switch event {
    case "ui_update":
        // ...
    case "handshake":
        // ...
    }
}
```

Leave a case out entirely for any event you don't care about — an
unhandled event is silently ignored, exactly like Python's
`getattr(plugin, f"on_{event}", None)` no-op fallback.

**The event name has no `on_` prefix.** Python's dispatch was
`getattr(plugin, f"on_{event}")`, so the *method* was named `on_wifi_update`
but the *event* was `wifi_update` — that's what `HandleEvent` receives
directly (`"wifi_update"`, not `"on_wifi_update"`). There's no prefix to
strip.

**Delivery is per-plugin, in-order, and isolated.** Each loaded plugin
gets its own bounded queue and goroutine: your events arrive in the order
they were emitted, a panic inside your `HandleEvent` is recovered and
counted (visible via `Manager.List`'s `Status.Panics`) without affecting
any other plugin, and a full queue drops the newest event for *your*
plugin only rather than blocking the rest of the daemon.

### Which events actually fire

| Event | When | Typical `args` |
|---|---|---|
| `loaded` | Automatically, right after your `OnLoad` returns successfully | none |
| `config_changed` | Automatically, immediately after `loaded` | `args[0]` is the **full** merged daemon `config.Map` — not just your own plugin's config sub-map. Use this if you need config outside your own `[main.plugins.<name>]` section (`cache` reads `config['bettercap']['handshakes']` this way) |
| `wifi_update` | The agent refreshed its (filtered) access-point list | `agent, []map[string]interface{}` (concrete `[]agent.AP` under the hood) |
| `unfiltered_ap_list` | The agent refreshed the pre-filter access-point list | `agent, []interface{}` (each element a `map[string]interface{}`) — note the different slice shape from `wifi_update` |
| `association` | The agent sent an association frame | `agent, map[string]interface{}` (the AP) |
| `deauthentication` | The agent deauthed a client | `agent, map[string]interface{} (AP), map[string]interface{} (station)` |
| `handshake` | A new handshake was captured | `agent, filename string, accessPoint, clientStation` — **`accessPoint`/`clientStation` are sometimes plain BSSID strings instead of maps** (when the agent couldn't match the BSSID to its current session), so type-assert defensively; see `internal/plugins/native/cache`'s `HandleEvent` for the pattern |
| `epoch` | One epoch of the main loop completed | implementation-specific epoch data |
| `internet_available` | Internet connectivity detected | `agent` |
| `ui_update` | The display is about to redraw | none required — read your own stored `Capabilities.View` |
| `ready` | Startup finished, main loop about to begin | `agent` |
| `bcap_<tag>` | A raw bettercap websocket event with tag `<tag>` (lowercased, sanitized to `[a-z0-9_]`) | `agent, map[string]interface{}` (the raw event) |
| everything else `example`'s Python ancestor demonstrated (`bored`, `sad`, `excited`, `lonely`, `rebooting`, `wait`, `sleep`, `channel_hop`, `free_channel`, `peer_detected`, `peer_lost`, ...) | corresponding daemon state change | see `internal/plugins/native/example` for the full no-op switch |

**`ui_setup` is not an event a native plugin needs to wait for.** Python
fired `on_ui_setup(ui)` once, right after `on_loaded`, before the first
render. A native plugin's `OnLoad` already only runs once real
`Agent`/`View` capabilities exist (see `cmd/pwnagotchi/main.go`'s
`capsFor` ordering — it's built after both the real agent and view/display
are constructed), which is the same "once, at startup, before first
render" timing. Just add your UI elements directly inside `OnLoad`.

## `Capabilities`: what your plugin can actually touch

`OnLoad` receives one `Capabilities` value. Every field is a narrow
interface — never the concrete `*agent.Agent`/`*view.View`/... type, and
never a bag of `interface{}` — so you only see the operations you're
meant to use, and tests can inject fakes instead of a real daemon.

| Field | Purpose |
|---|---|
| `Config` | Your plugin's own config sub-map (`config['main']['plugins'][name]`) — the same shape Python's `self.options` was |
| `Log` | A plugin-scoped logger (`Printf`), every line prefixed with your plugin's name. Always non-nil once handed to you |
| `Agent` | Run bettercap commands (`Run`), read session state (`Session`), and the small derived-accessor set (`IsModuleRunning`, `StartModule`, `RestartModule`) |
| `View` | Mutate on-screen state (`Set`), add/remove your own widgets (`AddText`, `AddLabeledValue`, `RemoveElement`, `HasElement`), show/clear the transient "uploading to X" mood (`OnUploading`, `OnNormal` — used by upload-handshakes-to-a-service plugins like `wigle`/`ohcapi`/`pwncrack`), and check the resolved display model (`Kind()` — a string, replacing Python's ~90 generated `is_waveshare_v2()`-style predicates with one comparison) |
| `Bettercap` | The real bettercap REST API client (`Request`), independent of `Agent.Run`'s fire-and-forget style |
| `Grid` | The pwngrid peer/API client (`Report`, `MemoryGet`, `MemorySet`) |
| `State` | A private, namespaced directory for persisted state (`Dir()`) — use this instead of hardcoding a path under `/etc/pwnagotchi/`; see `docs/migration-ledger.md`'s note on how `wpa-sec`'s legacy-DB migration became a real bug precisely because of a hardcoded path |
| `Exec` | Real argument-vector process execution (`Run(ctx, name, args...)`) — **never** call `os/exec` directly from a plugin; this is the injectable, testable, shell-injection-safe seam |
| `HTTPClient` | A `*http.Client` for any real outbound HTTP your plugin needs |
| `Clock` | An injectable time source (`Now()`) — use this instead of calling `time.Now()` directly if your plugin has scheduled/periodic behavior, so tests can control time |
| `GPIO` / `I2C` / `SPI` | Hardware bus capabilities for GPIO lines / I2C devices / SPI devices. **Not yet backed by a real Linux implementation** — the bus-abstraction work is a separate, not-yet-complete migration task. Code against these interfaces now; production wiring may hand you `nil` until that lands, so a plugin dereferencing one without a nil check gets a clear panic/error today, never a silently-faked success |
| `Web` | Register your own HTTP routes (`Handle`) or render a shared template (`Render`) directly on the real server mux, under `/plugins/<name>/...` |
| `System` | Trigger the same real, dangerous shutdown/reboot/restart the web UI's own routes use (`Shutdown`, `Reboot(mode)`, `Restart(mode)`) — `switcher`'s reboot-after-task behavior is the reference user |

`FontStyle` (used by `View.AddText`/`AddLabeledValue`) is a small enum —
`FontSmall`, `FontBold`, `FontBoldSmall`, `FontMedium`, `FontHuge`,
`FontBoldBig` — matching the font role names Python's `fonts` module
exposed as attributes.

## Webhooks and extra routes

```go
type WebhookHandler interface {
    OnWebhook(subpath string, r *http.Request) (WebhookResponse, error)
}
```

Handles `http(s)://<host>:<port>/plugins/<name>/<subpath>` — a real
`*http.Request`, in-process, no serialization boundary. Set
`Metadata.HasWebhook = true` if you implement this.

If you need more than one generic passthrough route (extra pages, an API
surface with several endpoints — `webgpsmap`'s map page and `ohcapi`'s API
are the bundled reference cases), implement `RouteRegistrar` instead and
register as many routes as you want directly on the shared mux:

```go
type RouteRegistrar interface {
    RegisterRoutes(web WebCapability)
}
```

**A state-changing (POST) route must apply CSRF protection itself** — a
`WebCapability`-registered route shares infrastructure with the rest of
`internal/web`, but a raw `OnWebhook`/`RegisterRoutes` handler is
responsible for checking the daemon's own CSRF token on any POST it
accepts, the same as the original Python plugin API required (`csrf_token()`
+ `render_template_string`). See `internal/web/csrf.go`.

## Adapting existing `EventEmitter`-shaped code: `EmitterPlugin`

If you already have Go code implementing the simpler
`On(event string, args ...interface{})` shape used throughout
`internal/agent`, `internal/mesh`, `internal/ui/view`, and `internal/cli`
(this was the shape the very first native plugins used, before the
manager existed), wrap it instead of rewriting it:

```go
mgr.Register(&pluginmanager.EmitterPlugin{
    PluginName: "my-plugin",
    Meta:       pluginmanager.Metadata{ /* ... */ },
    Emitter:    myExistingEmitter,
})
```

This still gets a real per-plugin queue, panic isolation, and observable
counters — it's an adapter, not a lesser code path. **For new plugins,
implement `EventHandler` directly** (`HandleEvent(event, args)`);
`EmitterPlugin` exists specifically for folding pre-existing
`EventEmitter`-shaped code into the manager, not as the recommended
starting point.

## Registering your plugin (today)

There is currently one, central place bundled native plugins are wired
in: `cmd/pwnagotchi/main.go`'s `registerNativePlugins` function calls
`Manager.Register` for each one, and `nativePluginConfigs` +
`Manager.LoadAll` decides which actually start, based on each plugin's
own `enabled` flag in `config.toml` — exactly like real Python's
config-driven `plugins.load()`.

```go
// in registerNativePlugins:
if err := mgr.Register(myplugin.New()); err != nil {
    log.Printf("pluginmanager: %v", err)
}
```

**This manual, central registration step is specific to *bundled*
plugins compiled into this daemon.** For *third-party* plugins, a
separate out-of-process distribution system now exists at
`internal/pluginrpc`: a versioned TOML manifest (name, version, target
os/arch, a sha256 checksum of the compiled executable, and the specific
`Capabilities` fields the plugin declares needing), checksum verification
before every spawn, a bounded newline-delimited-JSON RPC protocol between
the daemon and the plugin's own separately-compiled executable, and
process-boundary crash isolation (both a hard crash — the process
exiting — and a soft hang — the process staying alive but never
answering a heartbeat — are detected and reported without affecting any
other plugin or the daemon itself). `pluginrpc.RemotePlugin` implements
the exact same `pluginmanager.Plugin`/`Loader`/`Unloader`/`EventHandler`
interfaces this document already described, so a third-party out-of-
process plugin participates in `Manager.Register`/`Load`/`On`/`Unload`
identically to a bundled in-process one — see
`internal/pluginrpc/remoteplugin_test.go` for a real, end-to-end example
spawning an actual subprocess through a real `*pluginmanager.Manager`.
`internal/plugins/cmd.go`'s `search`/`install`/`upgrade`/`uninstall`/
`edit`/`list` CLI subcommands now operate against this manifest-based
system; a legacy Python `.py` plugin name/file is rejected with a clear
message pointing back at this document, never silently installed or
treated as loaded, and never triggers installing Python.

A plugin SDK for *writing* a third-party executable (the plugin-side
mirror of `pluginmanager.Capabilities`, wrapping `pluginrpc.Client`) is
still a natural next step for anyone packaging a real third-party plugin
today — `pluginrpc.Client` already implements the low-level wire protocol
(see `internal/pluginrpc/sdk.go`); only a handful of representative
capability methods (`Log.Printf`, `Agent.Run`, `Agent.Session`,
`View.Set`, `View.Update`, `Exec.Run`, `Clock.Now`) are wired end-to-end
today in `internal/pluginrpc/dispatcher.go` as a proven, tested pattern —
extending it to the rest of `Capabilities`' methods is intentionally left
as fast, mechanical follow-up (add one `case` per method; no design
uncertainty remains), not a blocked or stubbed capability.

## Learn by example

Read these in order of complexity — each is a real, tested, currently
merged plugin:

1. `internal/plugins/native/example` — every hook, minimal logic, the
   reference for "what does a plugin skeleton look like."
2. `internal/plugins/native/memtemp` — `OnLoad` reading config and adding
   real UI elements, `HandleEvent("ui_update", ...)` refreshing them,
   `OnUnload` removing them, an injected clock-free scheduled diff
   (`cpu_load_since`'s own never-sleeping sample tracking).
3. `internal/plugins/native/cache` — `config_changed`-driven setup using
   the *full* daemon config, multiple event cases, a background
   time-based cleanup driven off `ui_update` ticks and an injected
   `Clock`.
4. `internal/plugins/native/switcher` — `Capabilities.Exec` (real argv
   process execution) and `Capabilities.System` (a real reboot) usage,
   plus the "commands as an intentionally-shell-program config field"
   pattern for a plugin that legitimately needs to run
   user-configured shell commands.

Each has a co-located `_test.go` using fakes for every capability it
touches — copy that pattern for your own plugin's tests. "It registers
successfully" is not sufficient test coverage; test every hook, every
config-driven branch, and every error path.

## Source of truth

If anything here seems to disagree with the actual code, the code wins —
re-read these directly:

- `internal/pluginmanager/manager.go` — `Plugin`, `Loader`, `Unloader`,
  `EventHandler`, `WebhookHandler`, `RouteRegistrar`, `Manager` itself.
- `internal/pluginmanager/capabilities.go` — the full `Capabilities`
  struct and every capability interface.
- `internal/pluginmanager/adapter.go` — `EmitterPlugin`.
- `cmd/pwnagotchi/main.go` — `registerNativePlugins`, `nativePluginConfigs`,
  and the `capsFor` closure that builds real `Capabilities` values.
- `docs/migration-ledger.md` — where this fits in the overall
  Python-to-Go migration, and which bundled plugins are/aren't native yet.
