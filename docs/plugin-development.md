# Plugin Development

The Go daemon supports two plugin models:

1. **Third-party plugins** are separate Go executables. They import the public
   `pkg/plugin` SDK and communicate with the daemon over stdin/stdout RPC.
2. **Bundled plugins** are packages inside this repository. They use
   `internal/pluginmanager` and are compiled into the daemon.

Python plugins are not loaded. Do not copy a `.py` file into
`custom_plugins`; port it to one of the Go models below.

## Third-Party Plugin Quick Start

Create a separate Go module:

```sh
mkdir hello-pwnagotchi
cd hello-pwnagotchi
go mod init example.net/hello-pwnagotchi
go get github.com/jayofelony/pwnagotchi@<compatible-commit>
```

Use the public SDK. External modules must not import `internal/pluginrpc` or
`internal/pluginmanager`; Go's `internal` import rule intentionally prevents
that.

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jayofelony/pwnagotchi/pkg/plugin"
)

func main() {
	client := plugin.NewStdioClient()

	client.OnEvent(func(event string, args json.RawMessage) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		switch event {
		case "loaded":
			if err := client.Log(ctx, "hello plugin loaded"); err != nil {
				fmt.Fprintln(plugin.Stderr, err)
			}
		case "ui_update":
			if err := client.ViewSet(ctx, "status", "hello from plugin"); err != nil {
				fmt.Fprintln(plugin.Stderr, err)
			}
		}
	})

	if err := client.Run(); err != nil {
		fmt.Fprintln(plugin.Stderr, err)
		os.Exit(1)
	}
}
```

`stdout` is reserved for the RPC protocol. Write diagnostics to
`plugin.Stderr` or use `Client.Log`. The SDK reads RPC responses on a separate
goroutine, so a serial event handler may safely make capability calls.

Build the target used by the Pi image:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -o hello-pwnagotchi .
sha256sum hello-pwnagotchi
```

Use the same Pwnagotchi revision in development and release builds. The RPC
manifest schema is versioned, but this repository does not currently publish
independent SDK compatibility releases.

## Manifest

Each executable has one strict TOML manifest:

```toml
manifest_version = 1
name = "hello-pwnagotchi"
version = "1.0.0"
author = "Example Author"
license = "GPL-3.0"
description = "Updates the status line on each UI refresh."
homepage = "https://example.net/hello-pwnagotchi"
os = "linux"
arch = "arm64"
sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
executable_url = "https://plugins.example.net/hello-pwnagotchi/1.0.0/hello-pwnagotchi"
capabilities = ["Log", "View"]
```

Rules enforced by the daemon:

- `manifest_version` must be exactly `1`.
- `name` is 1-64 ASCII letters, digits, `.`, `_`, or `-`, and must begin
  with a letter or digit.
- Unknown TOML fields and duplicate capability groups are rejected.
- `os` and `arch` must exactly match the running daemon.
- `sha256` must be 64 hexadecimal characters and must match the installed
  executable every time it is spawned.
- `executable_url` must be an absolute HTTP or HTTPS URL. HTTPS is strongly
  recommended.
- A local manifest and executable must be regular files; symlinks and
  non-executable binaries are rejected.

`version`, `os`, `arch`, `sha256`, and `executable_url` are required.
Descriptive fields should be populated even where the parser permits an empty
value.

## RPC Capabilities

Declare every group the plugin calls. Calling an undeclared group fails even
when the host has that capability.

| Manifest group | Public SDK methods | Daemon operation |
|---|---|---|
| `Log` | `Log` | Plugin-scoped log line |
| `Agent` | `AgentRun`, `AgentSession`, `AgentSupportedChannels`, `AgentResetHistory` | Bettercap commands/session and agent channel/history access |
| `View` | `ViewSet`, `ViewUpdate` | Set view state and request a render |
| `Exec` | `Exec` | Run one executable with an argument vector |
| `Clock` | `Now` | Read the daemon clock |

These are the only third-party RPC capability groups currently supported.
Manifest validation rejects `GPIO`, `I2C`, `SPI`, `System`, `Web`, and other
built-in-only groups.

`Exec` is not an operating-system sandbox. It avoids shell interpolation, but
the plugin can also call `os/exec`, open files, or access the network directly
because it is a normal process running as the daemon's service user.

## Events and Lifecycle

An enabled installed plugin is started during daemon startup. It receives:

1. `loaded`
2. `config_changed`, whose argument is the full merged daemon configuration
3. normal daemon events such as `ready`, `wifi_update`, `handshake`, `epoch`,
   `internet_available`, and `ui_update`

Event `args` is a JSON array encoded in `json.RawMessage`. Decode only the
events and fields your plugin needs, and tolerate missing or additional data:

```go
var args []json.RawMessage
if err := json.Unmarshal(raw, &args); err != nil {
	return
}
```

Event delivery is serial and bounded. Keep handlers short and give every
capability call a context deadline. The plugin-side queue holds 64 events and
drops new events while full. The manager also has a per-plugin queue of 64
events. A remote process is killed after three missed five-second heartbeats;
individual host calls default to a ten-second timeout.

