# Feature Matrix

This is the current operational status of the Go daemon. Historical
file-by-file migration evidence is in `migration-ledger.md`.

Status terms:

- **Supported**: implemented in Go and covered by the normal test suite.
- **Hardware-dependent**: implemented, but requires the named Linux device or
  service.
- **Partial**: useful implementation exists with a documented gap.
- **Unsupported**: recognized but deliberately returns an error or is not
  wired.

## Core

| Area | Go implementation | Status | Notes |
|---|---|---|---|
| CLI and modes | `cmd/pwnagotchi`, `internal/cli` | Supported | Includes complete `plugins --help` and strict argument handling |
| TOML/YAML config | `internal/config` | Supported | Merging, legacy conversion, drop-ins, atomic writes |
| Identity | `internal/identity` | Hardware-dependent | Uses installed `pwngrid` for key generation |
| Bettercap client | `internal/bettercap` | Hardware-dependent | REST and WebSocket clients require the service |
| Agent | `internal/agent` | Hardware-dependent | Recon, interaction limits, recovery, channel/history access |
| Automata and epoch | `internal/automata`, `internal/epoch` | Supported | Main state and counters |
| Mesh and grid | `internal/mesh`, `internal/grid` | Hardware-dependent | Requires network and pwngrid service/API |
| Session parsing | `internal/session` | Supported | Parses prior log sessions |
| Voice/locales | `internal/voice` | Supported | Locale catalogs embedded |
| PCAP parsing | `internal/wifiparse` | Supported | Pure Go; no Scapy or CGO |
| Logging implementation | `internal/logging` | Partial | Setup/rotation exist; many packages still use standard `log` |
| Filesystem mounts | `internal/fs` | Hardware-dependent | Real mount/zram commands require Linux/root |
| Host lifecycle | `internal/unit` | Hardware-dependent | Real hostname, restart, reboot, shutdown actions |

## Web UI

| Area | Status | Notes |
|---|---|---|
| Main UI and frame endpoint | Supported | Embedded templates/assets and rendered frame cache |
| Basic authentication | Supported | Enabled in packaged defaults; placeholder credentials must be changed |
| CSRF | Supported | Unsafe core and generic plugin webhook requests are checked |
| Inbox | Supported | Read via GET; seen/delete mutations use POST |
| Plugin list/toggle/upgrade | Supported | POST, name validation, config rollback on failed save |
| `webcfg` | Supported | Structured TOML rewrite and atomic save |
| `logtail` | Supported | Native streaming endpoint |
| CORS | Supported | Explicit configured origin only |
| HTTP resource limits | Supported | Header/read/write/idle limits and bounded structured bodies |

## Plugins

| Area | Status | Notes |
|---|---|---|
| Built-in manager | Supported | Lifecycle locking, serial queues, panic isolation, status counters |
| 23 bundled plugins + `example` | Supported | All are native Go; see plugin compatibility matrix |
| Installed plugin discovery | Supported | Registered at startup after strict validation |
| Repository index | Supported | Strict JSON, versioned, 1 MiB limit |
| Manifest | Supported | Strict TOML, target/name/capability checks, 256 KiB limit |
| Binary install/upgrade | Supported | 128 MiB limit, SHA-256, staging, rollback |
| Built-in service updater | Partial | Checks by default; opt-in bettercap/pwngrid replacement requires checksum and rollback; daemon self-update is report-only |
| Third-party RPC | Supported | `Log`, `Agent`, `View`, `Exec`, `Clock` |
| Public plugin SDK | Supported | `pkg/plugin`; event callbacks may safely call capabilities |
| Crash isolation | Supported | Child exit and missed-heartbeat detection |
| Third-party webhook/routes | Unsupported | Remote protocol has no web capability |
| Python plugin loading | Unsupported | No Python bridge or `.py` fallback |
| Plugin signatures/sandbox | Unsupported | Checksums are not signatures; processes are not sandboxed |

## UI and Hardware

| Area | Status | Notes |
|---|---|---|
| Canvas, widgets, fonts | Supported | Pure-Go rendering with embedded fonts |
| Headless view | Supported | Used when display is disabled or initialization fails |
| `DummyDisplay` | Supported | Functional no-hardware driver |
| Physical display layouts/registry | Partial | Names and layouts resolve |
| Physical display I/O | Unsupported | E-ink/OLED/LCD initialize/render/clear return explicit errors |
| I2C | Hardware-dependent | Linux `i2c-dev` backend using `/dev/i2c-*` |
| GPIO | Hardware-dependent | Linux sysfs backend; no pull-bias configuration |
| SPI | Unsupported | Interface exists; no production implementation |
| PWM | Unsupported | No generalized production capability |
| Pi onboard monitor mode | Hardware-dependent | Requires compatible Nexmon kernel/firmware |
| USB monitor adapter | Hardware-dependent | Launcher prefers a non-`brcmfmac` adapter |

## Deployment

| Area | Status | Notes |
|---|---|---|
| Static arm64 daemon build | Supported | `CGO_ENABLED=0 GOOS=linux GOARCH=arm64` |
| systemd service | Supported | Runs daemon as root on the image |
| bettercap build | Hardware/image-dependent | Built natively in arm64 pi-gen chroot |
| pwngrid install | Supported by pipeline | Checksum-verified arm64 release |
| I2C boot enablement | Supported by updated pipeline | `dtparam=i2c_arm=on` |
| `pwnagotchi` command alias | Supported by updated pipeline | Symlink to `/usr/bin/pwnagotchi-go` |
| Daemon updates | Supported manually/image rebuild | The running daemon is not replaced by its own auto-update callback |
| Full image reproducibility | Partial | Kernel/Nexmon and upstream archive drift remain sensitive |

## Verification

The normal release gate is:

```sh
go fmt ./...
go test ./...
go test -race ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -o /tmp/pwnagotchi-go ./cmd/pwnagotchi
bash -n deploy/scripts/* deploy/pi-gen-stage/*/*.sh
```

Hardware tests remain separate because they can alter interfaces, buses, or
host lifecycle state.
