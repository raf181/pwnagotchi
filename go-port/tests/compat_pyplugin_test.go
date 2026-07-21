//go:build compatibility

package tests

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
	"github.com/jayofelony/pwnagotchi/go-port/internal/pyplugin"
)

// fakeAgentRef stands in for a *internal/agent.Agent in this test the same
// way a real *agent.Agent would be stubbed by toJSONArg in production: an
// opaque Go type with no special-cased JSON shape, so it becomes a
// {"__goref__": "fakeAgentRef"} marker on the wire.
type fakeAgentRef struct{}

// TestCompatPyPluginBridgeRunsRealCachePlugin proves the pyplugin bridge
// (internal/pyplugin) actually runs a REAL, unmodified bundled plugin
// (pwnagotchi/plugins/default/cache.py) end-to-end: real plugins.load(),
// real on_config_changed/on_wifi_update dispatch through Python's own
// per-plugin worker-thread queue, and a real .apcache file written to disk
// by the plugin's own code — not a mock, not a simulated result.
func TestCompatPyPluginBridgeRunsRealCachePlugin(t *testing.T) {
	python := pythonBin(t)
	root := repoRoot(t)

	handshakesDir := t.TempDir()
	cfg := config.Map{
		"main": config.Map{
			"plugins": config.Map{
				"cache": config.Map{"enabled": true},
			},
		},
		"bettercap": config.Map{
			"handshakes": handshakesDir,
		},
	}

	b, err := pyplugin.New(pyplugin.Options{
		Python: python,
		Dir:    root,
		Config: cfg,
	})
	if err != nil {
		t.Fatalf("pyplugin.New: %v", err)
	}
	defer b.Close()

	foundCache := false
	for _, name := range b.Loaded {
		if name == "cache" {
			foundCache = true
		}
	}
	if !foundCache {
		t.Fatalf("expected 'cache' plugin in bridge.Loaded, got %v", b.Loaded)
	}

	ap := map[string]interface{}{
		"hostname":   "compattestnet",
		"mac":        "aa:bb:cc:dd:ee:ff",
		"encryption": "WPA2",
	}
	b.On("wifi_update", &fakeAgentRef{}, []interface{}{ap})

	wantFile := filepath.Join(handshakesDir, "cache", "compattestnet_aabbccddeeff.apcache")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(wantFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			entries, _ := os.ReadDir(filepath.Join(handshakesDir, "cache"))
			t.Fatalf("real cache.py never wrote %s within 10s; cache dir contains: %v", wantFile, entries)
		}
		time.Sleep(100 * time.Millisecond)
	}

	data, err := os.ReadFile(wantFile)
	if err != nil {
		t.Fatalf("reading real cache file written by cache.py: %v", err)
	}
	t.Logf("real cache.py wrote %s: %s", wantFile, data)
}

// TestCompatPyPluginBridgeListAndToggle proves the bridge's synchronous
// "call" protocol against real Python plugins.py state: list_plugins
// reflects the real plugins.loaded/plugins.database globals, and
// toggle_plugin performs a real load/unload via the real
// plugins.toggle_plugin (not a Go-side simulation).
func TestCompatPyPluginBridgeListAndToggle(t *testing.T) {
	python := pythonBin(t)
	root := repoRoot(t)

	cfg := config.Map{
		"main": config.Map{
			"plugins": config.Map{
				"cache": config.Map{"enabled": true},
			},
		},
		"bettercap": config.Map{"handshakes": t.TempDir()},
	}

	b, err := pyplugin.New(pyplugin.Options{Python: python, Dir: root, Config: cfg})
	if err != nil {
		t.Fatalf("pyplugin.New: %v", err)
	}
	defer b.Close()

	list, err := b.ListPlugins()
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	if _, ok := list.Loaded["cache"]; !ok {
		t.Fatalf("expected 'cache' in real Loaded map, got %v", list.Loaded)
	}
	if _, ok := list.Database["cache"]; !ok {
		t.Fatalf("expected 'cache' in real Database map, got %v", list.Database)
	}
	foundDefault := false
	for _, n := range list.DefaultPlugins {
		if n == "cache" {
			foundDefault = true
		}
	}
	if !foundDefault {
		t.Fatalf("expected 'cache' to be classified as a default bundled plugin, got %v", list.DefaultPlugins)
	}
	// logtail was never enabled, so it should be in Database (discovered on
	// disk) but not in Loaded — a real, meaningful distinction the real
	// Python plugins module itself makes.
	if _, ok := list.Loaded["logtail"]; ok {
		t.Fatalf("logtail should not be loaded (never enabled), got %v", list.Loaded)
	}
	if _, ok := list.Database["logtail"]; !ok {
		t.Fatalf("expected logtail to be discovered in Database even though disabled, got %v", list.Database)
	}

	changed, err := b.TogglePlugin("logtail", true)
	if err != nil {
		t.Fatalf("TogglePlugin(logtail, true): %v", err)
	}
	if !changed {
		t.Fatalf("expected TogglePlugin to report a real change")
	}

	list2, err := b.ListPlugins()
	if err != nil {
		t.Fatalf("ListPlugins after toggle: %v", err)
	}
	if _, ok := list2.Loaded["logtail"]; !ok {
		t.Fatalf("expected logtail to be really loaded after toggle-on, got %v", list2.Loaded)
	}
}

