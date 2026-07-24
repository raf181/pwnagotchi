package web

import (
	"net/http"
	"sort"
	"strings"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/plugins"
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
// page, built from the real, live pluginmanager.Manager state — every
// bundled plugin is native as of this migration, so this is the only
// plugin backend.
func (s *Server) pluginsIndex(w http.ResponseWriter, r *http.Request) {
	if s.pluginMgr == nil {
		http.Error(w, "no plugin manager available", http.StatusServiceUnavailable)
		return
	}

	rowsByName := map[string]pluginRow{}
	for _, st := range s.pluginMgr.List() {
		rowsByName[st.Name] = pluginRow{
			Name:        st.Name,
			Loaded:      st.Enabled,
			Version:     st.Metadata.Version,
			Author:      st.Metadata.Author,
			Description: st.Metadata.Description,
			HasWebhook:  st.Metadata.HasWebhook,
			IsDefault:   true,
			HasInfo:     st.Enabled && st.Metadata.Description != "",
		}
	}

	names := make([]string, 0, len(rowsByName))
	for n := range rowsByName {
		names = append(names, n)
	}
	sort.Strings(names)

	rows := make([]pluginRow, 0, len(names))
	for _, n := range names {
		rows = append(rows, rowsByName[n])
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

	if name == "toggle" && r.Method == http.MethodPost {
		s.pluginToggle(w, r)
		return
	}
	if name == "upgrade" && r.Method == http.MethodPost {
		s.pluginUpgrade(w, r)
		return
	}

	// Every plugin is native (registered with the plugin manager) as of
	// this migration — real in-process dispatch, no bridge/IPC involved
	// at all. A name the manager doesn't know about is simply unknown.
	if s.pluginMgr == nil || !s.pluginMgr.Has(name) {
		http.NotFound(w, r)
		return
	}
	s.pluginWebhookNative(w, r, name, subpath)
}

// pluginWebhookNative ports the webhook passthrough against a plugin
// loaded natively in the manager: a real *http.Request handed directly to
// the plugin's OnWebhook, no serialization boundary.
func (s *Server) pluginWebhookNative(w http.ResponseWriter, r *http.Request, name, subpath string) {
	resp, err := s.pluginMgr.Webhook(name, subpath, r)
	if err != nil {
		http.Error(w, "", http.StatusNotFound)
		return
	}
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	if resp.Status == 0 {
		resp.Status = http.StatusOK
	}
	w.WriteHeader(resp.Status)
	w.Write(resp.Body)
}

// pluginToggle ports the `name == "toggle"` branch of Handler.plugins:
// real load/unload via the plugin manager, with Go itself persisting the
// new enabled state to the on-disk config.
func (s *Server) pluginToggle(w http.ResponseWriter, r *http.Request) {
	if !checkCSRF(r) {
		http.Error(w, "failed", http.StatusForbidden)
		return
	}
	name := r.FormValue("plugin")
	checked := r.FormValue("enabled") != ""

	var pluginCfg config.Map
	if s.cfg != nil {
		pluginCfg = pluginConfigEntry(s.cfg, name)
		pluginCfg["enabled"] = checked
	}

	if s.pluginMgr == nil || !s.pluginMgr.Has(name) {
		w.Write([]byte("failed"))
		return
	}
	changed, err := s.pluginMgr.Toggle(name, checked, pluginCfg, s.cfg)
	if err != nil || !changed {
		w.Write([]byte("failed"))
		return
	}

	if s.cfg != nil && s.cfgPath != "" {
		_ = config.SaveConfig(s.cfg, s.cfgPath)
	}

	w.Write([]byte("success"))
}

// pluginConfigEntry returns (creating if necessary) config['main']
// ['plugins'][name], mutating cfg in place — the same config.Map every
// other subsystem shares a reference to, so the change is visible
// immediately without a separate propagation step (see webcfg.go's doc
// comment for why that already works this way in this port).
func pluginConfigEntry(cfg config.Map, name string) config.Map {
	mainCfg, _ := cfg["main"].(config.Map)
	if mainCfg == nil {
		mainCfg = config.Map{}
		cfg["main"] = mainCfg
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
	return entry
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
