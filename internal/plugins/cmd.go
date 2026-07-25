// Package plugins ports pwnagotchi/plugins/cmd.py: the `pwnagotchi
// plugins ...` subcommand (search/list/update/upgrade/enable/disable/
// install/uninstall/edit).
//
// The install/uninstall/upgrade/list/search backend was rewritten to the
// Go-only third-party plugin distribution system (internal/pluginrpc):
// a versioned TOML manifest, checksum-verified separately-compiled Go
// executables, and a bounded RPC protocol — replacing the old *.py
// file-copying package manager entirely. Existing Python plugin names
// are detected and rejected with a precise migration message (see
// legacyPythonPluginError) rather than ever being installed or silently
// treated as loaded, per the Go-only migration's explicit requirement.
package plugins

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginrpc"
)

// PluginInstallDir is the Go-owned root every third-party plugin is
// installed under: PluginInstallDir/<name>/manifest.toml +
// PluginInstallDir/<name>/<name> (the checksum-verified executable).
// Overridable for tests, matching this port's established
// package-level-var override pattern.
var PluginInstallDir = "/etc/pwnagotchi/plugins/"

// HTTPClient is the injectable client every repository/manifest/
// executable fetch uses — production gets a real bounded *http.Client;
// tests point this at httptest.NewServer, never a real network call.
var HTTPClient = &http.Client{Timeout: pluginrpc.DefaultHTTPTimeout}

// bundledPluginNames are the 23 real bundled plugins now compiled
// directly into this daemon (see cmd/pwnagotchi/main.go's
// registerNativePlugins) plus the two pre-existing native-but-special-
// cased ones (logtail, webcfg) — installing any of these via the
// third-party system is meaningless: they're already loaded.
var bundledPluginNames = map[string]bool{
	"wpa-sec": true, "memtemp": true, "cache": true, "switcher": true,
	"gpio_buttons": true, "wittypi": true, "ups_lite": true, "pwncrack": true,
	"gps": true, "example": true, "auto-tune": true, "auto_backup": true,
	"auto-update": true, "ohcapi": true, "session-stats": true,
	"webgpsmap": true, "fix_services": true, "pisugarx": true,
	"pwnstore_ui": true, "bt-tether": true, "wigle": true, "grid": true,
	"logtail": true, "webcfg": true,
}

func mainField(cfg config.Map) config.Map {
	m, _ := cfg["main"].(config.Map)
	return m
}

func stringField(m config.Map, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func pluginsField(cfg config.Map) config.Map {
	m := mainField(cfg)
	if m == nil {
		return nil
	}
	p, _ := m["plugins"].(config.Map)
	return p
}

// Enable mirrors plugins.cmd.enable.
func Enable(cfg config.Map, userConfigPath, name string) int {
	main := mainField(cfg)
	plugins, _ := main["plugins"].(config.Map)
	if plugins == nil {
		plugins = config.Map{}
		main["plugins"] = plugins
	}
	entry, _ := plugins[name].(config.Map)
	if entry == nil {
		entry = config.Map{}
		plugins[name] = entry
	}
	entry["enabled"] = true
	config.SaveConfig(cfg, userConfigPath)
	return 0
}

// Disable mirrors plugins.cmd.disable.
func Disable(cfg config.Map, userConfigPath, name string) int {
	main := mainField(cfg)
	plugins, _ := main["plugins"].(config.Map)
	if plugins == nil {
		plugins = config.Map{}
		main["plugins"] = plugins
	}
	entry, _ := plugins[name].(config.Map)
	if entry == nil {
		entry = config.Map{}
		plugins[name] = entry
	}
	entry["enabled"] = false
	config.SaveConfig(cfg, userConfigPath)
	return 0
}

func matchGlob(pattern, name string) bool {
	ok, err := filepath.Match(pattern, name)
	return err == nil && ok
}

// repositoryIndexURL reads config['main']['plugin_repository_index'] —
// the Go-only replacement for the old Python custom_plugin_repos list.
// Empty means "no repository configured" (a real, disclosed state: no
// third-party Go plugin repository is known to exist yet as of this
// migration — see docs/plugin-development.md), not an error.
func repositoryIndexURL(cfg config.Map) string {
	return stringField(mainField(cfg), "plugin_repository_index")
}

// fetchIndex fetches the configured repository index, or returns
// (nil, nil) if none is configured — a real, distinguishable "not
// configured" state the caller must handle explicitly, never silently
// treated as "no plugins available due to an error."
func fetchIndex(cfg config.Map) (*pluginrpc.Index, error) {
	url := repositoryIndexURL(cfg)
	if url == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), pluginrpc.DefaultHTTPTimeout)
	defer cancel()
	return pluginrpc.FetchIndex(ctx, HTTPClient, url)
}

