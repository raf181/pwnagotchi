package sessionstats

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// fakeClock is an injectable, monotonically-advancing clock so tests
// control exactly which timestamp keys land in p.stats.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

var _ pluginmanager.Clock = (*fakeClock)(nil)

// fakeSystemReader is a deterministic systemReader fake — no real
// /proc/meminfo, /sys thermal zone, or /proc/stat reads in tests.
type fakeSystemReader struct {
	celsius                    int
	mem                        float64
	cpu                        float64
	celsiusErr, memErr, cpuErr error
}

func (f *fakeSystemReader) Celsius() (int, error)               { return f.celsius, f.celsiusErr }
func (f *fakeSystemReader) MemUsage() (float64, error)          { return f.mem, f.memErr }
func (f *fakeSystemReader) CPULoad(tag string) (float64, error) { return f.cpu, f.cpuErr }

func newTestPlugin(t *testing.T) (*Plugin, *fakeClock, *fakeSystemReader) {
	t.Helper()
	dir := t.TempDir()
	clk := &fakeClock{now: time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)}
	sys := &fakeSystemReader{celsius: 42, mem: 0.5, cpu: 0.25}
	p := New()
	p.sys = sys
	if err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"save_directory": dir, "update_interval": int64(3600)},
		Clock:  clk,
	}); err != nil {
		t.Fatalf("OnLoad: %v", err)
	}
	t.Cleanup(func() { p.OnUnload() })
	return p, clk, sys
}

func TestOnLoadCreatesSaveDirAndSessionFile(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "sessions")
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"save_directory": sessionDir},
	}); err != nil {
		t.Fatal(err)
	}
	defer p.OnUnload()
	if info, err := os.Stat(sessionDir); err != nil || !info.IsDir() {
		t.Fatalf("expected save directory created: %v", err)
	}
	p.mu.Lock()
	sessionPath := p.sessionPath
	p.mu.Unlock()
	if sessionPath == "" || !strings.HasPrefix(filepath.Base(sessionPath), "stats_") {
		t.Fatalf("expected a stats_*.json session path, got %q", sessionPath)
	}
}

// TestOnLoadLoadsHistoricalDataFromPreviousSession ports on_loaded's
// "session_files[-2]" historical-carryover logic exactly, including its
// real off-by-one-looking quirk: real Python's StatusFile.__init__ never
// creates the file on disk until the first .update() call (confirmed by
// reading utils.py's StatusFile — it only reads an already-existing
// path, never touches a missing one), so the CURRENT run's own session
// file does not exist yet at the point this historical scan runs, in
// either language. With exactly 3 pre-existing files on disk, "the
// second-to-last" is the CHRONOLOGICALLY MIDDLE one, not the newest —
// this port must reproduce that, not "fix" it into loading the newest.
func TestOnLoadLoadsHistoricalDataFromPreviousSession(t *testing.T) {
	dir := t.TempDir()
	oldest := filepath.Join(dir, "stats_2026_07_18_10_00.json")
	middle := filepath.Join(dir, "stats_2026_07_20_10_00.json")
	newest := filepath.Join(dir, "stats_2026_07_23_10_00.json")
	writeSessionFile(t, oldest, map[string]StatsEntry{"08:00:00.000": {NumPeers: 9}})
	writeSessionFile(t, middle, map[string]StatsEntry{"09:00:00.000": {NumPeers: 5, NumHandshakes: 2}})
	writeSessionFile(t, newest, map[string]StatsEntry{"10:00:00.000": {NumPeers: 1}})

	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{Config: config.Map{"save_directory": dir}}); err != nil {
		t.Fatal(err)
	}
	defer p.OnUnload()

	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.stats["09:00:00.000"]
	if !ok {
		t.Fatalf("expected the middle (second-to-last) file's data loaded, got stats=%v", p.stats)
	}
	if entry.NumPeers != 5 || entry.NumHandshakes != 2 {
		t.Fatalf("unexpected historical entry: %+v", entry)
	}
	if _, ok := p.stats["10:00:00.000"]; ok {
		t.Fatal("expected the newest file NOT to be loaded (real Python only ever reads session_files[-2])")
	}
}

