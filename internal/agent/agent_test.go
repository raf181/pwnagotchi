package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/epoch"
	"github.com/jayofelony/pwnagotchi/internal/identity"
	"github.com/jayofelony/pwnagotchi/internal/mesh"
	"github.com/jayofelony/pwnagotchi/internal/unit"
)

type fakeView struct {
	agent         *Agent
	sets          map[string]string
	assocs        []AP
	deauths       []Station
	normalCalls   int
	handshakeMsgs []int
	closestPeer   *mesh.Peer
	closestCount  int
	rebooting     bool
}

func newFakeView() *fakeView { return &fakeView{sets: map[string]string{}} }

func (v *fakeView) OnStarting()                                               {}
func (v *fakeView) OnGrateful()                                               {}
func (v *fakeView) OnLonely()                                                 {}
func (v *fakeView) OnBored()                                                  {}
func (v *fakeView) OnSad()                                                    {}
func (v *fakeView) OnAngry()                                                  {}
func (v *fakeView) OnExcited()                                                {}
func (v *fakeView) OnRebooting()                                              { v.rebooting = true }
func (v *fakeView) OnUploading(to string)                                     {}
func (v *fakeView) OnMiss(who string)                                         {}
func (v *fakeView) Wait(t float64, sleeping bool)                             {}
func (v *fakeView) OnStateChange(event string, cb func(old, new interface{})) {}
func (v *fakeView) OnNewPeer(p *mesh.Peer)                                    {}
func (v *fakeView) OnLostPeer(p *mesh.Peer)                                   {}
func (v *fakeView) OnReadingLogs(linesSoFar int)                              {}
func (v *fakeView) SetAgent(a *Agent)                                         { v.agent = a }
func (v *fakeView) Set(key string, value interface{}) {
	if value == nil {
		v.sets[key] = ""
		return
	}
	s, _ := value.(string)
	v.sets[key] = s
}
func (v *fakeView) SetClosestPeer(peer *mesh.Peer, count int) {
	v.closestPeer = peer
	v.closestCount = count
}
func (v *fakeView) OnHandshakes(newShakes int) { v.handshakeMsgs = append(v.handshakeMsgs, newShakes) }
func (v *fakeView) OnAssoc(ap AP)              { v.assocs = append(v.assocs, ap) }
func (v *fakeView) OnDeauth(sta Station)       { v.deauths = append(v.deauths, sta) }
func (v *fakeView) OnNormal()                  { v.normalCalls++ }

type fakeEmitter struct{ events []string }

func (e *fakeEmitter) On(event string, args ...interface{}) { e.events = append(e.events, event) }

// bettercapFake serves a minimal /api/session (GET/POST) endpoint backing a
// mutable session document, enough for Agent's Session()/Run() call sites.
type bettercapFake struct {
	mu      chan struct{}
	session map[string]interface{}
}

func newBettercapFake() *bettercapFake {
	return &bettercapFake{
		mu: make(chan struct{}, 1),
		session: map[string]interface{}{
			"interfaces": []interface{}{
				map[string]interface{}{"name": "wlan0mon"},
			},
			"modules": []interface{}{
				map[string]interface{}{"name": "wifi", "running": true},
			},
			"wifi": map[string]interface{}{
				"aps": []interface{}{},
			},
		},
	}
}

func (b *bettercapFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(b.session)
	}
}

func testConfig(t *testing.T, handshakesDir string) config.Map {
	t.Helper()
	return config.Map{
		"main": config.Map{
			"iface":         "wlan0mon",
			"mon_start_cmd": "",
			"no_restart":    true,
			"whitelist":     []interface{}{"examplenet"},
			"lang":          "en",
			"log":           config.Map{"path": filepath.Join(t.TempDir(), "session.log")},
		},
		"bettercap": config.Map{
			"handshakes": handshakesDir,
			"silence":    []interface{}{"wifi.client.probe"},
		},
		"personality": config.Map{
			"bond_encounters_factor":    int64(20000),
			"sad_num_epochs":            int64(25),
			"bored_num_epochs":          int64(15),
			"excited_num_epochs":        int64(10),
			"max_misses_for_recon":      int64(5),
			"max_inactive_scale":        int64(2),
			"recon_inactive_multiplier": int64(2),
			"recon_time":                int64(30),
			"channels":                  []interface{}{},
			"max_interactions":          int64(3),
			"associate":                 true,
			"deauth":                    true,
			"ap_ttl":                    int64(120),
			"sta_ttl":                   int64(300),
			"min_rssi":                  int64(-200),
			"advertise":                 false,
		},
	}
}

