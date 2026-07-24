package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
)

// newTestServerWithFullCfg mirrors newTestServer but lets a test control
// the full daemon config (s.cfg) and where it's persisted (s.cfgPath) —
// webcfg's whole point is mutating/persisting that shared config, so the
// plain newTestServer helper (cfgPath="") can't exercise it.
func newTestServerWithFullCfg(fullCfg config.Map, cfgPath string, agent AgentInfo, actions Actions) *Server {
	uiCfg, _ := fullCfg["ui"].(config.Map)
	if uiCfg == nil {
		uiCfg = config.Map{}
		fullCfg["ui"] = uiCfg
	}
	if _, ok := uiCfg["web"]; !ok {
		uiCfg["web"] = config.Map{"enabled": true}
	}
	return New(fullCfg, "test-unit", agent, nil, nil, actions, fullCfg, cfgPath)
}

func getCSRFCookie(t *testing.T, mux http.Handler) *http.Cookie {
	t.Helper()
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, c := range rr.Result().Cookies() {
		if c.Name == csrfCookieName {
			return c
		}
	}
	t.Fatal("expected a csrf cookie from the index page")
	return nil
}

// TestWebcfgGetConfigReturnsRealLiveConfig proves GET /plugins/webcfg/get-config
// serves the real, live s.cfg as JSON — the same shared config.Map value
// the rest of the daemon (agent/view) was constructed with — not a stale
// snapshot or a reconstruction.
func TestWebcfgGetConfigReturnsRealLiveConfig(t *testing.T) {
	fullCfg := config.Map{
		"main": config.Map{"name": "pwnagotchi-test", "lang": "en"},
	}
	s := newTestServerWithFullCfg(fullCfg, "", fakeAgentInfo{}, &fakeActions{})
	mux := newMux(s)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/plugins/webcfg/get-config", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "pwnagotchi-test") {
		t.Fatalf("expected the real live config in the response, got %s", rr.Body.String())
	}
}

