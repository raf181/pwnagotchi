package pluginrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
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
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("pluginrpc: parsing repository index from %s: %w", url, err)
	}
	return &idx, nil
}

// FetchManifest retrieves and parses one plugin's manifest over HTTP.
func FetchManifest(ctx context.Context, client *http.Client, url string) (*Manifest, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pluginrpc: fetching manifest from %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pluginrpc: fetching manifest from %s: HTTP %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return ParseManifest(data)
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
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return writeExecutable(destPath, data)
}

// DefaultHTTPTimeout bounds every repository/manifest/executable fetch —
// callers should wrap their context with this (or their own bound)
// rather than making an unbounded network call.
const DefaultHTTPTimeout = 30 * time.Second

func writeExecutable(destPath string, data []byte) error {
	return os.WriteFile(destPath, data, 0o755)
}
