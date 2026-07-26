# Pwnagotchi Go

This repository contains a Go implementation of Pwnagotchi for 64-bit
Raspberry Pi systems. The daemon, web UI, and 23 original bundled plugins
plus the `example` plugin are implemented in Go. A normal build produces one
static daemon binary and does not require a Python runtime.

Pwnagotchi uses bettercap to collect WPA handshake material from nearby Wi-Fi
networks. Only run it on networks and radio spectrum you are authorized to
test.

## Current Status

- Core agent, automata, bettercap, mesh, pwngrid, web UI, configuration, and
  plugin runtime are native Go.
- The manager has 24 built-in entries: 23 bundled plugins and `example`.
- Third-party plugins are separately compiled Go executables with a versioned
  manifest, target validation, SHA-256 verification, bounded RPC, heartbeat
  monitoring, and per-process crash isolation.
- Linux I2C and legacy sysfs GPIO backends are wired for hardware plugins.
- `DummyDisplay` and headless rendering work. Physical e-ink/OLED/LCD drivers
  are registered but their hardware operations remain unsupported in Go.
- SPI is not implemented or wired. GPIO input pull bias is not configured by
  the sysfs backend, so button circuits need an appropriate hardware pull-up
  or pull-down.
- Existing Python `.py` plugins are not loaded. They must be ported to Go.
- The bundled auto-update plugin checks releases by default but does not
  install them unless `main.plugins.auto-update.install = true`. It never
  replaces the running Pwnagotchi daemon in-process; deploy daemon/image
  updates explicitly.

See [Documentation](docs/README.md) for the current status, known differences,
plugin guides, and historical migration records.

## Security First

The web UI can reboot, shut down, reconfigure, and manage plugins. The packaged
defaults enable Basic authentication, but the initial web username and password
are both placeholders. Set real credentials before exposing port `8080`.

```toml
[ui.web]
enabled = true
address = "0.0.0.0"
auth = true
username = "admin"
password = "replace-with-a-long-unique-password"
```

Also replace any image-default SSH password, preferably install an SSH key and
disable password authentication. Do not expose bettercap (`8081`), pwngrid, or
the Pwnagotchi web UI directly to the public internet.

## Build

Go 1.25 or newer is required by `go.mod`.

```sh
go test ./...
go vet ./...
go test -race ./...
go build ./...
```

Cross-compile the daemon for the Pi Zero 2 W image target:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -o pwnagotchi-go ./cmd/pwnagotchi
```

Equivalent Make targets are available:

```sh
make fmt
make vet
make test
make race
make build
```

## Run

The daemon reads:

- `/etc/pwnagotchi/default.toml`
- `/etc/pwnagotchi/config.toml`
- configured `conf.d` drop-ins

Useful non-destructive commands:

```sh
pwnagotchi --help
pwnagotchi --version
pwnagotchi --print-config
pwnagotchi plugins --help
pwnagotchi plugins list
pwnagotchi plugins doctor --all --no-hardware
```

`pwnagotchi-go` is the installed binary name. Updated image builds also create
`/usr/bin/pwnagotchi` as a symlink to it.

## Plugins

There are two supported plugin paths:

1. Bundled plugins live in this module, implement
   `internal/pluginmanager` interfaces, and are registered in
   `cmd/pwnagotchi/main.go`.
2. Third-party plugins import the public
   `github.com/jayofelony/pwnagotchi/pkg/plugin` SDK and are installed from a
   configured repository index as checksum-verified executables.

Start with:

- [Plugin development](docs/plugin-development.md)
- [Plugin repository format](docs/plugin-repository.md)
- [Plugin compatibility](docs/plugin-compatibility-matrix.md)

The configured repository URL is:

```toml
[main]
plugin_repository_index = "https://plugins.example.net/linux-arm64/index.json"
```

It is empty by default because this project does not ship a trusted public
third-party repository.

## Repository Layout

| Path | Purpose |
|---|---|
| `cmd/pwnagotchi` | CLI and daemon composition root |
| `internal/agent` | Bettercap orchestration and interaction history |
| `internal/config` | Defaults, TOML/YAML loading, merging, and atomic writes |
| `internal/pluginmanager` | Built-in plugin lifecycle and event queues |
| `internal/pluginrpc` | Third-party manifest, repository, host, and RPC protocol |
| `pkg/plugin` | Public third-party plugin SDK |
| `internal/plugins/native` | Built-in plugin packages |
| `internal/ui`, `internal/web` | Rendering, views, and HTTP UI |
| `internal/pluginhost` | Runtime capability adapters, including Linux GPIO/I2C |
| `deploy` | Pi image pipeline, services, launchers, and hardware setup |
| `docs` | Current guides and historical migration evidence |

## Raspberry Pi

Read [deploy/README.md](deploy/README.md) before building or updating an image.
Monitor mode and frame injection depend on a compatible USB adapter or a
kernel/firmware combination supported by Nexmon. A stock Pi Zero 2 W onboard
radio does not provide the required monitor mode.

## License

Pwnagotchi was created by
[@evilsocket](https://github.com/evilsocket) and is maintained by the project
contributors. This repository is licensed under GPL-3.0; see [LICENSE.md](LICENSE.md).
