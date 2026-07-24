package plugins

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/config"
)

func TestDoctorPassesWithNoThirdPartyPluginsInstalled(t *testing.T) {
	withTempInstallDir(t)
	rc := Doctor(config.Map{})
	if rc != 0 {
		t.Fatalf("Doctor rc = %d, want 0 with nothing installed", rc)
	}
}

func TestDoctorPassesWhenInstalledChecksumMatches(t *testing.T) {
	installDir := withTempInstallDir(t)
	repo := newFakeRepo(t, map[string]repoPlugin{
		"goodplug": {version: "1.0.0", author: "x", execBin: []byte("real-bytes")},
	})
	cfg := testCfg(repo.srv.URL + "/index.json")
	if rc := Install(cfg, filepath.Join(t.TempDir(), "config.toml"), "goodplug"); rc != 0 {
		t.Fatalf("Install rc = %d", rc)
	}
	_ = installDir

	if rc := Doctor(cfg); rc != 0 {
		t.Fatalf("Doctor rc = %d, want 0 for a checksum-verified install", rc)
	}
}

func TestDoctorFailsWhenExecutableTamperedAfterInstall(t *testing.T) {
	installDir := withTempInstallDir(t)
	repo := newFakeRepo(t, map[string]repoPlugin{
		"plug": {version: "1.0.0", author: "x", execBin: []byte("real-bytes")},
	})
	cfg := testCfg(repo.srv.URL + "/index.json")
	if rc := Install(cfg, filepath.Join(t.TempDir(), "config.toml"), "plug"); rc != 0 {
		t.Fatalf("Install rc = %d", rc)
	}

	// Simulate corruption/tampering after install.
	execPath := filepath.Join(installDir, "plug", "plug")
	if err := os.WriteFile(execPath, []byte("corrupted!!"), 0o755); err != nil {
		t.Fatal(err)
	}

	if rc := Doctor(cfg); rc != 1 {
		t.Fatalf("Doctor rc = %d, want 1 for a tampered installed executable", rc)
	}
}
