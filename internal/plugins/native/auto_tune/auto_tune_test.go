package autotune

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

type recordedRun struct{ cmd string }

type fakeAgent struct {
	mu       sync.Mutex
	runs     []recordedRun
	channels []int
	reset    bool
}

func (f *fakeAgent) Run(cmd string, verbose bool) (interface{}, error) {
	f.mu.Lock()
	f.runs = append(f.runs, recordedRun{cmd})
	f.mu.Unlock()
	return nil, nil
}
func (f *fakeAgent) Session(string) (interface{}, error) { return nil, nil }
func (f *fakeAgent) IsModuleRunning(string) bool         { return false }
func (f *fakeAgent) StartModule(string)                  {}
func (f *fakeAgent) RestartModule(string)                {}
func (f *fakeAgent) SupportedChannels() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.channels...)
}
func (f *fakeAgent) ResetHistory() {
	f.mu.Lock()
	f.reset = true
	f.mu.Unlock()
}
func (f *fakeAgent) snapshot() []recordedRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRun(nil), f.runs...)
}

var _ pluginmanager.AgentCapability = (*fakeAgent)(nil)

func ap(hostname, mac string, channel int) map[string]interface{} {
	return map[string]interface{}{"hostname": hostname, "mac": mac, "channel": channel, "rssi": -50}
}

func newTestPlugin(t *testing.T) (*Plugin, *fakeAgent) {
	t.Helper()
	p := New()
	p.presetsDir = t.TempDir()
	p.configPath = filepath.Join(t.TempDir(), "config.toml")
	agent := &fakeAgent{}
	if err := p.OnLoad(pluginmanager.Capabilities{Config: config.Map{}, Agent: agent}); err != nil {
		t.Fatal(err)
	}
	fullCfg := config.Map{"personality": config.Map{"channels": []interface{}{}, "max_interactions": int64(3)}}
	p.HandleEvent("config_changed", []interface{}{fullCfg})
	return p, agent
}

func TestIncrementChistoTracksStatAndAllActions(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.mu.Lock()
	p.incrementChistoLocked("Associations", 6, 1)
	p.incrementChistoLocked("Associations", 6, 1)
	p.incrementChistoLocked("Associations", 11, 1)
	p.incrementChistoLocked("Deauths", 6, -1) // simulate a "lost" style negative count
	p.mu.Unlock()

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.chistos["Associations"][6] != 2 {
		t.Fatalf("Associations[6] = %d, want 2", p.chistos["Associations"][6])
	}
	if p.chistos["Associations"][11] != 1 {
		t.Fatalf("Associations[11] = %d, want 1", p.chistos["Associations"][11])
	}
	if p.chistos["Associations"][-1] != 3 {
		t.Fatalf("Associations[-1] (total) = %d, want 3", p.chistos["Associations"][-1])
	}
	if p.chistos["Deauths"][6] != -1 {
		t.Fatalf("Deauths[6] = %d, want -1", p.chistos["Deauths"][6])
	}
	// _all_actions counts calls (unweighted), not the signed count value.
	if p.chistos["_all_actions"][6] != 3 { // 2 Associations + 1 Deauths call on channel 6
		t.Fatalf("_all_actions[6] = %d, want 3", p.chistos["_all_actions"][6])
	}
	if p.chistos["_all_actions"][-1] != 4 {
		t.Fatalf("_all_actions[-1] = %d, want 4", p.chistos["_all_actions"][-1])
	}
}

func TestMarkAPSeenFirstTimeCreatesRecordAndChistos(t *testing.T) {
	p, _ := newTestPlugin(t)
	a := ap("myrouter", "AA:BB:CC:DD:EE:FF", 6)

	p.mu.Lock()
	p.markAPSeenLocked(a, "")
	p.mu.Unlock()

	p.mu.Lock()
	defer p.mu.Unlock()
	id := apIdentity(a)
	rec, ok := p.knownAPs[id]
	if !ok {
		t.Fatal("expected AP record created")
	}
	if !rec.visible || rec.seen != 1 {
		t.Fatalf("unexpected record state: %+v", rec)
	}
	if p.chistos["Unique APs"][6] != 1 || p.chistos["Current APs"][6] != 1 {
		t.Fatalf("unexpected chistos: %+v", p.chistos)
	}
}

