//go:build compatibility

package plugins

import (
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
	"github.com/jayofelony/pwnagotchi/go-port/internal/pyplugin"
)

// TestCompatRemainingBundledPluginsLoadAndStayResponsive covers every
// bundled pwnagotchi/plugins/default/*.py plugin NOT already exercised by
// tests/compat_pyplugin_test.go (cache, logtail, gpio_buttons, memtemp) or
// TestCompatPisugarxOnLoadedSeesRealPwnagotchiConfigGlobal (pisugarx) —
// the 18 remaining: auto-tune, auto-update, auto_backup, bt-tether,
// fix_services, gps, grid, ohcapi, pwncrack, pwnstore_ui, session-stats,
// switcher, ups_lite, webcfg, webgpsmap, wigle, wittypi, wpa-sec.
//
// This starts ONE real bridge with all 18 enabled simultaneously (the same
// shape as a real deployed unit with every bundled plugin turned on),
// using the SAME default config values real defaults.toml ships (see
// docs/plugin-compatibility-matrix.md), with two deliberate exceptions:
//
//   - api_key/token fields (auto-update, ohcapi, pwncrack, wigle, wpa-sec)
//     are left empty/absent, exactly matching each plugin's own real
//     defaults.toml default (an empty string) — every one of these
//     plugins' on_loaded/on_config_changed already has a real, explicit
//     `if not self.options.get('api_key'): return` (or equivalent) early
//     exit for this exact case, so this isn't a workaround, it's the
//     plugin's own real designed-for "not configured yet" behavior. This
//     avoids any real outbound call to a third-party service (GitHub,
//     WiGLE, wpa-sec.stanev.org, OpenHandshakes) from an automated test —
//     see docs/plugin-compatibility-matrix.md for why those specific
//     network calls are marked unverified rather than exercised here.
//   - filesystem-writing plugins (auto_backup, pwncrack via
//     bettercap.handshakes, session-stats, wigle/wpa-sec via
//     bettercap.handshakes) point at real t.TempDir()s instead of the
//     real default system paths, so this test never touches
//     /etc/pwnagotchi or /etc/pwnagotchi/handshakes.
//
// Verifies: (1) every one of the 18 real, unmodified Python files imports
// and instantiates successfully (proves real dependencies — requests,
// dbus, smbus, toml, dateutil, scapy — actually resolve in the bridge's
// Python environment) and lands in Loaded; (2) the bridge (running all 18
// plugins' real on_loaded/on_config_changed concurrently, several of which
// start their own background threads — bt-tether, session-stats) stays
// fully responsive afterward, proving no plugin's startup path took down
// or wedged the shared bridge process.
func TestCompatRemainingBundledPluginsLoadAndStayResponsive(t *testing.T) {
	python := pythonBin(t)
	root := repoRoot(t)

	handshakesDir := t.TempDir()
	backupDir := t.TempDir()
	sessionStatsDir := t.TempDir()

	cfg := config.Map{
		"main": config.Map{
			"plugins": config.Map{
				"auto-tune": config.Map{"enabled": true},
				"auto-update": config.Map{
					"enabled": true, "install": false, "interval": int64(1), "token": "",
				},
				"auto_backup": config.Map{
					"enabled": true, "backup_location": backupDir,
				},
				"bt-tether": config.Map{
					"enabled": true, "auto_reconnect": true, "show_on_screen": true,
				},
				"fix_services": config.Map{"enabled": true},
				"gps": config.Map{
					"enabled": true, "speed": int64(19200), "device": "/dev/ttyUSB0",
				},
				"grid": config.Map{"enabled": true, "report": true},
				"ohcapi": config.Map{
					"enabled": true, "api_key": "", "receive_email": "yes",
				},
				"pwncrack":    config.Map{"enabled": true, "key": ""},
				"pwnstore_ui": config.Map{"enabled": true},
				"session-stats": config.Map{
					"enabled": true, "save_directory": sessionStatsDir,
				},
				"switcher":  config.Map{"enabled": true},
				"ups_lite":  config.Map{"enabled": true, "shutdown": int64(2)},
				"webcfg":    config.Map{"enabled": true},
				"webgpsmap": config.Map{"enabled": true},
				"wigle": config.Map{
					"enabled": true, "api_key": "", "donate": false, "timeout": int64(30),
				},
				"wittypi": config.Map{"enabled": true},
				"wpa-sec": config.Map{
					"enabled": true, "api_key": "", "api_url": "https://wpa-sec.stanev.org",
					"download_results": false, "show_pwd": false, "single_files": false,
				},
			},
		},
		"bettercap": config.Map{
			"handshakes": handshakesDir,
		},
	}

	b, err := pyplugin.New(pyplugin.Options{Python: python, Dir: root, Config: cfg, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("pyplugin.New: %v", err)
	}
	defer b.Close()

	want := []string{
		"auto-tune", "auto-update", "auto_backup", "bt-tether", "fix_services",
		"gps", "grid", "ohcapi", "pwncrack", "pwnstore_ui", "session-stats",
		"switcher", "ups_lite", "webcfg", "webgpsmap", "wigle", "wittypi", "wpa-sec",
	}
	loadedSet := map[string]bool{}
	for _, name := range b.Loaded {
		loadedSet[name] = true
	}
	var missing []string
	for _, name := range want {
		if !loadedSet[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("expected all 18 plugins loaded, missing %v (got Loaded=%v)", missing, b.Loaded)
	}

	// Real on_loaded for several of these (bt-tether, session-stats)
	// spawns real background threads. Confirm the bridge is still fully
	// responsive with all 18 running concurrently, not just that the
	// initial ready line arrived.
	deadline := time.Now().Add(10 * time.Second)
	for {
		list, err := b.ListPlugins()
		if err == nil {
			for _, name := range want {
				if _, ok := list.Loaded[name]; !ok {
					t.Fatalf("ListPlugins.Loaded missing %q after startup: %v", name, list.Loaded)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bridge stopped responding to ListPlugins with all 18 plugins running: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
