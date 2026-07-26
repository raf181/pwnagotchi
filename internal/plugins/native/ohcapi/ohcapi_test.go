package ohcapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type recordedCmd struct {
	name string
	args []string
}

type fakeRunner struct {
	mu    sync.Mutex
	cmds  []recordedCmd
	onRun func(name string, args []string)
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.cmds = append(f.cmds, recordedCmd{name, args})
	cb := f.onRun
	f.mu.Unlock()
	if cb != nil {
		cb(name, args)
	}
	return nil, nil
}

type fakeView struct {
	mu          sync.Mutex
	uploadCalls []string
	normalCalls int
}

func (f *fakeView) Set(string, string)                                                   {}
func (f *fakeView) Update(bool)                                                          {}
func (f *fakeView) Kind() string                                                         { return "dummydisplay" }
func (f *fakeView) HasElement(string) bool                                               { return false }
func (f *fakeView) RemoveElement(string)                                                 {}
func (f *fakeView) AddText(string, string, int, int, pluginmanager.FontStyle, bool, int) {}
func (f *fakeView) AddLabeledValue(string, string, string, int, int, pluginmanager.FontStyle, pluginmanager.FontStyle, int) {
}
func (f *fakeView) OnUploading(to string) {
	f.mu.Lock()
	f.uploadCalls = append(f.uploadCalls, to)
	f.mu.Unlock()
}
func (f *fakeView) OnNormal()   { f.mu.Lock(); f.normalCalls++; f.mu.Unlock() }
func (f *fakeView) Width() int  { return 250 }
func (f *fakeView) Height() int { return 122 }

var _ pluginmanager.ViewCapability = (*fakeView)(nil)
var _ pluginmanager.CommandRunner = (*fakeRunner)(nil)
var _ pluginmanager.Clock = (*fakeClock)(nil)

func resetOverrides(t *testing.T) {
	t.Helper()
	origReport, origAdd, origCheck, origRedirect := reportPath, addTasksURLVar, internetCheckURLVar, webhookRedirectVar
	t.Cleanup(func() {
		reportPath = origReport
		addTasksURLVar = origAdd
		internetCheckURLVar = origCheck
		webhookRedirectVar = origRedirect
	})
	reportPath = filepath.Join(t.TempDir(), ".ohc_uploads")
}

func newLoadedPlugin(t *testing.T, apiKey string) (*Plugin, *fakeRunner, *fakeView, *fakeClock) {
	t.Helper()
	resetOverrides(t)
	runner := &fakeRunner{}
	view := &fakeView{}
	clock := &fakeClock{now: time.Now()}
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"api_key": apiKey},
		Exec:   runner,
		View:   view,
		Clock:  clock,
	}); err != nil {
		t.Fatal(err)
	}
	return p, runner, view, clock
}

func TestOnLoadRequiresAPIKey(t *testing.T) {
	p, _, _, _ := newLoadedPlugin(t, "")
	if p.ready {
		t.Fatal("expected plugin to remain not-ready without api_key")
	}
}

func TestOnLoadReadyWithAPIKeyAndDefaults(t *testing.T) {
	p, _, _, _ := newLoadedPlugin(t, "key123")
	if !p.ready {
		t.Fatal("expected plugin ready with api_key set")
	}
	if p.receiveEmail != "yes" {
		t.Fatalf("receiveEmail default = %q, want yes", p.receiveEmail)
	}
	if p.sleep != defaultSleep {
		t.Fatalf("sleep default = %v, want %v", p.sleep, defaultSleep)
	}
}

func TestExtractEssidBssidFromHashNormal(t *testing.T) {
	essidHex := "68656c6c6f" // "hello"
	// build with correct field indices: 0..5 -> field[3]=mac field[5]=essidhex
	parts := []string{"WPA", "02", "hash", "aabbccddeeff", "sta", essidHex}
	line := joinStar(parts)
	essid, bssid := extractEssidBssidFromHash(line)
	if essid != "hello" {
		t.Fatalf("essid = %q, want hello", essid)
	}
	if bssid != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("bssid = %q, want aa:bb:cc:dd:ee:ff", bssid)
	}
}