// installedManifests scans PluginInstallDir for real, already-installed
// third-party plugin manifests.
func installedManifests() map[string]*pluginrpc.Manifest {
	out := map[string]*pluginrpc.Manifest{}
	entries, err := os.ReadDir(PluginInstallDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(PluginInstallDir, e.Name(), "manifest.toml"))
		if err != nil {
			continue
		}
		m, err := pluginrpc.ParseManifest(data)
		if err != nil {
			log.Printf("plugins: ignoring corrupt manifest for %q: %v", e.Name(), err)
			continue
		}
		out[m.Name] = m
	}
	return out
}

// legacyPythonPluginPath reports whether a *.py file for name exists
// under the daemon's configured (legacy) custom_plugins directory — real,
// leftover evidence of a pre-migration Python plugin install — and
// returns its path if so.
func legacyPythonPluginPath(cfg config.Map, name string) (string, bool) {
	dir := stringField(mainField(cfg), "custom_plugins")
	if dir == "" {
		return "", false
	}
	path := filepath.Join(dir, name+".py")
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	return path, true
}

// legacyPythonPluginMessage is the precise, actionable migration message
// the Go-only migration requires instead of ever installing/running
// Python or silently treating a legacy plugin as loaded.
func legacyPythonPluginMessage(name, pyPath string) string {
	return fmt.Sprintf(
		"%s is a legacy Python plugin (found at %s) and cannot be installed or loaded on this Go-only pwnagotchi build — "+
			"the Python plugin bridge has been removed. Port it to a native Go plugin (see docs/plugin-development.md) "+
			"or obtain a Go build of it from a third-party plugin repository instead.",
		name, pyPath,
	)
}

// ListPlugins mirrors plugins.cmd.list_plugins against the Go-only
// manifest system: installed (scanned from PluginInstallDir) and
// available (fetched from the configured repository index, if any).
func ListPlugins(cfg config.Map, installedOnly bool, pattern string) int {
	if pattern == "" {
		pattern = "*"
	}
	installed := installedManifests()

	idx, err := fetchIndex(cfg)
	if err != nil {
		log.Printf("plugins: fetching repository index: %v", err)
	}

	availableNotInstalled := map[string]pluginrpc.IndexEntry{}
	if idx != nil {
		for _, e := range idx.Plugins {
			if _, ok := installed[e.Name]; !ok {
				availableNotInstalled[e.Name] = e
			}
		}
	}

	if len(installed) == 0 && len(availableNotInstalled) == 0 {
		if idx == nil && repositoryIndexURL(cfg) == "" {
			fmt.Println("No plugin repository configured (main.plugin_repository_index is empty) and no plugins installed.")
		} else {
			fmt.Println("Maybe try: sudo pwnagotchi plugins update")
		}
		return 1
	}

	maxLen := len("Plugin")
	for name := range installed {
		if len(name) > maxLen {
			maxLen = len(name)
		}
	}
	for name := range availableNotInstalled {
		if len(name) > maxLen {
			maxLen = len(name)
		}
	}

	fmtLine := func(name, version, enabled, status, author string) string {
		return fmt.Sprintf("|%s|%9s|%10s|%15s|%22s|",
			centerPad(name, maxLen), centerPad(version, 9), centerPad(enabled, 10), centerPad(status, 15), centerPad(author, 22))
	}
	header := fmtLine("Plugin", "Version", "Active", "Status", "Author")
	lineLength := len(header)
	fmt.Println(strings.Repeat("-", lineLength))
	fmt.Println(header)
	fmt.Println(strings.Repeat("-", lineLength))

	found := false

	{
		names := make([]string, 0, len(installed))
		for name := range installed {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if !matchGlob(pattern, name) {
				continue
			}
			found = true
			m := installed[name]
			status := "installed"
			if idx != nil {
				for _, e := range idx.Plugins {
					if e.Name == name && config.ParseVersion(e.Version) != nil &&
						compareVersionSlices(config.ParseVersion(e.Version), config.ParseVersion(m.Version)) > 0 {
						status = "installed (^)"
					}
				}
			}
			enabled := "disabled"
			if plugins := pluginsField(cfg); plugins != nil {
				if entry, ok := plugins[name].(config.Map); ok {
					if e, ok := entry["enabled"].(bool); ok && e {
						enabled = "enabled"
					}
				}
			}
			fmt.Println(fmtLine(name, m.Version, enabled, status, m.Author))
		}
	}

	if !installedOnly {
		names := make([]string, 0, len(availableNotInstalled))
		for name := range availableNotInstalled {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if !matchGlob(pattern, name) {
				continue
			}
			found = true
			e := availableNotInstalled[name]
			fmt.Println(fmtLine(name, e.Version, "-", "available", e.Author))
		}
	}

	fmt.Println(strings.Repeat("-", lineLength))
	if !found {
		fmt.Println("Maybe try: sudo pwnagotchi plugins update")
		return 1
	}
	return 0
}

