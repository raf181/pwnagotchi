# Known Differences — Python vs. Go

The Python plugin bridge (`internal/pyplugin`) has been fully removed. All
23 bundled plugins + `example` have native Go ports on
`internal/pluginmanager` (see `docs/migration-ledger.md` and
`docs/plugin-development.md`); `internal/plugins/loader.go`, the
`compatibility-test` Makefile target, and the Python-vs-Go differential
tests that exercised the bridge (`tests/compat_*_test.go`,
`tests/plugins/compat_*_test.go`) have all been deleted along with it.
Nothing in this repository's normal build/test/CI path installs, invokes,
or depends on a Python interpreter. The bridge-specific divergences that
used to appear in this document (stub `agent`/`view`/`display` arguments,
the synchronous webhook/toggle RPC extension, the `pwnagotchi.config`
load-window global, streaming-webhook capture limits) no longer apply —
native plugins call real, non-stubbed `Capabilities` methods directly, so
that entire class of divergence is gone, not just undocumented. See
`docs/plugin-compatibility-matrix.md` for the current native-plugin
architecture and per-plugin verification status.

The `pwnagotchi/` Python source tree (including all 23 original bundled
plugin `.py` files) remains on disk, unexecuted, for the sole reason that
the display-hardware bus/driver work depending on parts of that tree is
explicitly out of scope for this migration by user request — see
`docs/migration-ledger.md` and the repository root `README.md`'s "Status
and known gaps" section.

This document records every place the Go port either (a) intentionally
replicates a real Python behavior that looks like a bug, or (b) cannot be
byte-identical to Python for a structural reason (library choice, language
semantics, filesystem/OS behavior). Nothing here is "fixed" silently — if a
divergence exists, it is listed.

## Intentionally-preserved Python quirks (bugs kept as behavior)

