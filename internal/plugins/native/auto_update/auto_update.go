// Package autoupdate is the native Go port of
// pwnagotchi/plugins/default/auto-update.py: checks GitHub releases for
// bettercap, pwngrid, and pwnagotchi itself, and — if a newer,
// architecture-matching release exists — downloads, checksum-verifies,
// and swaps in the new binary, restarting the affected systemd service.
//
// Original Python author: evilsocket@gmail.com (see auto-update.py's own
// __author__ field, left untouched). This Go port is by raf181.
//
// Real Python's install() had two paths: a "native" binary-swap path
// (download zip, verify sha256, stop service, move binary, chmod,
// restart service) used for bettercap/pwngrid, and a "pip install into a
// venv" path used only for pwnagotchi itself, because pwnagotchi used to
// be a Python package. Now that pwnagotchi is this native Go binary, that
// second path no longer applies to anything — GO_ONLY_MIGRATION_PROMPT.md
// explicitly requires "update Go artifacts/image safely; never
// pip-install" — so this port uses the SAME real native binary-swap path
// (already proven correct for bettercap/pwngrid in the original file) for
// all three services uniformly. Nothing here pip-installs, shells out to
// `wget`/`unzip`/`sha256sum`, or touches a Python venv: downloading is a
// real net/http GET, extraction is the standard archive/zip reader, and
// checksum verification is crypto/sha256 — all in-process, no shell.
package autoupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
	"github.com/jayofelony/pwnagotchi/internal/version"
)

const (
	commandTimeout  = 5 * time.Minute
	httpTimeout     = 30 * time.Second
	defaultStateDir = "/root"
)

// StatePath is the real plugin's StatusFile equivalent
// (/root/.auto-update): a single JSON timestamp gating how often a check
// actually runs. Overridable for tests, matching this port's established
// package-level-var override pattern (e.g. internal/wpasec.DBPath).
var StatePath = filepath.Join(defaultStateDir, ".auto-update")

// repoSpec is one of the three real to_check tuples in on_internet_available:
// (repo, local-version-source, native, service name).
type repoSpec struct {
	repo        string
	localVer    func(p *Plugin) string
	serviceName string
}

func defaultRepoSpecs() []repoSpec {
	return []repoSpec{
		{repo: "jayofelony/bettercap", serviceName: "bettercap", localVer: func(p *Plugin) string { return p.localVersion("bettercap", "-version") }},
		{repo: "jayofelony/pwngrid", serviceName: "pwngrid-peer", localVer: func(p *Plugin) string { return p.localVersion("pwngrid", "-version") }},
		{repo: "jayofelony/pwnagotchi", serviceName: "pwnagotchi", localVer: func(p *Plugin) string { return version.Version }},
	}
}

// UpdateInfo mirrors the real `info` dict `check()` returns.
type UpdateInfo struct {
	Repo      string
	Current   string
	Available string
	URL       string
	Service   string
	Arch      string
}

// Plugin ports the AutoUpdate class.
type Plugin struct {
	mu sync.Mutex

	ready    bool
	interval float64 // hours
	install  bool
	token    string

	exec       pluginmanager.CommandRunner
	httpClient *http.Client
	view       pluginmanager.ViewCapability
	system     pluginmanager.SystemCapability
	log        pluginmanager.Logger
	clock      pluginmanager.Clock
	emit       func(event string, args ...interface{})

	repoSpecs []repoSpec
	arch      string // overridable for tests; defaults to runtime.GOARCH-derived value
	apiBase   string // overridable for tests; defaults to the real GitHub API
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

const defaultGithubAPIBase = "https://api.github.com"

// githubAPIBase overrides the GitHub API base URL (production default:
// https://api.github.com) so tests can point it at an httptest.NewServer
// instead of ever making a real network call.
func (p *Plugin) githubAPIBase(base string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.apiBase = base
}

// New ports AutoUpdate.__init__.
func New() *Plugin {
	return &Plugin{repoSpecs: defaultRepoSpecs(), arch: goArchToUname(runtime.GOARCH), apiBase: defaultGithubAPIBase}
}

func (p *Plugin) Name() string { return "auto-update" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "1.1.1",
		Author:      "evilsocket@gmail.com",
		License:     "GPL3",
		Description: "This plugin checks when updates are available and applies them when internet is available.",
	}
}

// OnLoad ports on_loaded: requires options['interval'] to be set.
func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exec = caps.Exec
	p.httpClient = caps.HTTPClient
	if p.httpClient == nil {
		p.httpClient = &http.Client{Timeout: httpTimeout}
	}
	p.view = caps.View
	p.system = caps.System
	p.log = caps.Log
	p.emit = caps.Emit
	p.clock = caps.Clock
	if p.clock == nil {
		p.clock = realClock{}
	}

	p.interval, _ = toFloat(caps.Config["interval"])
	p.install, _ = caps.Config["install"].(bool)
	p.token, _ = caps.Config["token"].(string)

	if p.interval <= 0 {
		p.logf("main.plugins.auto-update.interval is not set")
		return nil
	}
	p.ready = true
	p.logf("plugin loaded.")
	return nil
}

