package wpasec

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/agent"
	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/identity"
	"github.com/jayofelony/pwnagotchi/internal/mesh"
	"github.com/jayofelony/pwnagotchi/internal/unit"
	_ "modernc.org/sqlite"
)

// fakeView is a minimal real agent.View implementation — this test only
// needs OnUploading/OnNormal to actually be callable without panicking;
// every other method is a real no-op, same shape as internal/agent's own
// test helper (a different package, so not reusable directly).
type fakeView struct {
	uploadingMsgs []string
	normalCalls   int
}

func (v *fakeView) OnStarting()                                               {}
func (v *fakeView) OnGrateful()                                               {}
func (v *fakeView) OnLonely()                                                 {}
func (v *fakeView) OnBored()                                                  {}
func (v *fakeView) OnSad()                                                    {}
func (v *fakeView) OnAngry()                                                  {}
func (v *fakeView) OnExcited()                                                {}
func (v *fakeView) OnRebooting()                                              {}
func (v *fakeView) OnMiss(who string)                                         {}
func (v *fakeView) Wait(t float64, sleeping bool)                             {}
func (v *fakeView) OnStateChange(event string, cb func(old, new interface{})) {}
func (v *fakeView) OnNewPeer(p *mesh.Peer)                                    {}
func (v *fakeView) OnLostPeer(p *mesh.Peer)                                   {}
func (v *fakeView) OnReadingLogs(linesSoFar int)                              {}
func (v *fakeView) SetAgent(a *agent.Agent)                                   {}
func (v *fakeView) Set(key string, value interface{})                         {}
func (v *fakeView) SetClosestPeer(peer *mesh.Peer, count int)                 {}
func (v *fakeView) OnHandshakes(newShakes int)                                {}
func (v *fakeView) OnAssoc(ap agent.AP)                                       {}
func (v *fakeView) OnDeauth(sta agent.Station)                                {}
func (v *fakeView) OnNormal()                                                 { v.normalCalls++ }
func (v *fakeView) OnUploading(to string)                                     { v.uploadingMsgs = append(v.uploadingMsgs, to) }

