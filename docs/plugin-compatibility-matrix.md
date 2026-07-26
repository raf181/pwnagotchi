# Plugin Compatibility

The manager exposes 24 built-in entries: the 23 Python bundled plugins and the
`example` reference plugin, all implemented in Go. No built-in executes Python.

`enabled` below is the packaged default. A dash means the plugin is available
but has no default config section and must be configured explicitly.

## Built-In Plugins

| Plugin | Default | Go location | Runtime notes |
|---|---:|---|---|
| `auto-tune` | on | `internal/plugins/native/auto_tune` | Agent channel/history access, presets, persisted config, CSRF-protected webhook |
| `auto_backup` | on | `internal/plugins/native/auto_backup` | Runs configured backup tools and exposes a webhook |
| `auto-update` | on (check only) | `internal/plugins/native/auto_update` | Privileged bettercap/pwngrid install is opt-in, bounded, checksum-required, and rollback-aware; daemon self-update is report-only |
| `bt-tether` | off | `internal/plugins/native/bt_tether` | Uses `bluetoothctl`, D-Bus, `ip`, and DHCP tools; interactive passkey-agent flows remain limited |
| `cache` | on | `internal/plugins/native/cache` | Writes AP cache data under the handshake directory |
| `example` | - | `internal/plugins/native/example` | Reference lifecycle/event/UI implementation |
| `fix_services` | on | `internal/plugins/native/fix_services` | Checks/restarts services and modules using real host actions |
| `gpio_buttons` | off | `internal/plugins/native/gpio_buttons` | Linux sysfs GPIO; external pull bias may be required |
| `gps` | off | `internal/plugins/native/gps` | Serial or GPSD input and per-handshake `.gps.json` files |
| `grid` | on | `internal/plugins/native/grid` | Reports captures, updates session data, checks inbox, emits `unread_inbox`, updates view |
| `logtail` | off | `internal/web/logtail.go` | Native streaming log endpoint |
| `memtemp` | off | `internal/plugins/native/memtemp` | CPU/memory/temperature UI values |
| `ohcapi` | off | `internal/plugins/native/ohcapi` | OnlineHashCrack upload and status webhook |
| `pisugarx` | off | `internal/plugins/native/pisugarx` | PiSugar I2C and shutdown actions; requires `/dev/i2c-1` |
| `pwncrack` | off | `internal/plugins/native/pwncrack` | Hash upload workflow |
| `pwnstore_ui` | on | `internal/plugins/native/pwnstore_ui` | Store browser and structured config editing; legacy Python-store install/uninstall is not supported |
| `session-stats` | off | `internal/plugins/native/session_stats` | Session aggregation, persistence, and webhook |
| `switcher` | - | `internal/plugins/native/switcher` | User-defined event tasks and optional reboot; command strings intentionally invoke a shell |
| `ups_lite` | off | `internal/plugins/native/ups_lite` | CW2015 I2C plus GPIO charge state; requires I2C and appropriate GPIO wiring |
| `webcfg` | on | `internal/web/webcfg.go` | Runtime config editor with atomic TOML writes |
| `webgpsmap` | off | `internal/plugins/native/webgpsmap` | Map, aggregate data, and offline map download through generic webhook subpaths |
| `wigle` | off | `internal/plugins/native/wigle` | Pure-Go PCAP parsing, GPS metadata, WiGLE upload |
| `wittypi` | - | `internal/plugins/native/wittypi` | Witty Pi I2C RTC/power scheduling |
| `wpa-sec` | off | `internal/wpasec/wpasec.go` | WPA-SEC upload and one-time legacy state migration |

Every entry has focused Go tests. Hardware and external-service branches use
fakes in the default suite and require separate device validation.

## Runtime Guarantees

- Plugins load only when `main.plugins.<name>.enabled = true`.
- Each event handler has a serial queue of 64 events.
- Panics are recovered and exposed in manager status.
- A full queue drops only that plugin's newest event.
- Load/unload/toggle operations are serialized per plugin.
- A failed load or crashed remote process is reported without stopping the
  daemon.
- Generic state-changing webhook requests require the daemon CSRF token.

Built-ins run inside the daemon and are not isolated from daemon memory. A bug
outside a recovered event callback, such as a background goroutine panic, can
still terminate the process.

## Hardware Caveats

I2C plugins use the real Linux `/dev/i2c-<bus>` backend. The image must enable
I2C and the service account must be allowed to open the device.

GPIO plugins use `/sys/class/gpio`. The backend can set input/output, read,
write, and wait for falling edges, but it cannot configure pull-up or pull-down
bias. Use the peripheral's built-in resistor or add the required hardware
resistor.

SPI is not available. Physical display drivers are also not implemented, so
plugin UI state is visible in the web/headless rendering path but not on a real
e-ink/OLED/LCD panel with this Go build.

## Third-Party Plugins

Installed plugins are separate checksum-verified executables under:

```text
/etc/pwnagotchi/plugins/<name>/manifest.toml
/etc/pwnagotchi/plugins/<name>/<name>
```

They receive the same event vocabulary but a smaller RPC surface:

- `Log`
- `Agent`
- `View`
- `Exec`
- `Clock`

They cannot register plugin webhooks, access GPIO/I2C/SPI through RPC, or import
the daemon's internal packages. They must import
`github.com/jayofelony/pwnagotchi/pkg/plugin`.

Checksums detect mismatch or corruption but are not signatures. Third-party
processes are not sandboxed and normally inherit root privileges from the
systemd service. See [Plugin development](plugin-development.md).

## Removed Compatibility

The following Python mechanisms do not exist:

- loading a bare `.py` file
- `main.custom_plugins` discovery
- Python package dependencies
- Flask/Jinja plugin execution
- the former Python subprocess bridge

`main.custom_plugins` remains only so the CLI can identify a legacy file and
give a migration error. It is never executed.
