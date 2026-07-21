// Package plugins ports pwnagotchi/plugins/cmd.py: the `pwnagotchi
// plugins ...` subcommand (search/list/update/upgrade/enable/disable/
// install/uninstall/edit). This is a plugin-FILE package manager — it
// downloads/copies/edits .py plugin files and config entries, and does not
// require a running plugin loader/execution engine, so it's fully portable
// to Go independently of the (not yet built) Python plugin bridge.
package plugins

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
)

// SaveDir/DefaultInstallPath mirror plugins.cmd.SAVE_DIR/DEFAULT_INSTALL_PATH.
// DefaultPluginsPath mirrors plugins.default_path (the bundled
// pwnagotchi/plugins/default directory in the Python package — bundled
// plugins remain Python, run via the (not yet built) subprocess bridge, so
// this must point at wherever that Python package is installed on the
// target system). All three are vars, overridable for tests and for
// deployments with a non-standard layout.
var (
	SaveDir            = "/usr/local/share/pwnagotchi/available-plugins/"
	DefaultInstallPath = "/usr/local/share/pwnagotchi/installed-plugins/"
	DefaultPluginsPath = "/usr/local/share/pwnagotchi/plugins/default/"
)

var (
	versionRe = regexp.MustCompile(`__version__[\t ]*=[\t ]*['"]([^"']+)`)
	authorRe  = regexp.MustCompile(`__author__[\t ]*=[\t ]*['"]([^"']+)`)
)

func extractVersion(filename string) []string {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
	m := versionRe.FindSubmatch(data)
	if m == nil {
		return nil
	}
	return config.ParseVersion(string(m[1]))
}

func extractAuthor(filename string) string {
	data, err := os.ReadFile(filename)
	if err != nil {
		return "n/a"
	}
	m := authorRe.FindSubmatch(data)
	if m == nil {
		return "n/a"
	}
	return string(m[1])
}

func getAvailable() map[string]string {
	available := map[string]string{}
	matches, _ := filepath.Glob(filepath.Join(SaveDir, "*.py"))
	for _, filename := range matches {
		name := strings.TrimSuffix(filepath.Base(filename), ".py")
		available[name] = filename
	}
	return available
}