// newTestAgent builds a real *agent.Agent (not a mock) the same way
// internal/agent's own tests do, isolated from the real host's
// /etc/hostname via unit.HostnamePath — this package needs its own copy
// since that helper lives in an unexported _test.go file in a different
// package.
func newTestAgent(t *testing.T, cfg config.Map) (*agent.Agent, *fakeView) {
	t.Helper()
	dir := t.TempDir()
	hostnamePath := filepath.Join(dir, "hostname")
	if err := os.WriteFile(hostnamePath, []byte("pwnagotchi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := unit.HostnamePath
	unit.HostnamePath = hostnamePath
	unit.ResetNameCache()
	t.Cleanup(func() {
		unit.HostnamePath = orig
		unit.ResetNameCache()
	})

	view := &fakeView{}
	kp := &identity.KeyPair{Fingerprint: "testfingerprint"}
	a, err := agent.New(view, cfg, kp, nil)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	return a, view
}

func baseCfg(t *testing.T, apiURL, apiKey string, whitelist []interface{}) config.Map {
	handshakesDir := t.TempDir()
	return config.Map{
		"main": config.Map{
			"iface":         "",
			"mon_start_cmd": "",
			"no_restart":    true,
			"whitelist":     whitelist,
			"lang":          "en",
			"log":           config.Map{"path": filepath.Join(t.TempDir(), "session.log")},
			"plugins": config.Map{
				"wpa-sec": config.Map{
					"enabled": true,
					"api_key": apiKey,
					"api_url": apiURL,
				},
			},
		},
		"bettercap": config.Map{
			"handshakes": handshakesDir,
		},
		"personality": config.Map{
			"channels": []interface{}{},
		},
	}
}

// newTestPlugin overrides the package-level DBPath to a t.TempDir()
// path before calling New, so no test ever reads/writes the real host's
// /etc/pwnagotchi/.wpa_sec_go_db.json — restored after the test, same
// pattern as internal/unit.HostnamePath's own test isolation.
func newTestPlugin(t *testing.T, cfg config.Map) *Plugin {
	t.Helper()
	origDB := DBPath
	origLegacy := LegacyDBPath
	DBPath = filepath.Join(t.TempDir(), "wpa_sec_go_db.json")
	// Nonexistent by default: real migration behavior is covered by its
	// own dedicated tests, which point this at a real temp sqlite file.
	LegacyDBPath = filepath.Join(t.TempDir(), "no-such-legacy.db")
	t.Cleanup(func() {
		DBPath = origDB
		LegacyDBPath = origLegacy
	})
	return New(cfg)
}

// TestNewRequiresAPIKeyAndURL is a regression-shaped test for real
// wpa-sec.py's on_loaded guard: missing api_key or api_url must leave the
// plugin permanently not-ready (matching real Python exactly), not
// silently "ready with no key".
func TestNewRequiresAPIKeyAndURL(t *testing.T) {
	cfg := baseCfg(t, "", "", nil)
	p := newTestPlugin(t, cfg)
	if p.ready {
		t.Fatal("expected not ready with empty api_key/api_url")
	}

	cfg2 := baseCfg(t, "https://wpa-sec.stanev.org", "", nil)
	p2 := newTestPlugin(t, cfg2)
	if p2.ready {
		t.Fatal("expected not ready with empty api_key")
	}

	cfg3 := baseCfg(t, "", "somekey", nil)
	p3 := newTestPlugin(t, cfg3)
	if p3.ready {
		t.Fatal("expected not ready with empty api_url")
	}
}

// TestOnHandshakeRespectsWhitelist proves a whitelisted AP's handshake is
// never queued for upload — real config.RemoveWhitelisted, not a
// reimplementation of the whitelist matching logic.
func TestOnHandshakeRespectsWhitelist(t *testing.T) {
	cfg := baseCfg(t, "https://wpa-sec.stanev.org", "realkey", []interface{}{"examplenet"})
	p := newTestPlugin(t, cfg)
	a, _ := newTestAgent(t, cfg)

	p.onHandshake(a, "/handshakes/examplenet_aabbcc.pcap")

	p.mu.Lock()
	_, queued := p.records["/handshakes/examplenet_aabbcc.pcap"]
	p.mu.Unlock()
	if queued {
		t.Fatal("a whitelisted handshake must never be queued for upload")
	}
}

// TestOnHandshakeThenUploadRealHTTPRoundTrip is the core end-to-end
// proof this plugin actually works where the bridge-routed original
// couldn't: a real handshake event queues a real file, a real
// internet_available event performs a REAL HTTP multipart POST (verified
// server-side: real Content-Type, real "key" cookie, real file bytes),
// and a "hcxpcapngtool"-prefixed response marks it Successful — the same
// classification rule real wpa-sec.py uses.
func TestOnHandshakeThenUploadRealHTTPRoundTrip(t *testing.T) {
	var gotKey string
	var gotFileContent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("key"); err == nil {
			gotKey = c.Value
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("server: ParseMultipartForm: %v", err)
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("server: FormFile: %v", err)
		} else {
			gotFileContent, _ = io.ReadAll(f)
		}
		w.Write([]byte("hcxpcapngtool: 1 hash written\n"))
	}))
	defer srv.Close()

	cfg := baseCfg(t, srv.URL, "real-test-key", nil)
	p := newTestPlugin(t, cfg)
	if !p.ready {
		t.Fatal("expected ready with valid api_key/api_url")
	}
	a, view := newTestAgent(t, cfg)

	handshakeDir := t.TempDir()
	pcapPath := filepath.Join(handshakeDir, "testnet_aabbcc.pcap")
	if err := os.WriteFile(pcapPath, []byte("fake pcap bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	p.On("handshake", a, pcapPath, agent.AP{}, agent.Station{})
	p.mu.Lock()
	status, queued := p.records[pcapPath]
	p.mu.Unlock()
	if !queued || status != StatusToUpload {
		t.Fatalf("expected %s queued as StatusToUpload, got queued=%v status=%v", pcapPath, queued, status)
	}

	p.On("internet_available", a)

	if gotKey != "real-test-key" {
		t.Fatalf("expected the real api_key sent as the 'key' cookie, got %q", gotKey)
	}
	if string(gotFileContent) != "fake pcap bytes" {
		t.Fatalf("expected the real pcap file content uploaded, got %q", gotFileContent)
	}
	if len(view.uploadingMsgs) == 0 {
		t.Fatal("expected a real OnUploading status call during upload")
	}
	if view.normalCalls == 0 {
		t.Fatal("expected a real OnNormal call after uploads finish")
	}

	p.mu.Lock()
	status = p.records[pcapPath]
	p.mu.Unlock()
	if status != StatusSuccessful {
		t.Fatalf("expected StatusSuccessful after a real hcxpcapngtool-prefixed response, got %v", status)
	}

	// Real persistence: reload a fresh Plugin from disk and confirm the
	// state survived, matching real Python's sqlite DB surviving a
	// restart.
	p2 := New(cfg)
	p2.mu.Lock()
	status2 := p2.records[pcapPath]
	p2.mu.Unlock()
	if status2 != StatusSuccessful {
		t.Fatalf("expected persisted StatusSuccessful across a fresh load, got %v", status2)
	}
}

// TestUploadInvalidResponseMarksInvalidAndNeverRequeues proves a
// non-"hcxpcapngtool"-prefixed response is classified Invalid, and a
// subsequent real handshake event for the SAME path does not requeue it
// — matching real wpa-sec.py's `ON CONFLICT ... WHERE status = INVALID`
// upsert guard exactly.
func TestUploadInvalidResponseMarksInvalidAndNeverRequeues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not a valid capture"))
	}))
	defer srv.Close()

	cfg := baseCfg(t, srv.URL, "k", nil)
	p := newTestPlugin(t, cfg)
	a, _ := newTestAgent(t, cfg)

	handshakeDir := t.TempDir()
	pcapPath := filepath.Join(handshakeDir, "net_aabbcc.pcap")
	os.WriteFile(pcapPath, []byte("data"), 0o644)

	p.On("handshake", a, pcapPath, agent.AP{}, agent.Station{})
	p.On("internet_available", a)

	p.mu.Lock()
	status := p.records[pcapPath]
	p.mu.Unlock()
	if status != StatusInvalid {
		t.Fatalf("expected StatusInvalid, got %v", status)
	}

	// Re-fire the handshake event for the same path — must NOT requeue.
	p.On("handshake", a, pcapPath, agent.AP{}, agent.Station{})
	p.mu.Lock()
	status = p.records[pcapPath]
	p.mu.Unlock()
	if status != StatusInvalid {
		t.Fatalf("expected an invalid handshake to never be requeued, got %v", status)
	}
}

