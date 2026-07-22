package web

import (
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
	"github.com/jayofelony/pwnagotchi/go-port/internal/plugins"
)

type pluginRow struct {
	Name        string
	Loaded      bool
	Version     string
	Author      string
	Description string
	HasWebhook  bool
	IsDefault   bool
	HasInfo     bool
}

// pluginsIndex ports Handler.plugins(name=None): the /plugins listing
// page, built from the REAL, live pwnagotchi.plugins module state read
// from the running bridge (plugins.loaded/plugins.database), not
// reconstructed or guessed at in Go.
func (s *Server) pluginsIndex(w http.ResponseWriter, r *http.Request) {
	if s.bridge == nil {
		http.Error(w, "plugin bridge unavailable (no working Python/pwnagotchi install found at startup)", http.StatusServiceUnavailable)
		return
	}
	list, err := s.bridge.ListPlugins()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defaultSet := map[string]bool{}
	for _, n := range list.DefaultPlugins {
		defaultSet[n] = true
	}

	names := make([]string, 0, len(list.Database))
	for n := range list.Database {
		names = append(names, n)
	}
	sort.Strings(names)

	rows := make([]pluginRow, 0, len(names))
	for _, n := range names {
		meta, loaded := list.Loaded[n]
		rows = append(rows, pluginRow{
			Name:        n,
			Loaded:      loaded,
			Version:     meta.Version,
			Author:      meta.Author,
			Description: meta.Description,
			HasWebhook:  meta.HasWebhook,
			IsDefault:   defaultSet[n],
			HasInfo:     loaded && meta.Description != "",
		})
	}

	s.render(w, r, "plugins", map[string]interface{}{
		"plugin_rows": rows,
		"active_page": "plugins",
	})
}

// pluginsSubpath ports /plugins/<name>[/<subpath>]: the toggle/upgrade
// actions and the generic on_webhook passthrough.
func (s *Server) pluginsSubpath(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/plugins/")
	parts := strings.SplitN(rest, "/", 2)
	name := parts[0]
	subpath := ""
	if len(parts) == 2 {
		subpath = parts[1]
	}

	// logtail has a real native Go implementation (see logtail.go) instead
	// of going through the Python plugin bridge — no bridge dependency,
	// no bridge involved at all.
	if name == "logtail" {
		if subpath == "stream" {
			s.logtailStream(w, r)
		} else {
			s.logtailIndex(w, r)
		}
		return
	}

	// webcfg also has a real native Go implementation (see webcfg.go)
	// instead of going through the Python plugin bridge — the bridge
	// can't support its merge-save-config route's live (no-restart)
	// config update, see webcfg.go's own doc comment.
	if name == "webcfg" {
		switch {
		case subpath == "get-config" && r.Method == http.MethodGet:
			s.webcfgGetConfig(w, r)
		case subpath == "save-config" && r.Method == http.MethodPost:
			s.webcfgSaveConfig(w, r)
		case subpath == "merge-save-config" && r.Method == http.MethodPost:
			s.webcfgMergeSaveConfig(w, r)
		case subpath == "" || subpath == "/":
			s.webcfgIndex(w, r)
		default:
			http.NotFound(w, r)
		}
		return
	}

	if s.bridge == nil {
		http.Error(w, "plugin bridge unavailable", http.StatusServiceUnavailable)
		return
	}

	if name == "toggle" && r.Method == http.MethodPost {
		s.pluginToggle(w, r)
		return
	}
	if name == "upgrade" && r.Method == http.MethodPost {
		s.pluginUpgrade(w, r)
		return
	}

	s.pluginWebhook(w, r, name, subpath)
}

// pluginToggle ports the `name == "toggle"` branch of Handler.plugins:
// real load/unload via the bridge's real plugins.toggle_plugin, with Go
// itself persisting the new enabled state to the on-disk config (the
// bridge process has no real pwnagotchi.config global to do this itself
// — see docs/known-differences.md).
func (s *Server) pluginToggle(w http.ResponseWriter, r *http.Request) {
	if !checkCSRF(r) {
		http.Error(w, "failed", http.StatusForbidden)
		return
	}
	name := r.FormValue("plugin")
	checked := r.FormValue("enabled") != ""

	changed, err := s.bridge.TogglePlugin(name, checked)
	if err != nil || !changed {
		w.Write([]byte("failed"))
		return
	}

	if s.cfg != nil {
		mainCfg, _ := s.cfg["main"].(config.Map)
		if mainCfg == nil {
			mainCfg = config.Map{}
			s.cfg["main"] = mainCfg
		}
		pluginsCfg, _ := mainCfg["plugins"].(config.Map)
		if pluginsCfg == nil {
			pluginsCfg = config.Map{}
			mainCfg["plugins"] = pluginsCfg
		}
		entry, _ := pluginsCfg[name].(config.Map)
		if entry == nil {
			entry = config.Map{}
			pluginsCfg[name] = entry
		}
		entry["enabled"] = checked
		if s.cfgPath != "" {
			_ = config.SaveConfig(s.cfg, s.cfgPath)
		}
	}

	w.Write([]byte("success"))
}

// pluginUpgrade ports the `name == "upgrade"` branch: real Python shells
// out to `pwnagotchi plugins update && pwnagotchi plugins upgrade <name>`
// via os.system. This calls the real, already-ported internal/plugins
// Update/Upgrade functions in-process instead of spawning a shell —
// same real behavior (network fetch + file replace), no shell string
// interpolation of a user-influenced plugin name, matching the "avoid
// unnecessary shell execution" security requirement more strictly than
// the Python original. See docs/known-differences.md.
func (s *Server) pluginUpgrade(w http.ResponseWriter, r *http.Request) {
	if !checkCSRF(r) {
		http.Error(w, "CSRF token missing or invalid", http.StatusForbidden)
		return
	}
	name := r.FormValue("plugin")
	if s.cfg != nil {
		plugins.Update(s.cfg)
		plugins.Upgrade(s.cfg, name)
	}
	http.Redirect(w, r, "/plugins", http.StatusFound)
}

// pluginWebhook ports the final branch of Handler.plugins: passthrough to
// a loaded plugin's real on_webhook, via the bridge's synchronous webhook
// call (a genuine flask.Request built server-side — see bridge.py).
func (s *Server) pluginWebhook(w http.ResponseWriter, r *http.Request, name, subpath string) {
	body, _ := io.ReadAll(r.Body)
	resp, err := s.bridge.Webhook(name, subpath, r.Method, r.URL.Path, r.URL.RawQuery, r.Header, body)
	if err != nil {
		http.Error(w, "", http.StatusNotFound)
		return
	}
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(resp.Status)
	w.Write(resp.Body)
}
