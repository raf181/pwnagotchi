package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
	"github.com/jayofelony/pwnagotchi/internal/pluginrpc"
)

func writeInstalledFixture(t *testing.T, root, dirName, manifestName string, payload []byte) {
	t.Helper()
	dir := filepath.Join(root, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	execPath := filepath.Join(dir, manifestName)
	if err := os.WriteFile(execPath, payload, 0o755); err != nil {
		t.Fatal(err)
	}
	sum, err := pluginrpc.SHA256File(execPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`manifest_version = 1
name = %q
version = "1.0.0"
os = %q
arch = %q
sha256 = %q
executable_url = "https://example.invalid/plugin"
capabilities = ["Log"]
`, manifestName, runtime.GOOS, runtime.GOARCH, sum)
	if err := os.WriteFile(filepath.Join(dir, "manifest.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterInstalledAddsVerifiedPluginToManager(t *testing.T) {
	root := withTempInstallDir(t)
	writeInstalledFixture(t, root, "remote-example", "remote-example", []byte("fixture"))

	mgr := pluginmanager.New(pluginmanager.Options{})
	if errs := RegisterInstalled(mgr); len(errs) != 0 {
		t.Fatalf("RegisterInstalled errors: %v", errs)
	}
	if !mgr.Has("remote-example") {
		t.Fatal("verified installed plugin was not registered")
	}
}

func TestRegisterInstalledRejectsChecksumMismatch(t *testing.T) {
	root := withTempInstallDir(t)
	writeInstalledFixture(t, root, "remote-example", "remote-example", []byte("fixture"))
	if err := os.WriteFile(filepath.Join(root, "remote-example", "remote-example"), []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}

	mgr := pluginmanager.New(pluginmanager.Options{})
	if errs := RegisterInstalled(mgr); len(errs) != 1 {
		t.Fatalf("RegisterInstalled errors = %v, want one checksum error", errs)
	}
	if mgr.Has("remote-example") {
		t.Fatal("checksum-mismatched plugin was registered")
	}
}

func TestScanInstalledReportsDirectoryManifestMismatch(t *testing.T) {
	root := withTempInstallDir(t)
	writeInstalledFixture(t, root, "directory-name", "manifest-name", []byte("fixture"))
	installed, errs := scanInstalled()
	if len(installed) != 0 || len(errs) != 1 {
		t.Fatalf("scanInstalled installed=%v errors=%v", installed, errs)
	}
}