// TestCompatPyPluginBridgeWebhook proves plugin webhook passthrough
// (handler.py's `/plugins/<name>/<subpath>` route) actually calls the
// real bundled logtail.py plugin's on_webhook with a genuine flask.Request
// built via Flask's own test_request_context, and returns its real
// response (rendered via the real base.html template, proving the
// webhook bridge's Flask app is wired to the real templates directory).
func TestCompatPyPluginBridgeWebhook(t *testing.T) {
	python := pythonBin(t)
	root := repoRoot(t)

	cfg := config.Map{
		"main": config.Map{
			"plugins": config.Map{
				"logtail": config.Map{"enabled": true},
			},
		},
	}

	b, err := pyplugin.New(pyplugin.Options{Python: python, Dir: root, Config: cfg})
	if err != nil {
		t.Fatalf("pyplugin.New: %v", err)
	}
	defer b.Close()

	// logtail.py's on_config_changed (dispatched automatically by
	// plugins.load) sets self.ready = True on its own per-plugin worker
	// thread; give it a moment to run before exercising on_webhook.
	deadline := time.Now().Add(5 * time.Second)
	var resp *pyplugin.WebhookResponse
	for {
		resp, err = b.Webhook("logtail", "", "GET", "/plugins/logtail", "", nil, nil)
		if err != nil {
			t.Fatalf("Webhook: %v", err)
		}
		if resp.Status == 200 && len(resp.Body) > 0 && string(resp.Body) != "Plugin not ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("logtail plugin never became ready within 5s; last body: %q", resp.Body)
		}
		time.Sleep(100 * time.Millisecond)
	}

	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
	t.Logf("real logtail.py on_webhook response (%d bytes): %.200s", len(resp.Body), resp.Body)
}

// fakeViewRef stands in for a *internal/ui/view.View argument the same
// way toJSONArg would stub it in production: an opaque Go type with no
// JSON-safe shape, becoming {"__goref__": "fakeViewRef"} on the wire.
type fakeViewRef struct{}

// TestCompatPyPluginBridgeGracefullyHandlesUnsupportedHardwareAndStubs
// broadens bundled-plugin coverage beyond cache.py/logtail.py with two
// more real, unmodified plugins, each proving a different real resilience
// property required by the porting goal ("unsupported systems return
// clear errors", never a crash):
//
//  1. gpio_buttons.py: real Python's own on_loaded already degrades
//     gracefully when RPi.GPIO isn't importable (this venv has no
//     RPi.GPIO — this dev rig is not a Raspberry Pi) — a real, upstream
//     "no hardware" code path, not something go-port had to add.
//  2. memtemp.py: on_ui_setup calls `ui.is_waveshare_v2()` etc. on its
//     `ui` argument when no explicit position is configured (the
//     realistic default case) — a real *internal/ui/view.View argument
//     becomes an inert _GoProxyStub across the bridge, so this call
//     really does raise NotImplementedError inside the plugin, exactly
//     as docs/known-differences.md describes. This proves that failure
//     is contained (logged, not fatal): the bridge stays fully
//     responsive afterward, verified by a real list_plugins call.
func TestCompatPyPluginBridgeGracefullyHandlesUnsupportedHardwareAndStubs(t *testing.T) {
	python := pythonBin(t)
	root := repoRoot(t)

	cfg := config.Map{
		"main": config.Map{
			"plugins": config.Map{
				"gpio_buttons": config.Map{"enabled": true},
				"memtemp":      config.Map{"enabled": true},
			},
		},
	}

	b, err := pyplugin.New(pyplugin.Options{Python: python, Dir: root, Config: cfg})
	if err != nil {
		t.Fatalf("pyplugin.New: %v", err)
	}
	defer b.Close()

	list, err := b.ListPlugins()
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	for _, name := range []string{"gpio_buttons", "memtemp"} {
		if _, ok := list.Loaded[name]; !ok {
			t.Fatalf("expected %q to be really loaded despite no real hardware, got %v", name, list.Loaded)
		}
	}

	// memtemp's real on_ui_setup will call the stubbed view's
	// is_waveshare_v2()/etc — a real NotImplementedError inside the
	// plugin's own per-plugin worker thread, caught and logged by real
	// Python's own process_events, not propagated back through On().
	b.On("ui_setup", &fakeViewRef{})

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := b.ListPlugins(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bridge stopped responding after the stubbed ui_setup call — a plugin's internal exception must not take down the bridge")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