func writeSessionFile(t *testing.T, path string, data map[string]StatsEntry) {
	t.Helper()
	raw, err := json.Marshal(sessionFile{Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHandleEventWifiUpdateTracksNetworkCount(t *testing.T) {
	p, _, _ := newTestPlugin(t)
	aps := []map[string]interface{}{{"mac": "a"}, {"mac": "b"}, {"mac": "c"}}
	p.HandleEvent("wifi_update", []interface{}{nil, aps})
	p.mu.Lock()
	got := p.networks
	p.mu.Unlock()
	if got != 3 {
		t.Fatalf("networks = %d, want 3", got)
	}
}

func TestHandleEventWifiUpdateAcceptsInterfaceSlice(t *testing.T) {
	p, _, _ := newTestPlugin(t)
	aps := []interface{}{map[string]interface{}{"mac": "a"}, map[string]interface{}{"mac": "b"}}
	p.HandleEvent("wifi_update", []interface{}{nil, aps})
	p.mu.Lock()
	got := p.networks
	p.mu.Unlock()
	if got != 2 {
		t.Fatalf("networks = %d, want 2", got)
	}
}

func TestHandleEventHandshakeIncrements(t *testing.T) {
	p, _, _ := newTestPlugin(t)
	p.HandleEvent("handshake", nil)
	p.HandleEvent("handshake", nil)
	p.mu.Lock()
	got := p.handshakes
	p.mu.Unlock()
	if got != 2 {
		t.Fatalf("handshakes = %d, want 2", got)
	}
}

func TestHandleEventDeauthenticationIncrements(t *testing.T) {
	p, _, _ := newTestPlugin(t)
	p.HandleEvent("deauthentication", nil)
	p.mu.Lock()
	got := p.deauths
	p.mu.Unlock()
	if got != 1 {
		t.Fatalf("deauths = %d, want 1", got)
	}
}

func TestHandleEventEpochRecordsASample(t *testing.T) {
	p, _, _ := newTestPlugin(t)
	p.HandleEvent("wifi_update", []interface{}{nil, []map[string]interface{}{{"mac": "a"}}})
	p.HandleEvent("epoch", nil)

	p.mu.Lock()
	n := len(p.stats)
	p.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected 1 recorded sample after epoch, got %d", n)
	}
}

func TestCollectAndMaybeRecordSkipsUnchangedSamples(t *testing.T) {
	p, clk, _ := newTestPlugin(t)
	p.collectAndMaybeRecord()
	clk.now = clk.now.Add(time.Second)
	p.collectAndMaybeRecord() // no networks/handshakes change -> must not add a 2nd sample

	p.mu.Lock()
	n := len(p.stats)
	p.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected exactly 1 sample (unchanged data not re-recorded), got %d", n)
	}
}

func TestCollectAndMaybeRecordRecordsOnChange(t *testing.T) {
	p, clk, _ := newTestPlugin(t)
	p.collectAndMaybeRecord()
	clk.now = clk.now.Add(time.Second)
	p.HandleEvent("handshake", nil)
	p.collectAndMaybeRecord()

	p.mu.Lock()
	n := len(p.stats)
	p.mu.Unlock()
	if n != 2 {
		t.Fatalf("expected 2 samples after a real change, got %d", n)
	}
}

func TestOnUnloadStopsRealtimeLoop(t *testing.T) {
	dir := t.TempDir()
	p := New()
	p.sys = &fakeSystemReader{}
	if err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"save_directory": dir, "update_interval": int64(1)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := p.OnUnload(); err != nil {
		t.Fatalf("OnUnload: %v", err)
	}
	// A second Unload must not hang/panic.
	if err := p.OnUnload(); err != nil {
		t.Fatalf("second OnUnload: %v", err)
	}
}

func TestOnWebhookRootServesDashboard(t *testing.T) {
	p, _, _ := newTestPlugin(t)
	resp, err := p.OnWebhook("", httptest.NewRequest(http.MethodGet, "/plugins/session-stats/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusOK || !strings.Contains(string(resp.Body), "Session Stats") {
		t.Fatalf("unexpected dashboard response: status=%d body_prefix=%.80s", resp.Status, resp.Body)
	}
}

func TestOnWebhookSummaryAggregatesLiveData(t *testing.T) {
	p, clk, _ := newTestPlugin(t)
	p.HandleEvent("wifi_update", []interface{}{nil, []map[string]interface{}{{"mac": "a"}, {"mac": "b"}}})
	p.collectAndMaybeRecord()
	clk.now = clk.now.Add(time.Second)
	p.HandleEvent("handshake", nil)
	p.collectAndMaybeRecord()

	resp, err := p.OnWebhook("summary", httptest.NewRequest(http.MethodGet, "/plugins/session-stats/summary", nil))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("decoding summary JSON: %v", err)
	}
	if got["handshakes"].(float64) != 1 {
		t.Fatalf("unexpected summary: %v", got)
	}
	if got["duration"] != "2s" {
		t.Fatalf("duration = %v, want 2s (sample count, matching real Python's literal len(data) quirk)", got["duration"])
	}
}

func TestOnWebhookChartEndpointsReturnSortedValues(t *testing.T) {
	p, clk, _ := newTestPlugin(t)
	p.collectAndMaybeRecord()
	clk.now = clk.now.Add(time.Second)
	p.HandleEvent("handshake", nil)
	p.collectAndMaybeRecord()

	for _, endpoint := range []string{"networks", "handshakes", "deauths", "temp", "mem", "cpu"} {
		resp, err := p.OnWebhook(endpoint, httptest.NewRequest(http.MethodGet, "/plugins/session-stats/"+endpoint, nil))
		if err != nil {
			t.Fatalf("%s: %v", endpoint, err)
		}
		var got struct {
			Values [][][2]interface{} `json:"values"`
			Labels []string           `json:"labels"`
		}
		if err := json.Unmarshal(resp.Body, &got); err != nil {
			t.Fatalf("%s: decoding: %v", endpoint, err)
		}
		if len(got.Values) != 1 || len(got.Values[0]) != 2 {
			t.Fatalf("%s: expected 1 series of 2 points, got %+v", endpoint, got.Values)
		}
	}
}

func TestOnWebhookSessionsListsFiles(t *testing.T) {
	p, _, _ := newTestPlugin(t)
	// The session file isn't written to disk until the first real sample
	// is persisted (matches real Python's StatusFile, which never creates
	// a file eagerly either — see loadHistorical's doc comment).
	p.collectAndMaybeRecord()
	resp, err := p.OnWebhook("sessions", httptest.NewRequest(http.MethodGet, "/plugins/session-stats/sessions", nil))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 || !strings.HasPrefix(got.Files[0], "stats_") {
		t.Fatalf("unexpected sessions list: %v", got.Files)
	}
}

func TestOnWebhookUnknownPathReturnsErrorJSON(t *testing.T) {
	p, _, _ := newTestPlugin(t)
	resp, err := p.OnWebhook("bogus", httptest.NewRequest(http.MethodGet, "/plugins/session-stats/bogus", nil))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatal(err)
	}
	if got["error"] == "" {
		t.Fatalf("expected an error field, got %v", got)
	}
}

