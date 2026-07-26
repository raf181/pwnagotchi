package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/config"
)

// TestCenterPadMatchesPythonFormatSpec replays real Python
// '{:^N}'.format(s) output — NOT str.center(), which distributes odd
// padding differently (extra space on the left) — verified against the
// real interpreter: Python's format-spec centering puts the extra padding
// on the RIGHT.
func TestCenterPadMatchesPythonFormatSpec(t *testing.T) {
	cases := []struct {
		s     string
		width int
		want  string
	}{
		{"ab", 7, "  ab   "},
		{"Plugin", 10, "  Plugin  "},
		{"Active", 10, "  Active  "},
	}
	for _, c := range cases {
		if got := centerPad(c.s, c.width); got != c.want {
			t.Errorf("centerPad(%q, %d) = %q, want %q", c.s, c.width, got, c.want)
		}
	}
}

func TestListLineFormatMatchesPython(t *testing.T) {
	got := "|" + centerPad("wpa-sec", 10) + "|" + centerPad("1.2.3", 9) + "|" +
		centerPad("enabled", 10) + "|" + centerPad("installed", 15) + "|" + centerPad("n/a", 22) + "|"
	want := "| wpa-sec  |  1.2.3  | enabled  |   installed   |         n/a          |"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestEnableDisable(t *testing.T) {
	dir := t.TempDir()
	userConfigPath := filepath.Join(dir, "config.toml")
	cfg := config.Map{"main": config.Map{"plugins": config.Map{}}}

	if rc := Enable(cfg, userConfigPath, "grid"); rc != 0 {
		t.Fatalf("Enable rc = %d", rc)
	}
	plugins := cfg["main"].(config.Map)["plugins"].(config.Map)
	entry := plugins["grid"].(config.Map)
	if enabled, _ := entry["enabled"].(bool); !enabled {
		t.Fatal("expected grid.enabled = true")
	}

	if rc := Disable(cfg, userConfigPath, "grid"); rc != 0 {
		t.Fatalf("Disable rc = %d", rc)
	}
	if enabled, _ := entry["enabled"].(bool); enabled {
		t.Fatal("expected grid.enabled = false after Disable")
	}

	if _, err := os.Stat(userConfigPath); err != nil {
		t.Fatal("expected config to be saved to disk")
	}
}

// fakeRepo is a real httptest-backed Go-only plugin repository: an
// index listing manifests, manifests naming an executable URL, and the
// executable bytes themselves — all served from the same server, real
// SHA-256 checksums computed from the actual served bytes (never
// hand-typed/possibly-wrong hex).
type fakeRepo struct {
	srv     *httptest.Server
	entries map[string]repoPlugin // name -> plugin
}

type repoPlugin struct {
	version string
	author  string
	execBin []byte
}

func newFakeRepo(t *testing.T, plugins map[string]repoPlugin) *fakeRepo {
	t.Helper()
	r := &fakeRepo{entries: plugins}
	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, r.indexJSON())
	})
	for name, p := range plugins {
		name, p := name, p
		mux.HandleFunc("/manifest/"+name+".toml", func(w http.ResponseWriter, req *http.Request) {
			sum := sha256.Sum256(p.execBin)
			fmt.Fprintf(w, `manifest_version = 1
name = %q
version = %q
author = %q
license = "GPL3"
description = "test plugin"
os = %q
arch = %q
sha256 = %q
executable_url = %q
capabilities = ["Exec"]
`, name, p.version, p.author, runtime.GOOS, runtime.GOARCH, hex.EncodeToString(sum[:]), r.srv.URL+"/exec/"+name)
		})
		mux.HandleFunc("/exec/"+name, func(w http.ResponseWriter, req *http.Request) {
			w.Write(p.execBin)
		})
	}
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

func (r *fakeRepo) indexJSON() string {
	s := `{"index_version":1,"plugins":[`
	first := true
	for name, p := range r.entries {
		if !first {
			s += ","
		}
		first = false
		s += fmt.Sprintf(`{"name":%q,"version":%q,"author":%q,"description":"test plugin","manifest_url":%q}`,
			name, p.version, p.author, r.srv.URL+"/manifest/"+name+".toml")
	}
	s += `]}`
	return s
}

func testCfg(indexURL string) config.Map {
	return config.Map{"main": config.Map{"plugins": config.Map{}, "plugin_repository_index": indexURL}}
}

func withTempInstallDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := PluginInstallDir
	PluginInstallDir = dir
	t.Cleanup(func() { PluginInstallDir = orig })
	return dir
}

func TestListPluginsAvailableOnly(t *testing.T) {
	withTempInstallDir(t)
	repo := newFakeRepo(t, map[string]repoPlugin{
		"coolplug": {version: "1.2.3", author: "someone", execBin: []byte("binary-content")},
	})
	cfg := testCfg(repo.srv.URL + "/index.json")
	rc := ListPlugins(cfg, false, "*")
	if rc != 0 {
		t.Fatalf("ListPlugins rc = %d", rc)
	}
}

func TestListPluginsWithoutRepositoryStillListsBundledPlugins(t *testing.T) {
	withTempInstallDir(t)
	cfg := config.Map{"main": config.Map{"plugins": config.Map{}}}
	rc := ListPlugins(cfg, false, "*")
	if rc != 0 {
		t.Fatalf("ListPlugins rc = %d, want 0 because bundled plugins are available", rc)
	}
}

func TestUpdateValidatesConfiguredRepository(t *testing.T) {
	repo := newFakeRepo(t, map[string]repoPlugin{
		"available": {version: "1.0.0", author: "someone", execBin: []byte("bin")},
	})
	if rc := Update(testCfg(repo.srv.URL + "/index.json")); rc != 0 {
		t.Fatalf("Update rc = %d, want valid repository", rc)
	}
}

