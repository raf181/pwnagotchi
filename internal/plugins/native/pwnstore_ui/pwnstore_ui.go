// Package pwnstoreui is the native Go port of
// pwnagotchi/plugins/default/pwnstore_ui.py: a web-UI plugin gallery
// that browses a remote plugins.json store and lists what's installed.
//
// Original Python author: WPA2 (see pwnstore_ui.py's own __author__
// field, left untouched). This Go port is by raf181.
//
// IMPORTANT, deliberate divergence: real pwnstore_ui.py's install/
// uninstall actions shell out to a `pwnstore` CLI that downloads and
// installs raw *.py plugin files — exactly the Python plugin
// distribution mechanism this migration replaces (see
// GO_ONLY_MIGRATION_PROMPT.md's plugin-distribution requirement: third-
// party Python plugins "must never cause Python to be installed or
// silently be treated as loaded"). This port keeps the real browsing
// (fetch the store JSON, list installed) and config-file-editing
// behavior faithful, but Install/Uninstall return a clear, honest
// "not yet supported" failure instead of running a Python-plugin
// installer or lying about success — see installUnsupported below.
package pwnstoreui

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// storeHTML is the real frontend from pwnstore_ui.py's _render_store,
// copied verbatim (not rewritten) — see store.html.
//
//go:embed store.html
var storeHTML string

const defaultStoreURL = "https://raw.githubusercontent.com/wpa-2/pwnagotchi-store/main/plugins.json"

// installedPluginsDir mirrors Python's hardcoded
// "/usr/local/share/pwnagotchi/custom-plugins" — overridable for tests.
var installedPluginsDir = "/usr/local/share/pwnagotchi/custom-plugins"

// configPath mirrors Python's hardcoded "/etc/pwnagotchi/config.toml" —
// overridable for tests.
var configPath = "/etc/pwnagotchi/config.toml"

// restartDelay mirrors Python's `time.sleep(1)` before the real
// systemctl restart — overridable for tests so they don't need to wait
// a real second.
var restartDelay = 1 * time.Second

// Plugin ports the PwnStoreUI class.
type Plugin struct {
	mu       sync.Mutex
	storeURL string
	client   *http.Client
	exec     pluginmanager.CommandRunner
	log      pluginmanager.Logger
	ready    bool
}

func New() *Plugin { return &Plugin{storeURL: defaultStoreURL} }

func (p *Plugin) Name() string { return "pwnstore_ui" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "1.2.6",
		Author:      "WPA2",
		License:     "GPL3",
		Description: "Plugin store with web interface for browsing and installing plugins",
	}
}

// OnLoad ports on_loaded: real Python only checks whether the `pwnstore`
// CLI exists to decide whether install/uninstall can work at all — this
// port never has a working native install path (see package doc), so it
// always reports that clearly rather than probing for a CLI tool that,
// even if present, this port intentionally never shells out to.
func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.client = caps.HTTPClient
	if p.client == nil {
		p.client = http.DefaultClient
	}
	p.exec = caps.Exec
	p.log = caps.Log
	if url := stringField(caps.Config, "store_url", ""); url != "" {
		p.storeURL = url
	}
	p.ready = true
	if p.log != nil {
		p.log.Printf("plugin loaded (native install/uninstall not yet supported — see docs/plugin-development.md)")
	}
	return nil
}

// OnWebhook ports on_webhook's real path routing.
func (p *Plugin) OnWebhook(subpath string, r *http.Request) (pluginmanager.WebhookResponse, error) {
	path := strings.TrimPrefix(subpath, "/")
	switch path {
	case "", "/":
		return p.renderStore(r), nil
	case "api/plugins":
		return p.getPlugins(), nil
	case "api/installed":
		return p.getInstalled(), nil
	case "api/install":
		return p.installUnsupported(r, "install"), nil
	case "api/uninstall":
		return p.installUnsupported(r, "uninstall"), nil
	case "api/configure":
		return p.configurePlugin(r), nil
	case "api/restart":
		return p.restartPwnagotchi(), nil
	default:
		return pluginmanager.WebhookResponse{Status: http.StatusNotFound, Body: []byte("Not found")}, nil
	}
}

// renderStore ports _render_store: the real embedded HTML, with the
// CSRF-token meta tag populated from any csrf_token cookie already on
// the incoming request (this port's CSRF scheme is a double-submit
// cookie, not Flask-WTF's session token — see internal/web/csrf.go's
// doc comment — so unlike Python's generate_csrf() call, no new token
// can be minted from inside a webhook handler, which never sees the
// ResponseWriter needed to set one; using whatever the browser already
// carries mirrors Python's own effective behavior of an empty token
// when flask_wtf isn't installed, just non-empty when a session/cookie
// already exists).
func (p *Plugin) renderStore(r *http.Request) pluginmanager.WebhookResponse {
	token := ""
	if c, err := r.Cookie("csrf_token"); err == nil {
		token = c.Value
	}
	html := strings.Replace(storeHTML, "__CSRF_TOKEN__", token, 1)
	return pluginmanager.WebhookResponse{
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "text/html"},
		Body:    []byte(html),
	}
}

