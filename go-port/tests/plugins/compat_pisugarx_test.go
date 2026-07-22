//go:build compatibility

// Package plugins holds Python-vs-Go bundled-plugin compatibility tests,
// exercised through the real internal/pyplugin bridge against the real,
// unmodified pwnagotchi/plugins/default/*.py files — see
// docs/plugin-compatibility-matrix.md for the full inventory and
// per-plugin verification status this package (plus
// tests/compat_pyplugin_test.go) backs.
package plugins

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
	"github.com/jayofelony/pwnagotchi/go-port/internal/pyplugin"
)

func pythonBin(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("PWNAGOTCHI_PYTHON")
	if bin == "" {
		t.Skip("PWNAGOTCHI_PYTHON not set; run via `make compatibility-test`")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("python interpreter %s not found: %v", bin, err)
	}
	return bin
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("PWNAGOTCHI_REPO_ROOT")
	if root == "" {
		t.Skip("PWNAGOTCHI_REPO_ROOT not set; run via `make compatibility-test`")
	}
	return root
}

// TestCompatPisugarxOnLoadedSeesRealPwnagotchiConfigGlobal is a regression
// test for a real bug this investigation found and fixed:
// pwnagotchi/plugins/default/pisugarx.py's on_loaded reads the
// `pwnagotchi.config` MODULE GLOBAL directly (`cfg =
// pwnagotchi.config['main']['plugins']['pisugarx']`), not the `self.options`
// instance attribute plugins.load() always populates. Real cli.py sets
// `pwnagotchi.config = config` before ever calling plugins.load(); the Go
// bridge (internal/pyplugin/bridge.py) never did, so pisugarx.py's
// on_loaded always raised a real TypeError ('NoneType' object is not
// subscriptable) — silently caught and logged by plugins.py's own
// run_once, so the plugin still showed up as "loaded" in Go's b.Loaded
// even though its real startup logic never ran. bridge.py's main() now
// sets pwnagotchi.config for the real plugins.load(config) window only
// (see its comments for why this is deliberately not left set
// permanently: real plugins.toggle_plugin's enable path would otherwise
// persist to the hardcoded system path /etc/pwnagotchi/config.toml on
// every toggle, including from this test suite).
//
// internal/pyplugin.Bridge doesn't expose plugin-internal state (there is
// no RPC proxy for that — see known-differences.md), so this test proves
// the fix by capturing the bridge's real stderr log stream (which
// internal/pyplugin.relayStderr forwards through Go's stdlib "log"
// package) and asserting on the REAL log lines pisugarx.py's on_loaded
// itself produces: the crash signature must be ABSENT and the real
// "Rotation is disabled" log line (only reachable if `cfg.get('rotation',
// True)` actually read our real `rotation: false` from
// pwnagotchi.config, not the class's own `rotation_enabled = True`
// __init__ default) must be PRESENT.
func TestCompatPisugarxOnLoadedSeesRealPwnagotchiConfigGlobal(t *testing.T) {
	python := pythonBin(t)
	root := repoRoot(t)

	var logBuf bytes.Buffer
	prevOutput := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOutput)
		log.SetFlags(prevFlags)
	}()

	cfg := config.Map{
		"main": config.Map{
			"plugins": config.Map{
				"pisugarx": config.Map{
					"enabled":                       true,
					"rotation":                      false, // non-default: __init__'s own default is true
					"default_display":               "temp",
					"lowpower_shutdown":             true,
					"lowpower_shutdown_level":       int64(10),
					"max_charge_voltage_protection": false,
				},
			},
		},
	}

	b, err := pyplugin.New(pyplugin.Options{Python: python, Dir: root, Config: cfg})
	if err != nil {
		t.Fatalf("pyplugin.New: %v", err)
	}
	defer b.Close()

	foundPisugarx := false
	for _, name := range b.Loaded {
		if name == "pisugarx" {
			foundPisugarx = true
		}
	}
	if !foundPisugarx {
		t.Fatalf("expected pisugarx in Loaded (real __init__ gracefully handles no I2C hardware), got %v", b.Loaded)
	}

	// bridge.py's main() joins every plugin's on_loaded thread (bounded)
	// before printing "ready", and pyplugin.New() doesn't return until
	// that ready line arrives — so on_loaded has deterministically already
	// run by this point, no sleep/poll needed for it specifically. Give
	// the relayStderr goroutine a brief moment to drain the already-written
	// pipe bytes into logBuf.
	deadline := time.Now().Add(2 * time.Second)
	var out string
	for {
		out = logBuf.String()
		if strings.Contains(out, "PiSugarX") {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The specific TypeError message Python raises for
	// `None['main']['plugins']['pisugarx']` — NOT a bare "NoneType"
	// substring match, which would also false-positive on the separate,
	// genuine, pre-existing upstream bug this rig's lack of real I2C
	// hardware triggers a few lines later in the same on_loaded
	// (`self.ps.lowpower_shutdown = ...` with no `self.ps is None` guard,
	// since PiSugarServer() couldn't connect — a real Python fragility
	// independent of this fix, tracked in
	// docs/plugin-compatibility-matrix.md, not something go-port modifies
	// Python to paper over).
	if strings.Contains(out, "is not subscriptable") {
		t.Fatalf("pisugarx.py's on_loaded hit the pwnagotchi.config-is-None crash (fix regressed); bridge log:\n%s", out)
	}
	if !strings.Contains(out, "Rotation is disabled") {
		t.Fatalf("expected real pisugarx.py on_loaded log line reflecting our real rotation=false config (proves it read pwnagotchi.config, not just a class default); bridge log:\n%s", out)
	}
	t.Logf("bridge log confirms real on_loaded ran with the real config:\n%s", out)
}