func TestMarkAPSeenAgainMergesAndIncrementsContext(t *testing.T) {
	p, _ := newTestPlugin(t)
	a := ap("myrouter", "AA:BB:CC:DD:EE:FF", 6)
	p.mu.Lock()
	p.markAPSeenLocked(a, "assoc")
	p.mu.Unlock()

	a2 := ap("myrouter", "AA:BB:CC:DD:EE:FF", 6)
	a2["rssi"] = -40
	p.mu.Lock()
	p.markAPSeenLocked(a2, "assoc")
	p.mu.Unlock()

	p.mu.Lock()
	defer p.mu.Unlock()
	rec := p.knownAPs[apIdentity(a)]
	if rec.assoc != 2 {
		t.Fatalf("expected assoc count 2 after two assoc-context sightings, got %d", rec.assoc)
	}
	if rec.fields["rssi"] != -40 {
		t.Fatalf("expected merged rssi -40, got %v", rec.fields["rssi"])
	}
	// Current APs should NOT double-increment since the AP stayed visible
	// the whole time (only increments on a not-visible -> visible edge).
	if p.chistos["Current APs"][6] != 1 {
		t.Fatalf("Current APs[6] = %d, want 1 (no re-increment while already visible)", p.chistos["Current APs"][6])
	}
}

func TestBcapWifiAPLostTransitionsVisibilityAndMissedCases(t *testing.T) {
	p, _ := newTestPlugin(t)
	a := ap("myrouter", "AA:BB:CC:DD:EE:FF", 6)

	// Case 1: AP never seen before -> "Missed joins".
	p.HandleEvent("bcap_wifi_ap_lost", []interface{}{nil, map[string]interface{}{"data": a}})
	p.mu.Lock()
	if p.chistos["Missed joins"][6] != 1 {
		t.Fatalf("expected Missed joins[6]=1, got %d", p.chistos["Missed joins"][6])
	}
	p.mu.Unlock()

	// Case 2: AP known and visible -> Current APs decremented.
	p.HandleEvent("bcap_wifi_ap_new", []interface{}{nil, map[string]interface{}{"data": a}})
	p.HandleEvent("bcap_wifi_ap_lost", []interface{}{nil, map[string]interface{}{"data": a}})
	p.mu.Lock()
	if p.chistos["Current APs"][6] != 0 {
		t.Fatalf("expected Current APs[6]=0 after new+lost, got %d", p.chistos["Current APs"][6])
	}
	rec := p.knownAPs[apIdentity(a)]
	if rec.visible {
		t.Fatal("expected AP marked not visible after lost")
	}
	p.mu.Unlock()

	// Case 3: known but already not-visible -> "Missed rejoins".
	p.HandleEvent("bcap_wifi_ap_lost", []interface{}{nil, map[string]interface{}{"data": a}})
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.chistos["Missed rejoins"][6] != 1 {
		t.Fatalf("expected Missed rejoins[6]=1, got %d", p.chistos["Missed rejoins"][6])
	}
}

func TestWifiUpdateBuildsHistogramAndActiveChannels(t *testing.T) {
	p, _ := newTestPlugin(t)
	aps := []map[string]interface{}{
		ap("a", "AA:AA:AA:AA:AA:AA", 1),
		ap("b", "BB:BB:BB:BB:BB:BB", 1),
		ap("c", "CC:CC:CC:CC:CC:CC", 6),
	}
	p.HandleEvent("wifi_update", []interface{}{nil, aps})

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.loops != 1 {
		t.Fatalf("loops = %d, want 1", p.loops)
	}
	if p.histogram[1] != 2 || p.histogram[6] != 1 {
		t.Fatalf("unexpected histogram: %+v", p.histogram)
	}
	if len(p.activeChannels) != 2 {
		t.Fatalf("expected 2 active channels, got %v", p.activeChannels)
	}
}

