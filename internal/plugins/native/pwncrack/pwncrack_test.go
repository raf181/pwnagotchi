package pwncrack

import (
	"context"
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

type recordedCmd struct {
	name string
	args []string
}

type fakeRunner struct {
	mu   sync.Mutex
	cmds []recordedCmd
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.cmds = append(f.cmds, recordedCmd{name, args})
	f.mu.Unlock()
	return nil, nil
}

func (f *fakeRunner) snapshot() []recordedCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedCmd(nil), f.cmds...)
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func newTestPlugin(t *testing.T, serverURL, potfileURL string) (*Plugin, *fakeRunner, *fakeClock) {
	t.Helper()
	p := New()
	p.serverURL = serverURL
	p.potfileURL = potfileURL
	runner := &fakeRunner{}
	fc := &fakeClock{now: time.Now()}
	if err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"key": "test-key"},
		Exec:   runner,
		Clock:  fc,
	}); err != nil {
		t.Fatal(err)
	}
	return p, runner, fc
}

func configChangedArgs(handshakeDir string, whitelist []interface{}) []interface{} {
	return []interface{}{config.Map{
		"bettercap": config.Map{"handshakes": handshakeDir},
		"main":      config.Map{"whitelist": whitelist},
	}}
}

func TestConfigChangedSetsPaths(t *testing.T) {
	p, _, _ := newTestPlugin(t, "http://example.invalid", "http://example.invalid")
	dir := t.TempDir()
	p.HandleEvent("config_changed", configChangedArgs(dir, nil))

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.handshakeDir != dir {
		t.Fatalf("handshakeDir = %q, want %q", p.handshakeDir, dir)
	}
	if p.combinedFile != filepath.Join(dir, "combined.hc22000") {
		t.Fatalf("combinedFile = %q", p.combinedFile)
	}
}

