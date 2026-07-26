# Repository Analysis — pwnagotchi (Python, `noai` branch)

> **Historical analysis.** This is the pre-port inventory and contains status
> statements that are no longer current. Use
> [the documentation index](README.md), [architecture](architecture.md), and
> current source code for operational guidance.

> **Historical snapshot, kept for reference.** This is the exhaustive
> Python-codebase inventory that kicked off the migration, captured
> 2026-07-20 at commit `a15ae8fc`, back when the Go port lived nested under
> `go-port/` (since restructured to the repository root — see
> `docs/migration-ledger.md`) and `stage3/`/`config-32bit`/`config-64bit`
> still existed (since replaced by `deploy/pi-gen-stage/`). Paths below are
> preserved as originally written; do not treat them as current locations.
> For current Go-side status, see `docs/feature-matrix.md` and
> `docs/migration-ledger.md`.

This document is the exhaustive inventory of the Python codebase at `/root/pwnagotchi`
that the Go port must account for. It was produced by direct inspection of
source files (not inferred), and every path/behavior below has been read from the actual
file listed. Line counts are from `wc -l` at the time of writing (2026-07-20, commit
`a15ae8fc`).

Scope note: this repo bundles two very different things:

1. **The pwnagotchi daemon** (`pwnagotchi/` Python package) — the actual production
   program that runs on the device (Raspberry Pi). This is the subject of the Go port.
2. **OS image build tooling** (`stage3/`, `Makefile`, `config-32bit`, `config-64bit`,
   `scripts/*.sh`) — shell scripts and `pi-gen` stage directories used to build the
   Raspberry Pi SD-card image. These are infra for *building* the OS image the daemon
   runs on; they are not part of the running daemon and are **out of scope for
   behavioral porting** (there is nothing to run — they invoke `pi-gen`/`debootstrap`
   against a real ARM chroot). They are inventoried below for completeness but are not
   targets for Go translation.

There are **zero existing automated tests** anywhere in the Python repository (`find .
-iname "*test*"` returns nothing outside `venv/`). Python is still the behavioral
oracle per the porting goal, so `docs/python-baseline.md` captures characterization
evidence gathered by directly exercising the Python modules in this session.

## 1. Entry point / packaging

- `pyproject.toml`: setuptools project `pwnagotchi`, dynamic version from
  `pwnagotchi.__version__` (`pwnagotchi/_version.py` → `2.9.5.5`). Console script:
  `pwnagotchi = pwnagotchi.cli:pwnagotchi_cli`. Python `>=3.11`.
- Runtime dependencies (`[project.dependencies]`): PyYAML, dbus-python,
  file-read-backwards, flask, flask-cors, flask-wtf, gpiozero, inky, pycryptodome,
  pydrive2, python-dateutil, python-prctl, requests, rpi-lgpio, rpi_hardware_pwm,
  scapy, setuptools, smbus, smbus2, spidev, tomlkit, toml, tweepy, websockets,
  pisugar. (A comment notes numpy/gast/shimmy were deliberately removed — this fork
  has no ML/RL training pipeline; “No AI!” is even a literal string in `grid.py`.)
- `MANIFEST.in`, `LICENSE.md` (GPL), `CODE_OF_CONDUCT.md`, `CONTRIBUTING.md`.

## 2. Core package: `pwnagotchi/*.py` (2,876 lines, 11 files)

### `pwnagotchi/__init__.py` (167 lines)
Process-global unit/OS helpers. Module-level mutable globals: `_name`, `config`
(set by `cli.py`), `_cpu_stats`.
- `set_name(new_name)` — validates `^[a-zA-Z0-9\-]{2,25}$`; on change writes
  `/etc/hostname` (`wt`), reads/patches `/etc/hosts` (string-replaces old→new
  hostname), calls `os.system("hostname '%s'" % new_name)`, then `reboot()`.
  **Command injection note**: `new_name` is regex-validated to alnum+hyphen before
  being interpolated into `os.system`, so it is not exploitable through that path,
  but it is still a shell string, not `os/exec`-style argv.
- `name()` — lazily reads `/etc/hostname`, cached in `_name`.
- `uptime()` — reads `/proc/uptime`, returns whole seconds (int, truncated).
- `mem_usage()` — parses `/proc/meminfo` (`MemTotal`, `MemFree`, `Buffers`, `Cached`),
  returns `round(used/total, 1)`.
- `_cpu_stat()` / `cpu_load(tag=None)` — reads `/proc/stat` first line, diffs two
  samples 0.1s apart (or against a cached tagged sample) to compute non-idle fraction.
- `temperature(celsius=True)` — reads `/sys/class/thermal/thermal_zone0/temp` (millidegrees),
  truncates to int Celsius; Fahrenheit conversion has a bug preserved for parity:
  `(c * (9/5)) + 32` uses the already-truncated Celsius int, not exact conversion.
- `shutdown()` — logs, calls `view.ROOT.on_shutdown()` + `time.sleep(10)`, syncs all
  `fs.mounts`, `os.system("sync")`, `os.system("halt")`. **Privileged/destructive.**
- `restart(mode)` — touches `/root/.pwnagotchi-auto` or `/root/.pwnagotchi-manual`,
  `os.system("service bettercap restart")`, sleep 1s, `os.system("service pwnagotchi restart")`.
- `reboot(mode=None)` — `view.ROOT.on_rebooting()` + sleep 10s, touches marker file,
  syncs `fs.mounts`, `os.system("sync")`, `os.system("shutdown -r now")`. **Privileged.**

### `pwnagotchi/_version.py` (1 line)
`__version__ = '2.9.5.5'` — single source of truth for CLI `--version` and User-Agent
strings elsewhere (`grid.is_connected` sends `pwnagotchi/<version>`).

### `pwnagotchi/cli.py` (219 lines) — CLI entry point (`pwnagotchi_cli()`)
argparse parser `prog="pwnagotchi"`. Exact flags (verified live via `python3 -m
pwnagotchi.cli --help`, see `python-baseline.md` for full transcript):