func TestOnEpochRepopulatesFromRestrictChannelsAndMutatesLiveConfig(t *testing.T) {
	p := New()
	p.presetsDir = t.TempDir()
	agent := &fakeAgent{}
	if err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"extra_channels": int64(2), "restrict_channels": []interface{}{int64(1), int64(6), int64(11)}},
		Agent:  agent,
	}); err != nil {
		t.Fatal(err)
	}
	fullCfg := config.Map{"personality": config.Map{"channels": []interface{}{}}}
	p.HandleEvent("config_changed", []interface{}{fullCfg})

	p.HandleEvent("wifi_update", []interface{}{nil, []map[string]interface{}{ap("a", "AA:AA:AA:AA:AA:AA", 6)}})
	p.HandleEvent("epoch", nil)

	personality := fullCfg["personality"].(config.Map)
	channels, ok := personality["channels"].([]interface{})
	if !ok {
		t.Fatalf("expected personality.channels to be set, got %v", personality["channels"])
	}
	// active channel (6) plus up to 2 extra from restrict_channels.
	if len(channels) < 1 || len(channels) > 3 {
		t.Fatalf("unexpected channel count: %v", channels)
	}
	found6 := false
	for _, c := range channels {
		if c.(int) == 6 {
			found6 = true
		}
	}
	if !found6 {
		t.Fatalf("expected active channel 6 preserved in next_channels, got %v", channels)
	}
}

func TestOnEpochUsesSupportedChannelsFuncWhenNoRestrictConfigured(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.supportedChannels = func() []int { return []int{1, 2, 3} }
	p.opts.ExtraChannels = 5

	p.HandleEvent("epoch", nil)

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.unscannedChannels)+5 < 3 { // all 3 should have been drawn given extra_channels=5
		t.Fatalf("expected supported channels pool consumed, remaining: %v", p.unscannedChannels)
	}
}

func TestOnReadyRunsResetCommandsWhenEnabled(t *testing.T) {
	p, agent := newTestPlugin(t)
	p.opts.ResetHistory = true
	p.HandleEvent("ready", nil)

	runs := agent.snapshot()
	if len(runs) != 2 || runs[0].cmd != "wifi.recon clear" || runs[1].cmd != "wifi.clear" {
		t.Fatalf("unexpected Run calls: %+v", runs)
	}
	agent.mu.Lock()
	reset := agent.reset
	agent.mu.Unlock()
	if !reset {
		t.Fatal("expected in-process interaction history to be reset")
	}
}