func TestInternetAvailableConvertsUploadsAndDownloadsPotfile(t *testing.T) {
	var gotKey, gotFileName string
	var gotFileData []byte
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		gotKey = r.FormValue("key")
		file, header, err := r.FormFile("handshake")
		if err != nil {
			t.Errorf("FormFile: %v", err)
		} else {
			defer file.Close()
			gotFileName = header.Filename
			buf := make([]byte, 1024)
			n, _ := file.Read(buf)
			gotFileData = buf[:n]
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer uploadSrv.Close()

	potfileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("key"); got != "test-key" {
			t.Errorf("potfile request key = %q, want test-key", got)
		}
		w.Write([]byte("hash:password\n"))
	}))
	defer potfileSrv.Close()

	p, runner, _ := newTestPlugin(t, uploadSrv.URL, potfileSrv.URL)
	dir := t.TempDir()
	p.HandleEvent("config_changed", configChangedArgs(dir, nil))

	pcapPath := filepath.Join(dir, "net_aabbcc.pcap")
	if err := os.WriteFile(pcapPath, []byte("pcap-data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// hcxpcapngtool isn't really installed in this test environment; the
	// fake runner just records the call and the plugin falls back to
	// writing an empty combined file when hcxpcapngtool doesn't actually
	// produce one, matching Python's `if not os.path.exists(...)` guard.
	p.HandleEvent("internet_available", nil)

	cmds := runner.snapshot()
	if len(cmds) != 1 || cmds[0].name != "hcxpcapngtool" {
		t.Fatalf("expected exactly 1 hcxpcapngtool call, got %+v", cmds)
	}
	wantArgs := []string{"-o", filepath.Join(dir, "combined.hc22000"), pcapPath}
	if strings.Join(cmds[0].args, " ") != strings.Join(wantArgs, " ") {
		t.Fatalf("hcxpcapngtool args = %v, want %v", cmds[0].args, wantArgs)
	}

	if gotKey != "test-key" {
		t.Fatalf("uploaded key = %q, want test-key", gotKey)
	}
	if gotFileName != "combined.hc22000" {
		t.Fatalf("uploaded filename = %q", gotFileName)
	}
	_ = gotFileData

	potfilePath := filepath.Join(dir, "cracked.pwncrack.potfile")
	data, err := os.ReadFile(potfilePath)
	if err != nil {
		t.Fatalf("expected potfile written: %v", err)
	}
	if string(data) != "hash:password\n" {
		t.Fatalf("potfile contents = %q", data)
	}

	if _, err := os.Stat(filepath.Join(dir, "combined.hc22000")); !os.IsNotExist(err) {
		t.Fatal("expected the combined file to be removed after upload")
	}
}

func TestInternetAvailableSkipsWhitelistedFiles(t *testing.T) {
	uploadCalled := false
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploadCalled = true
		w.Write([]byte("{}"))
	}))
	defer uploadSrv.Close()
	potfileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(""))
	}))
	defer potfileSrv.Close()

	p, runner, _ := newTestPlugin(t, uploadSrv.URL, potfileSrv.URL)
	dir := t.TempDir()
	p.HandleEvent("config_changed", configChangedArgs(dir, []interface{}{"whitelisted"}))

	if err := os.WriteFile(filepath.Join(dir, "net_whitelisted.pcap"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	p.HandleEvent("internet_available", nil)

	if len(runner.snapshot()) != 0 {
		t.Fatalf("expected hcxpcapngtool never called for a whitelisted-only set, got %+v", runner.snapshot())
	}
	// Real Python's `if pcap_files:` guard means the whole upload step
	// (and therefore any HTTP call) is skipped entirely when every
	// present .pcap is whitelisted, not just the conversion step.
	if uploadCalled {
		t.Fatal("expected no upload HTTP call when all pcap files are whitelisted")
	}
}

func TestInternetAvailableRateLimited(t *testing.T) {
	callCount := 0
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Write([]byte("{}"))
	}))
	defer uploadSrv.Close()
	potfileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(""))
	}))
	defer potfileSrv.Close()

	p, runner, fc := newTestPlugin(t, uploadSrv.URL, potfileSrv.URL)
	dir := t.TempDir()
	p.HandleEvent("config_changed", configChangedArgs(dir, nil))
	os.WriteFile(filepath.Join(dir, "net_a.pcap"), []byte("x"), 0o644)

	p.HandleEvent("internet_available", nil)
	first := len(runner.snapshot())
	if first == 0 {
		t.Fatal("expected the first call to run")
	}

	fc.now = fc.now.Add(10 * time.Second) // still within the 600s timewait
	p.HandleEvent("internet_available", nil)
	if len(runner.snapshot()) != first {
		t.Fatalf("expected the second call within the timewait window to be skipped, got %d new calls", len(runner.snapshot())-first)
	}

	fc.now = fc.now.Add(700 * time.Second) // now past the window
	p.HandleEvent("internet_available", nil)
	if len(runner.snapshot()) == first {
		t.Fatal("expected a third call once the timewait window has elapsed")
	}
}

func TestPotfileDownloadFailureIsLoggedNotFatal(t *testing.T) {
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{}"))
	}))
	defer uploadSrv.Close()
	potfileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"nope"}`))
	}))
	defer potfileSrv.Close()

	p, _, _ := newTestPlugin(t, uploadSrv.URL, potfileSrv.URL)
	dir := t.TempDir()
	p.HandleEvent("config_changed", configChangedArgs(dir, nil))
	os.WriteFile(filepath.Join(dir, "net_a.pcap"), []byte("x"), 0o644)

	// Must not panic despite the 500 response.
	p.HandleEvent("internet_available", nil)

	if _, err := os.Stat(filepath.Join(dir, "cracked.pwncrack.potfile")); !os.IsNotExist(err) {
		t.Fatal("expected no potfile written on a failed download")
	}
}

func TestNoHandshakeDirIsANoOp(t *testing.T) {
	p, runner, _ := newTestPlugin(t, "http://example.invalid", "http://example.invalid")
	p.HandleEvent("internet_available", nil) // never got config_changed
	if len(runner.snapshot()) != 0 {
		t.Fatal("expected no action with no configured handshake dir")
	}
}
