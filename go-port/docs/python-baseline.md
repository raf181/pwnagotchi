# Python Baseline (behavioral oracle)

`find /root/pwnagotchi -iname "*test*"` (excluding `venv/`) returns **no results** —
there is no pre-existing automated test suite for this Python codebase. Per the
porting goal, Python is still the behavioral oracle, so this document records
characterization evidence gathered by directly exercising the installed Python
package (`venv/`, Python 3.11.2) in this session. Each transcript below is the
actual output of running the real code, not a description — these are the
golden values the Go port's characterization/differential tests must match
(mirrored as fixtures under `go-port/testdata/`).

Dependency versions confirmed installed in `venv/` (`pip3 list`):
Flask 3.1.3, flask-cors 6.0.5, Flask-WTF 1.3.0, gpiozero 2.0.1, pycryptodome
3.23.0, PyYAML 6.0.3, requests 2.34.2, scapy 2.7.0, tomlkit 0.15.1,
websockets 16.1.1.

## CLI (`pwnagotchi/cli.py`)

### `--version`
```
$ python3 -m pwnagotchi.cli --version
2.9.5.5
exit code: 0
```

### `--help`
```
usage: pwnagotchi [-h] [-C CONFIG] [-U USER_CONFIG] [--manual]
                  [--skip-session] [--clear] [--debug] [--version]
                  [--print-config] [--check-update] [--donate]
                  {plugins} ...

positional arguments:
  {plugins}

options:
  -h, --help            show this help message and exit
  -C CONFIG, --config CONFIG
                        Main configuration file.
  -U USER_CONFIG, --user-config USER_CONFIG
                        If this file exists, configuration will be merged and
                        this will override default values.
  --manual              Manual mode.
  --skip-session        Skip last session parsing in manual mode.
  --clear               Clear the ePaper display and exit.
  --debug               Enable debug logs.
  --version             Print the version.
  --print-config        Print the configuration.
  --check-update        Check for updates on Pwnagotchi. And tells current
                        version.
  --donate              How to donate to this project.
exit code: 0
```
This exact text (including argparse's line-wrapping at the terminal width used to
capture it, ~80 cols) is the compatibility target for `--help` output. Go's flag
libraries do not wrap identically to argparse by default — `known-differences.md`
records this as an accepted, documented difference unless a custom help formatter
is written to match column-for-column.

## Config loading (`pwnagotchi/utils.load_config`)

Verified end-to-end against a scratch directory (not `/etc/pwnagotchi`):

1. Fresh directory, no default/user config present → `load_config` copies the
   packaged `pwnagotchi/defaults.toml` to the configured default path, prints
   `"copying %s to %s ..."`, and returns the parsed defaults. Confirmed fields:
   `main.name == "pwnagotchi"`, `ui.display.type == "waveshare_4"` (defaults.toml
   ships `type = "waveshare_4"` verbatim, which is already a canonical alias
   target so normalization is a no-op here), `bettercap.handshakes ==
   "/etc/pwnagotchi/handshakes"`, top-level keys `{bettercap, fs, main,
   personality, ui}` exactly (5 top-level tables in `defaults.toml`; `ui.web` is
   nested under `ui`, not top-level).
2. With a user config overriding `main.name = "unit1"` and `ui.display.type =
   "ws4"`: merged result has `main.name == "unit1"` (user wins) and
   `ui.display.type == "waveshare_4"` (alias table normalizes `ws4` →
   `waveshare_4`, confirming the ~100-branch alias dispatch in `load_config`
   really does run after the merge, not before).

### `merge_config` semantics (recursive dict merge, user wins, default fills gaps)
```python
>>> merge_config({'a':1,'nested':{'x':1}}, {'a':2,'b':3,'nested':{'x':9,'y':2}})
{'a': 1, 'nested': {'x': 1, 'y': 2}, 'b': 3}
```
Confirms: scalar keys present in `user` are never overwritten by `default`; dict
keys are merged recursively (recursing into `merge_config(user[k], default[k])`);
keys only in `default` are copied into `user`. This exact tree-merge (not a
shallow update) must be replicated by the Go config loader.

## `pwnagotchi/utils.py` pure functions