func joinStar(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "*"
		}
		out += p
	}
	return out
}

func TestExtractEssidBssidFromHashMalformed(t *testing.T) {
	essid, bssid := extractEssidBssidFromHash("not-enough-fields")
	if essid != "unknown_ESSID" || bssid != "00:00:00:00:00:00" {
		t.Fatalf("expected fallback values, got %q %q", essid, bssid)
	}
	// Invalid hex in ESSID field, invalid-length MAC field.
	parts := []string{"a", "b", "c", "short", "d", "zzzz"}
	essid, bssid = extractEssidBssidFromHash(joinStar(parts))
	if essid != "unknown_ESSID" {
		t.Fatalf("expected unknown_ESSID for bad hex, got %q", essid)
	}
	if bssid != "00:00:00:00:00:00" {
		t.Fatalf("expected fallback bssid for short mac, got %q", bssid)
	}
}

func TestOnWebhookRedirects(t *testing.T) {
	resetOverrides(t)
	p := New()
	resp, err := p.OnWebhook("anything", httptest.NewRequest(http.MethodGet, "/plugins/ohcapi/anything", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusFound || resp.Headers["Location"] != webhookRedirect {
		t.Fatalf("unexpected webhook response: %+v", resp)
	}
}

func TestExtractHashesFromHandshakeInvokesHcxpcapngtool(t *testing.T) {
	p, runner, _, _ := newLoadedPlugin(t, "key123")
	dir := t.TempDir()
	pcapPath := filepath.Join(dir, "net.pcap")
	os.WriteFile(pcapPath, []byte("data"), 0o644)

	hccapx := filepath.Join(dir, "net.22000")
	runner.onRun = func(name string, args []string) {
		os.WriteFile(hccapx, []byte("WPA*hash1\nWPA*hash2\n"), 0o644)
	}

	hashes := p.extractHashesFromHandshake(pcapPath)
	if len(runner.cmds) != 1 || runner.cmds[0].name != "hcxpcapngtool" {
		t.Fatalf("expected hcxpcapngtool invocation, got %+v", runner.cmds)
	}
	if runner.cmds[0].args[0] != "-o" || runner.cmds[0].args[1] != hccapx || runner.cmds[0].args[2] != pcapPath {
		t.Fatalf("unexpected argv: %v", runner.cmds[0].args)
	}
	if len(hashes) != 2 {
		t.Fatalf("expected 2 hash lines, got %v", hashes)
	}
}

func TestExtractHashesFromHandshakeNoOutputReturnsEmpty(t *testing.T) {
	p, _, _, _ := newLoadedPlugin(t, "key123")
	dir := t.TempDir()
	pcapPath := filepath.Join(dir, "net.pcap")
	os.WriteFile(pcapPath, []byte("data"), 0o644)
	// no runner side effect: hcxpcapngtool "produces nothing"
	hashes := p.extractHashesFromHandshake(pcapPath)
	if hashes != nil {
		t.Fatalf("expected no hashes, got %v", hashes)
	}
}

func TestRunTasksUploadsAndPersistsReportedState(t *testing.T) {
	p, runner, view, _ := newLoadedPlugin(t, "key123")
	dir := t.TempDir()
	pcapPath := filepath.Join(dir, "net_a.pcap")
	os.WriteFile(pcapPath, []byte("data"), 0o644)
	hccapx := filepath.Join(dir, "net_a.22000")
	essidHex := "68656c6c6f"
	line := joinStar([]string{"WPA", "02", "hash", "aabbccddeeff", "sta", essidHex})
	runner.onRun = func(name string, args []string) {
		os.WriteFile(hccapx, []byte(line+"\n"), 0o644)
	}

	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	addTasksURLVar = srv.URL

	p.onConfigChanged([]interface{}{config.Map{"bettercap": config.Map{"handshakes": dir}}})
	p.runTasks()

	if gotBody["api_key"] != "key123" {
		t.Fatalf("expected api_key in upload payload, got %v", gotBody)
	}
	if view.normalCalls != 1 || len(view.uploadCalls) != 1 {
		t.Fatalf("expected 1 OnUploading + 1 OnNormal call, got %+v / %d", view.uploadCalls, view.normalCalls)
	}

	st, err := loadReportState()
	if err != nil {
		t.Fatalf("expected persisted report state: %v", err)
	}
	if len(st.Reported) != 1 || st.Reported[0] != pcapPath {
		t.Fatalf("expected pcap marked reported, got %v", st.Reported)
	}
	if len(st.ProcessedStations) != 1 || st.ProcessedStations[0] != [2]string{"hello", "aa:bb:cc:dd:ee:ff"} {
		t.Fatalf("expected processed station recorded, got %v", st.ProcessedStations)
	}

	// Second run: the file is now genuinely converted (hccapx exists) so it
	// must never be re-processed.
	runner.cmds = nil
	p.runTasks()
	if len(runner.cmds) != 0 {
		t.Fatalf("expected no re-processing of an already-extracted pcap, got %+v", runner.cmds)
	}
}

func TestRunTasksSkipsAlreadyReported(t *testing.T) {
	p, runner, _, _ := newLoadedPlugin(t, "key123")
	dir := t.TempDir()
	pcapPath := filepath.Join(dir, "net_b.pcap")
	os.WriteFile(pcapPath, []byte("data"), 0o644)
	p.report.Reported = []string{pcapPath}

	p.onConfigChanged([]interface{}{config.Map{"bettercap": config.Map{"handshakes": dir}}})
	p.runTasks()
	if len(runner.cmds) != 0 {
		t.Fatalf("expected no hcxpcapngtool call for an already-reported pcap, got %+v", runner.cmds)
	}
}

func TestRunTasksUploadFailureMarksSkipNotReported(t *testing.T) {
	p, runner, _, _ := newLoadedPlugin(t, "key123")
	dir := t.TempDir()
	pcapPath := filepath.Join(dir, "net_c.pcap")
	os.WriteFile(pcapPath, []byte("data"), 0o644)
	hccapx := filepath.Join(dir, "net_c.22000")
	line := joinStar([]string{"WPA", "02", "hash", "aabbccddeeff", "sta", "68656c6c6f"})
	runner.onRun = func(name string, args []string) {
		os.WriteFile(hccapx, []byte(line+"\n"), 0o644)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	addTasksURLVar = srv.URL

	p.onConfigChanged([]interface{}{config.Map{"bettercap": config.Map{"handshakes": dir}}})
	p.runTasks()

	if p.skip[pcapPath] != true {
		t.Fatal("expected failed upload to mark the pcap as skip")
	}
	if len(p.report.Reported) != 0 {
		t.Fatalf("expected nothing marked reported on upload failure, got %v", p.report.Reported)
	}
}

func TestOnInternetAvailableTriggersRunOnceAndRateLimits(t *testing.T) {
	p, runner, _, clock := newLoadedPlugin(t, "key123")
	dir := t.TempDir()
	p.onConfigChanged([]interface{}{config.Map{"bettercap": config.Map{"handshakes": dir}}})

	p.HandleEvent("internet_available", nil)
	if len(runner.cmds) != 0 {
		// no pcaps, so no commands — just confirm it ran without error
	}
	firstLastRun := p.lastRun
	if firstLastRun.IsZero() {
		t.Fatal("expected lastRun to be set after internet_available")
	}
	clock.advance(time.Second)
	p.HandleEvent("internet_available", nil)
	if p.lastRun.Sub(firstLastRun) < time.Second {
		t.Fatal("expected lastRun to advance on a second internet_available call")
	}
}

func TestNotReadyIgnoresEvents(t *testing.T) {
	p, runner, _, _ := newLoadedPlugin(t, "") // not ready: no api_key
	p.HandleEvent("internet_available", nil)
	p.HandleEvent("ui_update", nil)
	if len(runner.cmds) != 0 {
		t.Fatal("expected a not-ready plugin to never run tasks")
	}
}
