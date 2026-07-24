// Package pluginrpc is the Go-only distribution/ABI for third-party
// plugins, replacing the Python *.py package manager
// (internal/plugins/cmd.go's old behavior): a versioned manifest,
// checksum-verified separately-compiled Go executables, and a bounded
// newline-delimited-JSON RPC protocol between the daemon and each
// plugin subprocess — the same architectural idea as internal/pyplugin's
// bridge.py subprocess bridge, but the subprocess is a real, versioned Go
// binary speaking a defined protocol instead of an embedded Python
// script.
//
// See GO_ONLY_MIGRATION_PROMPT.md's plugin-distribution requirement:
// "prefer separately versioned Go executables with a manifest,
// checksum/signature verification, a bounded RPC protocol, explicit
// capabilities, and crash isolation."
package pluginrpc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// CurrentManifestVersion is the manifest schema version this build
// understands. A manifest with a newer ManifestVersion is rejected
// rather than partially/incorrectly interpreted.
const CurrentManifestVersion = 1

// Manifest describes one distributable third-party Go plugin: enough to
// verify, fetch, and spawn it safely without ever executing untrusted
// code the daemon can't first check the identity of.
type Manifest struct {
	ManifestVersion int      `toml:"manifest_version"`
	Name            string   `toml:"name"`
	Version         string   `toml:"version"`
	Author          string   `toml:"author"`
	License         string   `toml:"license"`
	Description     string   `toml:"description"`
	Homepage        string   `toml:"homepage"`
	OS              string   `toml:"os"`             // e.g. "linux"
	Arch            string   `toml:"arch"`           // e.g. "arm64"
	SHA256          string   `toml:"sha256"`         // hex-encoded, lowercase, of the executable this manifest describes
	ExecutableURL   string   `toml:"executable_url"` // where to fetch the compiled plugin binary from
	Capabilities    []string `toml:"capabilities"`   // subset of the pluginmanager.Capabilities fields this plugin declares needing (e.g. "Agent", "View", "Exec")
}

// ParseManifest decodes and validates a manifest's structural
// correctness (not the executable's checksum — see VerifyExecutable for
// that separate, explicit step). Returns a precise error for anything a
// human needs to fix, never a silently-accepted partial manifest.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if _, err := toml.Decode(string(data), &m); err != nil {
		return nil, fmt.Errorf("pluginrpc: invalid manifest TOML: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate checks every required field is present and well-formed.
func (m *Manifest) Validate() error {
	if m.ManifestVersion == 0 {
		return fmt.Errorf("pluginrpc: manifest missing manifest_version")
	}
	if m.ManifestVersion > CurrentManifestVersion {
		return fmt.Errorf("pluginrpc: manifest_version %d is newer than this daemon understands (max %d) — upgrade pwnagotchi first", m.ManifestVersion, CurrentManifestVersion)
	}
	if m.Name == "" {
		return fmt.Errorf("pluginrpc: manifest missing name")
	}
	if m.Version == "" {
		return fmt.Errorf("pluginrpc: manifest %q missing version", m.Name)
	}
	if m.OS == "" || m.Arch == "" {
		return fmt.Errorf("pluginrpc: manifest %q missing os/arch", m.Name)
	}
	sha := strings.ToLower(strings.TrimSpace(m.SHA256))
	if len(sha) != 64 {
		return fmt.Errorf("pluginrpc: manifest %q has an invalid sha256 (want 64 hex chars, got %d)", m.Name, len(sha))
	}
	if _, err := hex.DecodeString(sha); err != nil {
		return fmt.Errorf("pluginrpc: manifest %q sha256 is not valid hex: %w", m.Name, err)
	}
	m.SHA256 = sha
	if m.ExecutableURL == "" {
		return fmt.Errorf("pluginrpc: manifest %q missing executable_url", m.Name)
	}
	return nil
}

// ErrChecksumMismatch is returned by VerifyExecutable when a downloaded
// binary's real sha256 doesn't match the manifest — the daemon must
// refuse to spawn it, loudly, never fall back to running it anyway.
type ErrChecksumMismatch struct {
	Name     string
	Expected string
	Got      string
}

func (e *ErrChecksumMismatch) Error() string {
	return fmt.Sprintf("pluginrpc: checksum mismatch for plugin %q: manifest says %s, file is %s — refusing to run it", e.Name, e.Expected, e.Got)
}

// VerifyExecutable computes the real sha256 of the file at path and
// compares it against the manifest, byte for byte. This is the one gate
// every spawn must pass through first.
func (m *Manifest) VerifyExecutable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("pluginrpc: opening %s for checksum verification: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("pluginrpc: reading %s for checksum verification: %w", path, err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != m.SHA256 {
		return &ErrChecksumMismatch{Name: m.Name, Expected: m.SHA256, Got: got}
	}
	return nil
}

// SHA256File is a small standalone helper (used by the CLI's "package a
// plugin" workflow and by tests) that computes a file's hex sha256 —
// exactly the value that belongs in a manifest's sha256 field.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
