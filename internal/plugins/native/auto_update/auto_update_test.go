package autoupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

type recordedCmd struct {
	name string
	args []string
}

type fakeRunner struct {
	mu      sync.Mutex
	cmds    []recordedCmd
	which   map[string]string
	failing map[string]bool
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{which: map[string]string{}, failing: map[string]bool{}}
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.cmds = append(f.cmds, recordedCmd{name, args})
	f.mu.Unlock()
	if name == "which" && len(args) == 1 {
		if p, ok := f.which[args[0]]; ok {
			return []byte(p), nil
		}
		return nil, fmt.Errorf("not found")
	}
	if name == "bettercap" || name == "pwngrid" {
		return []byte(name + " v1.0.0"), nil
	}
	return nil, nil
}

func (f *fakeRunner) snapshot() []recordedCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedCmd(nil), f.cmds...)
}

type fakeView struct {
	mu       sync.Mutex
	statuses []string
}

func (v *fakeView) Set(key, value string) {
	if key == "status" {
		v.mu.Lock()
		v.statuses = append(v.statuses, value)
		v.mu.Unlock()
	}
}
func (v *fakeView) Update(bool)                                                          {}
func (v *fakeView) Kind() string                                                         { return "dummydisplay" }
func (v *fakeView) HasElement(string) bool                                               { return false }
func (v *fakeView) RemoveElement(string)                                                 {}
func (v *fakeView) AddText(string, string, int, int, pluginmanager.FontStyle, bool, int) {}
func (v *fakeView) AddLabeledValue(string, string, string, int, int, pluginmanager.FontStyle, pluginmanager.FontStyle, int) {
}
func (v *fakeView) OnUploading(string) {}
func (v *fakeView) OnNormal()          {}
func (v *fakeView) Width() int         { return 250 }
func (v *fakeView) Height() int        { return 122 }

type fakeSystem struct {
	mu          sync.Mutex
	rebootCalls int
}

func (s *fakeSystem) Shutdown() error { return nil }
func (s *fakeSystem) Reboot(string) error {
	s.mu.Lock()
	s.rebootCalls++
	s.mu.Unlock()
	return nil
}
func (s *fakeSystem) Restart(string) error { return nil }

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newTestPlugin(t *testing.T) (*Plugin, string) {
	t.Helper()
	dir := t.TempDir()
	orig := StatePath
	StatePath = filepath.Join(dir, ".auto-update")
	t.Cleanup(func() { StatePath = orig })
	p := New()
	return p, dir
}

func TestOnLoadRequiresInterval(t *testing.T) {
	p, _ := newTestPlugin(t)
	if err := p.OnLoad(pluginmanager.Capabilities{Config: map[string]interface{}{}}); err != nil {
		t.Fatal(err)
	}
	if p.ready {
		t.Fatal("expected plugin to remain not-ready with no interval configured")
	}
}

func TestOnLoadReadyWithInterval(t *testing.T) {
	p, _ := newTestPlugin(t)
	if err := p.OnLoad(pluginmanager.Capabilities{Config: map[string]interface{}{"interval": int64(24)}}); err != nil {
		t.Fatal(err)
	}
	if !p.ready {
		t.Fatal("expected plugin ready with a configured interval")
	}
}

func githubReleaseJSON(tag string, assetURLs ...string) string {
	type asset struct {
		URL string `json:"browser_download_url"`
	}
	var assets []asset
	for _, u := range assetURLs {
		assets = append(assets, asset{URL: u})
	}
	body, _ := json.Marshal(struct {
		TagName string  `json:"tag_name"`
		Assets  []asset `json:"assets"`
	}{TagName: tag, Assets: assets})
	return string(body)
}

func TestCheckPicksArchMatchingAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(githubReleaseJSON("v2.0.0",
			"https://example.com/tool_armv7l.zip",
			"https://example.com/tool_aarch64.zip",
		)))
	}))
	defer srv.Close()

	p := New()
	p.arch = "aarch64"
	p.httpClient = srv.Client()
	p.githubAPIBase(srv.URL)

	info, err := p.check("1.0.0", "some/repo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(info.URL, "aarch64") {
		t.Fatalf("expected aarch64 asset picked, got %q", info.URL)
	}
	if info.Available != "2.0.0" {
		t.Fatalf("Available = %q, want 2.0.0", info.Available)
	}
}

func TestCheckNoUpdateWhenLocalIsNewerOrEqual(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(githubReleaseJSON("v1.0.0", "https://example.com/tool_aarch64.zip")))
	}))
	defer srv.Close()

	p := New()
	p.arch = "aarch64"
	p.httpClient = srv.Client()
	p.githubAPIBase(srv.URL)

	info, err := p.check("1.0.0", "some/repo")
	if err != nil {
		t.Fatal(err)
	}
	if info.URL != "" {
		t.Fatalf("expected no update URL when local >= remote, got %q", info.URL)
	}
}

func TestCheckHandlesNon200Response(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	p := New()
	p.httpClient = srv.Client()
	p.githubAPIBase(srv.URL)

	if _, err := p.check("1.0.0", "some/repo"); err == nil {
		t.Fatal("expected an error on a non-200 GitHub API response")
	}
}

