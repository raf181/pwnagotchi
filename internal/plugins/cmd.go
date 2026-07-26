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
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// bundledPluginNames are the bundled plugins now compiled
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
	if err := pluginrpc.ValidatePluginName(name); err != nil {
		log.Print(err)
		return 1
	}
	main := mainField(cfg)
	if main == nil {
		main = config.Map{}
		cfg["main"] = main
	}
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
	if err := config.SaveConfig(cfg, userConfigPath); err != nil {
		log.Printf("plugins: saving config: %v", err)
		return 1
	}
	return 0
}

// Disable mirrors plugins.cmd.disable.
func Disable(cfg config.Map, userConfigPath, name string) int {
	if err := pluginrpc.ValidatePluginName(name); err != nil {
		log.Print(err)
		return 1
	}
	main := mainField(cfg)
	if main == nil {
		main = config.Map{}
		cfg["main"] = main
	}
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
	if err := config.SaveConfig(cfg, userConfigPath); err != nil {
		log.Printf("plugins: saving config: %v", err)
		return 1
	}
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

	if len(bundledPluginNames) == 0 && len(installed) == 0 && len(availableNotInstalled) == 0 {
		if idx == nil && repositoryIndexURL(cfg) == "" {
			fmt.Println("No plugin repository configured (main.plugin_repository_index is empty) and no plugins installed.")
		} else {
			fmt.Println("Maybe try: sudo pwnagotchi plugins update")
		}
		return 1
	}

	maxLen := len("Plugin")
	for name := range bundledPluginNames {
		if len(name) > maxLen {
			maxLen = len(name)
		}
	}
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
		names := make([]string, 0, len(bundledPluginNames))
		for name := range bundledPluginNames {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if !matchGlob(pattern, name) {
				continue
			}
			found = true
			fmt.Println(fmtLine(name, "built-in", pluginEnabledStatus(cfg, name), "bundled", "-"))
		}
	}

	{
		names := make([]string, 0, len(installed))
		for name := range installed {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if bundledPluginNames[name] {
				continue
			}
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
			fmt.Println(fmtLine(name, m.Version, pluginEnabledStatus(cfg, name), status, m.Author))
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

func pluginEnabledStatus(cfg config.Map, name string) string {
	if plugins := pluginsField(cfg); plugins != nil {
		if entry, ok := plugins[name].(config.Map); ok {
			if enabled, ok := entry["enabled"].(bool); ok && enabled {
				return "enabled"
			}
		}
	}
	return "disabled"
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
func Uninstall(_ config.Map, name string) int {
	if err := pluginrpc.ValidatePluginName(name); err != nil {
		log.Print(err)
		return 1
	}
	dir := filepath.Join(PluginInstallDir, name)
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		log.Printf("Plugin %s is not installed.", name)
		return 1
	} else if err != nil {
		log.Printf("plugins: inspecting %s: %v", dir, err)
		return 1
	}
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
func Install(cfg config.Map, _ string, name string) int {
	if err := pluginrpc.ValidatePluginName(name); err != nil {
		log.Print(err)
		return 1
	}
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
	manifest, manifestRaw, err := pluginrpc.FetchManifestDocument(ctx, HTTPClient, entry.ManifestURL)
	if err != nil {
		log.Printf("error fetching manifest for %s: %v", name, err)
		return 1
	}
	if manifest.Name != name {
		log.Printf("plugins: repository entry %q returned a manifest for %q", name, manifest.Name)
		return 1
	}
	if entry.Version != manifest.Version {
		log.Printf("plugins: repository entry %q advertises version %s but its manifest says %s", name, entry.Version, manifest.Version)
		return 1
	}
	if err := manifest.ValidateTarget(runtime.GOOS, runtime.GOARCH); err != nil {
		log.Print(err)
		return 1
	}
	if err := installPackage(ctx, name, manifest, manifestRaw); err != nil {
		log.Printf("plugins: installing %s: %v", name, err)
		return 1
	}

	fmt.Printf("%s installed. Enable it with `sudo pwnagotchi plugins enable %s`, then restart pwnagotchi.\n", name, name)
	return 0
}

func installPackage(ctx context.Context, name string, manifest *pluginrpc.Manifest, manifestRaw []byte) error {
	if err := pluginrpc.ValidatePluginName(name); err != nil {
		return err
	}
	if manifest == nil || manifest.Name != name {
		return fmt.Errorf("manifest name does not match install name %q", name)
	}
	if err := os.MkdirAll(PluginInstallDir, 0o755); err != nil {
		return err
	}
	stageDir, err := os.MkdirTemp(PluginInstallDir, "."+name+"-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stageDir)
	if err := os.Chmod(stageDir, 0o755); err != nil {
		return err
	}

	execPath := filepath.Join(stageDir, name)
	if err := pluginrpc.DownloadExecutable(ctx, HTTPClient, manifest.ExecutableURL, execPath); err != nil {
		return err
	}
	if err := manifest.VerifyExecutable(execPath); err != nil {
		return fmt.Errorf("checksum verification failed: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "manifest.toml"), manifestRaw, 0o644); err != nil {
		return err
	}

	targetDir := filepath.Join(PluginInstallDir, name)
	backupDir := ""
	if _, err := os.Lstat(targetDir); err == nil {
		backupDir, err = os.MkdirTemp(PluginInstallDir, "."+name+"-previous-")
		if err != nil {
			return err
		}
		if err := os.Remove(backupDir); err != nil {
			return err
		}
		if err := os.Rename(targetDir, backupDir); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.Rename(stageDir, targetDir); err != nil {
		if backupDir != "" {
			_ = os.Rename(backupDir, targetDir)
		}
		return err
	}
	if backupDir != "" {
		if err := os.RemoveAll(backupDir); err != nil {
			log.Printf("plugins: installed %s but could not remove previous version at %s: %v", name, backupDir, err)
		}
	}
	return nil
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

	failed := false
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
			failed = true
		}
	}
	if failed {
		return 1
	}
	return 0
}

// Update validates the configured repository directly. There is no local
// repository cache to mutate; list/search fetch the same small index live.
func Update(cfg config.Map) int {
	if repositoryIndexURL(cfg) == "" {
		fmt.Println("No plugin repository configured. Set main.plugin_repository_index to a Go-only plugin repository index URL.")
		return 1
	}
	idx, err := fetchIndex(cfg)
	if err != nil {
		log.Printf("plugins: validating repository index: %v", err)
		fmt.Printf("Plugin repository validation failed: %v\n", err)
		return 1
	}
	fmt.Printf("Plugin repository index is valid: %d plugin(s) available.\n", len(idx.Plugins))
	return 0
}

// Edit mirrors plugins.cmd.edit: opens $EDITOR (default vim) on a scratch
// TOML file containing just this plugin's config section, then merges any
// changes back in. Uses os/exec with an explicit argv (the editor name and
// a generated temp file path — never a shell string), same executable and
// argument shape as Python's subprocess.call([editor, tmp.name]).
func Edit(cfg config.Map, userConfigPath, name string) int {
	if err := pluginrpc.ValidatePluginName(name); err != nil {
		log.Print(err)
		return 1
	}
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
	if err := config.SaveConfig(cfg, userConfigPath); err != nil {
		return 1
	}
	return 0
}
