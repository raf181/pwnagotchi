package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
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
	// Captured from: line.format(name='wpa-sec', width=10, version='1.2.3',
	// enabled='enabled', status='installed', author='n/a')
	got := "|" + centerPad("wpa-sec", 10) + "|" + centerPad("1.2.3", 9) + "|" +
		centerPad("enabled", 10) + "|" + centerPad("installed", 15) + "|" + centerPad("n/a", 22) + "|"
	want := "| wpa-sec  |  1.2.3  | enabled  |   installed   |         n/a          |"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func writePlugin(t *testing.T, dir, name, version, author string) string {
	t.Helper()
	path := filepath.Join(dir, name+".py")
	content := "__version__ = '" + version + "'\n__author__ = '" + author + "'\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func setupDirs(t *testing.T) (available, installed, custom string) {
	t.Helper()
	dir := t.TempDir()
	available = filepath.Join(dir, "available")
	installed = filepath.Join(dir, "installed")
	custom = filepath.Join(dir, "custom")
	for _, d := range []string{available, installed, custom} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	origSave, origInstall, origDefault := SaveDir, DefaultInstallPath, DefaultPluginsPath
	SaveDir = available
	DefaultInstallPath = filepath.Join(dir, "default-install")
	DefaultPluginsPath = installed
	t.Cleanup(func() {
		SaveDir, DefaultInstallPath, DefaultPluginsPath = origSave, origInstall, origDefault
	})
	return available, installed, custom
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

func TestListPluginsAvailableOnly(t *testing.T) {
	available, _, _ := setupDirs(t)
	writePlugin(t, available, "wpa-sec", "1.2.3", "evilsocket")

	cfg := config.Map{"main": config.Map{"plugins": config.Map{}}}
	rc := ListPlugins(cfg, false, "*")
	if rc != 0 {
		t.Fatalf("ListPlugins rc = %d", rc)
	}
}

func TestListPluginsNoneFoundReturns1(t *testing.T) {
	setupDirs(t)
	cfg := config.Map{"main": config.Map{"plugins": config.Map{}}}
	rc := ListPlugins(cfg, false, "*")
	if rc != 1 {
		t.Fatalf("ListPlugins rc = %d, want 1 when nothing available", rc)
	}
}

func TestInstallCopiesFileAndUpdatesConfig(t *testing.T) {
	available, _, custom := setupDirs(t)
	writePlugin(t, available, "newplug", "1.0.0", "someone")

	dir := t.TempDir()
	userConfigPath := filepath.Join(dir, "config.toml")
	cfg := config.Map{"main": config.Map{"plugins": config.Map{}, "custom_plugins": custom}}

	rc := Install(cfg, userConfigPath, "newplug")
	if rc != 0 {
		t.Fatalf("Install rc = %d", rc)
	}
	if _, err := os.Stat(filepath.Join(custom, "newplug.py")); err != nil {
		t.Fatal("expected plugin file to be copied into custom_plugins")
	}
}

func TestInstallNotFoundReturns1(t *testing.T) {
	setupDirs(t)
	dir := t.TempDir()
	cfg := config.Map{"main": config.Map{"plugins": config.Map{}, "custom_plugins": dir}}
	rc := Install(cfg, filepath.Join(dir, "config.toml"), "doesnotexist")
	if rc != 1 {
		t.Fatalf("Install rc = %d, want 1", rc)
	}
}

func TestUninstallRemovesFile(t *testing.T) {
	_, installed, custom := setupDirs(t)
	pluginPath := writePlugin(t, custom, "toremove", "1.0.0", "x")
	_ = installed

	cfg := config.Map{"main": config.Map{"custom_plugins": custom}}
	rc := Uninstall(cfg, "toremove")
	if rc != 0 {
		t.Fatalf("Uninstall rc = %d", rc)
	}
	if _, err := os.Stat(pluginPath); !os.IsNotExist(err) {
		t.Fatal("expected plugin file to be removed")
	}
}

func TestUninstallNotInstalledReturns1(t *testing.T) {
	setupDirs(t)
	cfg := config.Map{"main": config.Map{}}
	rc := Uninstall(cfg, "nope")
	if rc != 1 {
		t.Fatalf("Uninstall rc = %d, want 1", rc)
	}
}

func TestUpgradeCopiesNewerVersion(t *testing.T) {
	available, _, custom := setupDirs(t)
	writePlugin(t, available, "plug", "2.0.0", "x")
	writePlugin(t, custom, "plug", "1.0.0", "x")

	cfg := config.Map{"main": config.Map{"custom_plugins": custom}}
	Upgrade(cfg, "*")

	data, err := os.ReadFile(filepath.Join(custom, "plug.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "2.0.0") {
		t.Fatalf("expected upgraded content with version 2.0.0, got %s", data)
	}
}
