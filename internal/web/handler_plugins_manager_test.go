package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// fakeNativePlugin is a minimal pluginmanager.Plugin + WebhookHandler used
// to prove internal/web routes a manager-known plugin natively — no bridge
// involved at all — and that the /plugins listing/toggle surfaces its real
// state.
type fakeNativePlugin struct {
	name      string
	meta      pluginmanager.Metadata
	calls     int
	unloadErr error
}

func (p *fakeNativePlugin) Name() string                     { return p.name }
func (p *fakeNativePlugin) Metadata() pluginmanager.Metadata { return p.meta }
func (p *fakeNativePlugin) OnUnload() error                  { return p.unloadErr }
func (p *fakeNativePlugin) OnWebhook(subpath string, r *http.Request) (pluginmanager.WebhookResponse, error) {
	p.calls++
	return pluginmanager.WebhookResponse{Status: http.StatusOK, Body: []byte("native:" + subpath)}, nil
}

func newTestServerWithPluginMgr(mgr *pluginmanager.Manager) (*Server, config.Map) {
	cfg := config.Map{"ui": config.Map{"web": config.Map{"enabled": true}}}
	s := New(cfg, "test-unit", fakeAgentInfo{}, nil, mgr, &fakeActions{}, cfg, "")
	return s, cfg
}

func TestPluginsIndexListsNativeManagerPlugins(t *testing.T) {
	mgr := pluginmanager.New(pluginmanager.Options{})
	p := &fakeNativePlugin{name: "native-one", meta: pluginmanager.Metadata{Version: "1.0", Description: "a native plugin"}}
	if err := mgr.Register(p); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Load(p.name, nil, nil); err != nil {
		t.Fatal(err)
	}

	s, _ := newTestServerWithPluginMgr(mgr)
	rr := httptest.NewRecorder()
	newMux(s).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/plugins", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "native-one") {
		t.Fatalf("expected native plugin name in rendered page, got: %s", rr.Body.String())
	}
}

func TestPluginWebhookRoutesToNativeManagerNotBridge(t *testing.T) {
	mgr := pluginmanager.New(pluginmanager.Options{})
	p := &fakeNativePlugin{name: "native-hook"}
	_ = mgr.Register(p)
	_ = mgr.Load(p.name, nil, nil)

	s, _ := newTestServerWithPluginMgr(mgr)
	// s.bridge is nil: if this request fell through to the bridge path it
	// would 503, not 200 — a 200 here proves the manager path was taken.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/plugins/native-hook/sub", nil)
	newMux(s).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 from native webhook route, got %d: %s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); body != "native:sub" {
		t.Fatalf("expected native webhook body, got %q", body)
	}
}