| flag | dest | type | default | help |
|---|---|---|---|---|
| `-C`, `--config` | config | store | `/etc/pwnagotchi/default.toml` | Main configuration file. |
| `-U`, `--user-config` | user_config | store | `/etc/pwnagotchi/config.toml` | If this file exists, configuration will be merged and this will override default values. |
| `--manual` | do_manual | store_true | False | Manual mode. |
| `--skip-session` | skip_session | store_true | False | Skip last session parsing in manual mode. |
| `--clear` | do_clear | store_true | False | Clear the ePaper display and exit. |
| `--debug` | debug | store_true | False | Enable debug logs. |
| `--version` | version | store_true | False | Print the version. |
| `--print-config` | print_config | store_true | False | Print the configuration. |
| `--check-update` | check_update | store_true | False | Check for updates on Pwnagotchi. And tells current version. |
| `--donate` | donate | store_true | False | How to donate to this project. |
| subcommand `plugins` | — | — | — | added by `plugins/cmd.py:add_parsers` |

Behavior order in `pwnagotchi_cli()`:
1. Parse args, register `plugins` subcommand (from `pwnagotchi/plugins/cmd.py`).
2. If a `plugins` subcommand was used (`plugins_cmd.used_plugin_cmd`), load config,
   set up logging, dispatch to `plugins_cmd.handle_cmd`, `sys.exit(rc)`.
3. `--version` → print `pwnagotchi.__version__`, exit 0.
4. `--donate` → print sponsor message, exit 0.
5. `--check-update` → GET `https://api.github.com/repos/jayofelony/pwnagotchi/releases/latest`,
   compares `tag_name` (strip leading `v`) to local version via `parse_version`
   (naive string-tuple compare, **not semver-aware**), prompts on stdin `[Y/N]`,
   if yes and `/root/.auto-update` exists: `os.system("rm /root/.auto-update &&
   systemctl restart pwnagotchi")`, else logs an error telling the user to enable
   auto-update. Exit 0 always (even on error path) after this block.
6. Otherwise: `config = utils.load_config(args)`.
7. `--print-config` → `print(tomlkit.dumps(config))`, exit 0.
8. Normal run: imports `KeyPair`, `Agent`, `fonts`, `Display`, `grid`, `plugins`;
   sets `pwnagotchi.config`; `fs.setup_mounts(config)`; `log.setup_logging(args,
   config)`; `fonts.init(config)`; `pwnagotchi.set_name(config['main']['name'])`;
   `plugins.load(config)`; builds `Display`; if `--clear`, calls `display.clear()`
   and exits 0; else builds `Agent(view=display, config=config,
   keypair=KeyPair(view=display))`.
9. Registers `SIGUSR1` handler that calls `agent._restart("MANU" if args.do_manual
   else "AUTO")` — used by external tooling to trigger an in-place mode-aware restart.
10. Dispatches to `do_manual_mode(agent)` or `do_auto_mode(agent)` (both defined as
    closures inside `pwnagotchi_cli()`).

`do_auto_mode` is the real main loop: `agent.start()`, then forever: `agent.recon()`
→ `get_access_points_by_channel()` → for each channel: `set_channel`, then for each
AP: `associate(ap)`, then for each station: `deauth(ap, sta)` + `sleep(1)` (nexmon
firmware workaround) → `agent.next_epoch()` → if `grid.is_connected()`: fire
`internet_available` plugin event. On exception containing `"wifi.interface not
set"`, sleeps 60s then advances epoch (recovery path for a disabled Wi-Fi adapter);
all other exceptions are logged via `logging.exception` and the loop continues
(the process never crashes from a normal-loop exception).

`do_manual_mode` parses last session stats, then loops forever calling
`display.on_manual_mode(...)` + `sleep(5)`, firing `internet_available` when
online.

### `pwnagotchi/utils.py` (695 lines) — config, helpers, pcap/status utilities
- `parse_version(version)` — `tuple(version.split('.'))`, i.e. **string** tuple
  comparison, not numeric (so `"10" < "9"` as strings — must replicate exactly).
- `remove_whitelisted(handshakes, whitelist, valid_on_error=True)` — normalizes
  (`lower + alnum-only`) basenames stripped of trailing `.pcap` and whitelist
  entries, keeps only handshakes with no whitelist substring match.
- `download_file(url, destination, chunk_size=128)` — streaming `requests.get`,
  `raise_for_status()`, writes binary in chunks.
- `unzip(file, destination, strip_dirs=0)` — `zipfile.ZipFile`, optional path-prefix
  stripping when extracting (used by plugin installer).
- `merge_config(user, default)` — recursive dict merge: keys present in `user` win;
  missing keys are filled from `default`. **This exact precedence (user overrides
  default, deep-merged) is the config precedence contract.**
- `keys_to_str(data)` — recursively stringifies dict/list keys (used when migrating
  legacy YAML configs that may have non-string keys).