```python
>>> parse_version('2.9.5.5')
('2', '9', '5', '5')                      # STRING tuple, not int — see gotcha below
>>> secs_to_hhmmss(3725)
'01:02:05'
>>> secs_to_hhmmss(59)
'00:00:59'
>>> remove_whitelisted(
...   ['/tmp/FooNet-2.4.pcap', '/tmp/Other.pcap', '/tmp/ANOTHER_EXAMPLE_NETWORK.pcap'],
...   ['foonet', 'ANOTHER_EXAMPLE_NETWORK'])
['/tmp/Other.pcap']
```

**Confirmed gotcha — `parse_version` is a lexical string-tuple comparison, not
numeric**, e.g.:
```python
>>> parse_version('2.10.0') > parse_version('2.9.5.5')
False
```
`'10' < '9'` lexically, so a real newer release `2.10.0` compares as *older* than
`2.9.5.5` under this scheme. `cli.py --check-update` relies on exactly this
comparison (`remote > local`). **The Go port must replicate this exact
(non-numeric) comparison for `--check-update` parity** — it is a bug, but
replicating it is in scope per "preserve CLI... errors" and "do not remove
difficult functionality"; it is recorded again in `known-differences.md` as an
intentionally-preserved quirk, not a Go defect.

## `pwnagotchi/log.py` — size parsing

```python
>>> parse_max_size('10M') -> 10485760      # 10 * 1024 * 1024
>>> parse_max_size('10K') -> 10240
>>> parse_max_size('10G') -> 10737418240
>>> parse_max_size('100') -> 100           # no unit -> bytes
>>> parse_max_size('10b') -> 10            # lowercase 'b' -> unit branch falls through to else -> bytes
```
Units are matched case-insensitively (`unit.lower()`) against `k`/`m`/`g` only;
any other suffix (including `'b'`) falls through to the raw-byte `else` branch.

## `pwnagotchi/mesh/wifi.py` — frequency→channel mapping

```python
freq_to_channel(2412) -> 1        # 2.4GHz low edge
freq_to_channel(2472) -> 13       # 2.4GHz high edge (channel 13)
freq_to_channel(2484) -> 14       # special-cased channel 14
freq_to_channel(5180) -> 36       # 5GHz UNII-1 start
freq_to_channel(5200) -> 37       # note: 37 is not a real Wi-Fi channel number;
                                   # formula is linear (freq-5180)/20+36 with no
                                   # validation against real channel allocations —
                                   # replicate the formula exactly, not "corrected"
freq_to_channel(5500) -> 100      # UNII-2e boundary
freq_to_channel(5745) -> 149      # UNII-3 start
freq_to_channel(5955) -> 11       # 6GHz start (also overlaps 2.4GHz channel numbering
                                   # namespace — same integer 11 means different things
                                   # in different bands; this is a pre-existing property
                                   # of the source formula, preserve as-is)
freq_to_channel(7115) -> 69       # 6GHz upper range
```

## Epoch log line format (serialization contract)

`pwnagotchi/epoch.py:Epoch.next()` emits exactly one INFO line per epoch:
```
[epoch %d] duration=%s slept_for=%s blind=%d sad=%d bored=%d inactive=%d active=%d peers=%d tot_bond=%.2f avg_bond=%.2f hops=%d missed=%d deauths=%d assocs=%d handshakes=%d cpu=%d%% mem=%d%% temperature=%dC
```
This is consumed back by `pwnagotchi/log.py:LastSession._parse_stats` via regex
`^.+\[epoch (\d+)] (.+)` then `([a-z_]+)=(\S+)` — i.e. **the log text is a
de-facto serialization format**, not just human-readable output. The Go port's
logger must produce byte-identical field ordering, key names, and formatting
(`%.2f`, `%d%%`) for any Go-side `LastSession`-equivalent parser to interoperate
with logs produced by either implementation, and for the differential test suite
to diff Python vs. Go log output directly.

## Additional verified CLI/process evidence (live runs, `venv/bin/pwnagotchi`)

- `pwnagotchi --donate` → exit 0, stdout (exact bytes, including the
  trailing space after `@` and leading space before `https`):
  ```
  Donations can be made @ 
   https://github.com/sponsors/jayofelony 

  But only if you really want to!
  ```