func TestPluginWebhookPostRequiresHeaderOrFormCSRF(t *testing.T) {
	mgr := pluginmanager.New(pluginmanager.Options{})
	p := &fakeNativePlugin{name: "native-hook"}
	_ = mgr.Register(p)
	_ = mgr.Load(p.name, nil, nil)
	s, _ := newTestServerWithPluginMgr(mgr)
	mux := newMux(s)

	req := httptest.NewRequest(http.MethodPost, "/plugins/native-hook/change", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden || p.calls != 0 {
		t.Fatalf("POST without CSRF: status=%d calls=%d", rr.Code, p.calls)
	}

	cookie := getCSRFCookie(t, mux)
	req = httptest.NewRequest(http.MethodPost, "/plugins/native-hook/change", strings.NewReader(`{}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRFToken", cookie.Value)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || p.calls != 1 {
		t.Fatalf("POST with CSRF header: status=%d calls=%d body=%s", rr.Code, p.calls, rr.Body.String())
	}
}

func TestPluginToggleUsesManagerWhenPluginIsNative(t *testing.T) {
	mgr := pluginmanager.New(pluginmanager.Options{})
	p := &fakeNativePlugin{name: "native-toggle"}
	_ = mgr.Register(p)
	// Deliberately not loaded yet: the toggle POST below must load it via
	// the manager (proving the manager path fired), not error out because
	// there's no bridge.

	s, cfg := newTestServerWithPluginMgr(mgr)
	mux := newMux(s)
	cookie := getCSRFCookie(t, mux)

	form := url.Values{
		"plugin":     {"native-toggle"},
		"enabled":    {"on"},
		"csrf_token": {cookie.Value},
	}
	req := httptest.NewRequest(http.MethodPost, "/plugins/toggle", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	body, _ := io.ReadAll(rr.Body)
	if string(body) != "success" {
		t.Fatalf("expected success, got %d: %s", rr.Code, body)
	}

	found := false
	for _, st := range mgr.List() {
		if st.Name == "native-toggle" {
			found = true
			if !st.Enabled {
				t.Fatal("expected native-toggle to be enabled via the manager after toggle")
			}
		}
	}
	if !found {
		t.Fatal("expected native-toggle to be registered")
	}

	mainCfg, _ := cfg["main"].(config.Map)
	pluginsCfg, _ := mainCfg["plugins"].(config.Map)
	entry, _ := pluginsCfg["native-toggle"].(config.Map)
	if entry == nil || entry["enabled"] != true {
		t.Fatalf("expected on-disk-style config entry to record enabled=true, got %v", entry)
	}
}

func TestPluginTogglePersistsChangedStateWhenUnloadCleanupFails(t *testing.T) {
	mgr := pluginmanager.New(pluginmanager.Options{})
	p := &fakeNativePlugin{name: "native-toggle", unloadErr: errors.New("cleanup failed")}
	if err := mgr.Register(p); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Load(p.name, nil, nil); err != nil {
		t.Fatal(err)
	}

	s, cfg := newTestServerWithPluginMgr(mgr)
	s.cfgPath = filepath.Join(t.TempDir(), "config.toml")
	pluginConfigEntry(cfg, p.name)["enabled"] = true
	mux := newMux(s)
	cookie := getCSRFCookie(t, mux)

	form := url.Values{
		"plugin":     {p.name},
		"csrf_token": {cookie.Value},
	}
	req := httptest.NewRequest(http.MethodPost, "/plugins/toggle", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
	entry := pluginConfigEntry(cfg, p.name)
	if entry["enabled"] != false {
		t.Fatalf("live enabled state = %v, want false", entry["enabled"])
	}
	loaded, err := config.LoadTOMLFileForEdit(s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	savedMain, _ := loaded["main"].(config.Map)
	savedPlugins, _ := savedMain["plugins"].(config.Map)
	savedEntry, _ := savedPlugins[p.name].(config.Map)
	if savedEntry["enabled"] != false {
		t.Fatalf("saved enabled state = %v, want false", savedEntry["enabled"])
	}
	for _, status := range mgr.List() {
		if status.Name == p.name && status.Enabled {
			t.Fatal("plugin remained enabled after unload")
		}
	}
}

func TestPluginToggleUnknownPluginDoesNotMutateConfig(t *testing.T) {
	mgr := pluginmanager.New(pluginmanager.Options{})
	s, cfg := newTestServerWithPluginMgr(mgr)
	mux := newMux(s)
	cookie := getCSRFCookie(t, mux)

	form := url.Values{
		"plugin":     {"unknown"},
		"enabled":    {"on"},
		"csrf_token": {cookie.Value},
	}
	req := httptest.NewRequest(http.MethodPost, "/plugins/toggle", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
	if _, ok := cfg["main"]; ok {
		t.Fatalf("unknown plugin mutated config: %v", cfg)
	}
}

func TestStagePluginEnabledRollbackRestoresMalformedConfig(t *testing.T) {
	cases := []config.Map{
		{"main": "invalid"},
		{"main": config.Map{"plugins": "invalid"}},
		{"main": config.Map{"plugins": config.Map{"native-toggle": "invalid"}}},
	}
	for _, cfg := range cases {
		want := cloneConfigMap(t, cfg)
		_, rollback := stagePluginEnabled(cfg, "native-toggle", true)
		rollback()
		if !reflect.DeepEqual(cfg, want) {
			t.Fatalf("rollback config = %#v, want %#v", cfg, want)
		}
	}
}

func cloneConfigMap(t *testing.T, cfg config.Map) config.Map {
	t.Helper()
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var cloned config.Map
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}
