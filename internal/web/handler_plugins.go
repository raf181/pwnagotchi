package web

import (
	"net/http"
	"sort"
	"strings"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginrpc"
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
	ensureCSRFToken(w, r)
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

	if name == "toggle" {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		s.pluginToggle(w, r)
		return
	}
	if name == "upgrade" {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
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
	if unsafeMethod(r.Method) && !checkCSRF(r) {
		http.Error(w, "CSRF token missing or invalid", http.StatusForbidden)
		return
	}
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
	if err := pluginrpc.ValidatePluginName(name); err != nil {
		http.Error(w, "failed", http.StatusBadRequest)
		return
	}
	checked := r.FormValue("enabled") != ""

	if s.pluginMgr == nil || !s.pluginMgr.Has(name) {
		http.Error(w, "failed", http.StatusNotFound)
		return
	}

	var pluginCfg config.Map
	rollbackConfig := func() {}
	if s.cfg != nil {
		pluginCfg, rollbackConfig = stagePluginEnabled(s.cfg, name, checked)
	}

	changed, toggleErr := s.pluginMgr.Toggle(name, checked, pluginCfg, s.cfg)
	if toggleErr != nil && !changed {
		rollbackConfig()
		http.Error(w, "failed", http.StatusInternalServerError)
		return
	}

	if changed && s.cfg != nil && s.cfgPath != "" {
		if err := config.SaveConfig(s.cfg, s.cfgPath); err != nil {
			rollbackConfig()
			_, _ = s.pluginMgr.Toggle(name, !checked, pluginCfg, s.cfg)
			http.Error(w, "failed", http.StatusInternalServerError)
			return
		}
	}

	if toggleErr != nil {
		http.Error(w, "plugin state changed but cleanup failed", http.StatusInternalServerError)
		return
	}

	w.Write([]byte("success"))
}

// stagePluginEnabled applies a requested state and returns a closure that
// restores the exact prior map shape. The rollback matters when runtime
// loading or persistence fails: failed toggles must not leave new config
// sections or a stale enabled value in the shared live configuration.
func stagePluginEnabled(cfg config.Map, name string, enabled bool) (config.Map, func()) {
	oldMain, hadMain := cfg["main"]
	mainCfg, mainWasMap := oldMain.(config.Map)
	if !mainWasMap {
		mainCfg = config.Map{}
		cfg["main"] = mainCfg
	}
	oldPlugins, hadPlugins := mainCfg["plugins"]
	pluginsCfg, pluginsWasMap := oldPlugins.(config.Map)
	if !pluginsWasMap {
		pluginsCfg = config.Map{}
		mainCfg["plugins"] = pluginsCfg
	}
	oldEntry, hadEntry := pluginsCfg[name]
	entry, entryWasMap := oldEntry.(config.Map)
	if !entryWasMap {
		entry = config.Map{}
		pluginsCfg[name] = entry
	}
	oldEnabled, hadEnabled := entry["enabled"]
	entry["enabled"] = enabled

	return entry, func() {
		if hadEnabled {
			entry["enabled"] = oldEnabled
		} else {
			delete(entry, "enabled")
		}
		if !entryWasMap {
			if hadEntry {
				pluginsCfg[name] = oldEntry
			} else {
				delete(pluginsCfg, name)
			}
		}
		if !pluginsWasMap {
			if hadPlugins {
				mainCfg["plugins"] = oldPlugins
			} else {
				delete(mainCfg, "plugins")
			}
		}
		if !mainWasMap {
			if hadMain {
				cfg["main"] = oldMain
			} else {
				delete(cfg, "main")
			}
		}
	}
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

// pluginUpgrade ports the `name == "upgrade"` branch. It calls the
// already-ported internal/plugins upgrade function in-process instead of
// spawning a shell, avoiding shell interpolation of a user-controlled
// plugin name. See docs/known-differences.md.
func (s *Server) pluginUpgrade(w http.ResponseWriter, r *http.Request) {
	if !checkCSRF(r) {
		http.Error(w, "CSRF token missing or invalid", http.StatusForbidden)
		return
	}
	name := r.FormValue("plugin")
	if err := pluginrpc.ValidatePluginName(name); err != nil {
		http.Error(w, "invalid plugin name", http.StatusBadRequest)
		return
	}
	if s.cfg != nil {
		if plugins.Upgrade(s.cfg, name) != 0 {
			http.Error(w, "plugin upgrade failed", http.StatusBadGateway)
			return
		}
	}
	http.Redirect(w, r, "/plugins", http.StatusFound)
}