func centerPad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	total := width - len(s)
	left := total / 2
	right := total - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}

func compareVersionSlices(a, b []string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

// Uninstall removes a real installed third-party plugin's directory
// (manifest + executable).
func Uninstall(cfg config.Map, name string) int {
	installed := installedManifests()
	if _, ok := installed[name]; !ok {
		log.Printf("Plugin %s is not installed.", name)
		return 1
	}
	dir := filepath.Join(PluginInstallDir, name)
	if err := os.RemoveAll(dir); err != nil {
		log.Printf("error removing %s: %v", dir, err)
		return 1
	}
	return 0
}

// Install fetches, checksum-verifies, and installs a third-party Go
// plugin by name from the configured repository index — or fails with a
// precise, actionable message for a bundled/legacy-Python name instead
// of ever running Python or silently treating it as loaded.
func Install(cfg config.Map, userConfigPath, name string) int {
	if bundledPluginNames[name] {
		fmt.Printf("%s is a bundled plugin already compiled into this daemon — nothing to install.\n", name)
		return 0
	}
	if pyPath, ok := legacyPythonPluginPath(cfg, name); ok {
		msg := legacyPythonPluginMessage(name, pyPath)
		log.Print(msg)
		fmt.Println(msg)
		return 1
	}

	idx, err := fetchIndex(cfg)
	if err != nil {
		log.Printf("plugins: fetching repository index: %v", err)
		return 1
	}
	if idx == nil {
		fmt.Println("No plugin repository configured (main.plugin_repository_index is empty).")
		return 1
	}
	var entry *pluginrpc.IndexEntry
	for i := range idx.Plugins {
		if idx.Plugins[i].Name == name {
			entry = &idx.Plugins[i]
			break
		}
	}
	if entry == nil {
		fmt.Printf("%s not found in the configured plugin repository.\n", name)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), pluginrpc.DefaultHTTPTimeout)
	defer cancel()
	manifest, err := pluginrpc.FetchManifest(ctx, HTTPClient, entry.ManifestURL)
	if err != nil {
		log.Printf("error fetching manifest for %s: %v", name, err)
		return 1
	}

	destDir := filepath.Join(PluginInstallDir, name)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		log.Printf("error creating %s: %v", destDir, err)
		return 1
	}
	execPath := filepath.Join(destDir, name)
	if err := pluginrpc.DownloadExecutable(ctx, HTTPClient, manifest.ExecutableURL, execPath); err != nil {
		log.Printf("error downloading %s: %v", name, err)
		return 1
	}
	if err := manifest.VerifyExecutable(execPath); err != nil {
		log.Printf("checksum verification failed for %s, refusing to install: %v", name, err)
		os.RemoveAll(destDir)
		return 1
	}
	manifestPath := filepath.Join(destDir, "manifest.toml")
	if err := writeManifestFile(manifestPath, entry.ManifestURL, manifest); err != nil {
		log.Printf("error saving manifest for %s: %v", name, err)
		os.RemoveAll(destDir)
		return 1
	}

	fmt.Printf("%s installed (restart pwnagotchi to activate).\n", name)
	return 0
}