func newTestAgent(t *testing.T, srv *httptest.Server, handshakesDir string) (*Agent, *fakeView) {
	t.Helper()

	dir := t.TempDir()
	hostnamePath := filepath.Join(dir, "hostname")
	if err := os.WriteFile(hostnamePath, []byte("pwnagotchi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	origHostnamePath := unit.HostnamePath
	unit.HostnamePath = hostnamePath
	unit.ResetNameCache()
	t.Cleanup(func() {
		unit.HostnamePath = origHostnamePath
		unit.ResetNameCache()
	})

	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())

	cfg := testConfig(t, handshakesDir)
	cfg["bettercap"].(config.Map)["hostname"] = u.Hostname()
	cfg["bettercap"].(config.Map)["port"] = int64(port)
	cfg["bettercap"].(config.Map)["scheme"] = "http"
	cfg["bettercap"].(config.Map)["username"] = "pwnagotchi"
	cfg["bettercap"].(config.Map)["password"] = "pwnagotchi"

	view := newFakeView()
	kp := &identity.KeyPair{Fingerprint: "testfingerprint"}

	a, err := New(view, cfg, kp, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.AsyncAdvertiser.Grid = nil // avoid real grid HTTP calls in most tests
	return a, view
}

func TestNewAgentDefaultsAndBettercapWiring(t *testing.T) {
	fake := newBettercapFake()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	dir := t.TempDir()
	a, view := newTestAgent(t, srv, dir)

	if view.agent != a {
		t.Fatal("SetAgent should have been called with the Agent itself")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("handshakes dir should exist (was already present in this case)")
	}
	if a.Client.Username != "pwnagotchi" || a.Client.Password != "pwnagotchi" {
		t.Fatalf("bettercap credentials = %s/%s, want pwnagotchi/pwnagotchi (Agent's OWN defaults, not Client's)", a.Client.Username, a.Client.Password)
	}
	if a.Mode != "auto" {
		t.Fatalf("Mode = %q, want auto", a.Mode)
	}
}

func TestNewAgentCreatesMissingHandshakesDir(t *testing.T) {
	fake := newBettercapFake()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	dir := filepath.Join(t.TempDir(), "handshakes-not-yet-created")
	_, _ = newTestAgent(t, srv, dir)

	if _, err := os.Stat(dir); err != nil {
		t.Fatal("expected handshakes directory to be created")
	}
}

func TestGetAccessPointsFiltersOpenAndWhitelisted(t *testing.T) {
	fake := newBettercapFake()
	fake.session["wifi"] = map[string]interface{}{
		"aps": []interface{}{
			map[string]interface{}{"mac": "aa:bb:cc:dd:ee:01", "hostname": "OpenNet", "encryption": "", "channel": float64(6), "clients": []interface{}{}},
			map[string]interface{}{"mac": "aa:bb:cc:dd:ee:02", "hostname": "examplenet", "encryption": "WPA2", "channel": float64(1), "clients": []interface{}{}},
			map[string]interface{}{"mac": "aa:bb:cc:dd:ee:03", "hostname": "TargetNet", "encryption": "WPA2", "channel": float64(11), "clients": []interface{}{}},
		},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	a, _ := newTestAgent(t, srv, t.TempDir())
	aps := a.GetAccessPoints()
	if len(aps) != 1 {
		t.Fatalf("GetAccessPoints() returned %d APs, want 1 (OpenNet and examplenet filtered)", len(aps))
	}
	if getString(aps[0], "hostname") != "TargetNet" {
		t.Fatalf("unexpected surviving AP: %v", aps[0])
	}
}

func TestGetAccessPointsByChannelGroupsAndSortsByPopulation(t *testing.T) {
	fake := newBettercapFake()
	fake.session["wifi"] = map[string]interface{}{
		"aps": []interface{}{
			map[string]interface{}{"mac": "aa:bb:cc:dd:ee:01", "hostname": "A", "encryption": "WPA2", "channel": float64(1), "clients": []interface{}{}},
			map[string]interface{}{"mac": "aa:bb:cc:dd:ee:02", "hostname": "B", "encryption": "WPA2", "channel": float64(6), "clients": []interface{}{}},
			map[string]interface{}{"mac": "aa:bb:cc:dd:ee:03", "hostname": "C", "encryption": "WPA2", "channel": float64(6), "clients": []interface{}{}},
		},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	a, _ := newTestAgent(t, srv, t.TempDir())
	groups := a.GetAccessPointsByChannel()
	if len(groups) != 2 {
		t.Fatalf("groups = %v, want 2 channels", groups)
	}
	if groups[0].Channel != 6 || len(groups[0].APs) != 2 {
		t.Fatalf("most populated channel should be first: %+v", groups[0])
	}
}

func TestShouldInteractRespectsHandshakeAndMaxInteractions(t *testing.T) {
	fake := newBettercapFake()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a, _ := newTestAgent(t, srv, t.TempDir())

	mac := "aa:bb:cc:dd:ee:ff"
	if !a.shouldInteract(mac) {
		t.Fatal("first interaction should be allowed")
	}
	if !a.shouldInteract(mac) {
		t.Fatal("second interaction (count=2 < max=3) should be allowed")
	}
	if a.shouldInteract(mac) {
		t.Fatal("third interaction (count=3, not < max=3) should be denied")
	}

	a.mu.Lock()
	a.handshakes["aa:bb -> "+mac] = true
	a.mu.Unlock()
	if a.hasHandshake(mac) == false {
		t.Fatal("hasHandshake should find the handshake by substring match")
	}
}

func TestAssociateSkipsWhenStale(t *testing.T) {
	fake := newBettercapFake()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a, view := newTestAgent(t, srv, t.TempDir())

	// Drive num_missed above max_misses_for_recon (5) to become stale.
	for i := 0; i < 6; i++ {
		a.Automata.Epoch.Track(epoch.TrackOptions{Miss: true})
	}
	if !a.IsStale() {
		t.Fatal("expected agent to be stale after 6 misses")
	}

	ap := AP{"mac": "aa:bb:cc:dd:ee:ff", "hostname": "Target", "vendor": "", "channel": float64(6), "rssi": float64(-40), "clients": []interface{}{}}
	a.Associate(ap, -1)
	if len(view.assocs) != 0 {
		t.Fatal("Associate should skip entirely when stale")
	}
}

func TestAssociateSendsFrameAndTracksEpoch(t *testing.T) {
	fake := newBettercapFake()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a, view := newTestAgent(t, srv, t.TempDir())

	ap := AP{"mac": "aa:bb:cc:dd:ee:ff", "hostname": "Target", "vendor": "", "channel": float64(6), "rssi": float64(-40), "clients": []interface{}{}}
	a.Associate(ap, 0)

	if len(view.assocs) != 1 {
		t.Fatalf("expected exactly one OnAssoc call, got %d", len(view.assocs))
	}
	if a.Automata.Epoch.NumAssocs != 1 {
		t.Fatalf("NumAssocs = %d, want 1", a.Automata.Epoch.NumAssocs)
	}
	if view.normalCalls != 1 {
		t.Fatalf("OnNormal calls = %d, want 1", view.normalCalls)
	}
}

func TestSetChannelHopsAndTracksEpoch(t *testing.T) {
	fake := newBettercapFake()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a, view := newTestAgent(t, srv, t.TempDir())

	a.SetChannel(6, false)
	if a.GetCurrentChannel() != 6 {
		t.Fatalf("GetCurrentChannel() = %d, want 6", a.GetCurrentChannel())
	}
	if a.Automata.Epoch.NumHops != 1 {
		t.Fatalf("NumHops = %d, want 1", a.Automata.Epoch.NumHops)
	}
	if view.sets["channel"] != "6" {
		t.Fatalf("view channel = %q, want 6", view.sets["channel"])
	}

	// Setting the SAME channel again must be a complete no-op.
	a.SetChannel(6, false)
	if a.Automata.Epoch.NumHops != 1 {
		t.Fatal("re-setting the same channel must not increment NumHops again")
	}
}

func TestOnEventRecordsHandshakeNotFoundInSession(t *testing.T) {
	fake := newBettercapFake()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a, view := newTestAgent(t, srv, t.TempDir())
	emitter := &fakeEmitter{}
	a.emit = emitter
	a.Automata.Emit = emitter

	msg := []byte(`{"tag":"wifi.client.handshake","data":{"file":"/tmp/x.pcap","station":"aa:aa:aa:aa:aa:aa","ap":"bb:bb:bb:bb:bb:bb"}}`)
	if err := a.onEvent(msg); err != nil {
		t.Fatalf("onEvent: %v", err)
	}

	a.mu.Lock()
	_, ok := a.handshakes["aa:aa:aa:aa:aa:aa -> bb:bb:bb:bb:bb:bb"]
	lastPwnd := a.lastPwnd
	a.mu.Unlock()
	if !ok {
		t.Fatal("expected the handshake to be recorded")
	}
	if lastPwnd != "bb:bb:bb:bb:bb:bb" {
		t.Fatalf("lastPwnd = %q, want the AP mac (not found in session)", lastPwnd)
	}
	if len(view.handshakeMsgs) != 1 || view.handshakeMsgs[0] != 1 {
		t.Fatalf("view.handshakeMsgs = %v, want [1]", view.handshakeMsgs)
	}

	foundHandshakeEvent := false
	for _, e := range emitter.events {
		if e == "handshake" {
			foundHandshakeEvent = true
		}
	}
	if !foundHandshakeEvent {
		t.Fatal("expected a 'handshake' plugin event")
	}
}

func TestOnEventDuplicateHandshakeIsIgnored(t *testing.T) {
	fake := newBettercapFake()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a, view := newTestAgent(t, srv, t.TempDir())

	msg := []byte(`{"tag":"wifi.client.handshake","data":{"file":"/tmp/x.pcap","station":"aa:aa:aa:aa:aa:aa","ap":"bb:bb:bb:bb:bb:bb"}}`)
	a.onEvent(msg)
	a.onEvent(msg)

	if len(view.handshakeMsgs) != 1 {
		t.Fatalf("expected exactly one OnHandshakes call across two identical events, got %d", len(view.handshakeMsgs))
	}
}

func TestSaveAndLoadRecoveryDataRoundTrip(t *testing.T) {
	fake := newBettercapFake()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a, _ := newTestAgent(t, srv, t.TempDir())

	dir := t.TempDir()
	origPath := RecoveryDataFile
	RecoveryDataFile = filepath.Join(dir, "recovery.json")
	t.Cleanup(func() { RecoveryDataFile = origPath })

	a.mu.Lock()
	a.history["aa:bb"] = 2
	a.handshakes["x -> y"] = map[string]interface{}{"tag": "wifi.client.handshake"}
	a.lastPwnd = "TargetNet"
	a.mu.Unlock()
	a.Automata.Epoch.Epoch = 7

	a.saveRecoveryData()

	// Reset in-memory state, then reload from disk.
	a.mu.Lock()
	a.history = map[string]int{}
	a.handshakes = map[string]interface{}{}
	a.lastPwnd = ""
	a.mu.Unlock()
	a.Automata.Epoch.Epoch = 0

	if err := a.loadRecoveryData(false, false); err != nil {
		t.Fatalf("loadRecoveryData: %v", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.history["aa:bb"] != 2 {
		t.Fatalf("history not restored: %v", a.history)
	}
	if _, ok := a.handshakes["x -> y"]; !ok {
		t.Fatalf("handshakes not restored: %v", a.handshakes)
	}
	if a.lastPwnd != "TargetNet" {
		t.Fatalf("lastPwnd = %q, want TargetNet", a.lastPwnd)
	}
	if a.Automata.Epoch.Epoch != 7 {
		t.Fatalf("epoch = %d, want 7", a.Automata.Epoch.Epoch)
	}
}

func TestFetchStatsSinglePass(t *testing.T) {
	fake := newBettercapFake()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	a, view := newTestAgent(t, srv, t.TempDir())

	// Run one manual pass of the fetch-stats body (not the infinite loop)
	// to keep the test fast and deterministic.
	a.updateUptime()
	a.updatePeers()
	a.updateCounters()
	a.updateHandshakes(0)

	if _, ok := view.sets["uptime"]; !ok {
		t.Fatal("expected uptime to be set")
	}
	if _, ok := view.sets["aps"]; !ok {
		t.Fatal("expected aps counter to be set")
	}
	if _, ok := view.sets["shakes"]; !ok {
		t.Fatal("expected shakes counter to be set")
	}
}
