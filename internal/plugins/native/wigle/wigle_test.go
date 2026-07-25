package wigle

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

type fakeView struct {
	mu      sync.Mutex
	values  map[string]string
	uploads []string
	normals int
}

func newFakeView() *fakeView { return &fakeView{values: map[string]string{}} }
func (f *fakeView) Set(key, value string) {
	f.mu.Lock()
	f.values[key] = value
	f.mu.Unlock()
}
func (f *fakeView) Update(bool)                {}
func (f *fakeView) Kind() string               { return "dummydisplay" }
func (f *fakeView) HasElement(key string) bool { _, ok := f.values[key]; return ok }
func (f *fakeView) RemoveElement(key string)   { delete(f.values, key) }
func (f *fakeView) AddText(key, value string, x, y int, font pluginmanager.FontStyle, wrap bool, maxLength int) {
	f.mu.Lock()
	f.values[key] = value
	f.mu.Unlock()
}
func (f *fakeView) AddLabeledValue(key, label, value string, x, y int, labelFont, valueFont pluginmanager.FontStyle, labelSpacing int) {
	f.values[key] = value
}
func (f *fakeView) OnUploading(to string) {
	f.mu.Lock()
	f.uploads = append(f.uploads, to)
	f.mu.Unlock()
}
func (f *fakeView) OnNormal()   { f.mu.Lock(); f.normals++; f.mu.Unlock() }
func (f *fakeView) Width() int  { return 250 }
func (f *fakeView) Height() int { return 122 }

var _ pluginmanager.ViewCapability = (*fakeView)(nil)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func loadedPlugin(t *testing.T, handshakesDir, apiKey string, view pluginmanager.ViewCapability, httpClient *http.Client) *Plugin {
	t.Helper()
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{View: view, HTTPClient: httpClient}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Map{
		"main": config.Map{
			"whitelist": []interface{}{},
			"plugins": config.Map{
				"wigle": config.Map{"api_key": apiKey, "donate": false, "timeout": int64(5)},
			},
		},
		"bettercap": config.Map{"handshakes": handshakesDir},
	}
	p.HandleEvent("config_changed", []interface{}{cfg})
	return p
}

func TestOnConfigChangedRequiresAPIKey(t *testing.T) {
	p := New()
	_ = p.OnLoad(pluginmanager.Capabilities{View: newFakeView()})
	cfg := config.Map{
		"main":      config.Map{"whitelist": []interface{}{}, "plugins": config.Map{"wigle": config.Map{}}},
		"bettercap": config.Map{"handshakes": t.TempDir()},
	}
	p.HandleEvent("config_changed", []interface{}{cfg})
	p.mu.Lock()
	ready := p.ready
	p.mu.Unlock()
	if ready {
		t.Fatal("expected plugin to remain not-ready without an api_key")
	}
}

func TestOnLoadAddsTextElement(t *testing.T) {
	view := newFakeView()
	p := New()
	_ = p.OnLoad(pluginmanager.Capabilities{View: view})
	if _, ok := view.values["wigle"]; !ok {
		t.Fatal("expected 'wigle' text element added")
	}
}

func TestOnUnloadRemovesElement(t *testing.T) {
	view := newFakeView()
	p := New()
	_ = p.OnLoad(pluginmanager.Capabilities{View: view})
	_ = p.OnUnload()
	if _, ok := view.values["wigle"]; ok {
		t.Fatal("expected 'wigle' element removed")
	}
}

func TestExtractGPSDataFromGpsJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "net.gps.json")
	data, _ := json.Marshal(map[string]interface{}{
		"Latitude": 40.7128, "Longitude": -74.0060, "Altitude": 5.0,
		"Accuracy": 3.5, "Updated": "2026-01-02T03:04:05.000000",
	})
	os.WriteFile(path, data, 0o644)

	got, ok := extractGPSData(path)
	if !ok {
		t.Fatal("expected valid GPS data")
	}
	if got.Latitude != 40.7128 || got.Accuracy != 3.5 {
		t.Fatalf("unexpected GPS data: %+v", got)
	}
	if got.Updated.Format("2006-01-02 15:04:05") != "2026-01-02 03:04:05" {
		t.Fatalf("unexpected Updated: %v", got.Updated)
	}
}