// writeManifestFile persists the fetched manifest's raw TOML so future
// `pwnagotchi plugins list`/`upgrade` calls don't need network access
// just to see what's installed. Re-fetches the manifest bytes rather
// than serializing the parsed struct, to preserve exactly what was
// verified.
func writeManifestFile(destPath, manifestURL string, m *pluginrpc.Manifest) error {
	ctx, cancel := context.WithTimeout(context.Background(), pluginrpc.DefaultHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return err
	}
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return os.WriteFile(destPath, data, 0o644)
}

// Upgrade checks every installed third-party plugin against the
// repository index and re-installs any with a newer available version.
func Upgrade(cfg config.Map, pattern string) int {
	if pattern == "" {
		pattern = "*"
	}
	installed := installedManifests()
	idx, err := fetchIndex(cfg)
	if err != nil {
		log.Printf("plugins: fetching repository index: %v", err)
		return 1
	}
	if idx == nil {
		fmt.Println("No plugin repository configured (main.plugin_repository_index is empty).")
		return 1
	}

	for name, m := range installed {
		if !matchGlob(pattern, name) {
			continue
		}
		var available *pluginrpc.IndexEntry
		for i := range idx.Plugins {
			if idx.Plugins[i].Name == name {
				available = &idx.Plugins[i]
				break
			}
		}
		if available == nil {
			continue
		}
		if compareVersionSlices(config.ParseVersion(available.Version), config.ParseVersion(m.Version)) <= 0 {
			continue
		}
		log.Printf("Upgrading %s from %s to %s", name, m.Version, available.Version)
		if rc := Install(cfg, "", name); rc != 0 {
			log.Printf("error upgrading %s", name)
		}
	}
	return 0
}

// checkInternet mirrors plugins.cmd._check_internet: a DNS resolution
// check (not an HTTP request), matching Python's socket.gethostbyname exactly.
func checkInternet() bool {
	_, err := net.LookupHost("google.com")
	return err == nil
}

// Update mirrors plugins.cmd.update: confirms connectivity and points
// the user at `plugins list`, which itself fetches the live repository
// index — there is no separate local cache to refresh in the Go-only
// design (the old Python version downloaded+unzipped a whole repo; the
// new index is small enough to fetch fresh every time).
func Update(cfg config.Map) int {
	if !checkInternet() {
		log.Print("No internet connection or DNS not working. Please follow these instructions:")
		log.Print("https://github.com/jayofelony/pwnagotchi/wiki/Step-2-Connecting")
		fmt.Println("No internet/DNS. Please follow these instructions:")
		fmt.Println("https://github.com/jayofelony/pwnagotchi/wiki/Step-2-Connecting")
		return 1
	}
	if repositoryIndexURL(cfg) == "" {
		fmt.Println("No plugin repository configured. Set main.plugin_repository_index to a Go-only plugin repository index URL.")
		return 1
	}
	log.Print("Internet detected - Please run sudo pwnagotchi plugins list")
	fmt.Println("Internet detected - Please run sudo pwnagotchi plugins list")
	return 0
}

// Edit mirrors plugins.cmd.edit: opens $EDITOR (default vim) on a scratch
// TOML file containing just this plugin's config section, then merges any
// changes back in. Uses os/exec with an explicit argv (the editor name and
// a generated temp file path — never a shell string), same executable and
// argument shape as Python's subprocess.call([editor, tmp.name]).
func Edit(cfg config.Map, userConfigPath, name string) int {
	plugins := pluginsField(cfg)
	if plugins == nil {
		return 1
	}
	entry, ok := plugins[name]
	if !ok {
		return 1
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}

	pluginConfig := config.Map{"main": config.Map{"plugins": config.Map{name: entry}}}

	tmp, err := os.CreateTemp("", "pwnagotchi-plugin-*.tmp")
	if err != nil {
		return 1
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := config.SaveConfig(pluginConfig, tmpPath); err != nil {
		tmp.Close()
		return 1
	}
	tmp.Close()

	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return 1
	}

	newCfg, err := config.LoadTOMLFileForEdit(tmpPath)
	if err != nil {
		return 1
	}
	newPlugins, _ := newCfg["main"].(config.Map)
	if newPlugins == nil {
		return 1
	}
	newPluginsMap, _ := newPlugins["plugins"].(config.Map)
	if newPluginsMap == nil {
		return 1
	}
	plugins[name] = newPluginsMap[name]
	config.SaveConfig(cfg, userConfigPath)
	return 0
}