// TestUploadNetworkErrorSkipsUntilReloadNotDeleted proves a real
// connection failure (server unreachable) marks the handshake
// skip-until-reload rather than deleting it — matching real Python's
// `except requests.exceptions.RequestException` branch, distinct from
// its `except OSError` (delete) branch.
func TestUploadNetworkErrorSkipsUntilReloadNotDeleted(t *testing.T) {
	// A real, guaranteed-closed local port: connection refused, not a
	// DNS or timeout ambiguity.
	closedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	unreachableURL := closedSrv.URL
	closedSrv.Close() // now nothing is listening there

	cfg := baseCfg(t, unreachableURL, "k", nil)
	p := newTestPlugin(t, cfg)
	a, _ := newTestAgent(t, cfg)

	handshakeDir := t.TempDir()
	pcapPath := filepath.Join(handshakeDir, "net_aabbcc.pcap")
	os.WriteFile(pcapPath, []byte("data"), 0o644)

	p.On("handshake", a, pcapPath, agent.AP{}, agent.Station{})
	p.On("internet_available", a)

	p.mu.Lock()
	status, stillQueued := p.records[pcapPath]
	skipped := p.skipUntilReload[pcapPath]
	p.mu.Unlock()
	if !stillQueued || status != StatusToUpload {
		t.Fatalf("expected the record to remain StatusToUpload (not deleted) after a network error, got queued=%v status=%v", stillQueued, status)
	}
	if !skipped {
		t.Fatal("expected the handshake marked skip-until-reload after a real connection failure")
	}
}

// makeLegacyDB creates a real sqlite database file at path with exactly
// the real Python wpa-sec.py plugin's schema
// (`handshakes(path TEXT PRIMARY KEY, status INTEGER)`), populated with
// the given rows — a genuine legacy database, not a fixture faked in a
// different format.
func makeLegacyDB(t *testing.T, path string, rows map[string]int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("opening legacy db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE handshakes (path TEXT PRIMARY KEY, status INTEGER)`); err != nil {
		t.Fatalf("creating legacy schema: %v", err)
	}
	for path, status := range rows {
		if _, err := db.Exec(`INSERT INTO handshakes (path, status) VALUES (?, ?)`, path, status); err != nil {
			t.Fatalf("inserting legacy row: %v", err)
		}
	}
}

