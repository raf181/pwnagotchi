package web

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/jayofelony/pwnagotchi/internal/config"
)

// Native Go replacement for plugins/default/webcfg.py's web UI (the
// runtime config editor at /plugins/webcfg). Original Python plugin
// authored by 33197631+dadav@users.noreply.github.com, modified by
// wsvdmeer (see pwnagotchi/plugins/default/webcfg.py's __author__ field —
// left untouched, that credit belongs to them). This Go implementation is
// a new port by raf181, not a translation-in-place of their file.
//
// Why native instead of routed through internal/pyplugin: webcfg.py's
// "merge-save-config" handler reads/writes the `pwnagotchi.config` Python
// MODULE GLOBAL directly (`pwnagotchi.config = merge_config(request.get_json(),
// pwnagotchi.config)`) so the change is visible to the rest of the running
// daemon without a restart. The bridge only sets that global for the real
// plugins.load() startup window, deliberately reset to None afterward (see
// docs/known-differences.md's pwnagotchi.config entry) — so this one
// handler could never work correctly through the bridge as designed. A
// native Go implementation sidesteps the whole problem: cmd/pwnagotchi's
// main.go passes the SAME config.Map value (a reference type) to
// agent.New/view.New/web.New, so mutating s.cfg's contents in place is
// already visible to every other subsystem immediately — no separate
// "propagate to N different holders" step is needed the way Python's
// three separate reassignments (self.config/pwnagotchi.config/agent._config)
// were working around.
func (s *Server) webcfgIndex(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "webcfg", map[string]interface{}{
		"active_page": "plugins",
	})
}

// webcfgGetConfig ports the GET "get-config" route: the full live config,
// as JSON, for the page's JS to flatten into its editable table.
func (s *Server) webcfgGetConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s.cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// decodeConfigJSON decodes a posted config JSON body the same way
// Python's json.loads distinguishes int from float (a bare "10" decodes
// to an int, "10.0" to a float) — Go's encoding/json defaults every JSON
// number to float64, which would silently turn whole-number config values
// like a port or a channel into "10.0" once re-encoded to TOML
// (BurntSushi/toml formats float64 with a decimal point even for a whole
// number), diverging from both the original file's formatting and every
// other int-valued config field this port already represents as int64
// (see internal/config's own TOML loading). UseNumber + normalizeJSONNumbers
// keeps this consistent.
func decodeConfigJSON(r *http.Request) (config.Map, error) {
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	var raw interface{}
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	normalized := normalizeJSONNumbers(raw)
	m, _ := normalized.(config.Map)
	if m == nil {
		m = config.Map{}
	}
	return m, nil
}

func normalizeJSONNumbers(v interface{}) interface{} {
	switch val := v.(type) {
	case json.Number:
		if i, err := val.Int64(); err == nil && !hasDecimalOrExponent(string(val)) {
			return i
		}
		f, _ := val.Float64()
		return f
	case map[string]interface{}:
		out := make(config.Map, len(val))
		for k, e := range val {
			out[k] = normalizeJSONNumbers(e)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, e := range val {
			out[i] = normalizeJSONNumbers(e)
		}
		return out
	default:
		return v
	}
}

func hasDecimalOrExponent(s string) bool {
	for _, c := range s {
		if c == '.' || c == 'e' || c == 'E' {
			return true
		}
	}
	return false
}

// webcfgSaveConfig ports the POST "save-config" route: a full,
// unconditional overwrite of the on-disk config with the posted JSON,
// followed by a real daemon restart in the agent's current mode — matching
// real webcfg.py exactly (it does NOT update any in-memory config copy
// either, since the imminent restart re-reads the file fresh; a Go
// process restart via internal/unit.Restart does the same).
func (s *Server) webcfgSaveConfig(w http.ResponseWriter, r *http.Request) {
	if !checkCSRF(r) {
		http.Error(w, "failed", http.StatusForbidden)
		return
	}
	posted, err := decodeConfigJSON(r)
	if err != nil {
		log.Printf("webcfg: save-config: decoding posted JSON: %v", err)
		http.Error(w, "config error", http.StatusInternalServerError)
		return
	}
	if err := config.SaveConfig(posted, s.cfgPath); err != nil {
		log.Printf("webcfg: save-config: %v", err)
		http.Error(w, "config error", http.StatusInternalServerError)
		return
	}

	mode := "MANU"
	if s.agent != nil && s.agent.Mode() == "auto" {
		mode = "AUTO"
	}
	w.Write([]byte("success"))
	go func() {
		if s.actions == nil {
			return
		}
		if err := s.actions.Restart(mode); err != nil {
			log.Printf("webcfg: restart after save-config: %v", err)
		}
	}()
}

// webcfgMergeSaveConfig ports the POST "merge-save-config" route: merge
// the posted JSON over the live config (posted values win, existing
// values fill any gap the posted table didn't have — see
// internal/config.MergeConfig), apply it to the live, shared s.cfg IN
// PLACE (so agent/view/every other holder of the same map reference sees
// it immediately, no restart — matching the real plugin's "no restart"
// promise for this route specifically, vs. save-config above), and
// persist to disk.
func (s *Server) webcfgMergeSaveConfig(w http.ResponseWriter, r *http.Request) {
	if !checkCSRF(r) {
		http.Error(w, "failed", http.StatusForbidden)
		return
	}
	posted, err := decodeConfigJSON(r)
	if err != nil {
		log.Printf("webcfg: merge-save-config: decoding posted JSON: %v", err)
		http.Error(w, "config error", http.StatusInternalServerError)
		return
	}
	if s.cfg == nil {
		http.Error(w, "config error", http.StatusInternalServerError)
		return
	}
	// MergeConfig(user, def) mutates and returns `user` (posted values win,
	// def only fills gaps posted doesn't have) — see internal/config/merge.go.
	merged := config.MergeConfig(posted, s.cfg)
	replaceMapContents(s.cfg, merged)

	if err := config.SaveConfig(s.cfg, s.cfgPath); err != nil {
		log.Printf("webcfg: merge-save-config: %v", err)
		http.Error(w, "config error", http.StatusInternalServerError)
		return
	}
	w.Write([]byte("success"))
}

// replaceMapContents overwrites dst's entries with src's, in place —
// dst stays the SAME map object every other subsystem already holds a
// reference to (agent.New/view.New/web.New in cmd/pwnagotchi/main.go are
// all handed the identical config.Map value at startup), so this is what
// actually makes a merge-save-config change visible elsewhere without a
// restart: reassigning `s.cfg = merged` would only rebind this Server's
// own field, leaving every other holder's reference pointing at the old,
// stale map.
func replaceMapContents(dst, src config.Map) {
	for k := range dst {
		delete(dst, k)
	}
	for k, v := range src {
		dst[k] = v
	}
}