- `pwnagotchi --nope` (unknown flag) → **exit code 2**, stderr:
  ```
  usage: pwnagotchi [-h] [-C CONFIG] [-U USER_CONFIG] [--manual]
                    [--skip-session] [--clear] [--debug] [--version]
                    [--print-config] [--check-update] [--donate]
                    {plugins} ...
  pwnagotchi: error: unrecognized arguments: --nope
  ```
  Stock argparse behavior — Go's flag parser must match this exit code.
- `pwnagotchi plugins list` (no plugins database yet) → exit 0, stdout:
  `Maybe try: sudo pwnagotchi plugins update`
- `pwnagotchi --print-config` → exit 0, 270 lines, byte-for-byte a
  `tomlkit` re-serialization of the packaged `defaults.toml` (comments
  preserved, e.g. the `auto_backup` inline `#More options availble ...`
  comment survives the round-trip — confirms why `tomlkit`, not plain
  `toml`, is used for the primary parse path). **Side effect confirmed**:
  even this read-only-looking flag creates `/etc/pwnagotchi/` and
  `/etc/pwnagotchi/default.toml` on first run (via `utils.load_config`),
  but does **not** create `config.toml`, and does **not** create the log
  files (log setup happens after the `--print-config` exit point in
  `cli.py`'s control flow).
- `pwnagotchi --manual --debug` (live run, several seconds, then
  terminated): confirmed live, not just from source:
  - `modprobe zram` attempted (and failed harmlessly in this container) for
    each of the two default `fs.memory.mounts` entries (`log`, `data`);
    failure does not abort startup.
  - First log line exactly: `[2026-07-20 23:10:07,529] [INFO] [MainThread] :
    -=-=-=-=-=-=-=-=-=-=-=-=-=-=-=- Pwnagotchi Re|Started
    -=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-`, confirming the format string
    `"[%(asctime)s] [%(levelname)s] [%(threadName)s] : %(message)s"`.
  - Default enabled plugin set loaded (from `defaults.toml`, no user
    config): `auto-tune, auto_backup, auto-update, fix_services, cache,
    grid, logtail, session-stats, webcfg, pwnstore_ui, switcher` — load
    order confirmed **non-alphabetical** (`pwnstore_ui` first), i.e. raw
    filesystem glob order, not sorted; **Go's `filepath.Glob` sorts
    lexicographically where Python's `glob.glob` does not** — an
    unavoidable, documented platform difference (see
    known-differences.md) unless the Go port explicitly shuffles/reads
    `os.ReadDir` raw order to match.
  - `fix_services` self-disabled live: `[Fix_Services] Detected WiFi
    driver: mt76x2u` / `... External WiFi adapter detected (mt76x2u).
    Plugin will be disabled.` — confirms the driver-sniffing logic
    actually runs against real `/sys/class/net/*/device/driver`.
  - Confirmed one worker thread per plugin (`Thread-N`) plus a distinct
    `Thread-N (run_once)` for each plugin's `on_loaded` — matches the
    documented dedicated-thread-for-`on_loaded` special case.
  - `pwnstore_ui` logged graceful degradation when the optional external
    `pwnstore` CLI binary is absent (`shutil.which` returns `None`): `[pwnstore_ui]
    pwnstore CLI not found — install/uninstall will not work` — no crash.
  - `ui.fps is 0, the display will only update for major changes` and
    `display module is disabled` are both logged at WARNING on every
    default-config startup (defaults: `ui.fps=0.0`, `ui.display.enabled=false`)
    — the Go port's default-config startup must emit the same two warnings.
  - Identity banner confirmed: `pwnagotchi@<64-hex-char-sha256> (v2.9.5.5)`.
  - `web ui available at http://[::]:8080/` confirmed live (IPv6 wildcard
    bind is real default behavior, not just a documented default).
  - Plugin version banner format confirmed: `plugin '<ClassName>' v<version>`
    — uses the plugin's **class name**, not its filename/module name (e.g.
    `plugin 'PwnStoreUI' v1.2.6` vs `plugin 'auto_tune' v1.0.1` — casing is
    whatever each plugin author chose; do not normalize in Go).
  - Manual-mode session summary format confirmed exactly: `the last session
    lasted 5 hours, 39 minutes, 11 seconds (8 completed epochs, trained for
    0), average reward:0.0 (min:1000 max:-1000)` — confirms `train_epochs`
    stays 0 and reward stays at sentinel bounds `1000`/`-1000` in this
    `noai` branch (no code path ever emits a `reward=`/training-epoch log
    token anymore — these fields are load-bearing in the format string but
    functionally inert).
  - Confirmed real outbound call: `Starting new HTTPS connection (1):
    api.opwngrid.xyz:443`, fired from manual mode's `grid.is_connected()`
    poll ~5s after startup. **This is a live public third-party API** —
    Go differential tests must mock/inject this client, not hit the real
    endpoint repeatedly.

