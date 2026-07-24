package webgpsmap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

type fakeWeb struct {
	routes map[string]http.HandlerFunc
}

func newFakeWeb() *fakeWeb { return &fakeWeb{routes: map[string]http.HandlerFunc{}} }

func (f *fakeWeb) Handle(subpath string, h http.HandlerFunc) { f.routes[subpath] = h }
func (f *fakeWeb) Render(w http.ResponseWriter, r *http.Request, template string, data map[string]interface{}) {
}

var _ pluginmanager.WebCapability = (*fakeWeb)(nil)

func newLoadedPlugin(t *testing.T, handshakesDir string) (*Plugin, *fakeWeb) {
	t.Helper()
	p := New()
	web := newFakeWeb()
	if err := p.OnLoad(pluginmanager.Capabilities{}); err != nil {
		t.Fatal(err)
	}
	p.RegisterRoutes(web)
	p.HandleEvent("config_changed", []interface{}{config.Map{
		"bettercap": config.Map{"handshakes": handshakesDir},
	}})
	return p, web
}

func TestRegisterRoutesRegistersIndexAllAndOfflinemap(t *testing.T) {
	_, web := newLoadedPlugin(t, t.TempDir())
	for _, want := range []string{"", "all", "offlinemap"} {
		if _, ok := web.routes[want]; !ok {
			t.Fatalf("expected route %q registered, got %v", want, web.routes)
		}
	}
}

func TestNotReadyBeforeConfigChanged(t *testing.T) {
	p := New()
	web := newFakeWeb()
	_ = p.OnLoad(pluginmanager.Capabilities{})
	p.RegisterRoutes(web)

	rr := httptest.NewRecorder()
	web.routes[""].ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/plugins/webgpsmap/", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 before config_changed, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Not ready yet") {
		t.Fatalf("expected not-ready body, got %s", rr.Body.String())
	}
}

func TestIndexServesRealEmbeddedHTML(t *testing.T) {
	_, web := newLoadedPlugin(t, t.TempDir())
	rr := httptest.NewRecorder()
	web.routes[""].ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/plugins/webgpsmap/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "offlinePositions") {
		t.Fatalf("expected the real map HTML (with its offlinePositions JS var) to be served, got %d bytes", rr.Body.Len())
	}
}