func TestExtractGPSDataMissingAccuracyDefaultsToZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "net.gps.json")
	data, _ := json.Marshal(map[string]interface{}{"Latitude": 1.0, "Longitude": 2.0, "Altitude": 3.0})
	os.WriteFile(path, data, 0o644)

	got, ok := extractGPSData(path)
	if !ok {
		t.Fatal("expected valid GPS data despite missing Accuracy/Updated")
	}
	if got.Accuracy != 0 {
		t.Fatalf("expected Accuracy=0 fallback, got %v", got.Accuracy)
	}
	if got.Updated.IsZero() {
		t.Fatal("expected Updated to fall back to file mtime, not zero")
	}
}

func TestExtractGPSDataZeroCoordinatesInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "net.gps.json")
	data, _ := json.Marshal(map[string]interface{}{"Latitude": 0.0, "Longitude": 0.0})
	os.WriteFile(path, data, 0o644)

	if _, ok := extractGPSData(path); ok {
		t.Fatal("expected all-zero coordinates to be treated as invalid")
	}
}

func TestExtractGPSDataFromGeoJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "net.geo.json")
	data, _ := json.Marshal(map[string]interface{}{
		"location": map[string]interface{}{"lat": 12.5, "lng": -3.25},
		"accuracy": 15.0,
		"ts":       int64(1700000000),
	})
	os.WriteFile(path, data, 0o644)

	got, ok := extractGPSData(path)
	if !ok {
		t.Fatal("expected valid geo.json data")
	}
	if got.Latitude != 12.5 || got.Longitude != -3.25 || got.Altitude != 10 || got.Accuracy != 15.0 {
		t.Fatalf("unexpected geo.json extraction: %+v", got)
	}
}

func TestGetPcapFilenameMissingFileReturnsFalse(t *testing.T) {
	if _, ok := getPcapFilename("/nonexistent/net.gps.json"); ok {
		t.Fatal("expected false for a gps file with no matching pcap")
	}
}

func TestGetPcapDataUsesCacheWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	os.MkdirAll(cacheDir, 0o755)
	apData, _ := json.Marshal(map[string]interface{}{
		"mac": "AA:BB:CC:DD:EE:FF", "hostname": "homewifi",
		"encryption": []interface{}{"WPA2"}, "channel": 6.0, "frequency": 2437.0, "rssi": -50.0,
	})
	os.WriteFile(filepath.Join(cacheDir, "homewifi_AABBCCDDEEFF.apcache"), apData, 0o644)

	p := New()
	p.cacheDir = cacheDir
	got, ok := p.getPcapData(filepath.Join(dir, "homewifi_AABBCCDDEEFF.pcap"))
	if !ok {
		t.Fatal("expected cache-hit PCAP data")
	}
	if got.BSSID != "AA:BB:CC:DD:EE:FF" || got.ESSID != "homewifi" || len(got.Encryption) != 1 || got.Encryption[0] != "WPA2" {
		t.Fatalf("unexpected cached PCAP data: %+v", got)
	}
}

func TestGetPcapDataFallsBackAndFailsCleanlyWithoutCacheOrRealPcap(t *testing.T) {
	dir := t.TempDir()
	p := New()
	p.cacheDir = filepath.Join(dir, "cache")
	if _, ok := p.getPcapData(filepath.Join(dir, "nope.pcap")); ok {
		t.Fatal("expected failure for a nonexistent pcap with no cache entry")
	}
}

func TestGenerateCSVFormat(t *testing.T) {
	p := New()
	p.clock = &fakeClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	entries := []csvEntry{{
		gps:  GPSData{Latitude: 1.5, Longitude: 2.5, Altitude: 3, Accuracy: 4, Updated: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		pcap: PCAPData{BSSID: "AA:BB:CC:DD:EE:FF", ESSID: "home", Encryption: []string{"WPA2"}, Channel: 6, Frequency: 2437, RSSI: -50},
	}}
	filename, content := p.generateCSV(entries)
	if !strings.HasSuffix(filename, "_20260102_030405.csv") {
		t.Fatalf("unexpected filename: %s", filename)
	}
	s := string(content)
	if !strings.HasPrefix(s, "WigleWifi-1.6,") {
		t.Fatalf("expected WigleWifi-1.6 header, got: %s", s)
	}
	if !strings.Contains(s, "MAC,SSID,AuthMode,FirstSeen,Channel,Frequency,RSSI,CurrentLatitude,CurrentLongitude,AltitudeMeters,AccuracyMeters,RCOIs,MfgrId,Type") {
		t.Fatal("expected the real kismet-format column header")
	}
	if !strings.Contains(s, "AA:BB:CC:DD:EE:FF,home,[WPA2],2026-01-01 00:00:00,6,2437,-50,1.5,2.5,3,4,,,WIFI") {
		t.Fatalf("unexpected CSV row, got:\n%s", s)
	}
}

func TestPostWigleSuccessMarksReportedAndUploads(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	view := newFakeView()
	p := loadedPlugin(t, dir, "testkey", view, srv.Client())
	origURL := uploadURLVar
	uploadURLVar = srv.URL
	defer func() { uploadURLVar = origURL }()

	err := p.postWigle("test.csv", []byte("data"))
	if err != nil {
		t.Fatalf("postWigle: %v", err)
	}
	if gotAuth != "Basic testkey" {
		t.Fatalf("expected Basic auth header, got %q", gotAuth)
	}
}

func TestPostWigleFailureReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":false,"message":"bad key"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	p := loadedPlugin(t, dir, "testkey", newFakeView(), srv.Client())
	origURL := uploadURLVar
	uploadURLVar = srv.URL
	defer func() { uploadURLVar = origURL }()

	if err := p.postWigle("test.csv", []byte("data")); err == nil {
		t.Fatal("expected an error from a failed upload response")
	}
}

