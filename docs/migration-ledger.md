# Migration ledger — Python → Go

> **Historical record.** This chronological ledger intentionally preserves
> claims and counts from earlier repository states. Use
> [the documentation index](README.md), [feature matrix](feature-matrix.md),
> and current source code for operational status.

Snapshot as of 2026-07-24, branch `fix-internal-antenna`. This is the
machine-checkable inventory required by `GO_ONLY_MIGRATION_PROMPT.md` §1: every
tracked Python file and Python-owned non-code asset, mapped to its current
disposition. Re-derive counts with `git ls-files` before trusting this file if
it's more than a few sessions old — it is a living ledger, not a one-time
report.

Disposition values used below:
- `native-go-replacement` — a real, tested Go implementation exists and the
  Python file is dead weight once the tree is deleted.
- `moved-asset` — non-code data (font, image, license, template, locale
  catalog) that must be copied/embedded into a Go-owned location, not
  reimplemented.
- `deleted-dead-code` — genuinely unused; safe to drop with no replacement.
- `bridged` — currently still executed for real via the Python subprocess
  bridge (`internal/pyplugin`). This is **not** a Go-only end state; every row
  marked `bridged` is open work, tracked as such.
- `not-yet-ported` — no Go implementation exists yet.

## Contradictions found in existing docs (resolve during the doc rewrite, task #13)

