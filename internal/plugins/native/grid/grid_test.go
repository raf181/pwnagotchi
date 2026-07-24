package grid

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/config"
	realgrid "github.com/jayofelony/pwnagotchi/internal/grid"
)

type fakeClient struct {
	mu           sync.Mutex
	reportCalls  []struct{ essid, bssid string }
	reportResult bool
	inboxResult  interface{}
	inboxErr     error
	updateCfg    config.Map
	updateErr    error
}

func (f *fakeClient) ReportAP(essid, bssid string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reportCalls = append(f.reportCalls, struct{ essid, bssid string }{essid, bssid})
	return f.reportResult
}
func (f *fakeClient) Inbox(page int, withPager bool) (interface{}, error) {
	return f.inboxResult, f.inboxErr
}
func (f *fakeClient) UpdateData(cfg config.Map, session realgrid.SessionSummary) error {
	f.mu.Lock()
	f.updateCfg = cfg
	f.mu.Unlock()
	return f.updateErr
}

var _ GridClient = (*fakeClient)(nil)

func newTestReportedPath(t *testing.T) string {
	t.Helper()
	orig := ReportedPath
	ReportedPath = filepath.Join(t.TempDir(), "api-report.json")
	t.Cleanup(func() { ReportedPath = orig })
	return ReportedPath
}

func fullCfg(handshakesDir string, whitelist []interface{}, reportEnabled bool) config.Map {
	return config.Map{
		"main": config.Map{
			"whitelist": whitelist,
			"plugins": config.Map{
				"grid": config.Map{"report": reportEnabled},
			},
		},
		"bettercap": config.Map{"handshakes": handshakesDir},
	}
}

func TestParsePcapFilenameWithEssidAndBssid(t *testing.T) {
	essid, bssid := parsePcapFilename("homewifi_aabbccddeeff.pcap")
	if essid != "homewifi" || bssid != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("got essid=%q bssid=%q", essid, bssid)
	}
}

func TestParsePcapFilenameBssidOnly(t *testing.T) {
	essid, bssid := parsePcapFilename("aabbccddeeff.pcap")
	if essid != "" || bssid != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("got essid=%q bssid=%q", essid, bssid)
	}
}

func TestParsePcapFilenameInvalidMacReturnsEmpty(t *testing.T) {
	essid, bssid := parsePcapFilename("not-a-mac.pcap")
	if essid != "" || bssid != "" {
		t.Fatalf("expected empty result for invalid mac, got essid=%q bssid=%q", essid, bssid)
	}
}

func TestOnConfigChangedParsesOptions(t *testing.T) {
	newTestReportedPath(t)
	dir := t.TempDir()
	p := New(&fakeClient{}, nil)
	p.HandleEvent("config_changed", []interface{}{fullCfg(dir, []interface{}{"AA:BB:CC"}, true)})

	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.reportEnabled {
		t.Fatal("expected report enabled")
	}
	if len(p.whitelist) != 1 || p.whitelist[0] != "AA:BB:CC" {
		t.Fatalf("unexpected whitelist: %v", p.whitelist)
	}
	if p.handshakesDir != dir {
		t.Fatalf("handshakesDir = %q, want %q", p.handshakesDir, dir)
	}
}

func TestCheckHandshakesReportsNewNetworkAndPersists(t *testing.T) {
	reportedPath := newTestReportedPath(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "homewifi_aabbccddeeff.pcap"), []byte("x"), 0o644)

	client := &fakeClient{reportResult: true}
	p := New(client, nil)
	p.HandleEvent("config_changed", []interface{}{fullCfg(dir, nil, true)})
	p.checkHandshakes()

	client.mu.Lock()
	calls := client.reportCalls
	client.mu.Unlock()
	if len(calls) != 1 || calls[0].essid != "homewifi" || calls[0].bssid != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("unexpected report calls: %+v", calls)
	}

	names, err := loadReportedList(reportedPath)
	if err != nil || len(names) != 1 || names[0] != "homewifi_aabbccddeeff" {
		t.Fatalf("expected persisted report list, got %v err=%v", names, err)
	}

	// Second run must not re-report (already in p.reported).
	p.checkHandshakes()
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.reportCalls) != 1 {
		t.Fatalf("expected no re-report on second run, got %d calls", len(client.reportCalls))
	}
}

func TestCheckHandshakesSkipsReportDisabled(t *testing.T) {
	newTestReportedPath(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "net_aabbccddeeff.pcap"), []byte("x"), 0o644)

	client := &fakeClient{reportResult: true}
	p := New(client, nil)
	p.HandleEvent("config_changed", []interface{}{fullCfg(dir, nil, false)})
	p.checkHandshakes()

	if len(client.reportCalls) != 0 {
		t.Fatal("expected no reporting when report=false")
	}
}

func TestCheckHandshakesRespectsWhitelist(t *testing.T) {
	newTestReportedPath(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "homewifi_aabbccddeeff.pcap"), []byte("x"), 0o644)

	client := &fakeClient{reportResult: true}
	p := New(client, nil)
	p.HandleEvent("config_changed", []interface{}{fullCfg(dir, []interface{}{"homewifi"}, true)})
	p.checkHandshakes()

	if len(client.reportCalls) != 0 {
		t.Fatal("expected whitelisted network to be skipped, not reported")
	}
}

func TestOnInternetAvailableCallsUpdateDataAndInbox(t *testing.T) {
	newTestReportedPath(t)
	dir := t.TempDir()
	client := &fakeClient{
		inboxResult: []interface{}{
			map[string]interface{}{"seen_at": nil},
			map[string]interface{}{"seen_at": "2026-01-01"},
		},
	}
	p := New(client, nil)
	p.HandleEvent("config_changed", []interface{}{fullCfg(dir, nil, false)})
	p.HandleEvent("internet_available", nil)

	client.mu.Lock()
	cfg := client.updateCfg
	client.mu.Unlock()
	if cfg == nil {
		t.Fatal("expected UpdateData to be called with the full config")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unreadMessages != 1 || p.totalMessages != 2 {
		t.Fatalf("unread=%d total=%d, want 1/2", p.unreadMessages, p.totalMessages)
	}
}

func TestOnInternetAvailableSkipsInboxWhenUpdateDataFails(t *testing.T) {
	newTestReportedPath(t)
	client := &fakeClient{updateErr: assertErr{}}
	p := New(client, nil)
	p.HandleEvent("config_changed", []interface{}{fullCfg(t.TempDir(), nil, false)})
	p.HandleEvent("internet_available", nil)

	client.mu.Lock()
	defer client.mu.Unlock()
	if client.inboxResult != nil {
		t.Fatal("inbox should not be consulted if UpdateData failed")
	}
}

type assertErr struct{}

func (assertErr) Error() string { return "boom" }

func TestOnWebhookRedirectsToOpwngrid(t *testing.T) {
	p := New(&fakeClient{}, nil)
	resp, err := p.OnWebhook("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 302 || resp.Headers["Location"] != "https://opwngrid.xyz" {
		t.Fatalf("unexpected webhook response: %+v", resp)
	}
}

func TestSessionSummaryFuncUsedWhenProvided(t *testing.T) {
	newTestReportedPath(t)
	called := false
	client := &fakeClient{}
	p := New(client, func() realgrid.SessionSummary {
		called = true
		return realgrid.SessionSummary{Handshakes: 3}
	})
	p.HandleEvent("config_changed", []interface{}{fullCfg(t.TempDir(), nil, false)})
	p.HandleEvent("internet_available", nil)
	if !called {
		t.Fatal("expected the injected session summary func to be called")
	}
}