No further live daemon runs were performed in this session (no real Wi-Fi
hardware, bettercap, or pwngrid-peer available in this container, and to
avoid repeated calls to the real `api.opwngrid.xyz` service). Bettercap
association/deauth/recon behavior, real display rendering, and real I2C/SPI
hardware plugin behavior are characterized from source only and marked
"unverified without hardware" in `feature-matrix.md`.

## Identity/crypto — exact algorithm (`pwnagotchi/identity.py`, read in full)

- `KeyPair` paths: `/etc/pwnagotchi/id_rsa` (private, PEM),
  `/etc/pwnagotchi/id_rsa.pub` (public, PEM), `/etc/pwnagotchi/fingerprint`
  (hex text).
- Key generation is **delegated entirely to the external `pwngrid` binary**:
  `os.system("pwngrid -generate -keys '<path>'")` — not implemented in
  Python at all. The Go port should shell out to the same `pwngrid` binary
  for key generation (byte-identical mesh-interop key material) rather than
  reimplementing RSA keygen to match an external, undocumented Go binary's
  parameters — recorded as the recommended approach in
  known-differences.md.
- Fingerprint = `sha256(ascii_pem_bytes).hexdigest()` where `ascii_pem_bytes`
  is the **public** key's PEM text with a forced header fixup: if the
  pycryptodome-exported PEM doesn't already contain `RSA PUBLIC KEY`, the
  code does a raw string replace `'PUBLIC KEY' → 'RSA PUBLIC KEY'`. This
  patched byte sequence (not the "natural" PEM) is what gets hashed and
  broadcast to mesh peers as the unit's identity — the Go port's PEM
  encoding must byte-match pycryptodome's output (including line-wrap
  width and trailing newline conventions) for the fingerprint to match a
  real Python-generated key, and any interop test must compare against a
  real pycryptodome-produced PEM, not just "any valid PEM".
- Signing: SHA-256 digest + `PKCS1_PSS` (RSASSA-PSS) with **salt length
  exactly 16 bytes**. Go: `rsa.SignPSS(rand.Reader, priv, crypto.SHA256,
  digest, &rsa.PSSOptions{SaltLength: 16, Hash: crypto.SHA256})`. Since PSS
  is randomized, cross-implementation testing must verify
  Python-signs/Go-verifies and Go-signs/Python-verifies, not byte-identical
  signatures.

## Full external-process / privileged-operation inventory (exact commands, verified by source line)