func writeHandshake(t *testing.T, dir, base string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, base+".pcap"), []byte("pcap"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAllReturnsPositionsFromGPSJSON(t *testing.T) {
	dir := t.TempDir()
	writeHandshake(t, dir, "MyNetwork_aabbccddeeff")
	gpsData := map[string]interface{}{"Latitude": 40.7128, "Longitude": -74.0060}
	data, _ := json.Marshal(gpsData)
	os.WriteFile(filepath.Join(dir, "MyNetwork_aabbccddeeff.gps.json"), data, 0o644)

	_, web := newLoadedPlugin(t, dir)
	rr := httptest.NewRecorder()
	web.routes["all"].ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/plugins/webgpsmap/all", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var got map[string]position
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	entry, ok := got["MyNetwork_aabbccddeeff"]
	if !ok {
		t.Fatalf("expected key MyNetwork_aabbccddeeff, got %v", got)
	}
	if entry.MAC != "aabbccddeeff" || entry.SSID != "MyNetwork" || entry.Type != "gps" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	if entry.Lat != 40.7128 || entry.Lng != -74.0060 {
		t.Fatalf("unexpected coordinates: %+v", entry)
	}
	if entry.Accuracy == nil || *entry.Accuracy != 50.0 {
		t.Fatalf("expected default gps accuracy 50.0, got %v", entry.Accuracy)
	}
}

func TestAllPrefersGeoJSONOverGPSJSON(t *testing.T) {
	dir := t.TempDir()
	writeHandshake(t, dir, "Net_aabbccddeeff")
	gps, _ := json.Marshal(map[string]interface{}{"Latitude": 1.0, "Longitude": 1.0})
	os.WriteFile(filepath.Join(dir, "Net_aabbccddeeff.gps.json"), gps, 0o644)
	geo, _ := json.Marshal(map[string]interface{}{"location": map[string]interface{}{"lat": 2.0, "lng": 2.0}, "accuracy": 15.5})
	os.WriteFile(filepath.Join(dir, "Net_aabbccddeeff.geo.json"), geo, 0o644)

	_, web := newLoadedPlugin(t, dir)
	rr := httptest.NewRecorder()
	web.routes["all"].ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/plugins/webgpsmap/all", nil))
	var got map[string]position
	json.Unmarshal(rr.Body.Bytes(), &got)
	entry := got["Net_aabbccddeeff"]
	if entry.Type != "geo" || entry.Lat != 2.0 || entry.Lng != 2.0 {
		t.Fatalf("expected .geo.json to take precedence, got %+v", entry)
	}
	if entry.Accuracy == nil || *entry.Accuracy != 15.5 {
		t.Fatalf("expected geo accuracy 15.5, got %v", entry.Accuracy)
	}
}

func TestLocationFieldOverridesBareLatLng(t *testing.T) {
	dir := t.TempDir()
	writeHandshake(t, dir, "Net_112233445566")
	geo, _ := json.Marshal(map[string]interface{}{
		"lat": 1.0, "long": 1.0,
		"location": map[string]interface{}{"lat": 9.0, "lng": 9.0},
	})
	os.WriteFile(filepath.Join(dir, "Net_112233445566.geo.json"), geo, 0o644)

	_, web := newLoadedPlugin(t, dir)
	rr := httptest.NewRecorder()
	web.routes["all"].ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/plugins/webgpsmap/all", nil))
	var got map[string]position
	json.Unmarshal(rr.Body.Bytes(), &got)
	entry := got["Net_112233445566"]
	if entry.Lat != 9.0 || entry.Lng != 9.0 {
		t.Fatalf("expected location.lat/lng to override bare lat/long, got %+v", entry)
	}
}

func TestZeroCoordinatesAreSkipped(t *testing.T) {
	dir := t.TempDir()
	writeHandshake(t, dir, "Net_aaaaaaaaaaaa")
	gps, _ := json.Marshal(map[string]interface{}{"Latitude": 0.0, "Longitude": 0.0})
	os.WriteFile(filepath.Join(dir, "Net_aaaaaaaaaaaa.gps.json"), gps, 0o644)

	_, web := newLoadedPlugin(t, dir)
	rr := httptest.NewRecorder()
	web.routes["all"].ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/plugins/webgpsmap/all", nil))
	var got map[string]position
	json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got) != 0 {
		t.Fatalf("expected all-zero coordinates to be skipped, got %v", got)
	}
}

func TestMalformedJSONIsSkippedNotFatal(t *testing.T) {
	dir := t.TempDir()
	writeHandshake(t, dir, "Net_aaaaaaaaaaaa")
	os.WriteFile(filepath.Join(dir, "Net_aaaaaaaaaaaa.gps.json"), []byte("{not valid json"), 0o644)

	_, web := newLoadedPlugin(t, dir)
	rr := httptest.NewRecorder()
	web.routes["all"].ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/plugins/webgpsmap/all", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 even with a malformed sidecar file, got %d", rr.Code)
	}
	var got map[string]position
	json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got) != 0 {
		t.Fatalf("expected malformed file skipped, got %v", got)
	}
}

func TestCrackedPasswordIsAttached(t *testing.T) {
	dir := t.TempDir()
	writeHandshake(t, dir, "Net_aabbccddeeff")
	gps, _ := json.Marshal(map[string]interface{}{"Latitude": 5.0, "Longitude": 5.0})
	os.WriteFile(filepath.Join(dir, "Net_aabbccddeeff.gps.json"), gps, 0o644)
	os.WriteFile(filepath.Join(dir, "Net_aabbccddeeff.pcap.cracked"), []byte("hunter2"), 0o644)

	_, web := newLoadedPlugin(t, dir)
	rr := httptest.NewRecorder()
	web.routes["all"].ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/plugins/webgpsmap/all", nil))
	var got map[string]position
	json.Unmarshal(rr.Body.Bytes(), &got)
	entry := got["Net_aabbccddeeff"]
	if entry.Password == nil || *entry.Password != "hunter2" {
		t.Fatalf("expected cracked password attached, got %+v", entry)
	}
}

func TestOfflineMapEmbedsPositionsJSON(t *testing.T) {
	dir := t.TempDir()
	writeHandshake(t, dir, "Net_aabbccddeeff")
	gps, _ := json.Marshal(map[string]interface{}{"Latitude": 5.0, "Longitude": 5.0})
	os.WriteFile(filepath.Join(dir, "Net_aabbccddeeff.gps.json"), gps, 0o644)

	_, web := newLoadedPlugin(t, dir)
	rr := httptest.NewRecorder()
	web.routes["offlinemap"].ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/plugins/webgpsmap/offlinemap", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Disposition"); got != "attachment; filename=webgpsmap.html" {
		t.Fatalf("unexpected Content-Disposition: %q", got)
	}
	if strings.Contains(rr.Body.String(), "var offlinePositions = null;") {
		t.Fatal("expected the offlinePositions placeholder to be replaced")
	}
	if !strings.Contains(rr.Body.String(), "Net_aabbccddeeff") {
		t.Fatal("expected the embedded JSON to contain the real position data")
	}
}