- `save_config(config, target)` — `tomlkit.dumps` write.
- `load_config(args)` — the config bootstrap, in order:
  1. `os.makedirs(dirname(args.config))` if missing.
  2. Locate `defaults.toml` next to the installed `pwnagotchi` package
     (`os.path.dirname(pwnagotchi.__file__)`).
  3. Migrate legacy boot configs: for each of `/boot/config.yml`,
     `/boot/firmware/config.yml`, `/boot/config.toml`, `/boot/firmware/config.toml`
     (checked in that order) — if found, merge into existing user config if present,
     then `shutil.move` it onto `args.user_config` and stop scanning (first match wins).
  4. If `/boot/firmware/pwnagotchi` directory exists: `rmtree('/etc/pwnagotchi')`
     then move the whole folder to `/etc/`. **Destructive — silently deletes
     `/etc/pwnagotchi`.**
  5. If `args.config` (default file) doesn't exist, copy the packaged
     `defaults.toml` there. Else, compare byte-for-byte against the packaged
     defaults; if different, **overwrite** the user's default file with the
     packaged one (defaults file is not user-editable state; drift is clobbered).
  6. `load_toml_file(filename)` helper: if the text contains `"[main]"`, parse with
     `tomlkit` (preserves formatting/comments); otherwise assumes a legacy
     *dotted* TOML (e.g. `main.name = "x"` flat keys) and parses with the plain
     `toml` library, then rewrites the file in `tomlkit` nested form, backing up
     the original as `<file>.ORIG`.
  7. Load defaults via `load_toml_file(args.config)`.
  8. Load user config: if `args.user_config` doesn't exist but a sibling
     `.yml` does, converts YAML→TOML (`yaml.safe_load` → `keys_to_str` →
     `tomlkit.dump`). Else if `args.user_config` exists, loads it. Then
     `config = merge_config(user_config, config)` (user wins).  Any exception in
     this block is logged and **`sys.exit(1)`**.
  9. Drop-in directory: `config['main']['confd']` (default
     `/etc/pwnagotchi/conf.d/`), globs `*.toml` inside it (only `.toml`, legacy
     `.yaml` no longer supported here), merges each in glob order **with drop-ins
     winning over what's accumulated so far** (`merge_config(additional_config,
     config)` — additional is the "user" argument to `merge_config`).
  10. Normalizes `config['ui']['display']['type']` through an enormous alias table
      (~100 `elif` branches) mapping every historical/aliased display-type string to
      its canonical key (e.g. `ws4`/`waveshare4`/`waveshare2in13v4` → `waveshare_4`).
      Any unrecognized type silently falls back to `dummydisplay` with a debug log
      (never a hard error) — this fallback must be preserved exactly, including
      being silent at `debug` level only.
  11. Returns the merged, normalized config dict.
- `secs_to_hhmmss(secs)` — `%02d:%02d:%02d` from `divmod` chain.
- `total_unique_handshakes(path)` — `len(glob.glob(path/*.pcap))`.
- `iface_channels(ifname)` — shells out to `/sbin/iw <ifname> info | grep wiphy | cut
  -d ' ' -f 2` to get the phy index, then `/sbin/iw phy<phy> channels | grep ' MHz' |
  grep -v disabled | sed ...` to extract enabled channel numbers. **Uses
  `subprocess.getoutput`, i.e. runs through `/bin/sh -c`** — a shell string built
  from an interface name that is only ever sourced from trusted config, not user
  input, but this is a real `sh -c` invocation to preserve if bit-for-bit parity of
  channel discovery is required (Go should use `os/exec` with argv where possible
  and only fall back to shell for the piped `grep|sed` if replicating the exact
  parsing is required — see known-differences.md).
- `WifiInfo` enum + `extract_from_pcap(path, fields)` — uses `scapy` to sniff a pcap
  file offline and extract BSSID/ESSID/ENCRYPTION/CHANNEL/FREQUENCY/RSSI from
  802.11 management frames. Raises `FieldNotFoundError` per-field on miss.
- `StatusFile` — a tiny status-cache class: reads a path (raw text or JSON) at
  construction, tracks mtime, exposes `newer_then_minutes/hours/days`, and
  `update()` which uses `pwnagotchi.fs.ensure_write` for atomic write.
- `md5(fname)` — chunked MD5 of a file.

### `pwnagotchi/identity.py` (70 lines) — `KeyPair`
RSA identity keypair, `DefaultPath = "/etc/pwnagotchi/"`.
- On construction: if `id_rsa`/`id_rsa.pub` missing, calls `view.on_keys_generation()`
  then **shells out**: `os.system("pwngrid -generate -keys '%s'" % path)` — an
  external Go binary (`pwngrid`) not part of this Python repo, must be treated as an
  external dependency/black box the Go port also needs to invoke identically.
  Loop-retries on any load exception (corrupted keys get deleted and regenerated).
- Loads PEM keys via `Crypto.PublicKey.RSA.importKey`; normalizes exported PEM
  header to always contain `RSA PUBLIC KEY` (pycryptodome sometimes exports plain
  `PUBLIC KEY`); base64-encodes the PEM for `pub_key_pem_b64`; fingerprint =
  `sha256(ascii pem).hexdigest()`, written to `fingerprint` file.
- `sign(message)` — SHA256 + PKCS1_PSS (`saltLen=16`) over the private key; returns
  raw signature and base64 signature. **Cryptographic primitive that must match
  bit-for-bit** (Go: `crypto/rsa` PSS with salt length 16, SHA-256) for pwngrid mesh
  interop.

### `pwnagotchi/automata.py` (142 lines) — `Automata` (mood/state-machine mixin)
Mixed into `Agent`. Holds `Epoch`. Mood transition rules (`next_epoch`):
stale→(angry if factor≥2.0 else lonely) / sad-for→(angry if factor≥2.0 else sad) /
bored-for→bored / active_for≥`excited_num_epochs`→excited / active_for≥5 and
`_has_support_network_for(5.0)`→grateful. `_has_support_network_for(factor)` =
`sum(peer.encounters)/bond_encounters_factor >= factor`. Fires `plugins.on('epoch',
...)` every epoch, and if `blind_for >= mon_max_blind_epochs`, logs critical and
calls `self._restart()` (which is `Agent._restart`, defined in `agent.py`).
`wait_for(t, sleeping)` fires `sleep`/`wait` plugin events, calls `view.wait`, tracks
epoch sleep time.

### `pwnagotchi/epoch.py` (244 lines) — `Epoch`
Per-epoch counters (`num_deauths`, `num_assocs`, `num_missed`, `num_shakes`,
`num_hops`, `num_slept`, `blind_for`, `sad_for`, `bored_for`, `active_for`,
`inactive_for`, bond factors, per-channel observation histograms sized
`wifi.NumChannels=233`). `observe(aps, peers)` builds normalized
`aps_histogram`/`sta_histogram`/`peers_histogram` (division by `len+1e-10` to avoid
div-by-zero) used as an ML-style observation vector (vestigial from the original
AI-driven pwnagotchi; retained for compatibility even though there is no trainer in
this fork). `next()` logs a single structured INFO line per epoch with an exact
format string (`"[epoch %d] duration=%s slept_for=%s blind=%d sad=%d bored=%d
inactive=%d active=%d peers=%d tot_bond=%.2f avg_bond=%.2f hops=%d missed=%d
deauths=%d assocs=%d handshakes=%d cpu=%d%% mem=%d%% temperature=%dC"`) — this exact
line format is parsed back by `log.py`'s `LastSession` regexes, so **the format
string is a serialization contract, not just a log message**.

### `pwnagotchi/bettercap.py` (118 lines) — `Client` (bettercap REST/WS client)
- `Client(hostname, scheme, port, username, password)`: builds `url =
  "%s://%s:%d/api"`, `websocket = "ws://%s:%s@%s:%d/api"` (credentials embedded in
  URL), `auth = HTTPBasicAuth`.
- `session(sess="session")` — `GET {url}/{sess}`, JSON-decoded via `decode()`.
- `decode(r, verbose_errors=True)` — on JSON parse failure: if HTTP 200, logs an
  error and returns raw text; else raises `Exception("error %d: %s" %
  (status, text.strip()))`.
- `start_websocket(consumer)` — connects `ws://.../api/events` with
  `ping_interval=15`, `ping_timeout=180`, `max_queue=10000`; on
  `ConnectionClosedError` tries one ping-pong keepalive before reconnecting with
  jittered sleep (`0.5 + 5.0*random()`); on `ConnectionRefusedError` retries with
  jitter; on bare `OSError` calls `pwnagotchi.restart("AUTO")` (i.e. a broken
  bettercap connection at the OS level triggers a full unit restart).
- `run(command, verbose_errors=True)` — `POST {url}/session {"cmd": command}`,
  retries forever on `requests.exceptions.ConnectionError` with jittered sleep.
- Module level: `requests.adapters.DEFAULT_RETRIES = 5`.

### `pwnagotchi/grid.py` (130 lines) — pwngrid-peer REST client
`API_ADDRESS = "http://127.0.0.1:8666/api/v1"` (local pwngrid-peer daemon, an
external Go binary). `is_connected()` — `GET https://api.opwngrid.xyz/api/v1/uptime`
with `User-Agent: pwnagotchi/<version>`, timeout `(30.0, 60.0)` (connect, read),
returns `True` only if `r.json().get('isUp')` — **any exception is swallowed and
treated as not-connected**. `call(path, obj=None)` — GET if `obj is None`, POST
JSON if dict, POST raw `data=` otherwise; non-200 raises. Endpoints:
`/mesh/{true,false}` (advertise), `/mesh/data` (GET/POST advertisement),
`/mesh/memory`, `/mesh/peers`, `/report/ap`, `/inbox?p=N`, `/inbox/<id>`,
`/inbox/<id>/<mark>`, `/unit/<to>/inbox`. `update_data(last_session)` shells out to
`uname -a`, `bettercap -version`, `pwngrid -version` via `subprocess.getoutput` to
build a telemetry payload POSTed to `/data` (also reads `/root/brain.json` if
present — vestigial AI artifact, tolerantly ignored if missing).

### `pwnagotchi/mesh/peer.py` (89 lines) — `Peer`
Wraps a pwngrid peer JSON object; RFC3339 timestamp parsing with a documented
`0001-01-01T00:00:00Z` zero-value sentinel treated as "now". Exposes
`is_good_friend`, `face`, `name`, `identity`, `pwnd_run/total`, `is_closer` (by RSSI).

### `pwnagotchi/mesh/utils.py` (112 lines) — `AsyncAdvertiser` (mixed into `Agent`)
Owns the peer-polling background thread (`_adv_poller`, daemon thread named
`"Grid"`, 20s initial delay then 3s poll loop calling `grid.peers()`), diffing
peer sets to fire `peer_detected`/`peer_lost` plugin events and `view` callbacks.
`start_advertising()` only runs if `personality.advertise` is true; sets a
`view.on_state_change('face', ...)` listener so face changes are re-broadcast into
the mesh advertisement blob.

### `pwnagotchi/mesh/wifi.py` (28 lines)
`NumChannels = 233`. `freq_to_channel(freq)` — MHz→channel for 2.4/5/6 GHz bands
(exact breakpoints documented in source; raises `ValueError` on out-of-range input).

### `pwnagotchi/log.py` (333 lines) — `LastSession` + logging setup
- `LastSession` parses the **plaintext log file** (not a structured format) backward
  (`FileReadBackwards`) looking for the previous session boundary (`'connecting to
  http'` token), extracting counts of deauth/assoc/handshake lines (dedup via a
  `cache` dict keyed by full line text), `[epoch N] k=v ...` lines (regex
  `^.+\[epoch (\d+)] (.+)` then `([a-z_]+)=(\S+)` sub-parser) to compute
  min/max/avg reward, and `"detected unit "` lines via a peer regex to reconstruct
  a synthetic `Peer`. **This means the on-disk log format is itself a serialization
  contract** — any change to log line wording anywhere in the codebase that matches
  these tokens changes `LastSession` parsing.
- `setup_logging(args, config)` — formatter `"[%(asctime)s] [%(levelname)s]
  [%(threadName)s] : %(message)s"`; sets root logger level (DEBUG if `args.debug`
  else INFO); performs custom log rotation (`log_rotation`) *before* opening file
  handlers (since Python's built-in rotating handlers would break concurrent
  session-log parsing); adds `FileHandler`s for `main.log.path` (INFO) and
  `main.log.path-debug` (DEBUG); when not debugging, disables scapy logger,
  silences urllib3/requests FutureWarning/DeprecationWarning noise.
- `log_rotation`/`parse_max_size`/`do_rotate` — size-based rotation
  (`main.log.rotation.size`, e.g. `"10M"`), gzips the rotated file, keeps
  incrementing `-N` suffix to avoid clobbering existing archives.

### `pwnagotchi/voice.py` (251 lines) — `Voice`
`gettext`-based i18n: loads `pwnagotchi/locale/<lang>/LC_MESSAGES/voice.mo` (over
190 locale directories bundled, see §5). Each `on_*` method returns a `random.choice`
among several flavor strings (or a single formatted string), passed through
`self._` (gettext). This is presentation text, not logic — a Go port needs an
equivalent `.mo`/`.po` consumption mechanism or a lookup table to preserve the exact
translated phrasing per language.

### `pwnagotchi/agent.py` (506 lines) — `Agent(Client, Automata, AsyncAdvertiser)`
The central orchestrator; see class docstring notes inline above for `Client`,
`Automata`, `AsyncAdvertiser` mixins. Key behaviors:
- `RECOVERY_DATA_FILE = '/root/.pwnagotchi-recovery'` — JSON recovery blob
  (`started_at`, `epoch`, `history`, `handshakes`, `last_pwnd`) written on
  reboot/restart (`_save_recovery_data`) and loaded once at event-poller startup
  (`_load_recovery_data`, deletes the file after a successful load).
- Constructor wires up `bettercap.Client` with config `bettercap.{hostname,scheme,
  port,username,password}` (defaults `127.0.0.1`/`http`/`8081`/`pwnagotchi`/`pwnagotchi`),
  creates `handshakes` dir if missing, starts `ui.web.server.Server` (Flask, in a
  background thread).
- `start()` — waits for bettercap API (`_wait_bettercap`, infinite retry loop),
  `setup_events()` (silences configured event tags via `events.ignore`), 
  `set_starting()`, `start_monitor_mode()` (waits for the monitor interface to
  appear in `session()['interfaces']`, optionally running `main.mon_start_cmd` via
  `self.run('!%s' % cmd)` — bettercap's `!` shell-command syntax), resets wifi
  settings (`wifi.interface`, `ap_ttl`, `sta_ttl`, `rssi.min`, handshake file path,
  `handshakes.aggregate false`), (re)starts the `wifi` bettercap module,
  `start_advertising()`. Then `start_event_polling()` (daemon thread `"Event
  Polling"`, asyncio event loop, bettercap websocket `_on_event` consumer),
  `start_session_fetcher()` (daemon thread `"Session Fetcher"`, 5s poll loop
  updating uptime/advertisement/peers/counters/handshakes UI state), `next_epoch()`,
  `set_ready()`.
- `_on_event(msg)` — async handler for bettercap websocket JSON events; re-emits
  every event as a plugin hook named `bcap_<tag with non-[a-z0-9_] replaced by _>`;
  specifically handles `wifi.client.handshake` (dedup key `"sta -> ap"`), resolving
  AP/station identity from the current `session()` snapshot, updating
  `_last_pwnd`, firing the `handshake` plugin event, updating handshake UI counters.
- `recon()`, `get_access_points()` (whitelist filtering by hostname/MAC prefix/MAC,
  drops open/unencrypted APs), `get_access_points_by_channel()` (grouped + sorted
  by population desc), `associate`/`deauth` (respect `personality.{associate,
  deauth}` toggles, per-target interaction cap `max_interactions`, optional
  throttle sleep, `_should_interact` skip-if-already-handshaked logic),
  `set_channel` (computed wait time based on whether a deauth/assoc happened on the
  previous channel — `hop_recon_time` vs `min_recon_time`).
- `_reboot()`/`_restart(mode)` — save recovery data then delegate to
  `pwnagotchi.reboot()`/`pwnagotchi.restart(mode)` (module-level, in `__init__.py`).

## 3. UI subsystem: `pwnagotchi/ui/*` (~900 lines core + ~4,300 lines of hw drivers)

- `ui/state.py` (59 lines) — `State`: thread-safe (`Lock`) dict of named widgets with
  change-tracking (`_changes`) and per-key listeners (`add_listener`), used so
  `View.update()` only redraws on real changes.
- `ui/components.py` (95 lines) — PIL-based drawable widgets: `Widget` (abstract),
  `Bitmap`, `Line`, `Rect`, `FilledRect`, `Text` (supports wrapped multi-line and a
  PNG-image mode with alpha-to-white flattening + optional color inversion),
  `LabeledValue` (label + value pair).
- `ui/colors.py` / `ui/faces.py` — identical face-string constant modules (`faces.py`
  is the canonical one imported everywhere; `colors.py` duplicates the same
  constants — appears to be dead/legacy code, confirm before deciding whether to
  port both or just `faces`). `faces.load_from_config(cfg)` overwrites module
  globals by uppercased key from `[ui.faces]` config, i.e. **faces are
  runtime-configurable via TOML, replacing the module's hardcoded emoticons.**
- `ui/fonts.py` (38 lines) — global `ImageFont.truetype` handles (`Bold`,
  `BoldSmall`, `BoldBig`, `Medium`, `Small`, `Huge`), sized in `init(config)` reading
  `ui.font.name` / `ui.font.size_offset`; `status_font()` builds a separate font
  object with the offset applied. **Requires the named TTF font
  (`DejaVuSansMono[-Bold]`) to be resolvable by the underlying font engine** (Go:
  needs an embedded/openable font file, not just a name string — Pillow resolves by
  fontconfig name, Go's `image/font` needs actual font bytes).
- `ui/view.py` (416 lines) — `View`: owns the `State`, canvas (`PIL.Image` mode `1`,
  i.e. 1-bit), invert handling (swaps the meaning of `BLACK`/`WHITE` module
  globals — **a genuinely global mutable used across the codebase**, not just an
  instance attribute), rotation, and the full catalog of `on_*` mood/event methods
  (`on_starting`, `on_manual_mode`, `on_new_peer`, `on_lost_peer`, `on_bored`,
  `on_sad`, `on_angry`, `on_motivated`, `on_demotivated`, `on_excited`, `on_assoc`,
  `on_deauth`, `on_miss`, `on_grateful`, `on_lonely`, `on_handshakes`,
  `on_unread_messages`, `on_uploading`, `on_rebooting`, `on_custom`, `on_shutdown`,
  `wait`). Each sets a random face (from `faces`) + a `Voice` string, then calls
  `update()`. `update()` composes the whole canvas only if something changed (or
  `force=True`), fires `ui_update` plugin hook, draws every state widget, saves the
  frame via `ui.web.update_frame` (PNG at `/var/tmp/pwnagotchi/pwnagotchi.png`,
  guarded by a lock), and invokes any registered render callbacks (used by
  `Display._on_view_rendered`). `ui.fps` config: if `>0`, a background thread
  refreshes on a timer (and animates a `█` cursor suffix on the `name` field if
  `ui.cursor` is true); if `0`, no periodic refresh — updates are purely
  event-driven and `uptime`/`name` changes are explicitly ignored to avoid
  needless partial e-paper refreshes.
- `ui/display.py` (347 lines) — `Display(View)`: resolves the concrete hardware
  driver via `hw.display_for(config)`, exposes ~90 `is_<hwname>()` predicate methods
  (used by plugins to branch on hardware), runs a dedicated `Renderer` daemon thread
  that blocks on an `Event` and calls `implementation.render(canvas)`
  non-blockingly relative to the UI-update thread (so slow e-paper refresh doesn't
  stall event processing), and — notably — executes an arbitrary configured shell
  command on every rendered frame: `os.system(config['ui']['web']['on_frame'])` if
  non-empty. **This is a user-configurable arbitrary command execution point.**
- `ui/hw/__init__.py` (377 lines) — `display_for(config)`: a giant if/elif dispatch
  from the normalized `ui.display.type` string (see `utils.load_config` alias table)
  to one of ~90 concrete driver classes, each lazily imported only when selected.
- `ui/hw/base.py` (43 lines) — `DisplayImpl` abstract base: holds `config`,
  `name`, and a `_layout` dict of widget positions (`face`, `name`, `channel`,
  `aps`, `uptime`, `line1`, `line2`, `friend_face`, `friend_name`, `shakes`, `mode`,
  `status`); subclasses must implement `layout()`, `initialize()`, `render(canvas)`,
  `clear()`.
- `ui/hw/dummydisplay.py` (44 lines) — the no-hardware fallback driver; computes a
  480×720-ish layout scaled from config `width`/`height`, all render/clear/init are
  no-ops. **This is the only driver with zero physical I/O — the natural first
  real Go implementation and the default for any unrecognized `ui.display.type`.**
- **~90 remaining `ui/hw/*.py` files** (waveshare*, wavesharelcd*, inky/inkyv2,
  papirus, oledhat, lcdhat, dfrobot(_v1/_v2), gfxhat, i2coled, pitft, minipitft(2),
  tftbonnet, displayhatmini, pirateaudio, gamepi15/20, spotpear24in/154lcd,
  argonpod, whisplay, adafruit2in13, weact_2in9) — each is a thin (43–110 line)
  adapter that imports a vendor Python driver library (Waveshare's `epdconfig`/`epd*`
  modules, Pimoroni's `inky`/`ST7789`/`gfxhat`, `luma.oled`, etc.), talks to the
  panel over **SPI and/or I2C and GPIO** (chip-select, reset, busy/DC lines), and
  implements the same four-method `DisplayImpl` contract. These vendor Python
  packages are not vendored into this repo (installed system-wide on the Pi image by
  `stage3/`); they are third-party, chip-specific, closed-protocol drivers. See
  `known-differences.md` for the porting strategy (interface + dummy + a small set
  of real Go SPI/I2C implementations; the rest documented as unimplemented with
  explicit unsupported errors, never fake success).

### `pwnagotchi/ui/web/*` (388 lines) — embedded Flask app
- `ui/web/__init__.py` (15 lines) — frame cache: `frame_path =
  '/var/tmp/pwnagotchi/pwnagotchi.png'`, `frame_lock` (thread lock), `update_frame(img)`
  saves the current canvas as PNG under the lock, creating the parent dir if needed.
- `ui/web/server.py` (55 lines) — `Server(agent, config)`: if `ui.web.enabled`,
  starts a daemon thread running a Flask app bound to `ui.web.address:ui.web.port`
  (default `::`/`8080`, i.e. listens on both IPv4/IPv6 by default), random
  `app.secret_key` (`secrets.token_urlsafe(256)`), optional CORS
  (`ui.web.origin`), always wraps with `flask_wtf.CSRFProtect`.
- `ui/web/handler.py` (318 lines) — `Handler`, registers routes:
  - `GET /css/theme.css` — dynamic CSS from `ui.web.theme.{accent_r,g,b}` (defaults
    76/175/80).
  - `GET /` (`index.html`), `GET /ui` (serves the PNG frame under `frame_lock`).
  - `POST /shutdown`, `POST /reboot`, `POST /restart` (form field `mode` ∈
    {AUTO,MANU}, default MANU) — each renders a status page then spawns a daemon
    thread calling `pwnagotchi.{shutdown,reboot,restart}`.
  - `GET /inbox`, `/inbox/profile`, `/inbox/peers`, `/inbox/<id>`,
    `/inbox/<id>/<mark>`, `/inbox/new`, `POST /inbox/send` — thin wrappers over
    `pwnagotchi.grid` inbox/mesh endpoints, rendering Jinja templates.
  - `GET|POST /plugins`, `/plugins/<name>`, `/plugins/<name>/<path:subpath>` — lists
    plugins; `name == "toggle"` (POST) calls `plugins.toggle_plugin`; `name ==
    "upgrade"` (POST) does **`os.system(f"pwnagotchi plugins update && pwnagotchi
    plugins upgrade {request.form['plugin']}")`** — a user-form-value interpolated
    directly into a shell string; otherwise dispatches to the named plugin's
    `on_webhook(subpath, request)` if present, `404` otherwise.
  - All routes except the theme CSS route go through `with_auth` (HTTP Basic,
    constant-time compare via `secrets.compare_digest`, gated by `ui.web.auth`).

## 4. Plugin system: `pwnagotchi/plugins/*` (679 lines core + 20,257 lines of bundled plugins)

- `plugins/__init__.py` (244 lines):
  - `Plugin` base class uses `__init_subclass__` to auto-register any subclass into
    the global `loaded` dict keyed by **the plugin's *module* name** (`cls.__module__
    .split('.')[0]`), and pre-creates a `threading.Lock` per discovered `on_*`
    method name (`locks["plugin::on_x"]`) — though the locks dict appears to be
    legacy/unused by the current dispatch path (dispatch now goes through
    `PluginEventQueue`, not these locks).
  - `PluginEventQueue(threading.Thread)` — one worker thread per plugin, backed by a
    `queue.Queue`; `AddWork(event_name, ...)` special-cases `"loaded"` to run
    `on_loaded` in its **own** dedicated thread (because many plugins use
    `on_loaded` as a blocking main loop), otherwise enqueues onto the shared
    per-plugin worker; the worker thread calls `prctl.set_name("PLG <name>")`
    (Linux `PR_SET_NAME`, visible in `ps`/`top`) and processes events serially
    per-plugin (so a slow/blocking plugin callback can only stall *that plugin's*
    events, not the whole daemon).
  - `on(event_name, *args, **kwargs)` — fan-out to every loaded plugin that defines
    `on_<event_name>`, dispatched async via each plugin's queue.
  - `one(plugin_name, event_name, ...)` — same but targeted at a single plugin (used
    right after enabling a plugin at runtime).
  - `load_from_file` — `importlib.util.spec_from_file_location` +
    `module_from_spec` + `exec_module` (arbitrary Python file execution — this *is*
    the plugin mechanism; no sandboxing).
  - `load_from_path(path, enabled)` — globs `*.py`, records every file found into
    `database` (even if not enabled — used for `list`/`upgrade` CLI), loads only
    those whose name is in the `enabled` set.
  - `load(config)` — loads `default_path` (`pwnagotchi/plugins/default/`) then
    `config['main']['custom_plugins']` (default
    `/usr/local/share/pwnagotchi/custom-plugins/`), assigns each loaded plugin's
    `.options` from `config['main']['plugins'][name]` (or `{}`), fires `loaded`
    then `config_changed` events.
  - `toggle_plugin(name, enable)` — runtime enable/disable used by the web UI;
    persists the change via `utils.save_config` to `/etc/pwnagotchi/config.toml`
    (hardcoded absolute path, not `args.user_config`).
- `plugins/cmd.py` (435 lines) — `pwnagotchi plugins <subcommand>` CLI: `search`,
  `list [-i/--installed]`, `update`, `upgrade [pattern]`, `enable <name>`,
  `disable <name>`, `install <name>`, `uninstall <name>`, `edit <name>` (opens
  `$EDITOR` on a temp TOML snippet of just that plugin's config, default editor
  `vim`). `update` downloads+unzips each URL in
  `main.custom_plugin_repos` into `/usr/local/share/pwnagotchi/available-plugins/`
  (after a DNS reachability check against `google.com`); `install`/`uninstall` copy
  into `main.custom_plugins` (default
  `/usr/local/share/pwnagotchi/installed-plugins/`); version/author are extracted
  from plugin source via regex on `__version__`/`__author__` literals (not imported
  — avoids executing untrusted code just to list it).
- `plugins/default/*.py` (24 files, 20,257 lines total) — see table below. These are
  **first-party, actively used, non-optional-in-spirit plugins** shipped with the
  daemon; several depend on Linux system services (`systemctl`), D-Bus
  (`bt-tether.py`), I2C hardware chips (`pisugarx.py`, `ups_lite.py`, `wittypi.py`),
  or GPIO (`gpio_buttons.py`). Given their size and system coupling, full 1:1 Go
  reimplementation of all 24 is not attempted in the initial port; the strategy is
  the tested Python-subprocess/IPC bridge described in `known-differences.md` and
  `final-port-report.md`, with a subset re-implemented natively in Go where the
  logic is self-contained (pure computation / simple HTTP) and testable without
  hardware.

| plugin file | lines | version | purpose (from `__description__`/inspection) |
|---|---|---|---|
| `bt-tether.py` | 5039 | 1.2.5 | Bluetooth NAP tethering via BlueZ/NetworkManager/D-Bus; largest plugin by far |
| `pisugarx.py` | 846 | 1.2 | PiSugar X UPS: I2C battery/RTC chip, web UI, auto-shutdown |
| `auto-tune.py` | 772 | 1.0.1 | Adjusts AUTO-mode personality parameters at runtime |
| `session-stats.py` | 739 | 0.2.0 | Persists per-session stats to disk, web UI |
| `webcfg.py` | 706 | 1.0.0 | Runtime config editor exposed over the web UI |
| `logtail.py` | 458 | 0.1.0 | Tails the log file for the web UI |
| `fix_services.py` | 480 | 1.0.1 | Restarts/fixes services on blindness/firmware crashes; disables for external adapters |
| `auto_backup.py` | 394 | 2.2 | Scheduled backups to a configurable location |
| `webgpsmap.py` | 404 | 1.4.0 | OpenStreetMap view of handshake GPS positions |
| `wigle.py` | 371 | (from `_version`) | Uploads WiFi observations to wigle.net |
| `pwnstore_ui.py` | 370 | 1.2.6 | Web UI plugin store (browse/install plugins) |
| `wpa-sec.py` | 286 | 2.1.2 | Uploads handshakes to wpa-sec.stanev.org |
| `ohcapi.py` | 229 | 1.1.0 | Uploads handshakes to OnlineHashCrack.com (API v2) |
| `memtemp.py` | 214 | 1.0.2 | CPU/mem/temperature widget |
| `switcher.py` | 150 | 0.0.1 | Generic cron-like task scheduler |
| `gps.py` | 164 | 1.0.1 | Tags handshakes with GPS coordinates (serial or gpsd) |
| `cache.py` | 119 | 1.0.0 | Caches AP info |
| `auto-update.py` | 264 | 1.1.1 | Checks/applies updates when online |
| `example.py` | 130 | 1.0.0 | Reference implementation of every plugin callback |
| `ups_lite.py` | 97 | 1.3.0 | UPS Lite v1.3 voltage indicator |
| `wittypi.py` | 75 | 1.0.0 | Witty Pi 4 L3V7 battery info |
| `pwncrack.py` | 81 | 1.0.0 | Converts pcap→hc22000, uploads to pwncrack.org |
| `gpio_buttons.py` | 50 | 1.0.0 | GPIO button input handling |
| `grid.py` | 155 | 1.1.0 | Reports identity + pwned-network list to the mesh grid |

## 5. Filesystem/Linux integration: `pwnagotchi/fs/__init__.py` (192 lines)

`ensure_write(filename, mode='w')` — atomic write context manager: `tempfile.mkstemp`
in the same directory, write+`flush`+`fsync`, then `os.replace` (atomic rename on the
same filesystem). This is the one atomic-write primitive used across the codebase
(`utils.StatusFile.update`, several plugins).

`setup_mounts(config)` / `MemoryFS` — implements `fs.memory.mounts.*` config: for
each enabled mount (`log`, `data` by default), builds a `zram`-backed or plain
`tmpfs` mount at `/run/pwnagotchi/disk/<basename>`, bind-mounts the real directory
onto it, periodically rsyncs (or `copy_tree`) between RAM and disk on a
configurable interval, all via **raw shell invocations through `os.system`**:
`mountpoint -q`, `modprobe zram`, writes to `/sys/class/zram-control/hot_add` and
`/sys/block/zram<N>/{comp_algorithm,disksize,mem_limit}`, `mke2fs -t <fstype>
/dev/zram<N>`, `mount --bind`, `mount --make-private`, `mount -t <fstype> -o
nosuid,noexec,nodev[,user=pwnagotchi] ...`, `rsync -aXv --inplace --no-whole-file
--delete-after ... >/dev/null 2>&1`, `sync`, `umount -l`. **This entire subsystem
requires root and real Linux mount namespaces/zram** — it is Linux-only,
privileged, and cannot be meaningfully emulated in tests; the Go port must
implement the real behavior behind an interface and return a clear "unsupported"
error on non-Linux or non-root execution, never fake success (per porting
constraints).

## 6. Locales: `pwnagotchi/locale/*` (190+ language directories)

Pure data (`.po`/`.mo` gettext catalogs) consumed only by `voice.py`. Not executable
code; the Go port needs a compatible i18n loader (either parse `.mo` directly or a
build step converting the same catalogs to a Go-embeddable format) to reproduce
translated voice lines per configured `main.lang`.

## 7. Build/deploy infra (documented, not ported)

- `Makefile` — `32bit`/`64bit` targets clone/build `pi-gen` against `config-32bit`/
  `config-64bit`; `update_langs`/`compile_langs` shell out to `scripts/language.sh`
  per locale directory.
- `stage3/*` — `pi-gen` custom stage: `00-pre-pwn` through `08-pwnstore`, each with
  `NN-run.sh` (host-side) / `NN-run-chroot.sh` (runs inside the target chroot via
  `pi-gen`'s `on_chroot`) — installs system packages, `bettercap`/`pwngrid`
  binaries, `nexmon` firmware/driver, `hcxtools`, patches, and the `pwnstore`. This
  is OS-image assembly, not application logic; no Go equivalent is produced.
- `scripts/*.sh` (+ `.bat`/`.ps1`) — standalone operator tools distributed to end
  users' host machines (not run on the Pi): `backup.sh`/`restore.sh` (scp-based
  config backup), `language.sh` (gettext catalog compile/update), `*_connection_share.*`
  (host-side USB/Bluetooth network-sharing setup for macOS/Linux/Windows/OpenBSD).
  Out of scope for the daemon port; noted for completeness only.

## 8. Cross-cutting concerns summary (for the feature matrix / port priorities)

- **External processes invoked** (via `os.system`, `subprocess.getoutput`, or
  bettercap's `!` syntax): `hostname`, `sync`, `halt`, `shutdown -r now`, `service
  bettercap restart`, `service pwnagotchi restart`, `touch
  /root/.pwnagotchi-{auto,manual}`, `pwngrid -generate -keys <path>`, `pwngrid
  -version`, `bettercap -version`, `uname -a`, `/sbin/iw ... info`, `/sbin/iw phy...
  channels`, `mountpoint -q`, `modprobe zram`, `mke2fs`, `mount [--bind|
  --make-private|-t ...]`, `umount -l`, `rsync`, `systemctl restart pwnagotchi`, `rm
  /root/.auto-update`, arbitrary `main.mon_start_cmd`/`main.mon_stop_cmd`
  (`/usr/bin/monstart`/`monstop` by default), arbitrary `ui.web.on_frame`, arbitrary
  `pwnagotchi plugins update && pwnagotchi plugins upgrade <form value>` from the
  web UI upgrade route, `$EDITOR` (default `vim`) from the plugin `edit` CLI
  command, `pwnagotchi -generate` (typo-safe: actual is `pwngrid`).
- **Network**: bettercap REST (`:8081`, HTTP Basic auth) + WebSocket events;
  pwngrid-peer REST (`127.0.0.1:8666`); `api.opwngrid.xyz` (mesh uptime check);
  `api.github.com` (release check); the embedded Flask web UI (`:8080` by default,
  optional Basic auth + CORS + CSRF); numerous plugin-specific outbound HTTP APIs
  (wigle.net, wpa-sec.stanev.org, onlinehashcrack.com, pwncrack.org, GitHub raw zips
  for plugin repos).
- **Files/paths**: `/etc/pwnagotchi/{default,config}.toml`, `/etc/pwnagotchi/conf.d/
  *.toml`, `/etc/pwnagotchi/{id_rsa,id_rsa.pub,fingerprint}`, `/etc/pwnagotchi/log/
  pwnagotchi[-debug].log`, `/etc/pwnagotchi/handshakes/*.pcap`, `/etc/hostname`,
  `/etc/hosts`, `/proc/{uptime,meminfo,stat}`, `/sys/class/thermal/thermal_zone0/
  temp`, `/sys/class/zram-control/*`, `/sys/block/zram*/*`, `/root/.pwnagotchi-
  {auto,manual}`, `/root/.pwnagotchi-recovery`, `/root/.pwnagotchi-last-session`,
  `/root/brain.json` (legacy, optional), `/root/.auto-update`,
  `/var/tmp/pwnagotchi/pwnagotchi.png`, `/run/pwnagotchi/disk/*`,
  `/usr/local/share/pwnagotchi/{available,installed}-plugins/`,
  `/usr/local/share/pwnagotchi/custom-plugins/`, `/boot/{,firmware/}config.{yml,
  toml}` (one-time migration sources).
- **Signals**: `SIGUSR1` → mode-aware in-place restart (registered in `cli.py`).
  No explicit `SIGTERM`/`SIGINT` handling in the Python code (relies on default
  Python/OS behavior + systemd for service lifecycle — the `pwnagotchi`/`bettercap`
  systemd services referenced by `service ... restart` are external to this repo).
- **Threads/async** (all daemon, non-joined — process-lifetime background work):
  `Session Fetcher` (5s poll), `Event Polling` (asyncio loop consuming bettercap
  websocket), `Grid` (peer polling, mesh advertiser), `UI Handler` (fps-driven
  refresh, only if `ui.fps>0`), `Renderer` (hardware display render), `WebServer`
  (Flask), one `PluginEventQueue` worker per active plugin (plus one extra
  short-lived thread per plugin's `on_loaded` invocation), `File Sys` (per
  configured RAM-disk mount, periodic sync).
- **Privileged actions**: hostname change, `/etc/hosts` rewrite, reboot/shutdown,
  service restarts, zram/mount management, RSA key generation via external binary,
  arbitrary configured shell commands (`mon_start_cmd`, `on_frame`), plugin
  self-update shelling back into `pwnagotchi` CLI.

This document is the reference for `feature-matrix.md`. Every file named above must
appear there with a status.