// TestWebcfgSaveConfigWritesFileAndRestartsInCurrentMode proves the
// save-config route (a) requires a valid CSRF token, (b) writes the
// POSTED config verbatim to the real on-disk cfgPath (a real file,
// verified by re-reading it), and (c) triggers a real restart in the
// agent's real current mode — matching real webcfg.py's save-config
// exactly (full overwrite + restart, no in-memory merge since the
// restart re-reads fresh from disk).
func TestWebcfgSaveConfigWritesFileAndRestartsInCurrentMode(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	actions := &fakeActions{}
	fullCfg := config.Map{"main": config.Map{"name": "old"}}
	s := newTestServerWithFullCfg(fullCfg, cfgPath, fakeAgentInfo{mode: "auto"}, actions)
	mux := newMux(s)

	csrf := getCSRFCookie(t, mux)

	// Real nested JSON, matching what the browser's client-side
	// unFlattenJson() already converts the editable table's dotted keys
	// into before the fetch — the server (Python's real webcfg.py and
	// this Go port alike) only ever receives already-nested JSON.
	body := `{"main":{"name":"new-name","lang":"en"}}`
	req := httptest.NewRequest(http.MethodPost, "/plugins/webcfg/save-config?csrf_token="+csrf.Value, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(csrf)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "success" {
		t.Fatalf("expected \"success\" body, got %q", rr.Body.String())
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("expected a real config file written to %s: %v", cfgPath, err)
	}
	if !strings.Contains(string(data), "new-name") {
		t.Fatalf("expected the posted config content on disk, got:\n%s", data)
	}

	// Restart runs in a goroutine — poll briefly for it, matching this
	// package's other async-action tests.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if called, mode := actions.restartWasCalled(); called {
			if mode != "AUTO" {
				t.Fatalf("expected restart in the agent's real current mode AUTO, got %q", mode)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expected a real Restart call after save-config, none happened")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestWebcfgSaveConfigRejectsMissingCSRF proves save-config never writes
// or restarts without a valid CSRF token.
func TestWebcfgSaveConfigRejectsMissingCSRF(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	actions := &fakeActions{}
	s := newTestServerWithFullCfg(config.Map{"main": config.Map{}}, cfgPath, fakeAgentInfo{}, actions)
	mux := newMux(s)

	req := httptest.NewRequest(http.MethodPost, "/plugins/webcfg/save-config", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without csrf, got %d", rr.Code)
	}
	if _, err := os.Stat(cfgPath); err == nil {
		t.Fatal("must not write the config file without a valid csrf token")
	}
	if called, _ := actions.restartWasCalled(); called {
		t.Fatal("must not restart without a valid csrf token")
	}
}

// TestWebcfgMergeSaveConfigUpdatesLiveConfigWithoutRestart is the core
// behavioral proof for why this plugin was reimplemented natively: a real
// merge-save-config call (a) does NOT restart, (b) DOES persist to disk,
// and (c) makes the change visible immediately through the SAME s.cfg
// reference the rest of the daemon holds — proven here by reading
// fullCfg (the exact map instance passed to New, standing in for
// agent/view's shared reference in cmd/pwnagotchi/main.go) directly,
// without going through any webcfg API at all.
func TestWebcfgMergeSaveConfigUpdatesLiveConfigWithoutRestart(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	actions := &fakeActions{}
	fullCfg := config.Map{
		"main": config.Map{"name": "old-name", "lang": "en"},
		"ui":   config.Map{"web": config.Map{"enabled": true}},
	}
	s := New(fullCfg, "test-unit", fakeAgentInfo{}, nil, nil, actions, fullCfg, cfgPath)
	mux := newMux(s)
	csrf := getCSRFCookie(t, mux)

	body := `{"main":{"name":"merged-name"}}`
	req := httptest.NewRequest(http.MethodPost, "/plugins/webcfg/merge-save-config?csrf_token="+csrf.Value, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(csrf)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	// (a) no restart
	time.Sleep(50 * time.Millisecond)
	if called, _ := actions.restartWasCalled(); called {
		t.Fatal("merge-save-config must NOT restart, unlike save-config")
	}

	// (b) persisted to disk
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("expected a real config file written to %s: %v", cfgPath, err)
	}
	if !strings.Contains(string(data), "merged-name") {
		t.Fatalf("expected merged content on disk, got:\n%s", data)
	}

	// (c) visible immediately through the SAME map reference fullCfg is —
	// this is the actual live-update guarantee: no polling, no restart,
	// no re-read from disk needed, because it's the identical map object
	// agent.New/view.New would have been handed in cmd/pwnagotchi/main.go.
	mainCfg, _ := fullCfg["main"].(config.Map)
	if mainCfg == nil || mainCfg["name"] != "merged-name" {
		t.Fatalf("expected the original fullCfg map's own contents to be updated in place, got main=%v", mainCfg)
	}
	// The untouched key (lang) must still be present — merge, not replace.
	if mainCfg["lang"] != "en" {
		t.Fatalf("expected merge to preserve keys not present in the posted body, got main=%v", mainCfg)
	}
}

// TestWebcfgSaveConfigPreservesIntegerFormatting is a regression test for
// a real formatting divergence a naive JSON decode would introduce: Go's
// encoding/json decodes every JSON number as float64 by default, which
// BurntSushi/toml then re-encodes with a decimal point (e.g. "8080"
// becoming "8080.0") even for whole numbers — diverging from both the
// original file's own int formatting and Python's json.loads, which
// preserves the int/float distinction from the JSON literal's own
// syntax. decodeConfigJSON's UseNumber+normalizeJSONNumbers must keep
// whole numbers as real TOML integers.
func TestWebcfgSaveConfigPreservesIntegerFormatting(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	s := newTestServerWithFullCfg(config.Map{"main": config.Map{}}, cfgPath, fakeAgentInfo{}, &fakeActions{})
	mux := newMux(s)
	csrf := getCSRFCookie(t, mux)

	body := `{"ui.web.port":8080,"ui.fps":1.5}`
	req := httptest.NewRequest(http.MethodPost, "/plugins/webcfg/save-config?csrf_token="+csrf.Value, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(csrf)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading %s: %v", cfgPath, err)
	}
	content := string(data)
	if strings.Contains(content, "8080.0") {
		t.Fatalf("whole-number field was written as a float (8080.0), expected integer 8080; file:\n%s", content)
	}
	if !strings.Contains(content, "8080") {
		t.Fatalf("expected the integer value 8080 somewhere in the file:\n%s", content)
	}
	if !strings.Contains(content, "1.5") {
		t.Fatalf("expected the real float value 1.5 preserved, file:\n%s", content)
	}
}

// TestWebcfgIndexRendersRealPage proves the index page renders through
// the real template (not a bridge webhook), including a real csrf token
// available to its inline script for the JSON POST routes.
func TestWebcfgIndexRendersRealPage(t *testing.T) {
	s := newTestServerWithFullCfg(config.Map{"main": config.Map{}}, "", fakeAgentInfo{}, &fakeActions{})
	mux := newMux(s)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/plugins/webcfg", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Configuration Manager") {
		t.Fatalf("expected the real webcfg page content, got %s", body)
	}
	if !strings.Contains(body, "var csrfToken =") {
		t.Fatalf("expected a real csrf token embedded for the page's JSON POST calls, got %s", body)
	}
}
