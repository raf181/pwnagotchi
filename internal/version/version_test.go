package version

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestVersionMatchesPython reads ../../../pwnagotchi/_version.py directly so
// this test fails loudly if the Python version is bumped without updating Go.
func TestVersionMatchesPython(t *testing.T) {
	data, err := os.ReadFile("../../../pwnagotchi/_version.py")
	if err != nil {
		t.Skipf("python source not available: %v", err)
	}
	m := regexp.MustCompile(`__version__\s*=\s*'([^']+)'`).FindStringSubmatch(string(data))
	if m == nil {
		t.Fatalf("could not find __version__ in _version.py: %q", data)
	}
	want := strings.TrimSpace(m[1])
	if Version != want {
		t.Errorf("Version = %q, python __version__ = %q", Version, want)
	}
}
