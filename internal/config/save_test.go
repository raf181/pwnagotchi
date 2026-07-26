package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveConfigIsAtomicOnEncodingFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := []byte("[main]\nname = \"working\"\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}

	err := SaveConfig(Map{"unsupported": make(chan int)}, path)
	if err == nil {
		t.Fatal("expected unsupported TOML value to fail")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != string(original) {
		t.Fatalf("failed save changed working config: %q", data)
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o, want 640", info.Mode().Perm())
	}
}

func TestSaveConfigReplacesFileAndPreservesMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[old]\nvalue = true\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(Map{"main": Map{"name": "new"}}, path); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadTOMLFileForEdit(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["main"].(Map)["name"] != "new" {
		t.Fatalf("unexpected saved config: %v", cfg)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o, want 640", info.Mode().Perm())
	}
}