// TestMigratesLegacyPythonDatabaseOnFirstLoad proves upgrading from the
// real Python plugin (or an earlier native build) doesn't lose upload
// status: New() must read the real legacy sqlite file and populate
// records from it when no native JSON database exists yet.
func TestMigratesLegacyPythonDatabaseOnFirstLoad(t *testing.T) {
	origDB, origLegacy := DBPath, LegacyDBPath
	DBPath = filepath.Join(t.TempDir(), "wpa_sec_go_db.json")
	LegacyDBPath = filepath.Join(t.TempDir(), "legacy.wpa_sec_db")
	t.Cleanup(func() { DBPath = origDB; LegacyDBPath = origLegacy })

	makeLegacyDB(t, LegacyDBPath, map[string]int{
		"/etc/pwnagotchi/handshakes/net_a.pcap": int(StatusToUpload),
		"/etc/pwnagotchi/handshakes/net_b.pcap": int(StatusSuccessful),
		"/etc/pwnagotchi/handshakes/net_c.pcap": int(StatusInvalid),
	})

	cfg := baseCfg(t, "https://wpa-sec.example", "key", nil)
	p := New(cfg)

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.records) != 3 {
		t.Fatalf("expected 3 migrated records, got %d: %v", len(p.records), p.records)
	}
	if p.records["/etc/pwnagotchi/handshakes/net_a.pcap"] != StatusToUpload {
		t.Fatalf("net_a: expected StatusToUpload, got %v", p.records["/etc/pwnagotchi/handshakes/net_a.pcap"])
	}
	if p.records["/etc/pwnagotchi/handshakes/net_b.pcap"] != StatusSuccessful {
		t.Fatalf("net_b: expected StatusSuccessful, got %v", p.records["/etc/pwnagotchi/handshakes/net_b.pcap"])
	}
	if p.records["/etc/pwnagotchi/handshakes/net_c.pcap"] != StatusInvalid {
		t.Fatalf("net_c: expected StatusInvalid, got %v", p.records["/etc/pwnagotchi/handshakes/net_c.pcap"])
	}

	// The migration must also persist to the native JSON database, so a
	// second load doesn't need the legacy file at all.
	if _, err := os.Stat(DBPath); err != nil {
		t.Fatalf("expected migrated records to be persisted to DBPath: %v", err)
	}
}

// TestNoMigrationWhenLegacyDBAbsent proves a genuinely fresh install (no
// legacy Python database ever existed) starts with a clean, empty record
// set rather than erroring.
func TestNoMigrationWhenLegacyDBAbsent(t *testing.T) {
	origDB, origLegacy := DBPath, LegacyDBPath
	DBPath = filepath.Join(t.TempDir(), "wpa_sec_go_db.json")
	LegacyDBPath = filepath.Join(t.TempDir(), "does-not-exist.db")
	t.Cleanup(func() { DBPath = origDB; LegacyDBPath = origLegacy })

	cfg := baseCfg(t, "https://wpa-sec.example", "key", nil)
	p := New(cfg)

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.records) != 0 {
		t.Fatalf("expected no records on a fresh install, got %v", p.records)
	}
}

// TestNativeJSONDatabaseTakesPrecedenceOverLegacy proves migration only
// happens once: if the native JSON database already exists (e.g. this
// plugin already migrated on a previous boot, or genuinely started
// native-only), a legacy sqlite file present alongside it must NOT be
// re-read/re-merged on every subsequent load.
func TestNativeJSONDatabaseTakesPrecedenceOverLegacy(t *testing.T) {
	origDB, origLegacy := DBPath, LegacyDBPath
	DBPath = filepath.Join(t.TempDir(), "wpa_sec_go_db.json")
	LegacyDBPath = filepath.Join(t.TempDir(), "legacy.wpa_sec_db")
	t.Cleanup(func() { DBPath = origDB; LegacyDBPath = origLegacy })

	// Native DB already has its own (different) state...
	native := map[string]Status{"/native/only.pcap": StatusToUpload}
	data, err := json.Marshal(native)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(DBPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// ...while a legacy database also happens to still be present.
	makeLegacyDB(t, LegacyDBPath, map[string]int{"/legacy/only.pcap": int(StatusSuccessful)})

	cfg := baseCfg(t, "https://wpa-sec.example", "key", nil)
	p := New(cfg)

	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.records["/legacy/only.pcap"]; ok {
		t.Fatal("expected the legacy database NOT to be re-merged once a native database already exists")
	}
	if p.records["/native/only.pcap"] != StatusToUpload {
		t.Fatalf("expected the existing native record to be preserved untouched, got %v", p.records)
	}
}
