package pluginrpc

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validManifestTOML = `
manifest_version = 1
name = "example-plugin"
version = "1.2.3"
author = "someone"
license = "MIT"
description = "does a thing"
homepage = "https://example.invalid/example-plugin"
os = "linux"
arch = "arm64"
sha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
executable_url = "https://example.invalid/example-plugin/bin"
capabilities = ["Agent", "View"]
`

func TestParseManifestValid(t *testing.T) {
	m, err := ParseManifest([]byte(validManifestTOML))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Name != "example-plugin" || m.Version != "1.2.3" || m.Arch != "arm64" {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	if len(m.Capabilities) != 2 || m.Capabilities[0] != "Agent" {
		t.Fatalf("unexpected capabilities: %v", m.Capabilities)
	}
}

func TestParseManifestRejectsFutureVersion(t *testing.T) {
	toml := `
manifest_version = 999
name = "x"
version = "1.0.0"
os = "linux"
arch = "arm64"
sha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
executable_url = "https://example.invalid/x"
`
	_, err := ParseManifest([]byte(toml))
	if err == nil {
		t.Fatal("expected an error for a manifest_version newer than this build understands")
	}
}

func TestParseManifestRejectsMissingFields(t *testing.T) {
	cases := map[string]string{
		"missing name": `
manifest_version = 1
version = "1.0.0"
os = "linux"
arch = "arm64"
sha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
executable_url = "https://example.invalid/x"
`,
		"missing version": `
manifest_version = 1
name = "x"
os = "linux"
arch = "arm64"
sha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
executable_url = "https://example.invalid/x"
`,
		"missing os/arch": `
manifest_version = 1
name = "x"
version = "1.0.0"
sha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
executable_url = "https://example.invalid/x"
`,
		"bad sha256 length": `
manifest_version = 1
name = "x"
version = "1.0.0"
os = "linux"
arch = "arm64"
sha256 = "deadbeef"
executable_url = "https://example.invalid/x"
`,
		"missing executable_url": `
manifest_version = 1
name = "x"
version = "1.0.0"
os = "linux"
arch = "arm64"
sha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
`,
	}
	for label, toml := range cases {
		if _, err := ParseManifest([]byte(toml)); err == nil {
			t.Errorf("%s: expected an error, got none", label)
		}
	}
}

func TestParseManifestRejectsInvalidTOML(t *testing.T) {
	if _, err := ParseManifest([]byte("not { valid toml [[[")); err == nil {
		t.Fatal("expected a TOML parse error")
	}
}

func TestParseManifestRejectsUnsafeOrUnsupportedFields(t *testing.T) {
	cases := map[string]string{
		"path traversal name": strings.Replace(validManifestTOML, `name = "example-plugin"`, `name = "../outside"`, 1),
		"hidden name":         strings.Replace(validManifestTOML, `name = "example-plugin"`, `name = ".hidden"`, 1),
		"invalid URL":         strings.Replace(validManifestTOML, `https://example.invalid/example-plugin/bin`, `file:///tmp/plugin`, 1),
		"unsupported cap":     strings.Replace(validManifestTOML, `["Agent", "View"]`, `["System"]`, 1),
		"duplicate cap":       strings.Replace(validManifestTOML, `["Agent", "View"]`, `["Agent", "Agent"]`, 1),
		"unknown field":       validManifestTOML + "\ncapabilites = [\"Agent\"]\n",
	}
	for label, body := range cases {
		if _, err := ParseManifest([]byte(body)); err == nil {
			t.Errorf("%s: expected an error", label)
		}
	}
}

func TestManifestValidateTarget(t *testing.T) {
	m, err := ParseManifest([]byte(validManifestTOML))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ValidateTarget("linux", "arm64"); err != nil {
		t.Fatalf("matching target rejected: %v", err)
	}
	if err := m.ValidateTarget("linux", "amd64"); err == nil {
		t.Fatal("expected a mismatched target to be rejected")
	}
}

func TestReadManifestFileRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.toml")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), MaxManifestBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadManifestFile(path); err == nil {
		t.Fatal("expected an oversized local manifest to be rejected")
	}
}

func TestVerifyExecutableMatchesRealChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("hello world"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum, err := SHA256File(path)
	if err != nil {
		t.Fatal(err)
	}
	m := &Manifest{Name: "x", SHA256: sum}
	if err := m.VerifyExecutable(path); err != nil {
		t.Fatalf("expected a matching checksum to verify cleanly, got: %v", err)
	}
}

func TestVerifyExecutableRejectsMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	os.WriteFile(path, []byte("hello world"), 0o755)

	m := &Manifest{Name: "x", SHA256: "0000000000000000000000000000000000000000000000000000000000000000"[:64]}
	err := m.VerifyExecutable(path)
	if err == nil {
		t.Fatal("expected a checksum mismatch error")
	}
	var mismatch *ErrChecksumMismatch
	if e, ok := err.(*ErrChecksumMismatch); ok {
		mismatch = e
	}
	if mismatch == nil {
		t.Fatalf("expected *ErrChecksumMismatch, got %T", err)
	}
}

func TestVerifyExecutableDetectsTamperingAfterManifestIssued(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	os.WriteFile(path, []byte("original content"), 0o755)
	sum, _ := SHA256File(path)
	m := &Manifest{Name: "x", SHA256: sum}

	// Simulate the file being swapped out after the manifest was issued
	// (a compromised download, a MITM, ...) — this must be caught.
	os.WriteFile(path, []byte("tampered content!!"), 0o755)
	if err := m.VerifyExecutable(path); err == nil {
		t.Fatal("expected tampering to be detected")
	}
}

func TestVerifyExecutableRejectsNonExecutableAndSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin")
	if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := SHA256File(path)
	if err != nil {
		t.Fatal(err)
	}
	m := &Manifest{Name: "plugin", SHA256: sum}
	if err := m.VerifyExecutable(path); err == nil {
		t.Fatal("expected a file without executable permission to be rejected")
	}

	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "plugin-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := m.VerifyExecutable(link); err == nil {
		t.Fatal("expected a symbolic-link executable to be rejected")
	}
}