func TestNoMacParsedFromFilenameIsSkipped(t *testing.T) {
	dir := t.TempDir()
	// Not matching the 12-hex-char MAC pattern at all.
	os.WriteFile(filepath.Join(dir, "weird.gps.json"), []byte(`{"Latitude":1.0,"Longitude":1.0}`), 0o644)
	writeHandshake(t, dir, "weird")

	_, web := newLoadedPlugin(t, dir)
	rr := httptest.NewRecorder()
	web.routes["all"].ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/plugins/webgpsmap/all", nil))
	var got map[string]position
	json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got) != 0 {
		t.Fatalf("expected file with unparseable mac to be skipped, got %v", got)
	}
}

// TestOnWebhookServesRealHTTPRoundTrip proves the plugin is reachable
// through pluginmanager.WebhookHandler — the integration internal/web's
// pluginWebhookNative actually calls in production today, unlike
// RouteRegistrar/WebCapability, which nothing in cmd/pwnagotchi/main.go
// wires to a real server yet.
func TestOnWebhookServesRealHTTPRoundTrip(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Net_aabbccddeeff.gps.json"), []byte(`{"Latitude":40.7128,"Longitude":-74.0060}`), 0o644)
	writeHandshake(t, dir, "Net_aabbccddeeff")

	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{}); err != nil {
		t.Fatal(err)
	}
	p.HandleEvent("config_changed", []interface{}{config.Map{"bettercap": config.Map{"handshakes": dir}}})

	resp, err := p.OnWebhook("all", httptest.NewRequest(http.MethodGet, "/plugins/webgpsmap/all", nil))
	if err != nil {
		t.Fatalf("OnWebhook: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Status, resp.Body)
	}
	var got map[string]position
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := got["Net_aabbccddeeff"]; !ok {
		t.Fatalf("expected position keyed by ssid_mac, got %v", got)
	}

	indexResp, err := p.OnWebhook("", httptest.NewRequest(http.MethodGet, "/plugins/webgpsmap/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if indexResp.Status != http.StatusOK || !strings.Contains(string(indexResp.Body), "<html>") {
		t.Fatalf("expected the real map page HTML, got status=%d body-prefix=%q", indexResp.Status, string(indexResp.Body[:min(50, len(indexResp.Body))]))
	}
}

func TestOnWebhookNotReadyReturns500(t *testing.T) {
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{}); err != nil {
		t.Fatal(err)
	}
	resp, err := p.OnWebhook("all", httptest.NewRequest(http.MethodGet, "/plugins/webgpsmap/all", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusInternalServerError {
		t.Fatalf("expected 500 before config_changed, got %d", resp.Status)
	}
}

func TestOnWebhookUnknownSubpathReturns404(t *testing.T) {
	_, _ = newLoadedPlugin(t, t.TempDir())
	p := New()
	_ = p.OnLoad(pluginmanager.Capabilities{})
	p.HandleEvent("config_changed", []interface{}{config.Map{"bettercap": config.Map{"handshakes": t.TempDir()}}})
	resp, err := p.OnWebhook("nonsense", httptest.NewRequest(http.MethodGet, "/plugins/webgpsmap/nonsense", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown subpath, got %d", resp.Status)
	}
}

func TestNoHandshakesDirReturnsEmptyNotError(t *testing.T) {
	p := New()
	web := newFakeWeb()
	_ = p.OnLoad(pluginmanager.Capabilities{})
	p.RegisterRoutes(web)
	// config_changed with an empty handshakes dir leaves the plugin
	// explicitly not-ready (mirrors requiring a real bettercap.handshakes
	// path before this plugin can do anything).
	p.HandleEvent("config_changed", []interface{}{config.Map{"bettercap": config.Map{"handshakes": ""}}})

	rr := httptest.NewRecorder()
	web.routes["all"].ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/plugins/webgpsmap/all", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected not-ready 500 with no handshakes dir configured, got %d", rr.Code)
	}
}