// getPlugins ports _get_plugins: fetch the store JSON, pass it through
// verbatim, or "[]" on any failure (matching Python's bare `except:
// return Response("[]", ...)`- ).
func (p *Plugin) getPlugins() pluginmanager.WebhookResponse {
	p.mu.Lock()
	client := p.client
	storeURL := p.storeURL
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, storeURL, nil)
	if err != nil {
		return jsonResponse(http.StatusOK, []byte("[]"))
	}
	resp, err := client.Do(req)
	if err != nil {
		return jsonResponse(http.StatusOK, []byte("[]"))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return jsonResponse(http.StatusOK, []byte("[]"))
	}
	return jsonResponse(http.StatusOK, body)
}

// getInstalled ports _get_installed: list *.py basenames under
// installedPluginsDir. On a Go-only unit this directory will simply
// never contain anything (no Python plugin installer writes to it
// anymore) — a real, honest empty result, not a fabricated one.
func (p *Plugin) getInstalled() pluginmanager.WebhookResponse {
	entries, err := os.ReadDir(installedPluginsDir)
	if err != nil {
		return jsonResponse(http.StatusOK, []byte("[]"))
	}
	names := []string{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".py") {
			names = append(names, strings.TrimSuffix(e.Name(), ".py"))
		}
	}
	data, _ := json.Marshal(names)
	return jsonResponse(http.StatusOK, data)
}

// installUnsupported replaces _install_plugin/_uninstall_plugin: this
// port never shells out to a Python-plugin installer (see package doc).
// Returns a real, honest, structured failure — never a silent/fabricated
// success — so the frontend's existing "Install failed" messaging (it
// already has one, for the CLI-not-found case) does the right thing.
func (p *Plugin) installUnsupported(r *http.Request, action string) pluginmanager.WebhookResponse {
	var body struct {
		Plugin string `json:"plugin"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	msg := fmt.Sprintf(
		"native Go plugin %s is not supported: third-party Python plugins cannot be installed on a Go-only pwnagotchi unit; see docs/plugin-development.md for the native plugin path",
		action,
	)
	data, _ := json.Marshal(map[string]interface{}{"success": false, "error": msg})
	return jsonResponse(http.StatusServiceUnavailable, data)
}

// configurePlugin ports _configure_plugin: rewrite config.toml, dropping
// any existing "main.plugins.<name>." lines and appending a fresh
// enabled=true stanza plus the posted key/values — byte-for-byte the
// same line-based (not TOML-parser-based) editing real Python does,
// including its exact value-quoting heuristic (bare true/false/digits/
// bracketed-list vs. quoted string).
func (p *Plugin) configurePlugin(r *http.Request) pluginmanager.WebhookResponse {
	var body struct {
		Plugin string                 `json:"plugin"`
		Config map[string]interface{} `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Plugin == "" {
		return errorResponse(fmt.Errorf("invalid request"))
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return errorResponse(err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	prefix := fmt.Sprintf("main.plugins.%s.", body.Plugin)
	var out []string
	for _, l := range lines {
		if !strings.HasPrefix(strings.TrimSpace(l), prefix) {
			out = append(out, l)
		}
	}
	out = append(out, "", fmt.Sprintf("# PwnStore Configuration: %s", body.Plugin), prefix+"enabled = true")
	for k, v := range body.Config {
		if k == "enabled" {
			continue
		}
		out = append(out, prefix+k+" = "+formatConfigValue(v))
	}

	var buf bytes.Buffer
	for _, l := range out {
		buf.WriteString(l)
		buf.WriteString("\n")
	}
	if err := os.WriteFile(configPath, buf.Bytes(), 0o644); err != nil {
		return errorResponse(err)
	}
	data2, _ := json.Marshal(map[string]interface{}{"success": true})
	return jsonResponse(http.StatusOK, data2)
}

// formatConfigValue ports the exact string/bool/digit/list quoting
// heuristic from _configure_plugin.
func formatConfigValue(v interface{}) string {
	s := fmt.Sprintf("%v", v)
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	if lower == "true" || lower == "false" {
		return lower
	}
	if _, err := strconv.Atoi(s); err == nil {
		return s
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return s
	}
	return `"` + s + `"`
}

// restartPwnagotchi ports _restart_pwnagotchi: a real, delayed,
// backgrounded `systemctl restart pwnagotchi` via the injected argv
// CommandRunner (never a shell string) instead of Python's
// `subprocess.run(['systemctl', ...])` in a raw OS thread.
func (p *Plugin) restartPwnagotchi() pluginmanager.WebhookResponse {
	p.mu.Lock()
	exec := p.exec
	p.mu.Unlock()
	if exec != nil {
		go func() {
			time.Sleep(restartDelay)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = exec.Run(ctx, "systemctl", "restart", "pwnagotchi")
		}()
	}
	data, _ := json.Marshal(map[string]interface{}{"success": true})
	return jsonResponse(http.StatusOK, data)
}

func jsonResponse(status int, body []byte) pluginmanager.WebhookResponse {
	return pluginmanager.WebhookResponse{
		Status:  status,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    body,
	}
}

func errorResponse(err error) pluginmanager.WebhookResponse {
	data, _ := json.Marshal(map[string]interface{}{"success": false, "error": err.Error()})
	return jsonResponse(http.StatusInternalServerError, data)
}

func stringField(m config.Map, key, def string) string {
	if m == nil {
		return def
	}
	if s, ok := m[key].(string); ok && s != "" {
		return s
	}
	return def
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.WebhookHandler = (*Plugin)(nil)
