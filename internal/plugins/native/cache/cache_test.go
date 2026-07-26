package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
)

// fakeClock is an injectable, test-controlled clock.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func newTestPlugin(t *testing.T) (*Plugin, string, *fakeClock) {
	t.Helper()
	handshakesDir := t.TempDir()
	p := New()
	fc := &fakeClock{now: time.Now()}
	p.clock = fc
	fullCfg := config.Map{"bettercap": config.Map{"handshakes": handshakesDir}}
	p.HandleEvent("config_changed", []interface{}{fullCfg})
	if !p.ready {
		t.Fatal("expected plugin to be ready after config_changed")
	}
	return p, filepath.Join(handshakesDir, "cache"), fc
}

func TestConfigChangedCreatesCacheDirAndMakesReady(t *testing.T) {
	_, cacheDir, _ := newTestPlugin(t)
	if info, err := os.Stat(cacheDir); err != nil || !info.IsDir() {
		t.Fatalf("expected cache dir to exist at %s: %v", cacheDir, err)
	}
}

func TestConfigChangedMissingHandshakesDirLeavesNotReady(t *testing.T) {
	p := New()
	p.HandleEvent("config_changed", []interface{}{config.Map{"bettercap": config.Map{}}})
	if p.ready {
		t.Fatal("expected plugin to remain not-ready with no bettercap.handshakes")
	}
}

func apMap(mac, hostname string) map[string]interface{} {
	return map[string]interface{}{"mac": mac, "hostname": hostname}
}

func TestWifiUpdateCachesNamedAPsOnly(t *testing.T) {
	p, cacheDir, _ := newTestPlugin(t)
	aps := []map[string]interface{}{
		apMap("AA:BB:CC:DD:EE:01", "homewifi"),
		apMap("AA:BB:CC:DD:EE:02", ""),
		apMap("AA:BB:CC:DD:EE:03", "<hidden>"),
	}
	p.HandleEvent("wifi_update", []interface{}{nil, aps})

	if _, err := os.Stat(filepath.Join(cacheDir, "homewifi_AABBCCDDEE01"+cacheFileExt)); err != nil {
		t.Fatalf("expected cache file for named AP: %v", err)
	}
	entries, _ := os.ReadDir(cacheDir)
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 cache file (empty/hidden hostnames skipped), got %d: %v", len(entries), entries)
	}
}

func TestUnfilteredAPListAcceptsInterfaceSlice(t *testing.T) {
	p, cacheDir, _ := newTestPlugin(t)
	// Go's asSlice()-style plain []interface{} of maps, as unfiltered_ap_list
	// actually delivers it (unlike wifi_update's concrete []agent.AP).
	aps := []interface{}{apMap("11:22:33:44:55:66", "somebody")}
	p.HandleEvent("unfiltered_ap_list", []interface{}{nil, aps})
	if _, err := os.Stat(filepath.Join(cacheDir, "somebody_112233445566"+cacheFileExt)); err != nil {
		t.Fatalf("expected cache file: %v", err)
	}
}

func TestAssociationAndDeauthenticationCacheTheAP(t *testing.T) {
	p, cacheDir, _ := newTestPlugin(t)
	ap := apMap("AA:AA:AA:AA:AA:AA", "assocap")
	p.HandleEvent("association", []interface{}{nil, ap})
	if _, err := os.Stat(filepath.Join(cacheDir, "assocap_AAAAAAAAAAAA"+cacheFileExt)); err != nil {
		t.Fatalf("expected cache file from association: %v", err)
	}

	ap2 := apMap("BB:BB:BB:BB:BB:BB", "deauthap")
	p.HandleEvent("deauthentication", []interface{}{nil, ap2, map[string]interface{}{"mac": "sta"}})
	if _, err := os.Stat(filepath.Join(cacheDir, "deauthap_BBBBBBBBBBBB"+cacheFileExt)); err != nil {
		t.Fatalf("expected cache file from deauthentication: %v", err)
	}
}

// TestHostnameSanitizationStripsNonAlphanumerics ports the real regex
// (re.sub(r"[^a-zA-Z0-9]", "", hostname)) used to build the cache
// filename — hyphens/spaces/etc. are stripped, not preserved or escaped.
func TestHostnameSanitizationStripsNonAlphanumerics(t *testing.T) {
	p, cacheDir, _ := newTestPlugin(t)
	ap := apMap("EE:EE:EE:EE:EE:EE", "my-router 2")
	p.writeAPCache(ap)
	if _, err := os.Stat(filepath.Join(cacheDir, "myrouter2_EEEEEEEEEEEE"+cacheFileExt)); err != nil {
		t.Fatalf("expected sanitized-hostname cache file: %v", err)
	}
}