| Command (substitution points marked `<...>`) | Source | Privilege |
|---|---|---|
| `hostname '<new_name>'` | `__init__.py: set_name` | root |
| `sync` | `__init__.py: shutdown/reboot` | root |
| `halt` | `__init__.py: shutdown` | root |
| `shutdown -r now` | `__init__.py: reboot` | root |
| `service bettercap restart` | `__init__.py: restart` | root |
| `service pwnagotchi restart` | `__init__.py: restart` | root |
| `touch /root/.pwnagotchi-auto` / `-manual` | `__init__.py: restart/reboot` | root |
| `pwngrid -generate -keys '<path>'` | `identity.py` | root |
| `rm /root/.auto-update && systemctl restart pwnagotchi` | `cli.py --check-update` | root |
| `/sbin/iw <iface> info \| grep wiphy \| cut -d ' ' -f 2` | `utils.py: iface_channels` | none |
| `/sbin/iw phy<N> channels \| grep ' MHz' \| grep -v disabled \| sed ...` | `utils.py: iface_channels` | none |
| `uname -a` / `bettercap -version` / `pwngrid -version` | `grid.py: update_data` (dead code, no callers today) | none |
| `mountpoint -q <path>` | `fs/__init__.py: is_mountpoint` | none |
| `modprobe zram` | `fs/__init__.py: MemoryFS.zram_install` | root |
| `mke2fs -t <fstype> /dev/zram<N>` | `fs/__init__.py: MemoryFS._setup` | root |
| `mount --bind <mountpoint> <disk>` / `mount --make-private <disk>` | `fs/__init__.py: MemoryFS.mount` | root |
| `mount -t <fstype> -o nosuid,noexec,nodev,user=pwnagotchi /dev/zram<N> <mountpoint>/` (or tmpfs fallback) | `fs/__init__.py: MemoryFS.mount` | root |
| `rsync -aXv --inplace --no-whole-file --delete-after <src>/ <dst>/` | `fs/__init__.py: MemoryFS.sync` | root |
| `umount -l <mountpoint>` / `<disk>` | `fs/__init__.py: MemoryFS.umount` | root |
| bettercap `!<mon_start_cmd>` (default `/usr/bin/monstart`, run **inside bettercap**) | `agent.py: start_monitor_mode` | root (bettercap) |
| arbitrary `ui.web.on_frame` shell command (config-driven, empty by default) | `ui/display.py` | daemon's own privilege |
| `pwnagotchi plugins update && pwnagotchi plugins upgrade <form-value>` | `ui/web/handler.py` upgrade route | **root — confirmed command-injection vector**: `request.form['plugin']` interpolated unescaped into an `os.system` shell string. The Go port MUST NOT replicate this vulnerability — validate the plugin name against `^[a-zA-Z0-9_-]+$` and invoke via `os/exec` argv (or call the internal upgrade function directly), and record this as a deliberate security fix in known-differences.md, not a silent behavior change. |
| `$EDITOR` (default `vim`) | `plugins/cmd.py: edit` | invoking user |
| external `pwnstore` CLI, `systemctl restart pwnagotchi` | `plugins/default/pwnstore_ui.py` | root |
| `bluetoothctl` (interactive), `pkill -9 bluetoothctl`, `dhcpcd`/`dhclient`, `systemctl restart bluetooth` | `plugins/default/bt-tether.py` | root |
| `hcxpcapngtool` | `plugins/default/pwncrack.py`, `plugins/default/ohcapi.py` | invoking user |
| `wget`, `unzip`, `sha256sum`, `service <name> {stop,start}`, `pip install` | `plugins/default/auto-update.py` | root |
| `journalctl`, `tail`, `ip link show wlan0mon`, `sudo modprobe [-r] brcmfmac`, `systemctl restart bettercap` | `plugins/default/fix_services.py` | root (via explicit `sudo`) |
| dynamically generated systemd units + `systemctl daemon-reload/enable/start` | `plugins/default/switcher.py` | root |
| `tar czf ...` (backs up `/root/.ssh`, `/etc/ssh/`, `/etc/pwnagotchi/`) | `plugins/default/auto_backup.py` | root |