1. **`internal/wpasec/wpasec.go:71`** sets
   `DBPath = "/etc/pwnagotchi/.wpa_sec_go_db.json"`, a brand new path. The real
   Python `wpa-sec.py` (lines 38/76/97) uses `sqlite3.connect('/etc/pwnagotchi/.wpa_sec_db')`.
   `GO_ONLY_MIGRATION_PROMPT.md` §3 explicitly requires migrating/reading the
   existing sqlite file "so upgrades do not lose upload status; do not strand
   it in a new incompatible JSON file without migration" — that is exactly
   what the current code does. `plugin-compatibility-matrix.md:158` documents
   the JSON-file design as if it were simply an intentional structural choice
   ("uses a JSON-persisted map instead of real sqlite3") without mentioning
   the missing migration path. **This is a real upgrade-safety bug, not just
   a stale doc.** Needs a startup migration (read the sqlite DB once if the
   JSON file doesn't exist yet, or read both and merge) before this can be
   called done. Filed under task #4 (plugin ports).
2. **`feature-matrix.md:88`** and **`plugin-compatibility-matrix.md`** both
   describe all 23 bundled plugins as "fully inventoried and tested" and use
   the status label "Bridged (fully inventoried and tested)" in a way that
   reads as completion. Per `GO_ONLY_MIGRATION_PROMPT.md`'s actual definition
   of done, running through `internal/pyplugin`'s Python subprocess is
   explicitly **not** an acceptable end state — every plugin must become
   native Go (task #4). The docs are not factually wrong (bridged+tested is
   an accurate description of current behavior) but their framing
   contradicts the spec's finish line and must be reworded during task #13 to
   avoid reading as "done."
3. **`internal/ui/hw/registry.go:10-26`** (`ErrNoDriverInPythonEither`)
   documents `weact2in9` as a genuine upstream Python bug (no `display_for()`
   branch exists for it in real Python either) and treats surfacing a named
   error as *more* correct than Python's silent `None`/crash. This is a
   reasonable engineering call, but `GO_ONLY_MIGRATION_PROMPT.md` §4
   literally says "add the missing `weact2in9` path" as a requirement. These
   two are in tension: the current code satisfies "don't preserve a
   vulnerability/bug merely for parity" but not the literal instruction to
   add the path. Left as an open decision for task #7 — most likely
   resolution is to still add a real `weact2in9` driver (the physical
   hardware exists and is sold under that name) while keeping the documented,
   tested error for the specific alias-without-driver edge case, rather than
   treating the upstream bug as an excuse to skip the hardware.
4. `go-port/deploy/README.md` does not exist as of this snapshot (checked,
   absent) — `GO_ONLY_MIGRATION_PROMPT.md` §1 lists it as a file to
   reconcile; there is nothing to reconcile yet. Note for task #13: if it's
   created later, audit it then.
5. `final-port-report.md` (lines ~229-230) itself already admits "large prior
   'make the Go port primary' claims predate this session" — i.e. the
   contradiction-cleanup this ledger performs was already flagged as needed
   by a previous session. This ledger is the first artifact that actually
   enumerates every file rather than describing the port qualitatively.

## Core daemon (`pwnagotchi/*.py`, 12 files)

| Path | Disposition | Go destination | Evidence |
|---|---|---|---|
| `__init__.py` | native-go-replacement | `internal/unit/unit.go`, `internal/unit/lifecycle.go` | `internal/unit/unit_test.go`, `lifecycle_test.go` |
| `_version.py` | native-go-replacement | `internal/version/version.go` | `internal/version/version_test.go` |
| `agent.py` | native-go-replacement | `internal/agent/*.go` | `internal/agent/*_test.go` |
| `automata.py` | native-go-replacement | `internal/automata/automata.go` | `internal/automata/automata_test.go` |
| `bettercap.py` | native-go-replacement | `internal/bettercap/client.go` | `internal/bettercap/client_test.go` |
| `cli.py` | native-go-replacement | `cmd/pwnagotchi/main.go`, `internal/cli/cli.go` | `internal/cli/cli_test.go`, `tests/compat_cli_test.go` |
| `epoch.py` | native-go-replacement | `internal/epoch/epoch.go` | `internal/epoch/epoch_test.go` |
| `grid.py` | native-go-replacement | `internal/grid/grid.go` | `internal/grid/grid_test.go` |
| `identity.py` | native-go-replacement | `internal/identity/identity.go` | `internal/identity/identity_test.go`, `tests/compat_identity_test.go` |
| `log.py` | native-go-replacement | `internal/session/lastsession.go`, `internal/logging/logging.go`+`rotation.go` | `internal/session/lastsession_test.go`, `internal/logging/rotation_test.go` |
| `utils.py` | native-go-replacement (except `extract_from_pcap`) | `internal/config/*.go` | `internal/config/*_test.go` |
| `utils.py::extract_from_pcap` | not-yet-ported | none | Only real callers today are `grid.py`/`wigle.py`, themselves still bridged. **Must** be ported to pure Go once `grid`/`wigle` go native (task #4) — `final-port-report.md`'s "out of scope, not needed" framing is only valid while those two plugins remain bridged; it becomes required work the moment they're ported. |
| `voice.py` | native-go-replacement | `internal/voice/voice.go` | `internal/voice/voice_test.go` |

## Filesystem (`pwnagotchi/fs/__init__.py`, 1 file)

| Path | Disposition | Go destination | Evidence |
|---|---|---|---|
| `fs/__init__.py` | native-go-replacement | `internal/fs/memoryfs.go`, `setup.go` | `internal/fs/*_test.go` (now hermetic — see task #1, fixed this session) |

## Mesh (`pwnagotchi/mesh/*.py`, 3 files)

| Path | Disposition | Go destination | Evidence |
|---|---|---|---|
| `mesh/peer.py` | native-go-replacement | `internal/mesh/*.go` (Peer type) | `internal/mesh/*_test.go` |
| `mesh/utils.py` | native-go-replacement | `internal/mesh/*.go` | `internal/mesh/*_test.go` |
| `mesh/wifi.py` | native-go-replacement | `internal/mesh/*.go` | `internal/mesh/*_test.go` |

(`mesh/__init__.py` is an empty package marker — deleted-dead-code.)

## UI core (`pwnagotchi/ui/*.py` top-level, 7 files + web, 3 files)

| Path | Disposition | Go destination | Evidence |
|---|---|---|---|
| `ui/colors.py` | native-go-replacement | `internal/ui/view` (color constants) | `internal/ui/view/view_test.go` |
| `ui/components.py` | native-go-replacement | `internal/ui/components/components.go` | `components_test.go` |
| `ui/display.py` | native-go-replacement | `internal/ui/display/display.go` | `display_test.go` |
| `ui/faces.py` | native-go-replacement | `internal/ui/faces/faces.go` | `faces_test.go` |
| `ui/fonts.py` | native-go-replacement | `internal/ui/fonts/fonts.go` | `fonts_test.go` |
| `ui/state.py` | native-go-replacement | `internal/ui/state/state.go` | `state_test.go` |
| `ui/view.py` | native-go-replacement | `internal/ui/view/view.go`, `mood.go`, `elements.go` | `view_test.go` |
| `ui/web/__init__.py` | native-go-replacement | `internal/web/server.go` | `server_test.go` |
| `ui/web/handler.py` | native-go-replacement | `internal/web/handler.go`, `handler_plugins.go` | `server_test.go` |
| `ui/web/server.py` | native-go-replacement | `internal/web/server.go` | `server_test.go` |

Web static assets (37 files: `css/*.css` ×4, `fonts/*` ×4, `images/pwnagotchi.png`,
`js/*.js` ×5, `svg/*.svg` ×7, `templates/*.html` ×9 → renamed `.tmpl`) — all
**moved-asset**, byte-copied/re-embedded under `go-port/internal/web/static/`
and `go-port/internal/web/templates/`. Verified identical directory listing
between `pwnagotchi/ui/web/static|templates` and
`go-port/internal/web/static|templates` (diff is filenames only:
`.html`→`.tmpl`). Evidence: files exist, embedded via Go `embed.FS` in
`internal/web/assets.go` (not independently content-diffed by this ledger
pass — spot-check recommended before Python tree deletion).

## Display drivers (`pwnagotchi/ui/hw/*.py`, ~94 top-level files)

**Disposition: not-yet-ported for real hardware I/O.** All ~95 registered
type strings (verified: `registeredTypes` in
`go-port/internal/ui/hw/registry_gen.go` has 95 entries, `pythonClassNames`
map has 189... — the map lines include both key and value so 95 distinct
types) resolve via `internal/ui/hw/registry.go`'s `NewDriver`:
- `dummydisplay` → real native Go (`internal/ui/hw/dummy.go`) — the only
  fully implemented display today.
- `weact2in9` → `ErrNoDriverInPythonEither`, a documented/tested upstream-bug
  passthrough, not a driver (see contradiction #3 above).
- every other registered type (waveshare* ~80 files, inky/inkyv2, whisplay,
  dfrobot/dfrobot1/dfrobot2/dfrobot_v2, papirus, gamepi15/20, gfxhat,
  i2coled, minipitft/minipitft2, oledhat, pirateaudio, pitft, spotpear154lcd,
  spotpear24in, tftbonnet, argonpod, adafruit2in13, displayhatmini) →
  `unsupportedDriver{name, cfg}` — `Initialize`/`Render`/`Clear` all return a
  clear "unsupported" error, never fake success. This matches
  `feature-matrix.md`'s "Interface-only" status honestly, but is explicit
  **open work**, task #7.
- `Layout()` (dimensions/aliases) is real for every driver today
  (AST-extracted from the real Python source per `feature-matrix.md`), only
  the bus I/O (`Initialize`/`Render`/`Clear`) is missing.

Vendor transport libraries under `pwnagotchi/ui/hw/libs/**` (223 files across
`adafruit/` (7), `argon/` (1), `dfrobot/` (v1: 5, v2: 11, plus LICENSE + 2 BMP
+ readme.md + 2 TTF = moved-asset), `fb/` (2), `i2coled/` (2), `papirus/` (3),
`pimoroni/` (gfxhat 6, inkyphat 4, inkyphatv2 4, displayhatmini 1,
pirateaudio 1), `waveshare/` (epaper: ~130 across ~65 model dirs, lcd: ~30
across 12 model dirs, oled: ~9 across 2 model dirs), `weact/` (2),
`whisplay/` (1)) are **not-yet-ported** — they are the actual SPI/I2C
bit-banging/init-sequence/LUT code each top-level driver wraps. Task #6 (bus
abstractions) must land first; task #7 ports these by controller family,
reusing shared LUT/init-sequence logic rather than 1:1 file transcription.

DFRobot non-code assets to preserve as **moved-asset** before Python tree
deletion: `pwnagotchi/ui/hw/libs/dfrobot/LICENSE`,
`v2/display_extension/logo_colorbits{1,24}.bmp`,
`v2/display_extension/{wqydkzh,zkklt}.ttf`,
`v2/display_extension/readme.md`. None of these are yet present anywhere
under `go-port/` (checked: no matches for `wqydkzh`/`zkklt`/`logo_colorbits`
under `go-port/`) — **currently unaddressed**, flag for task #7.

## Plugins (`pwnagotchi/plugins/default/*.py`, 24 files + `webgpsmap.html`)

| Plugin | Disposition | Go destination | Notes |
|---|---|---|---|
| `logtail.py` | native-go-replacement (web routes only) | `internal/web/logtail.go` | Plugin file itself still also separately loadable via the bridge (dual-path) — bridge path is dead weight once task #4 finishes; `internal/web/server_test.go` |
| `webcfg.py` | native-go-replacement | `internal/web/webcfg.go` | same dual-path note; `internal/web/webcfg_test.go` |
| `wpa-sec.py` | native-go-replacement, **upgrade-migration missing** | `internal/wpasec/wpasec.go` | See contradiction #1 — `DBPath` is a new JSON file, real sqlite `.wpa_sec_db` is never read/migrated. `internal/wpasec/wpasec_test.go` covers upload/error-handling but not migration (there is no migration code to test) |
| `auto-tune.py`, `auto-update.py`, `auto_backup.py`, `bt-tether.py`, `cache.py`, `example.py`, `fix_services.py`, `gpio_buttons.py`, `gps.py`, `grid.py`, `memtemp.py`, `ohcapi.py`, `pisugarx.py`, `pwncrack.py`, `pwnstore_ui.py`, `session-stats.py`, `switcher.py`, `ups_lite.py`, `webgpsmap.py` (+ `webgpsmap.html`), `wigle.py`, `wittypi.py` | bridged | none — executed for real via `internal/pyplugin` bridge (`internal/plugins/loader.go`) | 21 files, all open work under task #4. `webgpsmap.html` (moved-asset once `webgpsmap.py` is ported — currently still Python-served, not yet copied anywhere under `go-port/`) |

`defaults.toml` config-only sections with no matching bundled source file:
`gps_listener` (line 62), `pwndroid` (line 83), `ups_hat_c` (line 105) — still
present, unresolved, per original audit. Task #4 must decide
port-vs-deprecate for each with an explicit migration note, not leave them
silently dangling.

Plugin package manager: `plugins/cmd.py` → **native-go-replacement**,
`internal/plugins/cmd.go` (`internal/plugins/cmd_test.go`) — but it is a
**Python-file** manager (scans/downloads/installs `*.py`), and
`defaults.toml`'s plugin repositories are Python repos. This whole subsystem
needs replacing per task #5, not just the file having a Go equivalent that
still deals in `.py` artifacts.

## Locale (`pwnagotchi/locale/**`, 186 language dirs)

- `.po`/`.pot` editable sources: 185 files. **Disposition: not-yet-ported.**
  Confirmed via `find go-port -iname '*.po' -o -iname '*.pot'` → zero matches
  anywhere in the Go tree. This directly confirms the original audit finding;
  task #10 is real, open work, not stale.
- `.mo` compiled catalogs: 184 files. **Disposition: moved-asset, done.**
  All 184 present under `go-port/internal/voice/locale/<lang>/LC_MESSAGES/voice.mo`,
  embedded and cross-checked (`internal/voice/voice_test.go`).
- `pwnagotchi/locale/__init__.py`: deleted-dead-code (empty package marker).
- `pwnagotchi/locale/voice.pot`: not-yet-ported, same as the `.po` files —
  this is the source-of-truth template `.po` files are derived from; must
  live in the Go-owned locale tree per task #10, not be lost when the Python
  tree is deleted.

## Generators (Python scripts, now under `go-port/scripts/`, 4 files)

| Path | Disposition | Notes |
|---|---|---|
| `go-port/scripts/gen_display_table.py` | not-yet-ported | Still Python, still required to regenerate `internal/ui/hw/registry_gen.go`/`layouts_gen.go`. Task #8. |
| `go-port/scripts/gen_display_is_methods.py` | not-yet-ported | Generates `internal/ui/display/is_methods_gen.go`. Task #8. |
| `go-port/scripts/gen_hw_registry.py` | not-yet-ported | Task #8. |
| `go-port/scripts/gen_hw_layouts.py` | not-yet-ported | Task #8. |

These are the only 4 Python files that live *inside* `go-port/` alongside
`bridge.py` (`internal/pyplugin/bridge.py`), `oracle.py`
(`tests/visual/oracle.py`), and `check-plugins.py`
(`deploy/scripts/check-plugins.py`) — 7 total Python files inside the
supposedly-Go module, confirmed via `git ls-files '*.py' | grep -v '^pwnagotchi/'`.

## Test/compat infra (Python files inside `go-port/`, 3 files)

| Path | Disposition | Notes |
|---|---|---|
| `internal/pyplugin/bridge.py` (+ `bridge.go`) | bridged (this IS the bridge) | Task #9 removes it entirely once task #4 finishes. |
| `tests/visual/oracle.py` | not-yet-ported | Visual regression oracle; task #9 replaces with immutable `legacy_golden*` fixtures. |
| `deploy/scripts/check-plugins.py` | not-yet-ported | CI plugin-load check; task #12 replaces with a native `pwnagotchi plugins doctor` command or Go test binary. |

## Packaging (non-Go files)

| Path | Disposition | Notes |
|---|---|---|
| `pyproject.toml` (root) | not-yet-ported | Root Python package metadata; deleted once daemon no longer needs `pip install .`. Task #12. |
| `deploy/pi-gen-stage/04-install-pwnagotchi/00-packages` | not-yet-ported | Installs Python/pip/Pillow/dbus/prctl/toml bindings; task #12 strips this to the Go binary only. |

## Summary

- Python files inventoried: 379 tracked (`git ls-files '*.py' | wc -l`), all
  accounted for above (372 under `pwnagotchi/` + 7 under `go-port/`).
- Non-`.py` Python-owned assets inventoried: web static (37), locale `.po`/`.pot`
  (185) + `.mo` (184) + `__init__.py` already counted, DFRobot assets (6:
  LICENSE, 2 bmp, 2 ttf, readme.md), `webgpsmap.html` (1), `defaults.toml`
  (config file, stays — not Python-owned, just references Python plugin
  repos), root `pyproject.toml` (1).
- Disposition counts (approximate, by file — driver/lib families counted as
  groups above): native-go-replacement ≈ 130 files (core 12, fs 1, mesh 3, ui
  10, plugins 3, .mo locale 184 counted separately as moved-asset — core
  count here is code files only); moved-asset ≈ 225 (web static 37, .mo 184,
  DFRobot assets 6); bridged = 21 plugin files (open work); not-yet-ported ≈
  323 (94 top-level drivers + 223 lib files + 185 .po/.pot + voice.pot + 4
  generators + oracle.py + check-plugins.py + extract_from_pcap +
  pyproject.toml + packages file — note some files counted in multiple
  concern-areas above by design, this is a coverage ledger not a strict
  partition); deleted-dead-code = 2 (`mesh/__init__.py`,
  `locale/__init__.py`).
- **No row is silently omitted.** Every path returned by
  `git ls-files 'pwnagotchi/*' '*.py'` plus the 7 in-`go-port` Python files
  plus every non-`.py` asset directory found via `find`/`git ls-files` this
  session is represented above, either individually or as a named,
  file-counted group.

## Progress since this ledger was generated (same session)

Recorded here rather than by editing the snapshot rows above, so this stays
an honest point-in-time document — re-run the `find`/`git ls-files` commands
this file describes before trusting exact counts again.

- **Contradiction #1 (wpa-sec legacy DB) fixed, not just flagged.**
  `internal/wpasec/wpasec.go` now has `migrateLegacyDB()`: on first native
  load, if the native JSON `DBPath` doesn't exist yet, it opens the real
  legacy sqlite file at `LegacyDBPath` (`/etc/pwnagotchi/.wpa_sec_db`)
  read-only via the pure-Go `modernc.org/sqlite` driver (no cgo — verified
  cross-compiling `CGO_ENABLED=0 GOOS=linux GOARCH=arm64` still produces a
  static ELF binary), copies every `(path, status)` row, and persists it to
  the native JSON DB. A genuinely fresh install (no legacy file) is
  unaffected; an already-migrated install doesn't re-read the legacy file on
  every boot. Covered by
  `TestMigratesLegacyPythonDatabaseOnFirstLoad`/`TestNoMigrationWhenLegacyDBAbsent`/
  `TestNativeJSONDatabaseTakesPrecedenceOverLegacy` in
  `internal/wpasec/wpasec_test.go`.
- **Native plugin manager built**: `go-port/internal/pluginmanager` (task #3
  in `GO_ONLY_MIGRATION_PROMPT.md` §3) — per-plugin bounded serial event
  queue, panic isolation, typed `Capabilities` (not an `interface{}` bag),
  `Load`/`Unload`/`Toggle`/`List`/`Webhook`, all covered by
  `manager_test.go` (17 tests, race-clean). `wpa-sec` is folded into it via
  `EmitterPlugin` (see `cmd/pwnagotchi/main.go`) instead of the old ad hoc
  `multiEmitter` fan-out. `internal/web`'s `/plugins` index, toggle, and
  webhook routes now check the manager first and only fall back to the
  Python bridge for a plugin name the manager doesn't know about yet — this
  is the exact seam the remaining 22 bundled-plugin ports (task #4) plug
  into with no further `internal/web` changes required per plugin.
  `logtail`/`webcfg` are NOT yet registered as manager `Plugin` entries
  (they keep their pre-existing bespoke native routes in
  `internal/web/logtail.go`/`webcfg.go`, which already worked and had no
  bridge dependency) — folding their *listing* into `Manager.List()`
  uniformly is left for task #4 to avoid a half-finished intermediate state.
- **Non-root test gate fixed**: `internal/fs.SetupMounts` no longer
  hardcodes `/run/pwnagotchi`; it takes an injected `runRoot` parameter
  (`fs.DefaultRunRoot` in production, `t.TempDir()` in tests). `go test ./...`
  now passes as an ordinary non-root user.
- **Stale tracked binary removed**: `go-port/pwnagotchi` (the tracked x86-64
  build artifact this ledger's snapshot flagged under task #11) is deleted;
  root `.gitignore` expanded to block Go binaries/coverage/profiles/image
  artifacts from being retracked.
- Full verification re-run after these changes: `gofmt -l .` clean, `go vet
  ./...` clean, `go build ./...` clean, `go test ./...` and
  `go test ./... -race` both green (28 packages),
  `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath ./cmd/pwnagotchi`
  still produces a static ELF binary.

### Second increment: UI capability foundation + memtemp ported (same session)

Porting `memtemp` surfaced a real gap: no native plugin could add its own
on-screen widgets (`ui.add_element`/`ui.is_waveshare_v2()` equivalents
didn't exist on the plugin-facing capability surface at all). Built the
foundation properly rather than special-casing one plugin:

- `internal/ui/view.View` gained `Kind()` (the resolved display registry
  name, replacing the need for ~90 generated `IsX()` predicates on a
  plugin capability interface with one string compare), `AddTextElement`,
  and `AddLabeledValueElement` (real `components.Text`/`LabeledValue`
  widgets through the existing font/state pipeline) — covered by new tests
  in `internal/ui/view/view_test.go`.
- `pluginmanager.ViewCapability` widened to `Set/Update/Kind/HasElement/
  RemoveElement/AddText/AddLabeledValue`; the placeholder, unused
  `DisplayCapability` was removed (View already covers it — Python's
  `Display` IS-A `View`, so this is a closer match to the real
  architecture, not a reduction).
- `pluginmanager.AgentCapability`'s method shapes were corrected to match
  the REAL `*agent.Agent`/embedded `*bettercap.Client` exactly
  (`Run(cmd string, verboseErrors bool) (interface{}, error)`,
  `Session(sess string) (interface{}, error)`) — the earlier speculative
  shape (`Run(cmd, verbose) error`, `Session() (map, error)`) was wrong and
  would have needed a wrapper adapter; now `*agent.Agent` satisfies
  `AgentCapability` directly with zero glue code.
- New `internal/pluginhost` package: `pluginhost.View` adapts whatever the
  daemon's real view is (`*display.Display`, which embeds `*view.View`, or
  `*cli.HeadlessView`) into `pluginmanager.ViewCapability` via runtime
  interface assertions, degrading display-only methods to safe no-ops in
  headless mode rather than requiring `*cli.HeadlessView` to grow dead
  methods. Covered by `internal/pluginhost/view_test.go` against both a
  real `*view.View` and a minimal headless fake.
- `cmd/pwnagotchi/main.go` restructured so `v`/`a` are declared before the
  plugin manager's `CapabilitiesFor` closure (which captures them by
  reference) and actual `Register`/`LoadAll` calls are deferred until
  after both the real view/display AND `*agent.Agent` exist — otherwise a
  native plugin's `OnLoad` could observe a nil/zero view or agent
  depending on registration order. Also fixed a real (if minor) parity
  bug this same restructure surfaced: `wpa-sec` was previously loaded
  unconditionally regardless of its own `enabled` config flag; both
  `wpa-sec` and `memtemp` (and every future native plugin) now go through
  `nativePluginConfigs(cfg)` + `Manager.LoadAll`, so each plugin's real
  per-plugin `enabled` flag is honored exactly like Python's
  `plugins.load()`.
- **`memtemp` ported and registered**
  (`internal/plugins/native/memtemp`, 10 tests): mem/cpu/cpu-since/temp/
  freq fields, horizontal/vertical layout, per-display-model default
  positions (`Kind()`-keyed), config-driven field/position/orientation
  overrides, exact `cpu_load_since` semantics preserved (own
  never-sleeping last-sample diff, distinct from `pwnagotchi.cpu_load()`'s
  always-0.1s-sleep form — see `internal/unit.ReadCPUStat`, newly exported
  for this). 2 of 23 bundled plugins now fully native (wpa-sec, memtemp);
  21 remain (task #4).
- Full verification re-run again: `gofmt -l .`/`go vet ./...`/`go build
  ./...` clean, `go test ./... -race` green (31 packages now), arm64
  cross-build still static.

### Third increment: loaded/config_changed lifecycle + cache ported (same session)

Porting `cache` surfaced a second real gap: the daemon never emitted
`loaded` or `config_changed` at all — grepped for both event names across
every `emit.On(...)` call site in `internal/` and found zero matches,
confirmed against the real `pwnagotchi/plugins/__init__.py`'s `load()`
(`on('loaded'); on('config_changed', config)`, once at startup, broadcast
to every just-loaded plugin) and `toggle_plugin()` (`one(name, 'loaded')`
then `one(name, 'config_changed', pwnagotchi.config)`, single-plugin
dispatch on a runtime enable). Both are required by
`GO_ONLY_MIGRATION_PROMPT.md` §3's lifecycle list and at least 6 bundled
plugins (`cache`, `webcfg`, `logtail`, `pwncrack`, `webgpsmap`, `wigle`)
call `on_config_changed` for real setup work (not just logging).

- `pluginmanager.Manager.Load` now delivers exactly `"loaded"` then
  `"config_changed"` (with the FULL merged config as its sole argument —
  Python's `on_config_changed(config)` receives the whole config, not
  just this plugin's own options; e.g. `cache.py` reads
  `config['bettercap']['handshakes']`) directly to the just-loaded
  plugin's own queue only — matching `one()`'s single-plugin semantics,
  not a broadcast `on()`. `Load`/`LoadAll`/`Toggle` signatures gained a
  `fullCfg config.Map` parameter accordingly; every call site
  (`cmd/pwnagotchi/main.go`, `internal/web/handler_plugins.go`, both test
  suites) updated. Covered by new
  `TestLoadDeliversLoadedThenConfigChangedToThatPluginOnly` plus updated
  assertions in the existing ordering/panic-isolation/unload tests.
- **`cache` ported and registered** (`internal/plugins/native/cache`, 13
  tests): AP JSON caching keyed by sanitized hostname+mac (regex-stripped
  exactly like Python's `re.sub(r"[^a-zA-Z0-9]", "", hostname)`,
  including the "hyphens/spaces are silently stripped, not escaped"
  quirk), config_changed-derived cache dir, wifi_update/
  unfiltered_ap_list/association/deauthentication/handshake hooks (both
  the concrete `[]agent.AP` and generic `[]interface{}` slice shapes the
  real emitters use for different events), 5-minute stale-file cleanup on
  a 60-second `ui_update` cadence and on unload, and the exported
  `ReadAPCache` helper `wigle` will need when it's ported. Deliberately
  documented divergence: real `agent.py` sometimes emits a plain BSSID
  string instead of an AP dict for a handshake's `access_point` argument
  (verified in the actual Python source, not a Go-port bug) — Python
  would raise/log a `TypeError` there; this port skips cleanly instead,
  never panics, and is covered by
  `TestHandshakeWithStringAPDoesNotPanic`.
- 3 of 23 bundled plugins now fully native (wpa-sec, memtemp, cache); 20
  remain (task #4).
- Full verification re-run once more: `gofmt -l .`/`go vet ./...`/`go
  build ./...` clean, `go test ./... -race` green (32 packages), arm64
  cross-build still static.

### Fourth increment: 6 more plugins ported (switcher, gpio_buttons, wittypi, ups_lite, pwncrack, gps, example) — same session

Added two more capabilities several of these needed, both real gaps
independently flagged by more than one implementation while porting:
`pluginmanager.SystemCapability` (Shutdown/Reboot/Restart — switcher's
reboot-after-task behavior; wired via a new `pluginhost.Exec` real
argv-based `CommandRunner` and reusing `unitActions` for `System`) and
`ViewCapability.OnUploading/OnNormal/Width/Height` (upload-progress mood
+ screen-size-relative positioning — needed by ohcapi/wigle/pwncrack and
by wittypi/ups_lite/example respectively; `wittypi`/`ups_lite`/`example`
were built by parallel workstreams against a placeholder fixed X-offset
before `Width()` existed, then corrected to the real
`view.Width()/2±N` formula once it landed).

Ported (each with its own focused test suite, all green under `-race`):
- **switcher** (Go-authored directly): generic event→shell-task scheduler,
  including the reboot-with-self-cleaning-timer systemd flow. `commands`
  is the documented "intentionally a shell program" config field (per
  the migration spec's own carve-out); executed as a real script file via
  the injected `CommandRunner`, never a shell string built by this code.
- **gpio_buttons**: edge-triggered GPIO button → shell command, via the
  (not-yet-hardware-backed) `GPIOCapability`; software debounce added
  since `GPIOLine` has no hardware bounce-time parameter.
- **wittypi** / **ups_lite**: I2C battery voltage/capacity + GPIO
  charging-state UI plugins. `ups_lite`'s CW2015 double-byteswap
  (`struct.unpack("<H", struct.pack(">H", ...))`) was ported as a direct
  big-endian 2-byte register read, verified numerically equivalent.
- **pwncrack**: `hcxpcapngtool` conversion (via `CommandRunner`, argv) +
  HTTP upload/potfile-download (via `Capabilities.HTTPClient` against
  `httptest`, never a real host) + rate-limiting via `Capabilities.Clock`.
- **gps**: bettercap GPS module enable + per-handshake coordinate saving
  + lat/long/alt UI. Documented a confirmed real (not Go-port) upstream
  Python behavior gap found while porting: `on_ready` — this plugin's
  entire bettercap-enable path — is only ever fired by a runtime
  `toggle_plugin` web-UI enable, never by the normal `plugins.load()`
  boot path (verified by grepping the real
  `pwnagotchi/plugins/__init__.py`: `load()` only calls `on('loaded')`/
  `on('config_changed', config)`). Ported faithfully rather than
  "fixing" a behavior difference outside this migration's mandate.
- **example**: replaces `example.py`; demonstrates the native hook
  surface. Paired with new `docs/plugin-development.md` — a from-scratch
  guide for authoring NEW native Go plugins (interfaces, `Capabilities`
  fields, verified event-name table, current bundled-only registration
  mechanism with an explicit forward-note that third-party distribution
  is still task #5's unfinished work).
- 10 of 23 bundled plugins now fully native (wpa-sec, memtemp, cache,
  switcher, gpio_buttons, wittypi, ups_lite, pwncrack, gps, example); 13
  remain: auto-tune, auto-update, auto_backup, bt-tether (~5000 lines,
  real D-Bus/NetworkManager bluetooth tethering — by far the largest
  remaining plugin), fix_services, ohcapi, pwnstore_ui, session-stats,
  webgpsmap, wigle, plus folding the pre-existing native logtail/webcfg
  into `pluginmanager.Manager.List()` uniformly (they already work,
  bridge-free, via bespoke `internal/web` routes).
- Full verification re-run once more after registering all 6 in
  `cmd/pwnagotchi/main.go`: `gofmt -l .`/`go vet ./...`/`go build ./...`
  clean, `go test ./... -race` green (37 packages), arm64 cross-build
  still static.

### User directive (2026-07-24): skip display hardware work

User: "continue but dont touch the display code i dont need it." Tasks #6
(bus abstractions), #7 (~94 display drivers), and #8 (display-table
generators) are on hold per this explicit instruction — see memory
`feedback_skip_display_code`. `GO_ONLY_MIGRATION_PROMPT.md`'s own
completion gates still require this work; it is deliberately NOT being
done, and the final report must say so plainly rather than silently
completing or silently omitting it.

### Fifth increment: 8 more plugins ported, pure-Go PCAP/802.11 library confirmed, locale sources preserved

Built `go-port/internal/wifiparse` (pure-Go, no third-party deps,
verified against real pcap/RadioTap/802.11 binary format specs) — the
"Port Scapy-dependent PCAP/PCAPNG extraction ... to tested pure Go"
requirement — 20 tests green. A duplicate, untested, unimported scaffold
at `internal/dot11` (same design, never wired up) was found and deleted
as dead code.

Ported (each with focused tests, all green under `-race`; several real
bugs were found and fixed while writing tests, not just registration
smoke-tests):
- **auto-tune**, **auto_backup**, **auto-update**, **ohcapi**,
  **session-stats**, **fix_services**, **pisugarx**. Real bugs caught and
  fixed: a self-deadlock in `ohcapi.OnLoad` (locking helper called while
  already holding the same non-reentrant mutex — hung forever on load with
  a missing/present `api_key`), a goroutine-leak/nil-channel hang in
  `session-stats`' periodic-tick loop (mutable struct fields re-read
  inside the goroutine instead of captured at spawn), and the identical
  self-deadlock pattern independently in `pisugarx.OnLoad`'s
  invalid-`default_display` path (reproduced via a real 7+-minute test
  hang, root-caused with a goroutine dump). All three are the same class
  of bug (a `logf` helper that locks, called while the lock is already
  held) — worth checking for elsewhere if more plugins are audited later.
- `auto-update`: real (non-placeholder) GitHub-release-check +
  arch-matched-asset download + sha256 verify + binary swap + service
  restart, applied uniformly to bettercap/pwngrid/pwnagotchi (the latter
  now being this same Go binary, not a pip-installed package) — no
  wget/unzip/pip shell-outs.
- `fix_services`: monitors journalctl/kernel logs for brcmfmac failure
  patterns, recovers via `modprobe -r/+brcmfmac` + `wifi.recon`, falls
  back to a real `SystemCapability.Reboot()` after repeated failures.
  Preserves a confirmed real upstream quirk (direct-remediation branches
  don't re-arm the rate-limit gate) as intentional, documented behavior.
- `pisugarx`: all three real board versions (PiSugar2/2Plus/3) with their
  distinct I2C register maps, discharge curves, and trimmed-mean battery
  percentage ported verbatim; low-power auto-shutdown via
  `SystemCapability`.
- Two duplicate/near-duplicate implementations were found where parallel
  workstreams overlapped in scope (an `internal/plugins/native/auto-update`
  hyphenated directory alongside the correct `auto_update`) — compared
  both, kept the fully-green one, deleted the failing/gofmt-dirty
  duplicate.
- **Locale migration (task #10) done**: `go-port/internal/voice`'s
  `//go:embed` narrowed to `locale/*/LC_MESSAGES/*.mo` only (was
  embedding the whole tree); all 184 editable `.po` sources + `voice.pot`
  copied to new, Go-owned, non-embedded `go-port/locale-src/` (with a
  README explaining the embed/non-embed split and how to regenerate a
  `.mo` with `msgfmt`, a generic gettext-suite tool, not Python tooling).
  Added `TestEveryEmbeddedCatalogLoads` (all 184 embedded catalogs parse
  via the real runtime loader) and `TestCatalogCountMatchesEditableSources`
  (embedded `.mo` set and editable `.po` set are kept in exact lockstep).
- 18 of 23 bundled plugins now fully native: wpa-sec, memtemp, cache,
  switcher, gpio_buttons, wittypi, ups_lite, pwncrack, gps, example,
  auto-tune, auto_backup, auto-update, ohcapi, session-stats, webgpsmap,
  fix_services, pisugarx. Remaining: grid, wigle (both were blocked on
  wifiparse, now unblocked, in progress), bt-tether (~5000 lines, largest
  remaining plugin, in progress), plus folding pre-existing native
  logtail/webcfg into `Manager.List()` uniformly.
- Full verification re-run: `gofmt -l .`/`go vet ./...`/`go build ./...`
  clean, `go test ./... -race` green (45 packages), arm64 cross-build
  still static.

### Sixth increment: pwnstore_ui ported, logtail/webcfg folded into manager, grid ported

- **pwnstore_ui** (Go-authored directly, judgment call): a genuine
  tension with the Go-only end state — real `pwnstore_ui.py`'s
  install/uninstall actions shell out to a `pwnstore` CLI that downloads
  and installs raw `*.py` plugin files, i.e. it IS the Python plugin
  distribution mechanism this migration replaces. Ported the real
  browsing UI faithfully (embedded real HTML verbatim via `go:embed`,
  live store-JSON fetch via `Capabilities.HTTPClient`, real
  config.toml-editing `api/configure`, real `systemctl restart` via
  `CommandRunner`), but `api/install`/`api/uninstall` return an honest,
  structured `success:false` with a clear message pointing at
  `docs/plugin-development.md`, instead of running a Python-plugin
  installer or fabricating success — matches the spec's explicit
  "existing Python plugins must never cause Python to be installed or
  silently be treated as loaded." 12 tests, all green.
- **logtail/webcfg folded into `Manager.List()`/toggle bookkeeping**
  (new `pluginhost.MetadataOnlyPlugin`, registered in
  `cmd/pwnagotchi/main.go`) without changing their actual routing —
  `internal/web/handler_plugins.go` already special-cases both names
  before any manager/bridge check, and continues to; this only fixes
  their visibility/enabled-state in the `/plugins` listing, which
  previously bypassed the manager entirely.
- **grid** ported (`internal/plugins/native/grid`, 11 tests): reports
  identity/pwned-networks to opwngrid.xyz via the already-ported
  `internal/grid` pwngrid API client, real PCAP filename parsing, cache
  reuse via `cache.ReadAPCache`.
- 20 of 23 bundled plugins now fully native (added pwnstore_ui, grid;
  logtail/webcfg now properly folded in rather than just "already
  working"). Remaining: **wigle** and **bt-tether** (~5000 lines, by far
  the largest remaining plugin) — both still in progress as of this
  writing.
- Full verification re-run (excluding the two still-in-flight packages):
  `gofmt -l .`/`go vet ./...` clean, `go build`/`go test ./... -race`
  green for every other package.

### Seventh increment: ALL 23 bundled plugins + example now native; Go-only third-party plugin distribution built (task #5)

Session hit an API session-limit mid-flight (background agents for
grid/wigle and the third-party-distribution system were interrupted);
resumed once it reset. One fork violated its scope instructions and
committed premature `bt_tether`/`wigle` registrations into
`cmd/pwnagotchi/main.go` before those packages existed, breaking the
build — caught immediately via `go build`, temporarily reverted, then
properly re-added once each package was independently verified
(`go build`/`go vet`/`go test -race` green in isolation) before wiring.

- **wigle** and **bt-tether** (~5000 lines, by far the largest plugin in
  the whole migration) both landed complete and fully tested (17 and 21
  tests respectively) despite the session interruption — bt-tether's
  real functionality: device discovery/pairing/trust/connect via real
  `bluetoothctl` argv commands, NAP profile connection via a single
  targeted `dbus-send` call (avoiding a full D-Bus client dependency for
  one call), real network bring-up/IP extraction via `ip`/`dhclient`
  parsing, the full original web UI (embedded verbatim) with every real
  backend route. Honestly documented gaps: no live scan-progress
  streaming (CommandRunner is request/response only, not a persistent
  session) and no interactive pairing-agent passkey confirmation.
- **grid** wired into `cmd/pwnagotchi/main.go` (needed a small
  `registerNativePlugins` signature change to receive the already-
  constructed `*grid.Client` and `*agent.Agent` — grid needs the real
  pwngrid API client directly, documented as a deliberate divergence from
  `Capabilities.GridCapability`, whose shape doesn't match the real
  client's actual methods).
- **pwnstore_ui** ported directly (not via fork): a genuine tension with
  the Go-only end state, since real `pwnstore_ui.py`'s install/uninstall
  IS the Python plugin distribution mechanism this migration replaces.
  Kept the real browsing UI (embedded HTML, live store-JSON fetch,
  config.toml editing, real `systemctl restart`) but made install/
  uninstall return an honest `success:false` pointing at
  `docs/plugin-development.md` instead of running a Python installer.
- **logtail/webcfg** folded into `Manager.List()`/toggle bookkeeping via
  a new `pluginhost.MetadataOnlyPlugin` (routing itself is unchanged —
  `internal/web` already special-cases both before any manager/bridge
  check).
- **All 23 of 23 bundled plugins + example are now native.** Zero
  bundled plugins depend on the Python bridge anymore.
- **Task #5 (Go-only third-party plugin distribution) built**, mostly by
  a background fork, verified and integrated directly: new
  `internal/pluginrpc` package — a versioned TOML manifest (checksum,
  capabilities, os/arch), a JSON repository-index format, a bounded
  newline-delimited-JSON RPC protocol to a separately-versioned Go
  executable (mirroring `internal/pyplugin`'s bridge shape but for real
  Go binaries), real crash detection (subprocess exit) and hang detection
  (missed heartbeats) verified against REAL spawned test-fixture
  binaries (not mocks), and a `RemotePlugin` type wired through the
  actual `pluginmanager.Manager` lifecycle. 20 tests, all green.
  `internal/plugins/cmd.go` (the old `.py`-file package manager) was
  rewritten end-to-end against this system: `search`/`list`/`install`/
  `upgrade`/`uninstall` now operate on manifests fetched over HTTP
  (`httptest`-verified, real checksum-mismatch rejection tested against
  genuinely tampered bytes), and — the single most important behavioral
  requirement — installing a bundled-plugin name is a safe no-op, while
  installing a name that only exists as a leftover legacy `.py` file
  fails with a precise, actionable migration message and never installs,
  runs, or is silently treated as loaded (own dedicated test). Rewrote
  `internal/plugins/cmd_test.go` completely to match (14 tests). Real
  external blocker, disclosed not hidden: no actual Go-only plugin
  repository is hosted anywhere yet — `main.plugin_repository_index` is
  an empty-by-default, documented placeholder; every function handles
  "not configured" as a distinct, real state rather than an error.
- Full verification: `gofmt -l .`/`go vet ./...`/`go build ./...` clean,
  `go test ./... -race` green across every package including
  `internal/pluginrpc`, arm64 cross-build still static.

### Eighth increment: Python bridge removed (task #9), Pi image packaging Go-only (task #12)

Both done by background forks, verified directly afterward.

- **Task #9**: `internal/pyplugin/` (bridge.go/bridge.py/bridge_test.go)
  and `internal/plugins/loader.go` (`PythonInterpreter`/`plugins.Load`)
  deleted entirely — no callers left once all 23 plugins went native.
  `internal/web.Server` no longer takes a `*pyplugin.Bridge` parameter;
  `/plugins` routing is now unconditionally the plugin manager, with a
  plain 404 for an unknown name instead of a bridge-unavailable 503.
  `cmd/pwnagotchi/main.go`'s `emit` chain no longer constructs or falls
  back to a bridge. Removed the `compatibility-test` Makefile target and
  all live `PWNAGOTCHI_PYTHON`/`python3`/`pip3`/`venv/bin` references from
  `.go` files, `Makefile`, and CI workflows — confirmed via
  `grep -rnE '(python3?|pip3?|PWNAGOTCHI_PYTHON|internal/pyplugin|venv/bin)'`
  across the tree: every remaining hit is a historical doc/code-comment
  explaining migration history (explicitly permitted by the spec), none
  is a live instruction to install/run Python. **Deliberately NOT done**
  (disclosed, not silently dropped): `go-port/tests/visual/oracle.py` and
  the 4 `go-port/scripts/gen_*.py` display-table generators are
  untouched — both are display-rendering-adjacent and explicitly
  excluded from this session's scope per the user's "don't touch the
  display code" instruction. This is a real, intentional gap against
  `GO_ONLY_MIGRATION_PROMPT.md`'s literal requirements, not an oversight.
- **Task #12**: removed all Python packages from
  `deploy/pi-gen-stage/04-install-pwnagotchi/00-packages` (deleted the
  file, following this repo's own precedent of stages with no package
  list); rewrote the install/chroot scripts to copy only the
  cross-compiled Go binary (dropped `/opt/pwnagotchi-src`, the
  `pyproject.toml` copy, and the ~50-line pip/apt-conflict-resolution
  block); deleted `deploy/scripts/check-plugins.py`, replaced by a new,
  real `pwnagotchi plugins doctor` Go subcommand (lists all 23 compiled-in
  bundled plugins, checksum-verifies every installed third-party
  manifest via `pluginrpc` — 3 new tests) wired into
  `.github/workflows/build-pi-image.yml`'s image-validation step
  alongside a new negative Python-residue check (no `.py` files, no
  `python3`/`pip3` binary, no `/opt/pwnagotchi-src` in the built image).
  **Real, disclosed blocker**: no working pi-gen/qemu-chroot build
  environment exists in this sandbox (documented pre-existing constraint,
  see `HANDOFF.md`) — the shell-script/YAML changes were verified for
  syntax (`bash -n`, YAML parse) and the new Go code fully tested, but an
  actual end-to-end image build was NOT performed. Reproduction: trigger
  `.github/workflows/build-pi-image.yml` via `workflow_dispatch` on a
  real runner/environment and inspect the image-validation-report
  artifact.
- Full verification after both landed: `gofmt -l .`/`go vet ./...`/
  `go build ./...` clean, `go test ./... -race` green (43 packages —
  `internal/pyplugin` and its test no longer exist to count), arm64
  cross-build still static.
### Ninth increment: root-module restructure (task #11) — done as a solo pass

Moved every `go-port/` subdirectory (`cmd`, `deploy`, `docs`, `internal`,
`locale-src`, `scripts`, `testdata`, `tests`) plus `go.mod`/`go.sum`/
`Makefile` to the repository root; changed the module path from
`github.com/jayofelony/pwnagotchi/go-port` to
`github.com/jayofelony/pwnagotchi`; rewrote all 105 `.go` files'
`go-port`-prefixed imports via a single sweep
(`grep -rl ... | xargs sed -i 's#.../go-port#...#g'`); fixed the one
functional (non-comment) leftover path in
`deploy/pi-gen-stage/05-configure-services/00-run.sh`
(`DEPLOY_DIR="${PWNAGOTCHI_REPO_DIR}/go-port/deploy"` →
`.../deploy`) and every path in `.github/workflows/build-pi-image.yml`
(trigger paths, `go-version-file`, `cache-dependency-path`,
`working-directory`, artifact path, pi-gen `STAGE_LIST`); updated
`.gitignore` (careful fix: a naive bare `/pwnagotchi` ignore rule would
have collided with the still-tracked `pwnagotchi/` Python source
directory at the new shared root — used `/pwnagotchi-go` instead,
matching the CI artifact name, with a comment explaining why); merged
both READMEs into one accurate root `README.md` (removed the
side-by-side-port/oracle framing entirely — every plugin is native, the
bridge is gone); swept remaining `go-port/`-prefixed path mentions in Go
doc-comments and deploy shell scripts (cosmetic, but cheap and worth
doing since they're now simply wrong, not historical); deleted the
leftover empty `go-port/` directory.
- Verified: `gofmt -l .`/`go build ./...`/`go vet ./...` clean on the
  FIRST attempt after the move (no import-path mistakes), `go test
  ./... -race` green across all 43 packages, `go mod tidy` produces no
  diff, arm64 cross-build still static, `bash -n` clean on every edited
  shell script. One test (`internal/voice`'s
  `TestCatalogCountMatchesEditableSources`) uses a `../../locale-src`
  relative path from its own package directory that, by construction
  (both the package and its target moved up by exactly one directory
  level), still resolves correctly with no code change needed — verified
  by re-running it, not assumed.
- Deliberately NOT deleted: the root `pwnagotchi/` Python source tree.
  Per the migration spec's own words ("This is not a source-deletion
  exercise... only then remove the Python implementation") and the
  explicit user instruction to skip display-hardware work this session,
  the ~94 unported display drivers still live there and are the only
  reason it remains. This is a real, disclosed tension with
  `GO_ONLY_MIGRATION_PROMPT.md`'s literal "no tracked `*.py`" completion
  gate — flagged here and in the final report, not silently resolved
  either way.
- Remaining: task #13 (final docs rewrite + verification gates).

### Tenth increment: task #13 — docs consolidated, final verification gates run

Resolved all five items filed under "Contradictions found in existing
docs" above:

1. **wpa-sec legacy DB migration** — confirmed already fixed (not just
   flagged) by the eighth/ninth increments: `internal/wpasec.migrateLegacyDB()`
   exists and is tested. `docs/plugin-compatibility-matrix.md`'s rewritten
   `wpa-sec` row now states the migration explicitly instead of describing
   the JSON-file design as a bare structural choice.
2. **"Bridged (fully inventoried and tested)" framing** — no longer an
   issue to reword: it's simply false now, since the bridge is deleted and
   every plugin is native. Removed the "Bridged" status from
   `feature-matrix.md`'s legend entirely and rewrote every plugin-related
   row in `feature-matrix.md` and all of `plugin-compatibility-matrix.md`
   to describe the current native-`pluginmanager` architecture.
3. **`weact2in9`** — left as an open decision for task #7 (display driver
   work, on hold per user instruction); not touched this pass.
4. **`deploy/README.md`** — now exists (created by a prior increment).
   Found still-stale `go-port/`-prefixed paths in it (architecture diagram,
   a `push to go-port/**` trigger instruction, a `docs/final-port-report.md`
   cross-reference) predating the root-module restructure — fixed. Also
   found and fixed a real internal contradiction: its own "Architecture"
   section still said stage `04-install-pwnagotchi` "installs the real
   pwnagotchi Python package (plugin bridge dependency)" three sections
   above where its own "No Python in the image" section correctly says the
   opposite — corrected to match the (correct) latter section.
5. **`final-port-report.md`** — replaced with a short pointer to this
   ledger and the other living docs, rather than rewritten in place; it
   had accumulated so many self-correcting "Update:" notes across its own
   history (each admitting the previous revision was stale) that
   maintaining it as a second, separately-updated rollup alongside this
   ledger was itself the contradiction risk, not any one factual claim in
   it.

Beyond the five filed items, found and fixed while reading every doc
end-to-end (not assumed from memory):

- `known-differences.md`'s identity-PEM entry and `feature-matrix.md`'s
  `cli.py`/`identity.py` rows all cited `tests/compat_cli_test.go` and
  `tests/compat_identity_test.go` as existing, current tests proving
  byte-exact/differential parity with real Python. Both files were
  actually deleted along with the rest of the compat-test infrastructure
  (eighth increment) and never replaced with an equivalent non-Python
  check. This is a real, previously-undisclosed reduction in verification
  strength (not a behavior change) — now disclosed explicitly in both
  docs rather than silently left claiming a test that no longer exists.
- `feature-matrix.md`'s `extract_from_pcap` row still said "not
  ported — and not needed" with the old bridge-era reasoning (its only
  callers were real Python plugins behind the bridge). This is now simply
  wrong: `extract_from_pcap` **is** ported (`internal/wifiparse`,
  confirmed via `internal/wifiparse/extract.go`'s own doc comment and
  passing tests), and `grid`/`wigle` are native Go plugins that call it
  directly. Corrected to **Ported**.
- `feature-matrix.md`'s `plugins/cmd.py` row still described the
  Python-era zip-download package manager; `internal/plugins/cmd.go` was
  completely rewritten against `internal/pluginrpc` (manifests/checksums)
  earlier in this migration and the doc was never updated to match —
  corrected.
- `feature-matrix.md`'s build/deploy infra table still listed
  `stage3/**`/`config-32bit`/`config-64bit`, all deleted in the ninth
  increment's restructure and replaced by `deploy/pi-gen-stage/**` —
  corrected.
- Considered whether any of the ~48 non-`ui/hw` Python files under
  `pwnagotchi/` (core daemon, the 23 original plugin sources, the
  `ui/state|view|display|colors|components|faces|fonts` rendering layer)
  could be deleted without touching display-hardware code, since they are
  all now fully superseded by native Go and none of the residue-gate hits
  outside `ui/hw`/generators/oracle.py are display-*driver* files per se.
  Checked directly rather than assumed: `pwnagotchi/ui/hw/base.py` imports
  `pwnagotchi.ui.fonts`, and a repo-wide grep confirms the whole
  `ui/state|view|display|colors|components|faces|fonts` layer plus `mesh/*`,
  `cli.py`, and several plugin sources are cross-imported by the `ui/hw/*`
  driver tree itself — the Python tree is not cleanly separable this way.
  **Left the whole tree untouched, as already decided** — deleting only
  part of it was considered and correctly ruled out, not overlooked.
- Fixed stale `go-port/`-prefixed paths in `docs/known-differences.md`,
  `docs/repository-analysis.md` (added a header disclosing it as a
  point-in-time snapshot predating the restructure), `docs/rendering-investigation.md`
  (also disclosed that `make compatibility-test`, which
  `tests/visual/compat_live_test.go`'s own comments still reference, no
  longer exists as a Makefile target — the test file itself was left
  untouched as display/visual-rendering-adjacent, out of scope this
  session), and `docs/python-baseline.md`.
- Wrote `docs/architecture.md` (new): process shape, the real
  `cmd/pwnagotchi/main.go` startup sequence in order, a package map, the
  two plugin distribution paths, and a "Status and known gaps" section
  matching the root `README.md`'s wording exactly so the two documents
  cannot silently drift apart.
- Re-verified `docs/plugin-development.md` line-by-line against current
  source (`internal/pluginmanager/manager.go`'s five interfaces,
  `capabilities.go`'s 13 `Capabilities` fields, `main.go`'s
  `registerNativePlugins`/`nativePluginConfigs` signatures) — accurate
  except one tense issue ("the bridge is being retired" → it's gone),
  fixed.
- Linked `docs/architecture.md` from the root `README.md`'s layout section.

**Final verification gates run and recorded** (`GO_ONLY_MIGRATION_PROMPT.md`
§"Required verification" 1 and 2), after all doc edits above:

- `gofmt -l .` — clean.
- `go vet ./...` — clean.
- `go mod tidy` — no diff.
- `go build ./...` — clean.
- `go test ./...` — all 43 Go packages `ok` (one, `cmd/pwnagotchi`, has no
  test files by design — it's a thin `main()` wrapper).
- `go test ./... -race` — same 43 packages, `ok`, zero data races.
- `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath` — succeeds,
  produces a real static ARM64 ELF binary (verified via `file`, then
  removed).
- Residue gate (`find` for `*.py`/`*.pyi`/`*.pyc`/`__pycache__`/
  `pyproject.toml`, outside `.git`): **fails as expected, for exactly the
  disclosed reason** — every hit is under `pwnagotchi/` (the untouched
  Python source tree), `tests/visual/oracle.py`, `scripts/gen_*.py` (the
  4 display-table generators), or root `pyproject.toml` (legacy Python
  packaging metadata, listing the same display-hardware-only dependencies
  — `gpiozero`, `inky`, `rpi-lgpio`, `rpi_hardware_pwm`, `smbus`/`spidev`
  — as the untouched driver tree). No hit outside this disclosed set.
- Residue gate (`git grep` for `python3?|pip3?|PWNAGOTCHI_PYTHON|internal/pyplugin|venv/bin`,
  excluding `docs/**` and `*.md`): every hit is either inside the
  disclosed `pwnagotchi/` tree, gettext `.po` comment noise
  (`#, python-brace-format`), `.idea/` IDE project files (not part of the
  build), or `.github/workflows/build-pi-image.yml`'s own two lines that
  *assert Python's absence* (`check "no python3 interpreter installed"`),
  not install/invoke it. No live instruction anywhere in `.go`/CI/deploy
  code to install or run Python.

**This migration is not "done" per `GO_ONLY_MIGRATION_PROMPT.md`'s own
literal completion gate** (no tracked `*.py` files) — that gate fails for
exactly one disclosed, user-directed reason: the ~94 real display-hardware
driver implementations, the SPI/I2C/GPIO/PWM bus abstraction work, and the
4 Python display-table generator scripts (tasks #6/#7/#8) remain
unported, on hold per the explicit "don't touch the display code, I don't
need it" instruction. Every other requirement in the spec — native plugin
manager, all 23 bundled plugins + example ported natively, Go-only
third-party plugin distribution, Python bridge/loader/compat-test removal,
locale preservation, root-module restructure, Go-only image packaging, and
now a consistent, contradiction-checked documentation set — is complete
and verified above. This is the precise, final blocker to report, not a
claim of completion.
