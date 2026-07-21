//go:build compatibility

package tests

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildGoBinary builds cmd/pwnagotchi once per test run and returns the
// path to the resulting binary.
func buildGoBinary(t *testing.T, root string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "pwnagotchi-go")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/pwnagotchi")
	cmd.Dir = filepath.Join(root, "go-port")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building cmd/pwnagotchi: %v\n%s", err, out)
	}
	return bin
}

func runPythonCLI(t *testing.T, python, root string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(python, append([]string{"-m", "pwnagotchi.cli"}, args...)...)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

func runGoCLI(t *testing.T, bin string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

// TestCompatCLIVersionHelpDonate verifies the Go binary's --version,
// --help, and --donate output are byte-for-byte identical to the real
// Python CLI's, for both stdout and exit code.
func TestCompatCLIVersionHelpDonate(t *testing.T) {
	python := pythonBin(t)
	root := repoRoot(t)
	bin := buildGoBinary(t, root)

	for _, flag := range []string{"--version", "--help", "--donate"} {
		pyOut, _, pyCode := runPythonCLI(t, python, root, flag)
		goOut, _, goCode := runGoCLI(t, bin, flag)

		if pyCode != goCode {
			t.Errorf("%s: exit code python=%d go=%d", flag, pyCode, goCode)
		}
		if pyOut != goOut {
			t.Errorf("%s: stdout mismatch\n--- python ---\n%s\n--- go ---\n%s", flag, pyOut, goOut)
		}
	}
}
