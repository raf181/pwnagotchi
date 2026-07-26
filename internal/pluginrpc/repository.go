package pluginrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	CurrentIndexVersion = 1
	MaxIndexBytes       = 1 << 20
	MaxManifestBytes    = 256 << 10
	MaxExecutableBytes  = 128 << 20
)

// IndexEntry is one plugin listed in a Go-only repository index — enough
// to show in a search/list, and a URL to fetch the full Manifest from
// before installing.
type IndexEntry struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Author      string `json:"author"`
	Description string `json:"description"`
	ManifestURL string `json:"manifest_url"`
}

// Index is the Go-only replacement for the old Python plugin repos'
// zip-of-*.py-files listing: a small, fetchable JSON document naming
// every available plugin and where to get its manifest.
type Index struct {
	IndexVersion int          `json:"index_version"`
	Plugins      []IndexEntry `json:"plugins"`
}

// FetchIndex retrieves and parses a repository index over HTTP via the
// injected client (production: a real *http.Client; tests:
// httptest.NewServer — never a bare http.Get, so this is always
// testable without a real network call).
func FetchIndex(ctx context.Context, client *http.Client, url string) (*Index, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pluginrpc: fetching repository index from %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pluginrpc: fetching repository index from %s: HTTP %d", url, resp.StatusCode)
	}
	data, err := readLimited(resp.Body, MaxIndexBytes, "repository index")
	if err != nil {
		return nil, err
	}
	var idx Index
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&idx); err != nil {
		return nil, fmt.Errorf("pluginrpc: parsing repository index from %s: %w", url, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("pluginrpc: parsing repository index from %s: %w", url, err)
	}
	if idx.IndexVersion != CurrentIndexVersion {
		return nil, fmt.Errorf("pluginrpc: unsupported repository index_version %d (want %d)", idx.IndexVersion, CurrentIndexVersion)
	}
	seen := map[string]bool{}
	for _, entry := range idx.Plugins {
		if err := ValidatePluginName(entry.Name); err != nil {
			return nil, fmt.Errorf("pluginrpc: invalid repository entry: %w", err)
		}
		if seen[entry.Name] {
			return nil, fmt.Errorf("pluginrpc: repository lists plugin %q more than once", entry.Name)
		}
		seen[entry.Name] = true
		if entry.Version == "" {
			return nil, fmt.Errorf("pluginrpc: repository entry %q has no version", entry.Name)
		}
		if err := validateHTTPURL(entry.ManifestURL); err != nil {
			return nil, fmt.Errorf("pluginrpc: repository entry %q has invalid manifest_url: %w", entry.Name, err)
		}
	}
	return &idx, nil
}

// FetchManifest retrieves and parses one plugin's manifest over HTTP.
func FetchManifest(ctx context.Context, client *http.Client, url string) (*Manifest, error) {
	manifest, _, err := FetchManifestDocument(ctx, client, url)
	return manifest, err
}

// FetchManifestDocument returns both the validated manifest and the exact
// bytes that were validated, allowing installers to persist one fetch
// without a time-of-check/time-of-use re-fetch.
func FetchManifestDocument(ctx context.Context, client *http.Client, url string) (*Manifest, []byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("pluginrpc: fetching manifest from %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("pluginrpc: fetching manifest from %s: HTTP %d", url, resp.StatusCode)
	}
	data, err := readLimited(resp.Body, MaxManifestBytes, "plugin manifest")
	if err != nil {
		return nil, nil, err
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return nil, nil, err
	}
	return manifest, data, nil
}

// DownloadExecutable fetches a plugin's compiled binary to destPath
// (0o755, so it's directly runnable) — the caller must call
// Manifest.VerifyExecutable on the result before ever spawning it (Spawn
// does this automatically, but callers building an install flow should
// verify before reporting "installed" too).
func DownloadExecutable(ctx context.Context, client *http.Client, url, destPath string) error {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("pluginrpc: downloading executable from %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pluginrpc: downloading executable from %s: HTTP %d", url, resp.StatusCode)
	}
	if resp.ContentLength > MaxExecutableBytes {
		return fmt.Errorf("pluginrpc: executable is too large (%d bytes; max %d)", resp.ContentLength, MaxExecutableBytes)
	}

	tmp, err := os.CreateTemp(filepath.Dir(destPath), "."+filepath.Base(destPath)+".download-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	n, copyErr := io.Copy(tmp, io.LimitReader(resp.Body, MaxExecutableBytes+1))
	if copyErr == nil && n > MaxExecutableBytes {
		copyErr = fmt.Errorf("pluginrpc: executable exceeds maximum size of %d bytes", MaxExecutableBytes)
	}
	if copyErr == nil {
		copyErr = tmp.Sync()
	}
	if closeErr := tmp.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return copyErr
	}
	return os.Rename(tmpPath, destPath)
}

// DefaultHTTPTimeout bounds every repository/manifest/executable fetch —
// callers should wrap their context with this (or their own bound)
// rather than making an unbounded network call.
const DefaultHTTPTimeout = 30 * time.Second

func readLimited(r io.Reader, max int64, label string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("pluginrpc: %s exceeds maximum size of %d bytes", label, max)
	}
	return data, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON documents are not allowed")
		}
		return err
	}
	return nil
}