func TestOnReadyNoOpWhenResetHistoryDisabled(t *testing.T) {
	p, agent := newTestPlugin(t)
	p.opts.ResetHistory = false
	p.HandleEvent("ready", nil)
	if len(agent.snapshot()) != 0 {
		t.Fatal("expected no Run calls when reset_history is disabled")
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.reset {
		t.Fatal("expected no history reset when reset_history is disabled")
	}
}

func TestHandshakeWithStringAPDoesNotPanic(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.HandleEvent("handshake", []interface{}{nil, "/some/file.pcap", "AA:BB:CC:DD:EE:FF", "11:22:33:44:55:66"})
}

func TestNormalizeMatchesPythonEdgeCases(t *testing.T) {
	cases := map[string]string{
		"":          "EMPTY",
		"<hidden>":  "HIDDEN",
		"My-Router": "myrouter",
		"a b c 123": "abc123",
	}
	for in, want := range cases {
		if got := normalizeName(in); got != want {
			t.Errorf("normalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPresetSaveLoadDeleteRoundTrip(t *testing.T) {
	p, _ := newTestPlugin(t)
	personality := p.fullCfg["personality"].(config.Map)
	personality["max_interactions"] = int64(3)
	p.opts.ExtraChannels = 7

	if err := p.savePreset("mypreset"); err != nil {
		t.Fatalf("savePreset: %v", err)
	}
	files := p.listPresetFiles()
	if len(files) != 1 || files[0] != "mypreset" {
		t.Fatalf("expected 1 preset 'mypreset', got %v", files)
	}

	personality["max_interactions"] = int64(99)
	p.opts.ExtraChannels = 1

	ok, msg := p.loadPreset("mypreset")
	if !ok {
		t.Fatalf("loadPreset failed: %s", msg)
	}
	if personality["max_interactions"] != int64(3) {
		t.Fatalf("expected max_interactions restored to 3, got %v", personality["max_interactions"])
	}
	if p.opts.ExtraChannels != 7 {
		t.Fatalf("expected extra_channels restored to 7, got %d", p.opts.ExtraChannels)
	}

	if !p.deletePreset("mypreset") {
		t.Fatal("expected deletePreset to succeed")
	}
	if len(p.listPresetFiles()) != 0 {
		t.Fatal("expected no presets after delete")
	}
}

func TestLoadPresetMissingFileReportsFalse(t *testing.T) {
	p, _ := newTestPlugin(t)
	ok, msg := p.loadPreset("does-not-exist")
	if ok {
		t.Fatal("expected loadPreset to report failure for a missing file")
	}
	if !strings.Contains(msg, "not found") {
		t.Fatalf("unexpected message: %s", msg)
	}
}

func TestOnWebhookGetRendersHistogramAndChistos(t *testing.T) {
	p, _ := newTestPlugin(t)
	p.HandleEvent("wifi_update", []interface{}{nil, []map[string]interface{}{ap("a", "AA:AA:AA:AA:AA:AA", 6)}})

	req := httptest.NewRequest(http.MethodGet, "/plugins/auto-tune/", nil)
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "test-token"})
	resp, err := p.OnWebhook("", req)
	if err != nil {
		t.Fatal(err)
	}
	body := string(resp.Body)
	if !strings.Contains(body, "AUTO Tune") || !strings.Contains(body, "Channel Statistics") {
		t.Fatalf("unexpected body: %s", body)
	}
	if !strings.Contains(body, `name="csrf_token" value="test-token"`) {
		t.Fatalf("expected CSRF token in form: %s", body)
	}
}

func TestPresetNamesCannotEscapePresetDirectory(t *testing.T) {
	p, _ := newTestPlugin(t)
	if err := p.savePreset("../outside"); err == nil {
		t.Fatal("expected traversal preset name to be rejected")
	}
	if ok, _ := p.loadPreset("../outside"); ok {
		t.Fatal("expected traversal preset load to be rejected")
	}
	if p.deletePreset("../outside") {
		t.Fatal("expected traversal preset delete to be rejected")
	}
}

func TestOnWebhookPostUpdateAppliesPersonalityEdit(t *testing.T) {
	p, _ := newTestPlugin(t)
	personality := p.fullCfg["personality"].(config.Map)
	personality["max_interactions"] = int64(3)

	form := url.Values{
		"newval,3,max_interactions,int": {"10"},
		"newval,15,extra_channels,int":  {"7"},
	}
	req := httptest.NewRequest(http.MethodPost, "/plugins/auto-tune/update", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.OnWebhook("update", req)
	if err != nil {
		t.Fatal(err)
	}
	if personality["max_interactions"] != 10 {
		t.Fatalf("expected max_interactions updated to 10, got %v", personality["max_interactions"])
	}
	if p.opts.ExtraChannels != 7 {
		t.Fatalf("expected extra_channels updated to 7, got %d", p.opts.ExtraChannels)
	}
	if !strings.Contains(string(resp.Body), "max_interactions") {
		t.Fatalf("expected change log in response body: %s", resp.Body)
	}

	saved, err := config.LoadTOMLFileForEdit(p.configPath)
	if err != nil {
		t.Fatalf("load saved config: %v", err)
	}
	if got := saved["personality"].(config.Map)["max_interactions"]; got != int64(10) {
		t.Fatalf("saved personality.max_interactions = %v, want 10", got)
	}
	plugins := saved["main"].(config.Map)["plugins"].(config.Map)
	autoTune := plugins[p.Name()].(config.Map)
	if got := autoTune["extra_channels"]; got != int64(7) {
		t.Fatalf("saved plugin extra_channels = %v, want 7", got)
	}
}

func TestOptionsParsingReadsAllFields(t *testing.T) {
	cfg := config.Map{
		"show_hidden":       true,
		"reset_history":     false,
		"extra_channels":    int64(20),
		"show_interactions": true,
		"restrict_channels": []interface{}{int64(1), int64(6)},
	}
	o := parseOptions(cfg)
	if !o.ShowHidden || o.ResetHistory || o.ExtraChannels != 20 || !o.ShowInteractions {
		t.Fatalf("unexpected options: %+v", o)
	}
	if len(o.RestrictChannels) != 2 || o.RestrictChannels[0] != 1 || o.RestrictChannels[1] != 6 {
		t.Fatalf("unexpected restrict channels: %v", o.RestrictChannels)
	}
}