func TestCacheRejectsUnsafeMACFilename(t *testing.T) {
	p, cacheDir, _ := newTestPlugin(t)
	p.writeAPCache(apMap("../../outside", "router"))
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unsafe MAC created cache files: %v", entries)
	}
}

func TestHandshakeWithMapAPCachesRealFile(t *testing.T) {
	p, cacheDir, _ := newTestPlugin(t)
	ap := apMap("CC:CC:CC:CC:CC:CC", "handshakeap")
	p.HandleEvent("handshake", []interface{}{nil, "/some/file.pcap", ap, map[string]interface{}{}})
	if _, err := os.Stat(filepath.Join(cacheDir, "handshakeap_CCCCCCCCCCCC"+cacheFileExt)); err != nil {
		t.Fatalf("expected cache file from handshake: %v", err)
	}
}

// TestHandshakeWithStringAPDoesNotPanic ports the real Python quirk where
// agent.py sometimes emits a plain BSSID string instead of a dict for
// access_point (see agent.py's two different plugins.on('handshake', ...)
// call sites) — must degrade safely, never panic the plugin/daemon.
func TestHandshakeWithStringAPDoesNotPanic(t *testing.T) {
	p, _, _ := newTestPlugin(t)
	p.HandleEvent("handshake", []interface{}{nil, "/some/file.pcap", "AA:BB:CC:DD:EE:FF", "11:22:33:44:55:66"})
}

func TestCleanAPCacheRemovesOnlyStaleFiles(t *testing.T) {
	p, cacheDir, fc := newTestPlugin(t)
	old := filepath.Join(cacheDir, "old"+cacheFileExt)
	fresh := filepath.Join(cacheDir, "fresh"+cacheFileExt)
	if err := os.WriteFile(old, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fresh, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := fc.now.Add(-10 * time.Minute)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	p.cleanAPCache()

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("expected stale file removed, stat err = %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("expected fresh file kept: %v", err)
	}
}

func TestUiUpdateOnlyCleansEvery60Seconds(t *testing.T) {
	p, cacheDir, fc := newTestPlugin(t)
	stale := filepath.Join(cacheDir, "stale"+cacheFileExt)
	if err := os.WriteFile(stale, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	staleTime := fc.now.Add(-10 * time.Minute)
	os.Chtimes(stale, staleTime, staleTime)

	// lastClean was just set by config_changed (newTestPlugin), so this
	// tick, only 30s later, must NOT clean yet.
	fc.now = fc.now.Add(30 * time.Second)
	p.HandleEvent("ui_update", nil)
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("expected no cleanup before the 60s interval elapses")
	}

	fc.now = fc.now.Add(40 * time.Second) // total 70s since lastClean
	p.HandleEvent("ui_update", nil)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("expected cleanup once 60s has elapsed")
	}
}

func TestOnUnloadCleansCache(t *testing.T) {
	p, cacheDir, fc := newTestPlugin(t)
	stale := filepath.Join(cacheDir, "stale"+cacheFileExt)
	os.WriteFile(stale, []byte("{}"), 0o644)
	staleTime := fc.now.Add(-10 * time.Minute)
	os.Chtimes(stale, staleTime, staleTime)

	if err := p.OnUnload(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("expected OnUnload to clean stale cache files")
	}
}

func TestReadAPCacheRoundTrip(t *testing.T) {
	p, cacheDir, _ := newTestPlugin(t)
	ap := apMap("DD:DD:DD:DD:DD:DD", "myrouter")
	p.writeAPCache(ap)

	got, ok := ReadAPCache(cacheDir, "/etc/pwnagotchi/handshakes/myrouter_DDDDDDDDDDDD.pcap")
	if !ok {
		t.Fatal("expected ReadAPCache to find the written entry")
	}
	if got["hostname"] != "myrouter" {
		t.Fatalf("unexpected cached AP: %v", got)
	}
}

func TestReadAPCacheMissingReturnsFalse(t *testing.T) {
	_, cacheDir, _ := newTestPlugin(t)
	if _, ok := ReadAPCache(cacheDir, "/nowhere/nope.pcap"); ok {
		t.Fatal("expected ReadAPCache to report not-found for a nonexistent entry")
	}
}
