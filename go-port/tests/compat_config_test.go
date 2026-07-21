//go:build compatibility

// Package tests holds Python-vs-Go differential tests. Build-tagged
// "compatibility" so they don't run under a plain `go test ./...` (they
// require a working Python venv with the pwnagotchi package importable —
// see docs/python-baseline.md) and are only exercised via `make
// compatibility-test`.
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
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

// runPythonLoadConfig invokes the REAL Python pwnagotchi.utils.load_config
// against a scratch directory and returns the JSON-serializable subset of
// the result we compare against the Go port.
const pythonLoadConfigScript = `
import sys, json
sys.path.insert(0, %q)
from pwnagotchi import utils

class Args:
    config = %q
    user_config = %q

cfg = utils.load_config(Args())
print(json.dumps({
    "main.name": cfg["main"]["name"],
    "ui.display.type": cfg["ui"]["display"]["type"],
    "top_keys": sorted(cfg.keys()),
}))
`

type loadConfigResult struct {
	MainName      string   `json:"main.name"`
	UIDisplayType string   `json:"ui.display.type"`
	TopKeys       []string `json:"top_keys"`
}

func runPythonLoadConfig(t *testing.T, root, cfgPath, userPath string) loadConfigResult {
	t.Helper()
	python := pythonBin(t)
	script := fmt.Sprintf(pythonLoadConfigScript, root, cfgPath, userPath)
	cmd := exec.Command(python, "-c", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("python load_config failed: %v\nstderr:\n%s", err, stderr.String())
	}
	var result loadConfigResult
	// Python prints one non-JSON "copying ..." line before the JSON line
	// when the config file doesn't exist yet; take the last line.
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	last := lines[len(lines)-1]
	if err := json.Unmarshal([]byte(last), &result); err != nil {
		t.Fatalf("parsing python output %q: %v\nfull stdout:\n%s", last, err, stdout.String())
	}
	return result
}

func TestCompatLoadConfigFreshDirectory(t *testing.T) {
	root := repoRoot(t)
	pyDir := t.TempDir()
	goDir := t.TempDir()

	pyResult := runPythonLoadConfig(t, root,
		filepath.Join(pyDir, "default.toml"), filepath.Join(pyDir, "config.toml"))

	goCfg, err := config.LoadConfig(config.Args{
		Config:     filepath.Join(goDir, "default.toml"),
		UserConfig: filepath.Join(goDir, "config.toml"),
	}, nil)
	if err != nil {
		t.Fatalf("go LoadConfig: %v", err)
	}

	if got := goCfg["main"].(config.Map)["name"]; got != pyResult.MainName {
		t.Errorf("main.name: go=%v python=%v", got, pyResult.MainName)
	}
	if got := goCfg["ui"].(config.Map)["display"].(config.Map)["type"]; got != pyResult.UIDisplayType {
		t.Errorf("ui.display.type: go=%v python=%v", got, pyResult.UIDisplayType)
	}
}

func TestCompatLoadConfigUserOverrideAndAlias(t *testing.T) {
	root := repoRoot(t)
	pyDir := t.TempDir()
	goDir := t.TempDir()

	userToml := "[main]\nname = \"unit1\"\n\n[ui.display]\ntype = \"ws4\"\n"
	if err := os.WriteFile(filepath.Join(pyDir, "config.toml"), []byte(userToml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goDir, "config.toml"), []byte(userToml), 0o644); err != nil {
		t.Fatal(err)
	}

	pyResult := runPythonLoadConfig(t, root,
		filepath.Join(pyDir, "default.toml"), filepath.Join(pyDir, "config.toml"))

	goCfg, err := config.LoadConfig(config.Args{
		Config:     filepath.Join(goDir, "default.toml"),
		UserConfig: filepath.Join(goDir, "config.toml"),
	}, nil)
	if err != nil {
		t.Fatalf("go LoadConfig: %v", err)
	}

	if got := goCfg["main"].(config.Map)["name"]; got != pyResult.MainName {
		t.Errorf("main.name: go=%v python=%v", got, pyResult.MainName)
	}
	if got := goCfg["ui"].(config.Map)["display"].(config.Map)["type"]; got != pyResult.UIDisplayType {
		t.Errorf("ui.display.type: go=%v python=%v", got, pyResult.UIDisplayType)
	}
	if pyResult.UIDisplayType != "waveshare_4" {
		t.Fatalf("sanity check failed: python itself did not normalize ws4 to waveshare_4 (got %q) — test fixture is stale", pyResult.UIDisplayType)
	}
}