func TestOnWebhookNamedSessionScopesToThatFile(t *testing.T) {
	p, _, _ := newTestPlugin(t)
	p.mu.Lock()
	saveDir := p.saveDir
	p.mu.Unlock()
	writeSessionFile(t, filepath.Join(saveDir, "stats_old.json"), map[string]StatsEntry{
		"01:00:00.000": {NumPeers: 9, NumHandshakes: 4},
	})

	resp, err := p.OnWebhook("summary", httptest.NewRequest(http.MethodGet, "/plugins/session-stats/summary?session=stats_old.json", nil))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatal(err)
	}
	if got["handshakes"].(float64) != 4 {
		t.Fatalf("expected the named session's data, got %v", got)
	}
}

func TestExtractKeyValuesSortsChronologically(t *testing.T) {
	data := map[string]StatsEntry{
		"10:00:02.000": {NumPeers: 3},
		"10:00:01.000": {NumPeers: 1},
		"10:00:03.000": {NumPeers: 2},
	}
	out := extractKeyValues(data, "num_peers")
	values := out["values"].([][][2]interface{})[0]
	// Sorted by timestamp: 10:00:01(NumPeers=1), 10:00:02(NumPeers=3), 10:00:03(NumPeers=2).
	want := []float64{1, 3, 2}
	for i, w := range want {
		if values[i][1].(float64) != w {
			t.Fatalf("index %d: got %v, want %v (values=%v)", i, values[i][1], w, values)
		}
	}
}