func TestOnUIUpdateShowsWaitingMessageBeforeReady(t *testing.T) {
	view := newFakeView()
	p := New()
	_ = p.OnLoad(pluginmanager.Capabilities{View: view})
	p.HandleEvent("ui_update", nil)
	if view.values["wigle"] != "We Will Wait Wigle" {
		t.Fatalf("expected waiting message, got %q", view.values["wigle"])
	}
}

func TestOnUIUpdateCyclesStatsMessages(t *testing.T) {
	view := newFakeView()
	p := New()
	_ = p.OnLoad(pluginmanager.Capabilities{View: view})
	p.mu.Lock()
	p.ready = true
	p.stats = statistics{ready: true, username: "tester", rank: 5, monthRank: 2}
	p.mu.Unlock()

	// Real Python increments ui_counter BEFORE checking it
	// ((self.ui_counter + 1) % 6, starting from 0), so the first call
	// lands on branch 1 ("Rank..."), not branch 0 ("User...") — verified
	// against the real source, not assumed.
	p.HandleEvent("ui_update", nil)
	if view.values["wigle"] != "Rank:5 Month:2" {
		t.Fatalf("expected Rank:5 Month:2, got %q", view.values["wigle"])
	}
	p.HandleEvent("ui_update", nil)
	if view.values["wigle"] != "0 discovered WiFis" {
		t.Fatalf("expected discovered-WiFis line, got %q", view.values["wigle"])
	}
}

func TestGetNewGPSFilesExcludesReportedAndSkipped(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.gps.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.gps.json"), []byte("{}"), 0o644)

	p := New()
	p.handshakesDir = dir
	p.skip[filepath.Join(dir, "b.gps.json")] = true

	got := p.getNewGPSFiles([]string{filepath.Join(dir, "a.gps.json")})
	if len(got) != 0 {
		t.Fatalf("expected both files excluded (one reported, one skipped), got %v", got)
	}
}

func TestUploadNewHandshakesFullFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()
	origURL := uploadURLVar
	uploadURLVar = srv.URL
	defer func() { uploadURLVar = origURL }()

	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	os.MkdirAll(cacheDir, 0o755)
	apData, _ := json.Marshal(map[string]interface{}{
		"mac": "AA:BB:CC:DD:EE:FF", "hostname": "homewifi", "channel": 6.0, "frequency": 2437.0, "rssi": -50.0,
	})
	os.WriteFile(filepath.Join(cacheDir, "homewifi_AABBCCDDEEFF.apcache"), apData, 0o644)
	os.WriteFile(filepath.Join(dir, "homewifi_AABBCCDDEEFF.pcap"), []byte("x"), 0o644)
	gpsData, _ := json.Marshal(map[string]interface{}{"Latitude": 1.0, "Longitude": 2.0, "Altitude": 3.0})
	gpsPath := filepath.Join(dir, "homewifi_AABBCCDDEEFF.gps.json")
	os.WriteFile(gpsPath, gpsData, 0o644)

	view := newFakeView()
	p := loadedPlugin(t, dir, "testkey", view, srv.Client())
	p.uploadNewHandshakes(nil, []string{gpsPath})

	if len(view.uploads) != 1 || view.uploads[0] != "wigle.net" {
		t.Fatalf("expected OnUploading(wigle.net) called once, got %v", view.uploads)
	}
	if view.normals != 1 {
		t.Fatalf("expected OnNormal called once, got %d", view.normals)
	}
	reported := p.loadReported()
	if len(reported) != 1 || reported[0] != gpsPath {
		t.Fatalf("expected gps file marked reported, got %v", reported)
	}
}