Third-party plugins do not currently expose `WebhookHandler` or arbitrary web
routes. Build web-facing functionality into the daemon as a bundled plugin, or
run a separately secured HTTP service.

## Publish and Install

Host the binary and manifest, then add the manifest URL to a repository index.
The complete index format and release procedure are in
[Plugin repositories](plugin-repository.md).

Configure the daemon:

```toml
[main]
plugin_repository_index = "https://plugins.example.net/linux-arm64/index.json"

[main.plugins.hello-pwnagotchi]
enabled = false
```

Install and activate:

```sh
sudo pwnagotchi plugins update
sudo pwnagotchi plugins search 'hello*'
sudo pwnagotchi plugins install hello-pwnagotchi
sudo pwnagotchi plugins enable hello-pwnagotchi
sudo systemctl restart pwnagotchi
sudo pwnagotchi plugins doctor --all --no-hardware
```

Files are installed atomically under:

```text
/etc/pwnagotchi/plugins/hello-pwnagotchi/
  manifest.toml
  hello-pwnagotchi
```

An upgrade is staged and verified before replacing the previous directory. A
failed upgrade retains the prior installed version.

## Trust Model

SHA-256 verifies that a binary matches its manifest. It is not a signature and
does not prove who published either file. The repository index, manifest, and
binary host are all trusted inputs.

Installed plugins are not sandboxed:

- They run as the Pwnagotchi service user, normally `root` on the image.
- They inherit normal filesystem, process, device, and network access.
- Manifest capabilities restrict daemon RPC calls only.
- A malicious repository can publish a malicious executable and matching
  checksum.

Use a repository you control or independently audit. Prefer HTTPS, immutable
release URLs, restricted publishing credentials, and signed release metadata
outside this schema.

## Protocol Limits

| Resource | Limit |
|---|---|
| Repository index | 1 MiB |
| Manifest | 256 KiB |
| Executable download | 128 MiB |
| One RPC line | 4 MiB |
| Concurrent capability calls per process | 16 |
| SDK event queue | 64 events |
| Repository HTTP timeout | 30 seconds |

Oversized or structurally invalid input is rejected.

## Bundled Plugins

A bundled plugin lives inside this module and can import
`internal/pluginmanager`.

The minimum interface is:

```go
type Plugin interface {
	Name() string
	Metadata() pluginmanager.Metadata
}
```

Implement optional interfaces as needed:

```go
type Loader interface {
	OnLoad(pluginmanager.Capabilities) error
}

type Unloader interface {
	OnUnload() error
}

type EventHandler interface {
	HandleEvent(event string, args []interface{})
}

type WebhookHandler interface {
	OnWebhook(subpath string, r *http.Request) (pluginmanager.WebhookResponse, error)
}
```

The production host currently supplies `Config`, `Log`, `Agent`, `View`,
`Exec`, `HTTPClient`, `Clock`, `GPIO`, `I2C`, `System`, and `Emit`. `SPI`,
`Bettercap`, `Grid`, `State`, and `Web` are not populated by the composition
root. Some built-ins receive specialized clients through their constructors.
Always check optional capabilities for `nil`.

Generic plugin webhooks are mounted below `/plugins/<name>/`. The central web
handler validates CSRF tokens for unsafe methods. A bundled handler must still
enforce its allowed HTTP methods, validate all names and paths, bound request
bodies, and escape generated HTML.

Register a new built-in in `registerNativePlugins` in
`cmd/pwnagotchi/main.go`, add its default config to both copies of
`defaults.toml`, and add focused tests using capability fakes.

Reference implementations:

- `internal/plugins/native/example`: lifecycle and UI skeleton
- `internal/plugins/native/cache`: full-config events and persisted state
- `internal/plugins/native/auto_tune`: events, agent calls, presets, and webhook
- `internal/plugins/native/bt_tether`: commands, background work, and webhook
- `internal/plugins/native/grid`: constructor injection and emitted events

## Porting a Python Plugin

| Python pattern | Third-party Go equivalent | Bundled Go equivalent |
|---|---|---|
| Class metadata | Manifest fields | `Metadata()` |
| `on_loaded` | `loaded` event | `OnLoad`, then `loaded` |
| `on_unload` | Process stdin closes / process exits | `OnUnload` |
| `on_<event>` | `OnEvent` switch | `HandleEvent` switch |
| `self.options` | Read `config_changed` JSON | `Capabilities.Config` |
| `logging.*` | `Client.Log` or stderr | `Capabilities.Log` |
| `agent.run` | `AgentRun` | `Capabilities.Agent.Run` |
| `ui.set` | `ViewSet` | `Capabilities.View.Set` |
| Flask webhook | Not supported remotely | `WebhookHandler` |
| Drop-in `.py` file | Not supported | Port and compile |

Before publishing, run:

```sh
go test ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath .
```

The source of truth is `pkg/plugin`, `internal/pluginrpc`,
`internal/pluginmanager`, and `cmd/pwnagotchi/main.go`.