None of these may be mocked to "always succeed" in the Go port — each is a
real external dependency the Go code must invoke the same way (argv form
via `os/exec`, not shell strings, except where replicating a piped
shell pipeline's exact parsing is the explicit goal, in which case the
pipeline should be reimplemented as direct Go-side parsing of two separate
`os/exec` calls' stdout rather than shelling out to `/bin/sh -c '...|...'`).

## Config precedence — verified merge order (most-specific wins)

1. Packaged `defaults.toml` (Go: `//go:embed`).
2. `/etc/pwnagotchi/default.toml` — always forcibly kept byte-identical to
   #1; drift is silently clobbered back on every `load_config` call. Never
   a legitimate user-customization target.
3. `/etc/pwnagotchi/config.toml` — real user config; `merge_config(user,
   default)`, user always wins, defaults fill gaps only.
4. `/etc/pwnagotchi/conf.d/*.toml` (raw glob order, not sorted) — each
   drop-in wins over everything merged so far; most-specific layer.
5. One-time legacy boot-partition migrations run **before** all of the
   above, only on paths existing: `/boot/config.yml` →
   `/boot/firmware/config.yml` → `/boot/config.toml` →
   `/boot/firmware/config.toml` (first match wins), and
   `/boot/firmware/pwnagotchi/` (destructive whole-folder migration,
   replaces `/etc/pwnagotchi` entirely).

## Bettercap / grid endpoint inventory (for the Go HTTP clients)

**Bettercap** (`bettercap.py`), default `127.0.0.1:8081`, HTTP Basic auth
(`pwnagotchi`/`pwnagotchi` unless overridden):
- `GET {scheme}://{host}:{port}/api/{session|session/wifi|...}` — **no
  explicit timeout** (Python `requests` default = infinite wait). Preserve
  or document as a deliberate improvement (recommend documenting a bounded
  Go default while noting the Python original has none).
- `POST {url}/api/session {"cmd": "<repl command>"}` — the single generic
  command-execution endpoint; retries forever on `ConnectionError`,
  jittered sleep `0.5–5.5s` between attempts.
- `ws://{user}:{pass}@{host}:{port}/api/events` — **always `ws://`, never
  `wss://`**, regardless of configured `scheme` — must not be "upgraded" in
  Go. Ping interval 15s, ping timeout 180s, max queue 10000.

**pwngrid-peer** (`grid.py`), local, `http://127.0.0.1:8666/api/v1`, **no
auth** (loopback-only trust boundary): `GET/POST /mesh/{true,false}`,
`/mesh/data`, `/mesh/memory`, `/mesh/peers`, `/report/ap`, `/inbox?p=N`,
`/inbox/<id>`, `/inbox/<id>/<mark>`, `POST /unit/<to>/inbox` (raw bytes).
All calls use explicit timeout `(30.0, 60.0)` (connect, read) — preserve
exactly (Go: dialer timeout ~30s + overall deadline ~90s, documented
approximation since Go's `http.Client.Timeout` doesn't split connect/read
the way Python's tuple does).

**Public/remote** (real third-party services — must not be hit repeatedly
in CI, mock/inject instead): `GET https://api.opwngrid.xyz/api/v1/uptime`
(connectivity check, `User-Agent: pwnagotchi/<version>`, any error →
"not connected"); `GET
https://api.github.com/repos/jayofelony/pwnagotchi/releases/latest`
(`--check-update`).

## Plugin hook inventory (authoritative, for the Go plugin interface)

Confirmed via `grep 'def on_'` across all bundled plugins plus every
`plugins.on(...)`/`plugins.one(...)` call site in the core: `on_loaded`,
`on_unload`, `on_config_changed`, `on_ready`, `on_grateful`, `on_lonely`,
`on_bored`, `on_sad`, `on_angry`, `on_excited`, `on_rebooting`,
`on_sleep`/`on_wait`, `on_epoch`, `on_wifi_update`,
`on_unfiltered_ap_list`, `on_bcap_<sanitized bettercap tag>` (**open-ended**
— any bettercap websocket event tag becomes a hook name at runtime via
`re.sub(r"[^a-z0-9_]+","_", tag.lower())`; Go cannot enumerate these as
fixed interface methods and needs a generic bettercap-event hook keyed by
string), `on_handshake`, `on_association`, `on_deauthentication`,
`on_channel_hop`, `on_peer_detected`, `on_peer_lost`,
`on_internet_available`, `on_display_setup`, `on_ui_setup`, `on_ui_update`,
`on_webhook` (called **synchronously** from the HTTP request thread/goroutine,
not through the async per-plugin queue — must block and return a response).
Also referenced (in `switcher.py`'s generic subscription list, vestigial
from pre-`noai` upstream, never fired by this branch's core):
`on_ai_ready`, `on_ai_policy`, `on_ai_training_{start,step,end}`,
`on_ai_{best,worst}_reward`, `on_free_channel` — recommend the Go port
keep these registrable-but-inert for third-party plugin compatibility, not
fire them.

Dispatch model to replicate: one dedicated worker goroutine per plugin
(mirrors Python's one-thread-per-plugin `PluginEventQueue`) consuming a
per-plugin buffered channel; `on_loaded` gets a dedicated one-shot goroutine
(Python plugins commonly block forever in `on_loaded` as a main loop).
Panics/errors in one plugin's hook must never affect other plugins or the
main daemon (`recover()` at the per-goroutine boundary, log and continue).

## No existing pytest/unittest suite

Because there is nothing to run, `go-port/tests/` characterization tests
(Go) and the fixtures under `go-port/testdata/` are the concrete, executable
encoding of everything captured in this document — each fixture records
`(input, python-observed-output)` pairs so `go test ./...` fails loudly if a Go
reimplementation diverges. New Python-side behavior discovered while porting
later subsystems (bettercap client wire format, plugin loader semantics, etc.)
must be appended to this file with the same "ran it, here's the real output"
standard — no speculative/undocumented behavior is to be assumed.
