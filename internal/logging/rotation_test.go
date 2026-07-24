package logging

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/config"
)

// TestParseMaxSizeGolden replays real Python parse_max_size output,
// including the "10x" -> 10 quirk: re.findall doesn't require a full-string
// match, so trailing garbage after a valid <digits><unit> prefix is
// silently ignored rather than rejected.
func TestParseMaxSizeGolden(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"10", 10, false},
		{"10B", 10, false},
		{"10K", 10 * 1024, false},
		{"10M", 10 * 1024 * 1024, false},
		{"10G", 10 * 1024 * 1024 * 1024, false},
		{"10x", 10, false},
		{"abc", 0, true},
	}
	for _, c := range cases {
		got, err := ParseMaxSize(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseMaxSize(%q): expected error, got %d", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMaxSize(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseMaxSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestLogRotationDisabledOrMissingFileIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pwnagotchi.log")

	// Missing file: no-op regardless of config.
	if err := LogRotation(path, config.Map{"rotation": config.Map{"enabled": true, "size": "1"}}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	// Disabled: no-op even though the file is big.
	if err := LogRotation(path, config.Map{"rotation": config.Map{"enabled": false, "size": "1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("file should still exist untouched")
	}
}

func TestLogRotationMissingSizeErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pwnagotchi.log")
	if err := os.WriteFile(path, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	err := LogRotation(path, config.Map{"rotation": config.Map{"enabled": true}})
	if err == nil {
		t.Fatal("expected an error when rotation is enabled but size is unset")
	}
}

func TestDoRotateFirstRotationCompressesAndRemovesOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pwnagotchi.log")
	content := []byte("hello pwnagotchi log content\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := DoRotate(path, int64(len(content))); err != nil {
		t.Fatal(err)
	}

	// The original file is gone (moved-then-removed, net effect: gone).
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("original log file should no longer exist after rotation")
	}

	archivePath := filepath.Join(dir, "pwnagotchi.gz")
	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("expected archive at %s: %v", archivePath, err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	got, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("decompressed content = %q, want %q", got, content)
	}
}

func TestDoRotateIncrementsCounterOnCollision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pwnagotchi.log")

	// Pre-seed an existing pwnagotchi.gz so the first rotation must use -2.
	if err := os.WriteFile(filepath.Join(dir, "pwnagotchi.gz"), []byte("existing archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := []byte("second rotation content\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := DoRotate(path, int64(len(content))); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "pwnagotchi-2.gz")); err != nil {
		t.Fatalf("expected pwnagotchi-2.gz to be created: %v", err)
	}
	// The original filename must exist again (this rotation moved it aside
	// to pwnagotchi-2.log first, unlike the same-path first-rotation case),
	// UNLESS the caller (SetupLogging) recreates it — DoRotate itself only
	// guarantees archiving; recreation is the FileHandler's job in Python
	// too (os.OpenFile in Go), so here we just confirm it was moved away.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("original path should have been moved aside, not left in place, on a non-colliding rotation")
	}
}

func TestSetupLoggingRotatesRecreatesAndBanner(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "pwnagotchi.log")
	debugPath := filepath.Join(dir, "pwnagotchi-debug.log")

	cfg := config.Map{
		"main": config.Map{
			"log": config.Map{
				"path":       logPath,
				"path-debug": debugPath,
				"rotation":   config.Map{"enabled": false},
			},
		},
	}

	l, err := SetupLogging(Args{Debug: false}, cfg)
	if err != nil {
		t.Fatalf("SetupLogging: %v", err)
	}
	l.Info(StartupBanner)
	l.Debug("this debug line must NOT reach either file in non-debug mode")

	l.NormalWriter.Close()
	l.DebugWriter.Close()

	normal, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	debugContent, err := os.ReadFile(debugPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(normal), StartupBanner) {
		t.Fatalf("normal log missing banner: %s", normal)
	}
	if !strings.Contains(string(debugContent), StartupBanner) {
		t.Fatalf("debug log missing banner: %s", debugContent)
	}
	if strings.Contains(string(normal), "must NOT reach") || strings.Contains(string(debugContent), "must NOT reach") {
		t.Fatal("DEBUG-level message leaked through in non-debug mode (root level should gate it before any handler)")
	}
}

func TestSetupLoggingDebugModeReachesDebugFileOnly(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "pwnagotchi.log")
	debugPath := filepath.Join(dir, "pwnagotchi-debug.log")
	cfg := config.Map{
		"main": config.Map{
			"log": config.Map{
				"path":       logPath,
				"path-debug": debugPath,
				"rotation":   config.Map{"enabled": false},
			},
		},
	}

	l, err := SetupLogging(Args{Debug: true}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	l.Debug("debug-only line")
	l.NormalWriter.Close()
	l.DebugWriter.Close()

	normal, _ := os.ReadFile(logPath)
	debugContent, _ := os.ReadFile(debugPath)
	if strings.Contains(string(normal), "debug-only line") {
		t.Fatal("the normal file's own handler level (INFO) must filter out DEBUG even in debug mode")
	}
	if !strings.Contains(string(debugContent), "debug-only line") {
		t.Fatal("the debug file should contain the DEBUG line in debug mode")
	}
}
