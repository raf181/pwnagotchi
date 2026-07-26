# Architecture

The primary runtime is one static Go daemon. Built-in plugins run in-process;
each enabled third-party plugin runs as one supervised child process. Bettercap
and pwngrid-peer remain separate system services.

## Runtime Shape

```text
systemd
  pwnagotchi.service
    pwnagotchi-go
      agent and automata
      web server
      display/view loop
      mesh advertiser
      built-in plugin queues
      third-party plugin child processes
  bettercap.service
  pwngrid-peer.service
```

Long-lived daemon goroutines include:

- bettercap REST/WebSocket handling and the agent loop
- mesh advertising
- optional display rendering
- the `net/http` web server
- one bounded serial event queue per loaded plugin
- third-party RPC readers, stderr relays, process monitors, and heartbeats

A panic in an in-process plugin event handler is recovered and counted.
Third-party crashes are isolated by the process boundary. Neither model is an
OS security sandbox.

## Startup Sequence

`cmd/pwnagotchi/main.go` composes the daemon in this order:

1. Parse the main CLI and plugin subcommands.
2. Load embedded defaults, the system default file, user config, and `conf.d`
   drop-ins.
3. Configure zram/tmpfs mounts.
4. Configure logging.
5. Construct the plugin manager and its capability factory.
6. Resolve the display and view. A failed or disabled physical display falls
   back to the headless view.
7. Reconcile the configured unit name and handle `--clear`.
8. Load/generate identity keys and construct the agent.
9. Register all built-in plugins.
10. Scan `/etc/pwnagotchi/plugins`, strictly validate manifests, targets,
    executable permissions, and checksums, then register valid third-party
    plugins.
11. Load every registered plugin whose
    `main.plugins.<name>.enabled` value is true. Each event handler receives
    `loaded` and then `config_changed`.
12. Register the `SIGUSR1` restart handler.
13. Construct and start the web server when enabled.
14. Enter automatic or manual mode.

One invalid third-party installation or one plugin load failure is logged and
does not prevent other plugins from loading.

## Package Map

| Package | Responsibility |
|---|---|
| `cmd/pwnagotchi` | Composition root and top-level CLI dispatch |
| `internal/config` | Defaults, migration, merging, normalization, atomic saves |
| `internal/bettercap` | Bettercap REST and WebSocket client |
| `internal/agent` | Reconnaissance, interactions, events, recovery, history |
| `internal/automata`, `internal/epoch` | Mood and per-epoch state |
| `internal/grid`, `internal/mesh` | pwngrid API and peer advertising |
| `internal/identity` | Unit key generation, signing, and fingerprint |
| `internal/ui/view`, `internal/ui/display` | Canvas state and rendering loop |
| `internal/ui/hw` | Display registry, layouts, dummy driver, unsupported physical drivers |
| `internal/web` | HTTP routes, auth, CSRF, templates, config and plugin controls |
| `internal/pluginmanager` | Built-in lifecycle, queues, status, webhooks, panic isolation |
| `internal/pluginrpc` | Repository, manifest, RPC, supervision, checksum validation |
| `internal/pluginhost` | Concrete capability adapters, Linux GPIO/I2C, exec, clock |
| `pkg/plugin` | Public third-party plugin SDK |
| `internal/plugins/native` | Built-in plugin implementations |
| `internal/wifiparse` | Pure-Go pcap/RadioTap/802.11 extraction |

## Configuration

Configuration is represented as nested `config.Map` values. Startup merges
defaults and user overrides; plugins receive their own config during `OnLoad`
and the complete merged config in the `config_changed` event.

Writes use a sibling temporary file, preserve the existing file mode and
ownership, synchronize data, and atomically rename over the target. This avoids
truncating a working `config.toml` when encoding or writing fails.

Two copies of defaults are intentionally kept:

- `internal/config/defaults.toml`, embedded into the binary
- `pwnagotchi/defaults.toml`, retained with the source baseline

Tests should ensure they remain identical.

## Plugin Runtime

### Built-In

Built-in plugins implement small optional interfaces:

- `Loader` and `Unloader`
- `EventHandler`
- `WebhookHandler`
- `RouteRegistrar`

The manager gives each event handler a serial queue of 64 events. A full queue
drops the newest event for that plugin and increments its drop counter instead
of blocking the daemon.

Production capabilities include agent, view, argv-based command execution,
HTTP, clock, Linux GPIO, Linux I2C, system actions, emitted events, and a
plugin-scoped logger. SPI and the generic Web capability are not populated.

### Third-Party

Installed third-party plugins use:

```text
repository index -> manifest -> downloaded executable
                 validate target and SHA-256
                 register RemotePlugin
                 spawn when enabled
```

The wire protocol is newline-delimited JSON over stdin/stdout. Lines are
limited to 4 MiB, capability calls are limited to 16 concurrent calls per
process, and the process is monitored with heartbeats. The supported RPC groups
are `Log`, `Agent`, `View`, `Exec`, and `Clock`.

The capability allowlist limits only calls back into daemon objects. The child
process still has the normal filesystem, device, process, and network access of
the service account.

## Web Security

The server uses Basic authentication when `ui.web.auth` is true. Packaged
defaults enable it, but placeholder credentials must be changed.

State-changing built-in routes and generic plugin webhooks require a
double-submit CSRF token. Inbox mutations, plugin toggles, plugin upgrades, and
hardware/plugin actions use POST. Request headers and bodies are bounded where
the route accepts structured input. The HTTP server also sets read-header,
read, write, idle, and maximum-header limits.

CORS is disabled unless `ui.web.origin` is configured. Enabling CORS does not
replace authentication or CSRF validation.

## Hardware Boundary

- `DummyDisplay` and the headless view are supported.
- Physical display names resolve to layouts, but their initialize/render/clear
  operations return explicit unsupported errors.
- Linux I2C uses `/dev/i2c-<bus>` and `I2C_RDWR`.
- Linux GPIO uses `/sys/class/gpio`; it does not configure pull bias.
- SPI has an interface but no production backend.
- Monitor mode depends on the Wi-Fi driver/firmware. The stock Pi Zero 2 W
  onboard radio is insufficient without a compatible Nexmon build.

See [Feature matrix](feature-matrix.md), [Known differences](known-differences.md),
and [Pi deployment](../deploy/README.md).