func TestUpdateRejectsInvalidRepository(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"index_version":999,"plugins":[]}`)
	}))
	t.Cleanup(srv.Close)
	if rc := Update(testCfg(srv.URL)); rc != 1 {
		t.Fatalf("Update rc = %d, want invalid repository failure", rc)
	}
}

func TestInstallDownloadsVerifiesAndPersistsManifest(t *testing.T) {
	installDir := withTempInstallDir(t)
	repo := newFakeRepo(t, map[string]repoPlugin{
		"newplug": {version: "1.0.0", author: "someone", execBin: []byte("real-binary-bytes")},
	})
	dir := t.TempDir()
	userConfigPath := filepath.Join(dir, "config.toml")
	cfg := testCfg(repo.srv.URL + "/index.json")

	rc := Install(cfg, userConfigPath, "newplug")
	if rc != 0 {
		t.Fatalf("Install rc = %d", rc)
	}

	execPath := filepath.Join(installDir, "newplug", "newplug")
	data, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("expected executable installed: %v", err)
	}
	if string(data) != "real-binary-bytes" {
		t.Fatalf("unexpected executable content: %s", data)
	}
	if info, err := os.Stat(execPath); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("expected installed executable to be executable, mode=%v err=%v", info.Mode(), err)
	}

	manifestPath := filepath.Join(installDir, "newplug", "manifest.toml")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected manifest.toml persisted: %v", err)
	}

	installed := installedManifests()
	m, ok := installed["newplug"]
	if !ok {
		t.Fatal("expected newplug to appear in installedManifests()")
	}
	if m.Version != "1.0.0" || m.Author != "someone" {
		t.Fatalf("unexpected manifest fields: %+v", m)
	}
}

// TestInstallRefusesOnChecksumMismatch serves a manifest whose sha256
// was computed for one payload, but an executable endpoint that returns
// different bytes (simulating tampering/corruption in transit) — Install
// must refuse and leave nothing behind.
func TestInstallRefusesOnChecksumMismatch(t *testing.T) {
	installDir := withTempInstallDir(t)

	original := []byte("original-bytes")
	tampered := []byte("tampered-bytes!!")
	sum := sha256.Sum256(original)

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"index_version":1,"plugins":[{"name":"badplug","version":"1.0.0","author":"x","description":"d","manifest_url":%q}]}`, srv.URL+"/manifest/badplug.toml")
	})
	mux.HandleFunc("/manifest/badplug.toml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `manifest_version = 1
name = "badplug"
version = "1.0.0"
author = "x"
license = "GPL3"
description = "d"
os = %q
arch = %q
sha256 = %q
executable_url = %q
capabilities = []
`, runtime.GOOS, runtime.GOARCH, hex.EncodeToString(sum[:]), srv.URL+"/exec/badplug")
	})
	mux.HandleFunc("/exec/badplug", func(w http.ResponseWriter, r *http.Request) {
		w.Write(tampered) // deliberately NOT what the manifest's sha256 describes
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := testCfg(srv.URL + "/index.json")
	rc := Install(cfg, filepath.Join(t.TempDir(), "config.toml"), "badplug")
	if rc == 0 {
		t.Fatal("expected Install to fail on checksum mismatch")
	}
	if _, err := os.Stat(filepath.Join(installDir, "badplug")); !os.IsNotExist(err) {
		t.Fatal("expected no plugin directory left behind after a checksum failure")
	}
}

func TestInstallNotFoundReturns1(t *testing.T) {
	withTempInstallDir(t)
	repo := newFakeRepo(t, map[string]repoPlugin{})
	cfg := testCfg(repo.srv.URL + "/index.json")
	rc := Install(cfg, filepath.Join(t.TempDir(), "config.toml"), "doesnotexist")
	if rc != 1 {
		t.Fatalf("Install rc = %d, want 1", rc)
	}
}

func TestInstallBundledPluginIsNoOpSuccess(t *testing.T) {
	withTempInstallDir(t)
	cfg := config.Map{"main": config.Map{"plugins": config.Map{}}}
	rc := Install(cfg, filepath.Join(t.TempDir(), "config.toml"), "wpa-sec")
	if rc != 0 {
		t.Fatalf("Install(bundled) rc = %d, want 0 (no-op success)", rc)
	}
	if _, ok := installedManifests()["wpa-sec"]; ok {
		t.Fatal("expected no manifest written for a bundled plugin")
	}
}

// TestInstallLegacyPythonPluginFailsWithMigrationMessage is the single
// most important behavioral requirement of the whole Go-only plugin
// distribution system: an existing Python plugin must never be
// installed/run, and must never be silently treated as loaded.
func TestInstallLegacyPythonPluginFailsWithMigrationMessage(t *testing.T) {
	withTempInstallDir(t)
	customDir := t.TempDir()
	pyPath := filepath.Join(customDir, "oldplugin.py")
	if err := os.WriteFile(pyPath, []byte("# legacy python plugin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Map{"main": config.Map{"plugins": config.Map{}, "custom_plugins": customDir}}

	rc := Install(cfg, filepath.Join(t.TempDir(), "config.toml"), "oldplugin")
	if rc != 1 {
		t.Fatalf("Install(legacy python) rc = %d, want 1", rc)
	}
	if _, ok := installedManifests()["oldplugin"]; ok {
		t.Fatal("legacy python plugin must never be recorded as installed")
	}
	// The .py file itself must be left untouched, never executed or moved.
	if _, err := os.Stat(pyPath); err != nil {
		t.Fatal("expected the legacy .py file to be left in place, untouched")
	}
}

func TestUninstallRemovesInstalledPlugin(t *testing.T) {
	installDir := withTempInstallDir(t)
	repo := newFakeRepo(t, map[string]repoPlugin{
		"toremove": {version: "1.0.0", author: "x", execBin: []byte("bin")},
	})
	cfg := testCfg(repo.srv.URL + "/index.json")
	if rc := Install(cfg, filepath.Join(t.TempDir(), "config.toml"), "toremove"); rc != 0 {
		t.Fatalf("Install rc = %d", rc)
	}

	rc := Uninstall(cfg, "toremove")
	if rc != 0 {
		t.Fatalf("Uninstall rc = %d", rc)
	}
	if _, err := os.Stat(filepath.Join(installDir, "toremove")); !os.IsNotExist(err) {
		t.Fatal("expected plugin directory to be removed")
	}
}

func TestUninstallNotInstalledReturns1(t *testing.T) {
	withTempInstallDir(t)
	cfg := config.Map{"main": config.Map{}}
	rc := Uninstall(cfg, "nope")
	if rc != 1 {
		t.Fatalf("Uninstall rc = %d, want 1", rc)
	}
}

func TestUninstallRemovesInstallationWithCorruptManifest(t *testing.T) {
	installDir := withTempInstallDir(t)
	dir := filepath.Join(installDir, "broken")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.toml"), []byte("not toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := Uninstall(nil, "broken"); rc != 0 {
		t.Fatalf("Uninstall rc = %d, want corrupt installation removable", rc)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("corrupt installation still exists: %v", err)
	}
}

func TestUpgradeInstallsNewerVersion(t *testing.T) {
	installDir := withTempInstallDir(t)
	repoV1 := newFakeRepo(t, map[string]repoPlugin{
		"plug": {version: "1.0.0", author: "x", execBin: []byte("v1")},
	})
	cfg := testCfg(repoV1.srv.URL + "/index.json")
	if rc := Install(cfg, filepath.Join(t.TempDir(), "config.toml"), "plug"); rc != 0 {
		t.Fatalf("initial Install rc = %d", rc)
	}

	repoV2 := newFakeRepo(t, map[string]repoPlugin{
		"plug": {version: "2.0.0", author: "x", execBin: []byte("v2-content")},
	})
	cfg["main"].(config.Map)["plugin_repository_index"] = repoV2.srv.URL + "/index.json"

	if rc := Upgrade(cfg, "*"); rc != 0 {
		t.Fatalf("Upgrade rc = %d", rc)
	}

	data, err := os.ReadFile(filepath.Join(installDir, "plug", "plug"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v2-content" {
		t.Fatalf("expected upgraded executable content, got %s", data)
	}
	m := installedManifests()["plug"]
	if m.Version != "2.0.0" {
		t.Fatalf("expected manifest version 2.0.0 after upgrade, got %s", m.Version)
	}
}

func TestUpgradeSkipsWhenAlreadyLatest(t *testing.T) {
	installDir := withTempInstallDir(t)
	repo := newFakeRepo(t, map[string]repoPlugin{
		"plug": {version: "1.0.0", author: "x", execBin: []byte("v1")},
	})
	cfg := testCfg(repo.srv.URL + "/index.json")
	if rc := Install(cfg, filepath.Join(t.TempDir(), "config.toml"), "plug"); rc != 0 {
		t.Fatalf("Install rc = %d", rc)
	}
	before, _ := os.Stat(filepath.Join(installDir, "plug", "plug"))

	if rc := Upgrade(cfg, "*"); rc != 0 {
		t.Fatalf("Upgrade rc = %d", rc)
	}
	after, _ := os.Stat(filepath.Join(installDir, "plug", "plug"))
	if before.ModTime() != after.ModTime() {
		t.Fatal("expected no re-download when already at the latest version")
	}
}

func TestInstallRejectsUnsafeNameBeforeFilesystemAccess(t *testing.T) {
	installDir := withTempInstallDir(t)
	cfg := config.Map{"main": config.Map{"plugins": config.Map{}}}
	if rc := Install(cfg, filepath.Join(t.TempDir(), "config.toml"), "../outside"); rc != 1 {
		t.Fatalf("Install rc = %d, want 1", rc)
	}
	if entries, err := os.ReadDir(installDir); err != nil || len(entries) != 0 {
		t.Fatalf("unsafe name changed install directory: entries=%v err=%v", entries, err)
	}
}

func TestUpgradeFailurePreservesInstalledVersion(t *testing.T) {
	installDir := withTempInstallDir(t)
	repoV1 := newFakeRepo(t, map[string]repoPlugin{
		"stable": {version: "1.0.0", author: "x", execBin: []byte("stable-v1")},
	})
	cfg := testCfg(repoV1.srv.URL + "/index.json")
	if rc := Install(cfg, filepath.Join(t.TempDir(), "config.toml"), "stable"); rc != 0 {
		t.Fatalf("initial Install rc = %d", rc)
	}

	expected := sha256.Sum256([]byte("expected-v2"))
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"index_version":1,"plugins":[{"name":"stable","version":"2.0.0","manifest_url":%q}]}`, srv.URL+"/manifest.toml")
	})
	mux.HandleFunc("/manifest.toml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `manifest_version = 1
name = "stable"
version = "2.0.0"
os = %q
arch = %q
sha256 = %q
executable_url = %q
capabilities = []
`, runtime.GOOS, runtime.GOARCH, hex.EncodeToString(expected[:]), srv.URL+"/stable")
	})
	mux.HandleFunc("/stable", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("corrupt-v2"))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg["main"].(config.Map)["plugin_repository_index"] = srv.URL + "/index.json"

	if rc := Upgrade(cfg, "stable"); rc != 1 {
		t.Fatalf("Upgrade rc = %d, want 1", rc)
	}
	data, err := os.ReadFile(filepath.Join(installDir, "stable", "stable"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "stable-v1" {
		t.Fatalf("failed upgrade replaced working executable: %q", data)
	}
	if got := installedManifests()["stable"].Version; got != "1.0.0" {
		t.Fatalf("failed upgrade replaced working manifest: version=%s", got)
	}
}
