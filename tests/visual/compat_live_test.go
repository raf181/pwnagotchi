//go:build compatibility

package visual

// TestCompatVisualGoldenMatchesLivePython regenerates the Python oracle
// from the REAL, currently-installed pwnagotchi.ui.view.View (not the
// checked-in testdata/python_golden_waveshare_4.png) and proves it's
// byte-identical to the checked-in golden. This is what actually protects
// against the golden silently drifting from real Python behavior (e.g. an
// upstream pwnagotchi/ui/*.py edit, a Pillow upgrade changing rasterization)
// — TestGoMatchesPythonGoldenNativeResolution in golden_test.go only proves
// Go matches whatever golden happens to be checked in, which is only as
// good as this test proving that golden is still real. Runs only under
// `make compatibility-test` (needs the real Python venv from
// docs/python-baseline.md), matching every other tests/compat_*_test.go's
// convention — including the Makefile's `-run TestCompat` filter, which is
// why this function is named with that prefix despite living in package
// visual instead of package tests.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCompatVisualGoldenMatchesLivePython(t *testing.T) {
	pythonBin := os.Getenv("PWNAGOTCHI_PYTHON")
	if pythonBin == "" {
		t.Skip("PWNAGOTCHI_PYTHON not set; run via `make compatibility-test`")
	}
	if _, err := os.Stat(pythonBin); err != nil {
		t.Skipf("python interpreter %s not found: %v", pythonBin, err)
	}
	repoRoot := os.Getenv("PWNAGOTCHI_REPO_ROOT")
	if repoRoot == "" {
		t.Skip("PWNAGOTCHI_REPO_ROOT not set; run via `make compatibility-test`")
	}

	outPath := filepath.Join(t.TempDir(), "live_python_oracle.png")
	cmd := exec.Command(pythonBin, "oracle.py", outPath)
	cmd.Dir = "." // tests/visual, where oracle.py lives
	cmd.Env = append(os.Environ(), "PWNAGOTCHI_REPO_ROOT="+repoRoot)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("running real Python oracle.py: %v\nstderr:\n%s", err, stderr.String())
	}

	live, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading live oracle output: %v", err)
	}
	golden, err := os.ReadFile("testdata/python_golden_waveshare_4.png")
	if err != nil {
		t.Fatalf("reading checked-in golden: %v", err)
	}
	if !bytes.Equal(live, golden) {
		t.Errorf("checked-in golden has drifted from real, live Python rendering — regenerate testdata/python_golden_waveshare_4.png via:\n  PWNAGOTCHI_REPO_ROOT=%s %s tests/visual/oracle.py tests/visual/testdata/python_golden_waveshare_4.png", repoRoot, pythonBin)
	}

	// Also directly re-run the Go-vs-golden comparison so a real
	// compatibility-test invocation exercises the full chain (live Python
	// -> golden -> Go) in one shot, not just the golden byte-equality
	// above.
	t.Run("GoStillMatches", TestGoMatchesPythonGoldenNativeResolution)
}