### `parse_version` / `--check-update` lexical string comparison
`pwnagotchi/utils.py:parse_version` returns a tuple of **strings**, not
integers (`'2.9.5.5'` → `('2','9','5','5')`). `cli.py --check-update` compares
these tuples with `>`, which is **lexical string comparison**: `'10' < '9'`,
so `parse_version('2.10.0') > parse_version('2.9.5.5')` is `False` — a real
newer release compares as *older*. `internal/config.CompareVersions`
replicates this exactly (see `TestCompareVersionsLexicalQuirk`). This is a
Python bug, but it is in-scope to preserve per the porting goal ("do not
remove difficult functionality"); a future Go-only build could fix it behind
a flag, but default behavior must match Python today.

### Boot-config install message: missing `%` operator
`utils.load_config` contains:
```python
print("installing new %s to %s ...", boot_conf, args.user_config)
```
This is missing the `%` operator, so it is `print()` called with three
positional arguments (`sep=' '`), **not** string formatting. The literal
`%s` placeholders are printed unsubstituted, followed by the actual paths,
space-separated: `installing new %s to %s ... /boot/config.toml
/etc/pwnagotchi/config.toml`. `internal/config.installBootConfig` builds this
exact string (see `TestLoadConfigBootMessageReproducesMissingPercentBug`)
rather than "fixing" it via real `%s` substitution.

### Boot-config `merge_config` call is dead code
The line right before the message above,
`merge_config(boot_conf, args.user_config)`, passes two **file path
strings** to a function whose whole body is guarded by
`isinstance(user, dict) and isinstance(default, dict)`. Since neither
argument is a dict, the guard fails, the function body never runs, and the
return value isn't even assigned. The only real effect of finding a boot
config file is the unconditional `shutil.move(boot_conf, user_config)`
immediately after — it **replaces** (not merges) any existing user config.
The Go port skips modeling the dead call entirely and only performs the
move (`pyShutilMove`), documented in `internal/config/load.go`.

### `remove_whitelisted` / handshake filename normalization
`os.path.basename(handshake).rstrip('.pcap')` is not "strip the `.pcap`
suffix" — Python's `str.rstrip(chars)` strips a trailing run of any
characters in the **set** `{'.', 'p', 'a', 'c'}`. So `"...pcap"` loses more
than the extension whenever the preceding characters happen to be in that
set (e.g. `"foo_bar.pcapa"` → `"foo_bar"`, and an all-cutset-chars name can
strip to `""`). `internal/config.normalizeHandshakeName` reproduces the
character-set strip precisely, verified against real Python output.

### `StatusFile.data_field_or` on raw-format data
Python's `data_field_or` does `if self.data is not None and name in
self.data:` then `return self.data[name]`. For `data_format="json"`,
`self.data` is a dict and this is a normal key lookup. For the default
`data_format="raw"`, `self.data` is a **plain string**, so `name in
self.data` is a substring test, and `self.data[name]` — indexing a string
with a non-integer key — raises `TypeError` in real Python. No bundled
plugin actually calls `data_field_or` on a raw-format `StatusFile` today, but
`internal/config.StatusFile.DataFieldOr` still reproduces the crash (as a
returned Go `error`) rather than silently returning a plausible value, since
"no production stubs / fake success" applies to edge cases too.

### `ensure_write` final file permissions
`tempfile.mkstemp` creates the scratch file `0600`, and `os.replace(tmp,
filename)` makes that scratch file *become* the target — so the final
file's permissions are always `0600`, **regardless of what the original
target's permissions were** (e.g. a world-readable `0644` status file
becomes `0600` after the first `update()`). `internal/fs.EnsureWrite` uses
`os.CreateTemp`, which defaults to the same `0600`, so this is naturally
replicated; `TestEnsureWriteCreatesAndReplaces` asserts it explicitly so a
future refactor doesn't accidentally "fix" it into permission-preserving.

### Display-type alias table: substring-of-a-bare-string branches
`utils.load_config`'s ~100-way `if/elif` chain normalizing
`config['ui']['display']['type']` has several branches written as
`elif config['ui']['display']['type'] in 'whisplay':` (also `oledhat`,
`lcdhat`, `spotpear24inch`, `spotpear154lcd`, `displayhatmini`,
`pirateaudio`, `gfxhat`, `pitft`, `argonpod`, `minipitft`, `minipitft2`,
`tftbonnet`, `waveshareoledlcd`, `i2coled`, `waveshare35lcd`,
`waveshareoledlcdvert`, `gamepi20`, `gamepi15`). Because the right-hand side
is a bare string (not a parenthesized tuple — missing a trailing comma),
Python's `in` operator does a **substring test against that literal**, not a
set-membership test. Consequences verified against the real interpreter
(`testdata/display_type_normalize_golden.json`):
- `config['ui']['display']['type'] == ""` matches the *first* such branch
  (`whisplay`) since `"" in "whisplay"` is `True`.
- Any short substring of the literal also matches, e.g. `"his"` also
  normalizes to `"whisplay"`.

`internal/config/displaytable_gen.go` (generated by
`scripts/gen_display_table.py` from `testdata/display_type_alias_table.json`)
encodes each branch's real match mode (`exactSet` vs. substring-of-literal)
and `internal/config.NormalizeDisplayType` evaluates them in the same order
as the Python source, so these edge cases resolve identically.

### `identity.KeyPair` public-key PEM: always X.509 body under an RSA-PKCS1 header
`identity.py` calls `pub_key.exportKey('PEM')` after loading a key from disk.
Regardless of the on-disk key's original format, pycryptodome's RSA
public-key PEM export always emits X.509 SubjectPublicKeyInfo (never PKCS1
`RSA PUBLIC KEY`), which means the `if 'RSA PUBLIC KEY' not in
self.pub_key_pem:` check in `identity.py` is **always true in practice** —
so the subsequent `.replace('PUBLIC KEY', 'RSA PUBLIC KEY')` always fires,
producing a PEM whose header/footer claim `-----BEGIN/END RSA PUBLIC
KEY-----` while the base64 body is still X.509 DER, not the PKCS1
`RSAPublicKey` DER the header implies. `internal/identity.exportPublicKeyPythonStyle`
reproduces this exactly (`x509.MarshalPKIXPublicKey` + literal string swap,
never `x509.MarshalPKCS1PublicKey`). Earlier in this migration this was
confirmed byte-for-byte against a real pycryptodome-generated key via a
dedicated Python-differential test; that test was deleted along with the
rest of the Python-comparison test infrastructure once the bridge was
removed (see the note at the top of this document) and has **not** been
replaced with an equivalent non-Python check — `internal/identity/identity_test.go`
today verifies internal round-trip consistency (generate/load,
regenerate-on-corruption, sign-produces-a-verifiable-signature) but no
longer cross-checks against real pycryptodome output. This is a real,
disclosed reduction in verification strength for this one behavior, not a
known behavior change — the encoding logic itself is unchanged.

Additionally, pycryptodome's `PEM.encode` does **not** emit a trailing
newline after the final `-----END ...-----` line, while Go's
`pem.EncodeToMemory` always does; `exportPublicKeyPythonStyle` strips it, since
the fingerprint is a hash over these exact bytes and a stray trailing `\n`
would silently produce a different (Go-only) fingerprint, breaking mesh
identity compatibility with real Python peers.

### `internal/automata` reads `internal/epoch`'s counters without a lock
`epoch.Epoch` guards its own counters with an internal mutex so `Track`/
`Next`/`Observe`/`Data` are individually race-free, but `internal/automata`
reads exported fields (`Epoch.InactiveFor`, `.ActiveFor`, `.NumMissed`,
`.BlindFor`, ...) directly, matching Python's own direct attribute access
(`self._epoch.inactive_for`, etc. — Python has no locking here either).
This is safe under the same condition Python's real deployment already
relies on: a single goroutine (the auto/manual-mode main loop, mirroring
`cli.py`'s `do_auto_mode`/`do_manual_mode`) drives all `Automata`/`Epoch`
mutation. It is not safe to call `Automata` methods from multiple goroutines
concurrently — the same restriction Python has always had, now made
explicit instead of implicit.

### `internal/bettercap` websocket auth: header instead of URL userinfo
Python's `websocket = "ws://%s:%s@%s:%d/api" % (username, password, ...)`
embeds Basic-auth credentials directly in the URL, which the `websockets`
library accepts and turns into a real `Authorization` header on the wire.
`gorilla/websocket`'s `Dialer` explicitly rejects any URL with userinfo
(`errMalformedURL`). `internal/bettercap.websocketDialTarget` strips the
userinfo and sets the equivalent `Authorization: Basic ...` header
directly, producing the identical bytes on the wire — bettercap's HTTP
server sees the same credentials either way. `Client.WebSocket` itself
still stores the field in Python's exact `ws://user:pass@host:port/api`
form (for parity/introspection); only the actual dial call transforms it.

### `internal/bettercap.Run`'s connection-error classification
Python's `requests` library raises a specific `requests.exceptions.ConnectionError`
for DNS failures, refused connections, and resets, distinct from
`requests.exceptions.Timeout` and other exceptions — `Client.run`'s retry
loop only catches the former. Go's `net/http` wraps all transport failures
generically; `internal/bettercap.isConnectionError` approximates Python's
classification by checking for `*net.OpError`/`*net.DNSError` in the error
chain. This is a best-effort mapping, not a guaranteed 1:1 match with every
edge case in `requests`' exception hierarchy — timeouts should propagate
(not retry) in both, and refused/reset/DNS-failure should retry in both,
but obscure transport errors on either side may be classified differently.

### `grid.advertise(enabled=False)` operator-precedence bug
```python
def advertise(enabled=True):
    return call("/mesh/%s" % 'true' if enabled else 'false')
```
`%` binds tighter than the conditional expression, so this parses as
`("/mesh/%s" % 'true') if enabled else 'false'`. `enabled=True` correctly
calls path `/mesh/true`; `enabled=False` calls the bare literal path
`'false'` — NOT `/mesh/false` — which becomes the URL
`{API_ADDRESS}false` (e.g. `http://127.0.0.1:8666/api/v1false`), a
malformed request against the real pwngrid-peer API. Verified against the
real interpreter's operator precedence. `internal/grid.Client.Advertise`
reproduces this exactly rather than "fixing" it to `/mesh/false`.

### `internal/grid`'s `(connect, read)` timeout approximation
Python's `requests.get(url, timeout=(30.0, 60.0))` sets a distinct connect
timeout (30s) and read timeout (60s, applied per socket-read operation
during the response, including body streaming). Go's `net/http` has no
equivalent per-read timeout; `internal/grid.NewClient` approximates it with
a dialer `Timeout` (connect) plus `Transport.ResponseHeaderTimeout` (time to
first response byte) and an overall `http.Client.Timeout` of connect+read as
a ceiling. A very slow (but not stalled) body stream that `requests` would
abort mid-read (each individual read exceeding 60s) might not be aborted at
the same point by Go's client. Not expected to matter in practice — grid
responses are small JSON bodies — but documented as a non-identical timeout
model.

### `mesh.Peer` timestamp-parse-failure fallback loses its "wrong type" quirk
```python
try:
    self.first_met = parse_rfc3339(obj.get('met_at', just_met))
    ...
except Exception as e:
    ...
    self.first_met = just_met   # just_met is a STRING, not a datetime!
```
On any timestamp parse failure, Python's fallback assigns the plain
formatted string `just_met` to fields that normally hold `datetime` objects
— a real dynamic-typing inconsistency: if any later code called a
`datetime` method on `first_met` after this fallback fired, it would raise
`AttributeError`. Go's `Peer.FirstMet`/`FirstSeen`/`PrevSeen` are statically
typed `time.Time`, so this exact inconsistency can't be reproduced; the Go
port parses `justMet` back into a `time.Time` on the fallback path instead
of leaving a raw string in a time-typed field. This is a case where the
port is *more* internally consistent than Python, not less — flagged here
because "preserve exact behavior" is otherwise the rule, and this is the
one place a straight port isn't possible without abandoning Go's type
system.

### `internal/voice` embeds locale catalogs instead of reading them from a Python install
Python's `Voice.__init__` computes `localedir` relative to
`pwnagotchi/__file__` — it only works because the Python package is
installed on disk next to `voice.py`. A Go binary has no such directory at
runtime, so `internal/voice` copies all 184 `LC_MESSAGES/voice.mo` files
into `internal/voice/locale/` (verbatim, unmodified bytes) and
embeds them via `go:embed`, making translation lookups self-contained. The
translated *strings* are byte-identical to what Python's `gettext` produces
from the same `.mo` files (verified for Italian in `voice_test.go`); only
the loading mechanism differs.

### `log.do_rotate`'s literal "gz"→"log" replace and the same-path first rotation
```python
archive_filename = os.path.join(base_path, "%s.gz" % name)
...
log_filename = archive_filename.replace('gz', 'log')
```
This is a literal substring replace of `"gz"` with `"log"` anywhere in the
path, not an extension-aware rename. On the FIRST rotation (no existing
`<name>.gz`), `archive_filename` is `<name>.gz`, so `log_filename` becomes
`<name>.log` — the SAME path as the original `filename` being rotated.
`shutil.move(x, x)` on an identical source/destination is a verified no-op
(confirmed against the real interpreter, not an exception), so the "move"
step does nothing, and the subsequent gzip-compress + `os.remove(log_filename)`
end up compressing and then deleting the *original* log file directly.
Only on the SECOND+ rotation (when `<name>.gz` already exists and the
counter advances to `<name>-2.gz` etc.) does `log_filename` differ from
`filename`, and the move actually relocates the file first.
`internal/logging.DoRotate` reproduces this exactly, including skipping the
rename when source and destination paths are identical (matching Go's own
`os.Rename` no-op-on-same-path behavior on Linux).

### `internal/logging.Logger` is not yet wired into other ported packages
As of this session, `identity`, `automata`, `epoch`, `bettercap`, `grid`,
`mesh`, and `session` all log via Go's stdlib `log` package (default
timestamp format, no level tag), NOT `internal/logging.Logger`'s
Python-matching `[asctime] [LEVELNAME] [threadName] : message` format. The
log *message content* in each of those packages matches Python's format
strings exactly (verified where a specific line format is a documented
contract, e.g. `epoch`'s `[epoch N] ...` line); what's still open is
routing all of them through a single `internal/logging.Logger` instance so
the *wrapper* (timestamp/level/thread prefix) matches too. This is real,
tracked outstanding work — see `docs/migration-ledger.md` — not a silent
gap.

### `wpa-sec` legacy sqlite state migration
The original `wpa-sec.py` tracked per-handshake upload status in a local
sqlite3 DB (`.wpa_sec_db`). The native Go port (`internal/wpasec`) uses a
JSON-persisted map instead (no cgo/SQL-driver dependency for one
`(path, status)` table — see the package's own doc comment) and, on first
native load, one-time migrates any existing `.wpa_sec_db` left behind by a
real prior Python install via the pure-Go, no-cgo `modernc.org/sqlite`
driver (`migrateLegacyDB()`), so upgrading from Python doesn't silently
re-upload every historical handshake. See `internal/wpasec/wpasec_test.go`
and `docs/plugin-compatibility-matrix.md`'s `wpa-sec` row.

### `Agent._fetch_stats`'s per-step exception granularity
Python wraps each of `_update_uptime`, `_update_advertisement`,
`_update_peers`, `_update_counters`, `_update_handshakes` in its OWN
`try/except`, logging a distinctly-worded
`"[agent:_fetch_stats] self.X: %s"` message per failing step — including a
real edge case where, if `self.session()` fails on the very first tick
(before `s` is ever assigned), the subsequent `self._update_uptime(s)` and
`self._update_advertisement(s)` calls raise `NameError: name 's' is not
defined` at the argument-evaluation step (neither function actually uses
its `s` parameter internally), producing two EXTRA cascading error log
lines beyond the original session error — while `_update_peers`/
`_update_counters`/`_update_handshakes` (which take no `s` argument) proceed
normally regardless. `internal/agent`'s `fetchStats` calls the same five
update steps every tick, but they're void-returning and internally
fail-safe (e.g. `updateUptime` just returns early if `unit.Uptime()`
errors) rather than each surfacing its own distinctly-worded log line, and
there is no Go equivalent of Python's "unassigned variable" state, so the
NameError cascade doesn't and can't occur. Net effect: on a real
`session()` failure, Go logs ONE error line (from the session call itself)
instead of Python's occasional three; the actual state updates that
happen or don't happen are equivalent either way (since the "cascading"
Python calls don't use their argument and wouldn't have updated anything
extra even if they'd succeeded).

### `LoadConfig`'s "copying ..." message source path
Python's `print("copying %s to %s ..." % (ref_defaults_file, args.config))`
names the real on-disk path to the packaged `pwnagotchi/defaults.toml`
(e.g. `/usr/lib/python3/.../pwnagotchi/defaults.toml`). The Go port embeds
`defaults.toml` into the binary via `go:embed` (see "internal/voice embeds
locale catalogs" above for the same rationale) — there is no on-disk source
path to name, so `internal/config.LoadConfig` prints the literal placeholder
`<packaged defaults.toml>` in that position instead. Every other part of the
message (and the actual file-copy behavior/target path) is unchanged.

### `display_for()` has no branch for "weact2in9" at all — a real upstream bug
`utils.load_config`'s display-type normalization accepts both `"weact2in9"`
and `"weact29in"` as aliases that both resolve to the canonical type
`"weact2in9"` (see `testdata/display_type_alias_table.json`). But
`pwnagotchi/ui/hw/__init__.py`'s `display_for()` — the function that turns
that normalized type string into an actual driver instance — has **no**
`elif` branch matching `"weact2in9"` anywhere in its ~94-branch chain, and
no trailing `else` clause. Read end-to-end
(`pwnagotchi/ui/hw/__init__.py`), confirming there is no dead/later branch
that catches it. The practical consequence in real Python: selecting this
display type in config causes `display_for()` to fall through and
implicitly `return None`; `Display.__init__` stores that `None` as
`self._display` without checking it; the daemon runs until the first time
anything calls a method on the display (e.g. the first `view.update()`),
at which point it crashes with `AttributeError: 'NoneType' object has no
attribute '...'`.

`internal/ui/hw.NewDriver` surfaces this as a named, documented error
(`hw.ErrNoDriverInPythonEither`) instead of either (a) silently returning
something that resolves, which would be LESS correct than Python since it
would hide a config mistake Python itself can't handle, or (b) crashing
opaquely the way Python does. This is is the one display type where "port
the bug" isn't the right call — there is no working Python behavior to
port, only a crash, so the Go port fails fast with a clear message instead.

## Structural differences (cannot be byte-identical, by design)

### TOML library: BurntSushi/toml instead of tomlkit/toml
Python uses `tomlkit` for the "modern" `[main]`-headed config format (which
preserves comments/formatting) and falls back to the `toml` package for
legacy dotted-key files. The Go port uses `github.com/BurntSushi/toml` for
both, since both source formats are valid TOML and the observable data
structure is what matters for config semantics. The one real gap: writing a
config back out (`SaveConfig`, or the dotted→bracketed rewrite in
`loadTOMLFile`) will not preserve comments or original key order the way
`tomlkit.dumps` does — Go's `toml.Encoder` writes map keys in whatever order
`range` yields them (effectively random for `map[string]interface{}`).
Human-authored configs are read correctly either way; only round-tripped
*written* files may reorder keys and drop comments.

### `StatusFile.Update` JSON key order
Same root cause: Python's `json.dump` preserves dict insertion order;
`encoding/json.Marshal` of a `map[string]interface{}` always emits keys in
sorted (alphabetical) order, because Go maps don't retain insertion order
once decoded. Values and structure are identical; on-disk key order can
differ from a byte-level diff against a Python-written file.

### conf.d dropin processing order
`glob.glob` order is filesystem-dependent in Python — the original code has
no explicit sort, so which dropin file's overlapping keys "win" when two
`conf.d/*.toml` files set the same key is itself not well-defined in
upstream Python (observed to often match directory/creation order on
ext4, not alphabetical). The Go port explicitly sorts dropin filenames
alphabetically (`internal/config.applyDropins`) for reproducibility. This is
a deliberate normalization of Python's own nondeterminism, not a new
divergence from any specific documented Python contract.

### `fs` package: real mount/zram commands via `os/exec`, not `os.system`
Python's `fs.py` shells out with `os.system(f"mount --bind {a} {b}")` —
string interpolation into a shell. `internal/fs.MemoryFS` uses
`exec.Command(name, args...)` with explicit argv (no shell, no injection
surface) for the equivalent operations (`mount`, `umount`, `mke2fs`,
`rsync`, `sync`, `modprobe`). Same executables, arguments, and ordering;
different (safer) invocation mechanism. Real mount/zram operations require
root and a real Linux kernel to exercise end-to-end, so `internal/fs`'s own
tests inject a fake `Runner` — the command construction and control flow are
verified, but "does a real zram device actually appear" is only verified on
real hardware (tracked as unverified-on-CI in `docs/migration-ledger.md`).

### CLI `--help` text formatting
Go's flag-parsing libraries do not wrap help text at the same column width
or with the same layout as Python's `argparse` (see
`docs/python-baseline.md` for the captured argparse output). The port's
`--help` covers the same flags with the same defaults and semantics, but the
exact character-by-character layout is not guaranteed to match unless a
custom help formatter is written.

### Web UI: CSRF scheme, template engine, and `/mesh/memory` shape
Three related, disclosed structural differences in `internal/web` (the
`ui/web/*.py` port):
- **CSRF**: Flask-WTF's `CSRFProtect` binds a token to the Flask session
  cookie (itself signed with a random per-process `app.secret_key`). Go has
  no Flask-WTF equivalent, so `internal/web/csrf.go` implements the
  standard double-submit-cookie pattern instead (random token in a cookie,
  echoed in every form's hidden field, compared constant-time on POST) —
  independently real CSRF protection, not weaker, but a different token
  lifecycle (survives across the cookie's lifetime rather than being tied
  to a server-side session).
- **Templates**: Jinja2's `{% extends %}`/`{% block %}` inheritance is
  ported to Go's `html/template` `{{block}}`/`{{define}}` mechanism
  (`internal/web/assets.go`'s `pageTemplate`). This is functionally
  equivalent (same data, same routes, same conditional logic) but was NOT
  byte-diffed against real Flask/Jinja output the way CLI text was —
  whitespace/formatting will differ even where content is identical.
- **`/mesh/memory` (the Peers page)**: the real pwngrid JSON shape isn't
  pinned down by an existing test (unlike `/mesh/peers`, which
  `internal/grid.Peers` already verifies returns a JSON array). Real
  Python's `peers.html` iterates `grid.memory()`'s result directly, dot-
  accessing `peer.fingerprint`/`peer.advertisement.*` — implying it's a
  list of records, but this was not independently confirmed against a
  real pwngrid instance in this session. `internal/web.normalizePeers`
  defensively accepts either a JSON array or an id-keyed object so the
  page degrades gracefully either way, rather than assuming and silently
  showing an empty list if wrong.

### Plugin toggle persists to a hardcoded config path, not `--user-config`
Real Python's `toggle_plugin` (and the native Go equivalent,
`internal/web.pluginToggle`) both persist an enable/disable flip via a
save to the hardcoded `/etc/pwnagotchi/config.toml` path, ignoring
whatever `--user-config` path was actually passed at daemon startup — a
real, if obscure, Python quirk (`internal/config.SaveConfig` writes to the
same hardcoded path deliberately, not "fixed" to respect the actual
startup flag). Native plugins receive a live-updated `Capabilities.Config`
map at `config_changed` immediately, with no bridge-era window/timing
concerns: every native plugin's `OnLoad`/`HandleEvent` sees exactly the
same shared `config.Map` the rest of the daemon does, updated in place by
`internal/web/webcfg.go`, the same way Python's `pwnagotchi.config`
module-global was *intended* to work but, per the bridge era's own
now-removed limitations, sometimes didn't.

### Plugin webhooks: no capture-whole-body limitation
The old Python subprocess bridge captured a plugin's entire `on_webhook`
response before returning it, which meant a handler streaming an
unbounded response (real Python's `logtail.py` `on_webhook(path="stream")`
is the one bundled plugin that did this) could never finish being
captured. Native Go plugins implement `WebhookHandler`/`RouteRegistrar`
against a real `*http.Request`/response writer in-process (see
`docs/plugin-development.md`), so this whole class of limitation is gone:
`internal/web/logtail.go`'s streaming tail-then-follow behavior uses a
real `http.Flusher`, no capture-then-respond step at all.

### UI text rendering: multi-line spacing bug (fixed) and residual rasterizer differences
See `docs/rendering-investigation.md` for the full pipeline trace. Summary:
a prior revision of `internal/ui/components.Text.Draw`'s multi-line loop
advanced each wrapped line by the font's design line-height metric
(`Metrics().Height`) instead of reproducing Pillow's actual formula
(glyph-bbox of `"A"` + a hardcoded default 4px `spacing`), causing wrapped
`status` text to visibly overlap/garble past its first line — this was the
literal cause of the reported "corrupted" Go UI text. Fixed via
`pilLineSpacing()`, regression-tested by `tests/visual/golden_test.go`
against a real-Python-rendered golden PNG. A residual ~4.5% pixel-level
mismatch remains and is NOT a bug: sub-pixel glyph-edge antialiasing noise
(FreeType vs. `golang.org/x/image/font` hinting) and one decorative face
glyph (`•`, rendered smaller/tighter by Python's hinter than Go's
supersample-then-threshold pipeline at the `huge` face size) — both
verified by direct pixel inspection, neither affects text legibility or
position.

## Unverified-without-hardware

See `docs/migration-ledger.md` for the full list of `internal/ui/hw/*`
drivers and other Linux/hardware integrations that return an explicit
"unsupported" error rather than fake success when the real hardware/kernel
support isn't present on the build/test machine — porting the ~92 real
driver implementations themselves (the registry/interface for all of them
already exists) is explicitly out of scope for the current phase of this
migration by user request, not an oversight; see the root `README.md`'s
"Status and known gaps" section.

This build/test machine does have a real MediaTek MT7612U USB Wi-Fi
adapter and a real running `bettercap` instance, so two items are not in
this bucket: `internal/config.IfaceChannels` and
`internal/bettercap.Client.Session()` are verified against real
hardware/services (`tests/live_hardware_test.go`, `-tags=live`, opt-in
only, never part of `go test ./...`). No `ui/hw` display driver has real
e-ink/OLED hardware to verify against in this rig, so those remain
unverified. The live-hardware suite deliberately never touches
`internal/unit.SetName`/`Restart`/`Reboot` or any other action that could
reboot the host mid-test-run — see `docs/migration-ledger.md`.

**Evidence this rig genuinely has no display hardware to test against**
(not just an assumption): `uname -a` shows a generic x86_64 kernel
(`7.0.14-5-pve`, a Proxmox VM/container), not a Raspberry Pi —
`/proc/device-tree/model` doesn't exist at all (that path only exists on
device-tree-booted ARM boards). There is no `/dev/spidev*` and no
`/dev/i2c-*` device node present — the e-ink/OLED drivers this port's
`ui/hw` package targets all talk to real panels over SPI or I2C, and
without those device nodes there is no bus for a real panel to be
attached to even hypothetically, let alone a real panel present. The only
`i2c_*` kernel modules loaded (`i2c_i801`, `i2c_smbus`) are the generic
x86 SMBus controller driver, unrelated to any display. This is the
concrete basis for "hardware unavailable" here, not an assumption — and
it's exactly the situation the porting goal's own done-criteria carve-out
anticipates ("hardware...integrations are implemented or clearly marked
unverified when hardware is unavailable").