func getInstalled(cfg config.Map) map[string]string {
	installed := map[string]string{}
	customPlugins := stringField(mainField(cfg), "custom_plugins")
	for _, dir := range []string{DefaultPluginsPath, customPlugins} {
		if dir == "" {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(dir, "*.py"))
		for _, filename := range matches {
			name := strings.TrimSuffix(filepath.Base(filename), ".py")
			installed[name] = filename
		}
	}
	return installed
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

// ListPlugins mirrors plugins.cmd.list_plugins.
func ListPlugins(cfg config.Map, installedOnly bool, pattern string) int {
	if pattern == "" {
		pattern = "*"
	}
	available := getAvailable()
	installed := getInstalled(cfg)

	availableAndInstalled := map[string]bool{}
	availableNotInstalled := map[string]bool{}
	for name := range available {
		availableAndInstalled[name] = true
		if _, ok := installed[name]; !ok {
			availableNotInstalled[name] = true
		}
	}
	for name := range installed {
		availableAndInstalled[name] = true
	}

	maxLenSet := availableNotInstalled
	if installedOnly {
		maxLenSet = availableAndInstalled
	}
	if len(maxLenSet) == 0 {
		fmt.Println("Maybe try: sudo pwnagotchi plugins update")
		return 1
	}
	maxLen := 0
	for name := range maxLenSet {
		if len(name) > maxLen {
			maxLen = len(name)
		}
	}

	fmtLine := func(name string, version, enabled, status, author string) string {
		return fmt.Sprintf("|%s|%9s|%10s|%15s|%22s|",
			centerPad(name, maxLen), centerPad(version, 9), centerPad(enabled, 10), centerPad(status, 15), centerPad(author, 22))
	}

	header := fmtLine("Plugin", "Version", "Active", "Status", "Author")
	lineLength := len(header) - 10

	fmt.Println(strings.Repeat("-", lineLength))
	fmt.Println(header)
	fmt.Println(strings.Repeat("-", lineLength))

	found := false

	if installedOnly {
		names := make([]string, 0, len(installed))
		for name := range installed {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			filename := installed[name]
			if !matchGlob(pattern, name) {
				continue
			}
			found = true
			installedVersion := extractVersion(filename)
			var availableVersion []string
			if af, ok := available[name]; ok {
				availableVersion = extractVersion(af)
			}
			status := "installed"
			if len(installedVersion) > 0 && len(availableVersion) > 0 && compareVersionSlices(availableVersion, installedVersion) > 0 {
				status = "installed (^)"
			}
			enabled := "disabled"
			if plugins := pluginsField(cfg); plugins != nil {
				if entry, ok := plugins[name].(config.Map); ok {
					if e, ok := entry["enabled"].(bool); ok && e {
						enabled = "enabled"
					}
				}
			}
			fmt.Println(fmtLine(name, strings.Join(installedVersion, "."), enabled, status, extractAuthor(filename)))
		}
	}

	notInstalledNames := make([]string, 0, len(availableNotInstalled))
	for name := range availableNotInstalled {
		notInstalledNames = append(notInstalledNames, name)
	}
	sort.Strings(notInstalledNames)
	for _, name := range notInstalledNames {
		if !matchGlob(pattern, name) {
			continue
		}
		found = true
		filename := available[name]
		fmt.Println(fmtLine(name, strings.Join(extractVersion(filename), "."), "-", "available", extractAuthor(filename)))
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

// Uninstall mirrors plugins.cmd.uninstall.
func Uninstall(cfg config.Map, name string) int {
	installed := getInstalled(cfg)
	filename, ok := installed[name]
	if !ok {
		log.Printf("Plugin %s is not installed.", name)
		return 1
	}
	if err := os.Remove(filename); err != nil {
		log.Printf("error removing %s: %v", filename, err)
		return 1
	}
	return 0
}

// Install mirrors plugins.cmd.install.
func Install(cfg config.Map, userConfigPath, name string) int {
	available := getAvailable()
	installed := getInstalled(cfg)

	filename, ok := available[name]
	if !ok {
		log.Printf("%s not found.", name)
		return 1
	}
	if _, ok := installed[name]; ok {
		log.Printf("%s already installed.", name)
	}

	installPath := stringField(mainField(cfg), "custom_plugins")
	if installPath == "" {
		installPath = DefaultInstallPath
		mainField(cfg)["custom_plugins"] = installPath
		config.SaveConfig(cfg, userConfigPath)
	}

	if err := os.MkdirAll(installPath, 0o755); err != nil {
		return 1
	}
	if err := copyFile(filename, filepath.Join(installPath, filepath.Base(filename))); err != nil {
		log.Printf("error installing %s: %v", name, err)
		return 1
	}

	for _, conf := range globYAMLSiblings(filename) {
		dst := filepath.Join(installPath, filepath.Base(conf))
		backupIfChanged(dst, conf)
		copyFile(conf, dst)
	}
	return 0
}

// Upgrade mirrors plugins.cmd.upgrade.
func Upgrade(cfg config.Map, pattern string) int {
	if pattern == "" {
		pattern = "*"
	}
	available := getAvailable()
	installed := getInstalled(cfg)

	for plugin, filename := range installed {
		if !matchGlob(pattern, plugin) {
			continue
		}
		availableFile, ok := available[plugin]
		if !ok {
			continue
		}
		availableVersion := extractVersion(availableFile)
		installedVersion := extractVersion(filename)
		if len(installedVersion) == 0 || len(availableVersion) == 0 || compareVersionSlices(availableVersion, installedVersion) <= 0 {
			continue
		}

		log.Printf("Upgrade %s from %s to %s", plugin, strings.Join(installedVersion, "."), strings.Join(availableVersion, "."))
		copyFile(availableFile, filename)

		for _, conf := range globYAMLSiblings(availableFile) {
			dst := filepath.Join(filepath.Dir(filename), filepath.Base(conf))
			backupIfChanged(dst, conf)
			copyFile(conf, dst)
		}
	}
	return 0
}

func globYAMLSiblings(pyFile string) []string {
	base := strings.TrimSuffix(pyFile, ".py")
	var out []string
	for _, ext := range []string{".yml", ".yaml"} {
		if _, err := os.Stat(base + ext); err == nil {
			out = append(out, base+ext)
		}
	}
	return out
}

func backupIfChanged(dst, src string) {
	if _, err := os.Stat(dst); err != nil {
		return
	}
	dstSum, err1 := config.MD5(dst)
	srcSum, err2 := config.MD5(src)
	if err1 == nil && err2 == nil && dstSum != srcSum {
		log.Printf("Backing up config: %s", filepath.Base(dst))
		os.Rename(dst, dst+".bak")
	}
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// checkInternet mirrors plugins.cmd._check_internet: a DNS resolution
// check (not an HTTP request), matching Python's socket.gethostbyname exactly.
func checkInternet() bool {
	_, err := net.LookupHost("google.com")
	return err == nil
}

// Update mirrors plugins.cmd.update.
func Update(cfg config.Map) int {
	if !checkInternet() {
		log.Print("No internet connection or DNS not working. Please follow these instructions:")
		log.Print("https://github.com/jayofelony/pwnagotchi/wiki/Step-2-Connecting")
		fmt.Println("No internet/DNS. Please follow these instructions:")
		fmt.Println("https://github.com/jayofelony/pwnagotchi/wiki/Step-2-Connecting")
		return 1
	}
	log.Print("Internet detected - Please run sudo pwnagotchi plugins list")
	fmt.Println("Internet detected - Please run sudo pwnagotchi plugins list")

	var urls []string
	if raw, ok := mainField(cfg)["custom_plugin_repos"].([]interface{}); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				urls = append(urls, s)
			}
		}
	}
	if len(urls) == 0 {
		log.Print("No plugin repositories configured.")
		return 1
	}

	rc := 0
	for idx, repoURL := range urls {
		dest := filepath.Join(SaveDir, fmt.Sprintf("plugins%d.zip", idx))
		log.Printf("Downloading plugins from %s to %s", repoURL, dest)

		if err := os.MkdirAll(SaveDir, 0o755); err != nil {
			log.Printf("Error while updating plugins: %v", err)
			rc = 1
			continue
		}
		if err := config.DownloadFile(repoURL, dest); err != nil {
			log.Printf("Error while updating plugins: %v", err)
			rc = 1
			continue
		}
		log.Print("Unzipping...")
		if err := config.Unzip(dest, SaveDir, 1); err != nil {
			log.Printf("Error while updating plugins: %v", err)
			rc = 1
			continue
		}
	}
	return rc
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