func (p *Plugin) OnUnload() error { return nil }

// HandleEvent ports on_internet_available.
func (p *Plugin) HandleEvent(event string, args []interface{}) {
	if event != "internet_available" {
		return
	}
	p.mu.Lock()
	ready := p.ready
	p.mu.Unlock()
	if !ready {
		return
	}
	p.checkAndInstall()
}

// newerThanHours ports StatusFile.newer_then_hours: has StatePath's
// mtime/timestamp been updated within the last `hours` hours?
func (p *Plugin) newerThanHours(hours float64) bool {
	data, err := os.ReadFile(StatePath)
	if err != nil {
		return false
	}
	var st struct {
		Last time.Time `json:"last_update"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return false
	}
	return p.clock.Now().Sub(st.Last) < time.Duration(hours*float64(time.Hour))
}

func (p *Plugin) writeState() {
	data, err := json.Marshal(struct {
		Last time.Time `json:"last_update"`
	}{Last: p.clock.Now()})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(StatePath), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(StatePath, data, 0o600)
}

// checkAndInstall ports the body of on_internet_available.
func (p *Plugin) checkAndInstall() {
	p.mu.Lock()
	interval := p.interval
	p.mu.Unlock()

	if p.newerThanHours(interval) {
		p.logf("last check happened less than %.0f hours ago", interval)
		return
	}

	p.logf("checking for updates ...")
	if p.view != nil {
		p.view.Set("status", "Checking for updates ...")
		p.view.Update(true)
	}

	var toInstall []UpdateInfo
	p.mu.Lock()
	specs := p.repoSpecs
	p.mu.Unlock()
	for _, spec := range specs {
		localVer := spec.localVer(p)
		info, err := p.check(localVer, spec.repo)
		if err != nil {
			p.logf("checking %s: %v", spec.repo, err)
			continue
		}
		if info.URL != "" {
			info.Service = spec.serviceName
			p.logf("update for %s available (local version is %q): %s", spec.repo, info.Current, info.URL)
			toInstall = append(toInstall, info)
		}
	}

	installed := 0
	p.mu.Lock()
	shouldInstall := p.install
	p.mu.Unlock()
	if len(toInstall) > 0 && shouldInstall {
		for _, update := range toInstall {
			if p.emit != nil {
				p.emit("updating")
			}
			if err := p.installUpdate(update); err != nil {
				p.logf("installing %s: %v", update.Repo, err)
				continue
			}
			installed++
		}
	}

	p.writeState()

	if installed > 0 && p.system != nil {
		if p.view != nil {
			p.view.Set("status", "Rebooting ...")
			p.view.Update(true)
		}
		p.system.Reboot("")
	}
}

// check ports the module-level check(version, repo, native, token)
// function: query GitHub's latest release, then pick the
// architecture-matching asset (arm/armhf vs arm64/aarch64), exactly like
// the real is_armhf/is_aarch branches.
func (p *Plugin) check(localVersion, repo string) (UpdateInfo, error) {
	info := UpdateInfo{Repo: repo, Current: localVersion, Arch: p.archOrDefault()}

	p.mu.Lock()
	apiBase := p.apiBase
	if apiBase == "" {
		apiBase = defaultGithubAPIBase
	}
	p.mu.Unlock()
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/repos/%s/releases/latest", apiBase, repo), nil)
	if err != nil {
		return info, err
	}
	p.mu.Lock()
	token := p.token
	p.mu.Unlock()
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return info, fmt.Errorf("failed to get latest release for %s: %d", repo, resp.StatusCode)
	}

	var latest struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&latest); err != nil {
		return info, err
	}

	availableVer := strings.ReplaceAll(latest.TagName, "v", "")
	info.Available = availableVer

	if config.CompareVersions(availableVer, localVersion) <= 0 {
		return info, nil
	}

	isArmhf := strings.HasPrefix(info.Arch, "arm") && !strings.HasPrefix(info.Arch, "aarch")
	isAarch := strings.HasPrefix(info.Arch, "aarch")
	for _, asset := range latest.Assets {
		url := asset.BrowserDownloadURL
		if !strings.HasSuffix(url, ".zip") {
			continue
		}
		if strings.Contains(url, info.Arch) ||
			(isArmhf && strings.Contains(url, "armhf")) ||
			(isAarch && strings.Contains(url, "aarch")) {
			info.URL = url
			break
		}
	}
	return info, nil
}

// installUpdate ports install(display, update): download the release
// zip, verify its sha256 checksum file (if present), stop the service,
// swap the binary in, chmod it executable, restart the service. Entirely
// native Go I/O — no wget/unzip/sha256sum/pip subprocess.
func (p *Plugin) installUpdate(update UpdateInfo) error {
	name := update.Repo
	if idx := strings.LastIndexByte(name, '/'); idx >= 0 {
		name = name[idx+1:]
	}

	if p.view != nil {
		p.view.Set("status", fmt.Sprintf("Downloading %s %s ...", name, update.Available))
		p.view.Update(true)
	}

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, update.URL, nil)
	if err != nil {
		return err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", update.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: HTTP %d", update.URL, resp.StatusCode)
	}
	zipData, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if p.view != nil {
		p.view.Set("status", fmt.Sprintf("Extracting %s %s ...", name, update.Available))
		p.view.Update(true)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("opening release zip: %w", err)
	}

	var binaryData []byte
	var checksumData []byte
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if base == name || strings.HasPrefix(base, name+"-") {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			binaryData, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return err
			}
		}
		if strings.HasSuffix(base, ".sha256") {
			rc, err := f.Open()
			if err == nil {
				checksumData, _ = io.ReadAll(rc)
				rc.Close()
			}
		}
	}
	if binaryData == nil {
		return fmt.Errorf("no binary named %q found in release zip", name)
	}

	if p.view != nil {
		p.view.Set("status", fmt.Sprintf("Verifying %s %s ...", name, update.Available))
		p.view.Update(true)
	}
	if len(checksumData) == 0 {
		p.logf("native update for %s without a SHA256 checksum file", name)
	} else if !verifyChecksum(binaryData, checksumData) {
		return fmt.Errorf("checksum mismatch for %s", name)
	}

	destPath, err := p.destPathFor(name)
	if err != nil {
		return err
	}

	if p.view != nil {
		p.view.Set("status", fmt.Sprintf("Installing %s %s ...", name, update.Available))
		p.view.Update(true)
	}
	p.logf("stopping %s ...", update.Service)
	p.systemctl("stop", update.Service)

	tmpPath := destPath + ".new"
	if err := os.WriteFile(tmpPath, binaryData, 0o755); err != nil {
		return fmt.Errorf("writing new binary: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("installing new binary: %w", err)
	}
	if err := os.Chmod(destPath, 0o755); err != nil {
		return err
	}

	p.logf("restarting %s ...", update.Service)
	p.systemctl("start", update.Service)
	return nil
}

// destPathFor mirrors `subprocess.getoutput("which %s" % name)`: find the
// currently-installed binary's real path via the injected CommandRunner
// (never a shell string built with untrusted input — name always comes
// from this plugin's own fixed repoSpecs, never remote data).
func (p *Plugin) destPathFor(name string) (string, error) {
	if p.exec == nil {
		return "", fmt.Errorf("no command runner available to locate %s", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := p.exec.Run(ctx, "which", name)
	if err != nil {
		return "", fmt.Errorf("can't find path for %s: %w", name, err)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", fmt.Errorf("can't find path for %s", name)
	}
	return path, nil
}

func (p *Plugin) systemctl(action, service string) {
	if p.exec == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := p.exec.Run(ctx, "systemctl", action, service); err != nil {
		p.logf("systemctl %s %s: %v", action, service, err)
	}
}

// localVersion ports parse_version(cmd): run `<bin> <flag>` and pull out
// the first dotted-numeric-looking token.
func (p *Plugin) localVersion(bin, flag string) string {
	if p.exec == nil {
		return "0.0.0"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := p.exec.Run(ctx, bin, flag)
	if err != nil {
		return "0.0.0"
	}
	for _, part := range strings.Fields(string(out)) {
		part = strings.TrimPrefix(strings.TrimSpace(part), "v")
		if looksLikeVersion(part) {
			return part
		}
	}
	return "0.0.0"
}

func looksLikeVersion(s string) bool {
	parts := strings.SplitN(s, ".", 3)
	if len(parts) < 3 {
		return false
	}
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(parts[0]) > 0
}

func (p *Plugin) archOrDefault() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.arch != "" {
		return p.arch
	}
	return goArchToUname(runtime.GOARCH)
}

// goArchToUname maps Go's GOARCH to the `platform.machine()` (uname -m)
// strings the real Python plugin compares release asset filenames
// against.
func goArchToUname(goarch string) string {
	switch goarch {
	case "arm64":
		return "aarch64"
	case "arm":
		return "armv7l"
	case "amd64":
		return "x86_64"
	default:
		return goarch
	}
}

func verifyChecksum(data, checksumFile []byte) bool {
	expected := strings.ToLower(strings.TrimSpace(string(checksumFile)))
	if idx := strings.IndexByte(expected, '='); idx >= 0 {
		expected = strings.TrimSpace(expected[idx+1:])
	}
	if idx := strings.IndexByte(expected, ' '); idx >= 0 {
		expected = expected[:idx]
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) == expected
}

func toFloat(v interface{}) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int64:
		return float64(t), true
	case int:
		return float64(t), true
	}
	return 0, false
}

func (p *Plugin) logf(format string, args ...interface{}) {
	if p.log != nil {
		p.log.Printf(format, args...)
	}
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.Unloader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