func buildReleaseZip(t *testing.T, binaryName string, binaryData []byte, includeChecksum bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create(binaryName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(binaryData); err != nil {
		t.Fatal(err)
	}
	if includeChecksum {
		sum := sha256.Sum256(binaryData)
		cf, err := zw.Create(binaryName + ".sha256")
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(cf, "%s  %s\n", hex.EncodeToString(sum[:]), binaryName)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInstallUpdateDownloadsVerifiesAndSwapsBinary(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, "bettercap")
	if err := os.WriteFile(destPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	newBinary := []byte("shiny-new-binary-content")
	zipData := buildReleaseZip(t, "bettercap", newBinary, true)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(zipData)
	}))
	defer srv.Close()

	runner := newFakeRunner()
	runner.which["bettercap"] = destPath
	view := &fakeView{}

	p := New()
	p.exec = runner
	p.httpClient = srv.Client()
	p.view = view
	p.log = nil

	update := UpdateInfo{Repo: "jayofelony/bettercap", Available: "2.0.0", URL: srv.URL, Service: "bettercap"}
	if err := p.installUpdate(update); err != nil {
		t.Fatalf("installUpdate: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newBinary) {
		t.Fatalf("binary content = %q, want %q", got, newBinary)
	}
	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatal("expected the new binary to be executable")
	}

	cmds := runner.snapshot()
	foundStop, foundStart := false, false
	for _, c := range cmds {
		if c.name == "systemctl" && len(c.args) == 2 && c.args[0] == "stop" && c.args[1] == "bettercap" {
			foundStop = true
		}
		if c.name == "systemctl" && len(c.args) == 2 && c.args[0] == "start" && c.args[1] == "bettercap" {
			foundStart = true
		}
		if c.name == "pip" || c.name == "wget" || c.name == "unzip" || c.name == "sha256sum" {
			t.Fatalf("must never shell out to %q — download/extract/verify must be native Go", c.name)
		}
	}
	if !foundStop || !foundStart {
		t.Fatalf("expected systemctl stop+start bettercap, got %+v", cmds)
	}
}

func TestInstallUpdateFailsOnChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, "bettercap")
	os.WriteFile(destPath, []byte("old"), 0o755)

	zipData := buildReleaseZip(t, "bettercap", []byte("real-content"), true)
	// Corrupt the zip's checksum file by rebuilding with mismatched data.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	bf, _ := zw.Create("bettercap")
	bf.Write([]byte("real-content"))
	cf, _ := zw.Create("bettercap.sha256")
	fmt.Fprintf(cf, "%s  bettercap\n", strings.Repeat("0", 64))
	zw.Close()
	zipData = buf.Bytes()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(zipData) }))
	defer srv.Close()

	runner := newFakeRunner()
	runner.which["bettercap"] = destPath
	p := New()
	p.exec = runner
	p.httpClient = srv.Client()
	p.view = &fakeView{}

	update := UpdateInfo{Repo: "jayofelony/bettercap", Available: "2.0.0", URL: srv.URL, Service: "bettercap"}
	if err := p.installUpdate(update); err == nil {
		t.Fatal("expected a checksum-mismatch error")
	}

	got, _ := os.ReadFile(destPath)
	if string(got) != "old" {
		t.Fatal("expected the original binary to remain untouched after a checksum failure")
	}
}

func TestCheckAndInstallRateLimitedByStateFile(t *testing.T) {
	p, _ := newTestPlugin(t)
	clock := &fakeClock{now: time.Now()}
	p.clock = clock
	p.writeState()

	if !p.newerThanHours(24) {
		t.Fatal("expected a just-written state file to count as newer than 24h ago")
	}
	clock.advance(25 * time.Hour)
	if p.newerThanHours(24) {
		t.Fatal("expected the state file to age out after the interval elapses")
	}
}

// TestHandleEventRebootsAfterSuccessfulInstall drives the full real path:
// GitHub release check -> download -> checksum verify -> binary swap ->
// systemctl restart -> reboot, against one httptest server serving both
// the release-metadata JSON (path contains "/repos/") and the release zip
// itself (any other path) — a single self-contained fake, no real network.
func TestHandleEventRebootsAfterSuccessfulInstall(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, "bettercap")
	os.WriteFile(destPath, []byte("old"), 0o755)

	newBinary := []byte("new-bettercap-binary")
	zipData := buildReleaseZip(t, "bettercap", newBinary, true)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/repos/") {
			w.Write([]byte(githubReleaseJSON("v9.9.9", srv.URL+"/download/bettercap_aarch64.zip")))
			return
		}
		w.Write(zipData)
	}))
	defer srv.Close()

	runner := newFakeRunner()
	runner.which["bettercap"] = destPath

	view := &fakeView{}
	sys := &fakeSystem{}
	var emitted []string

	p, _ := newTestPlugin(t)
	p.arch = "aarch64"
	p.repoSpecs = []repoSpec{
		{repo: "jayofelony/bettercap", serviceName: "bettercap", localVer: func(*Plugin) string { return "1.0.0" }},
	}

	if err := p.OnLoad(pluginmanager.Capabilities{
		Config:     map[string]interface{}{"interval": int64(24), "install": true},
		Exec:       runner,
		HTTPClient: srv.Client(),
		View:       view,
		System:     sys,
		Emit:       func(event string, args ...interface{}) { emitted = append(emitted, event) },
	}); err != nil {
		t.Fatal(err)
	}
	p.githubAPIBase(srv.URL)

	p.HandleEvent("internet_available", nil)

	if sys.rebootCalls != 1 {
		t.Fatalf("expected exactly 1 reboot after a successful install, got %d", sys.rebootCalls)
	}
	if len(emitted) == 0 || emitted[0] != "updating" {
		t.Fatalf("expected an 'updating' event to be emitted, got %v", emitted)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newBinary) {
		t.Fatalf("binary content = %q, want %q", got, newBinary)
	}
}
