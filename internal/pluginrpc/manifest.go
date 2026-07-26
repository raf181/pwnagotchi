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
// checksum verification, a bounded RPC protocol, explicit
// capabilities, and crash isolation."
package pluginrpc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// CurrentManifestVersion is the manifest schema version this build
// understands. A manifest with a newer ManifestVersion is rejected
// rather than partially/incorrectly interpreted.
const CurrentManifestVersion = 1

var pluginNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

var supportedCapabilityGroups = map[string]bool{
	"Log":   true,
	"Agent": true,
	"View":  true,
	"Exec":  true,
	"Clock": true,
}

// Manifest describes one distributable third-party Go plugin. SHA256
// protects the executable against accidental corruption or a mismatch
// with this manifest; the manifest is not signed, so repository trust is
// still required.
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
	metadata, err := toml.Decode(string(data), &m)
	if err != nil {
		return nil, fmt.Errorf("pluginrpc: invalid manifest TOML: %w", err)
	}
	if unknown := metadata.Undecoded(); len(unknown) > 0 {
		return nil, fmt.Errorf("pluginrpc: manifest contains unknown field %q", unknown[0].String())
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// ReadManifestFile reads and validates a local manifest without allowing a
// corrupt file to allocate unbounded memory.
func ReadManifestFile(path string) (*Manifest, []byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("pluginrpc: manifest %s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	data, err := readLimited(f, MaxManifestBytes, "plugin manifest")
	if err != nil {
		return nil, nil, err
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return nil, nil, err
	}
	return manifest, data, nil
}

// Validate checks every required field is present and well-formed.
func (m *Manifest) Validate() error {
	if m.ManifestVersion == 0 {
		return fmt.Errorf("pluginrpc: manifest missing manifest_version")
	}
	if m.ManifestVersion != CurrentManifestVersion {
		return fmt.Errorf("pluginrpc: unsupported manifest_version %d (want %d)", m.ManifestVersion, CurrentManifestVersion)
	}
	if err := ValidatePluginName(m.Name); err != nil {
		return err
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
	if err := validateHTTPURL(m.ExecutableURL); err != nil {
		return fmt.Errorf("pluginrpc: manifest %q has invalid executable_url: %w", m.Name, err)
	}
	seenCapabilities := map[string]bool{}
	for _, capability := range m.Capabilities {
		if !supportedCapabilityGroups[capability] {
			return fmt.Errorf("pluginrpc: manifest %q requests unsupported capability %q", m.Name, capability)
		}
		if seenCapabilities[capability] {
			return fmt.Errorf("pluginrpc: manifest %q lists capability %q more than once", m.Name, capability)
		}
		seenCapabilities[capability] = true
	}
	return nil
}

// ValidatePluginName rejects values that could escape the install root or
// produce ambiguous config keys.
func ValidatePluginName(name string) error {
	if name == "" {
		return fmt.Errorf("pluginrpc: manifest missing name")
	}
	if !pluginNamePattern.MatchString(name) {
		return fmt.Errorf("pluginrpc: invalid plugin name %q (use 1-64 letters, digits, dot, underscore, or hyphen; start with a letter or digit)", name)
	}
	return nil
}

// ValidateTarget ensures a package was built for this daemon's runtime.
func (m *Manifest) ValidateTarget(goos, goarch string) error {
	if m.OS != goos || m.Arch != goarch {
		return fmt.Errorf("pluginrpc: plugin %q targets %s/%s, but this daemon runs on %s/%s", m.Name, m.OS, m.Arch, goos, goarch)
	}
	return nil
}

func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("URL must include a host")
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
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("pluginrpc: inspecting %s for checksum verification: %w", path, err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("pluginrpc: executable %s is a symbolic link", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("pluginrpc: opening %s for checksum verification: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("pluginrpc: inspecting %s for checksum verification: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("pluginrpc: executable %s is not a regular file", path)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("pluginrpc: executable %s has no executable permission bits", path)
	}

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
