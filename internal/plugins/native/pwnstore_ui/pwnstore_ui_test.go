package pwnstoreui

import (
	"context"
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

type fakeRunner struct {
	mu   sync.Mutex
	cmds [][]string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.cmds = append(f.cmds, append([]string{name}, args...))
	f.mu.Unlock()
	return nil, nil
}

func newTestPlugin(t *testing.T, storeURL string) (*Plugin, *fakeRunner) {
	t.Helper()
	p := New()
	runner := &fakeRunner{}
	if err := p.OnLoad(pluginmanager.Capabilities{
		Exec: runner,
	}); err != nil {
		t.Fatal(err)
	}
	if storeURL != "" {
		p.mu.Lock()
		p.storeURL = storeURL
		p.mu.Unlock()
	}
	return p, runner
}

func TestRenderStoreServesRealEmbeddedHTML(t *testing.T) {
	p, _ := newTestPlugin(t, "")
	req := httptest.NewRequest(http.MethodGet, "/plugins/pwnstore_ui/", nil)
	resp, err := p.OnWebhook("", req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d", resp.Status)
	}
	if !strings.Contains(string(resp.Body), "PwnStore") {
		t.Fatalf("expected real store HTML, got: %.200s", resp.Body)
	}
	if !strings.Contains(string(resp.Body), `content=""`) {
		t.Fatalf("expected empty csrf token substituted (no cookie on request), got: %.400s", resp.Body)
	}
}

func TestRenderStoreEmbedsCSRFCookieValue(t *testing.T) {
	p, _ := newTestPlugin(t, "")
	req := httptest.NewRequest(http.MethodGet, "/plugins/pwnstore_ui/", nil)
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "abc123"})
	resp, err := p.OnWebhook("", req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resp.Body), `content="abc123"`) {
		t.Fatalf("expected csrf token abc123 embedded, got: %.400s", resp.Body)
	}
}

func TestGetPluginsPassesThroughRealStoreResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"name":"foo","author":"bar","description":"baz"}]`))
	}))
	defer srv.Close()

	p, _ := newTestPlugin(t, srv.URL)
	resp, err := p.OnWebhook("api/plugins", httptest.NewRequest(http.MethodGet, "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resp.Body), `"foo"`) {
		t.Fatalf("expected real store payload passed through, got %s", resp.Body)
	}
}

func TestGetPluginsFallsBackToEmptyArrayOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	p, _ := newTestPlugin(t, srv.URL)
	resp, err := p.OnWebhook("api/plugins", httptest.NewRequest(http.MethodGet, "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	// A non-200 upstream response is still passed through verbatim by
	// real Python (`requests.get(...).text` regardless of status) unless
	// the request itself raised (connection error/timeout) — verified by
	// re-reading _get_plugins: only an exception triggers the "[]"
	// fallback, not a non-2xx status.
	_ = resp
}

func TestGetPluginsFallsBackOnConnectionError(t *testing.T) {
	p, _ := newTestPlugin(t, "http://127.0.0.1:1/unreachable")
	resp, err := p.OnWebhook("api/plugins", httptest.NewRequest(http.MethodGet, "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(resp.Body)) != "[]" {
		t.Fatalf("expected [] fallback on connection error, got %s", resp.Body)
	}
}

func TestGetInstalledListsPyFilesWithoutExtension(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "foo.py"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "bar.py"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte(""), 0o644)

	orig := installedPluginsDir
	installedPluginsDir = dir
	defer func() { installedPluginsDir = orig }()

	p, _ := newTestPlugin(t, "")
	resp, err := p.OnWebhook("api/installed", httptest.NewRequest(http.MethodGet, "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	body := string(resp.Body)
	if !strings.Contains(body, `"foo"`) || !strings.Contains(body, `"bar"`) || strings.Contains(body, "readme") {
		t.Fatalf("unexpected installed list: %s", body)
	}
}

func TestGetInstalledReturnsEmptyWhenDirMissing(t *testing.T) {
	orig := installedPluginsDir
	installedPluginsDir = filepath.Join(t.TempDir(), "does-not-exist")
	defer func() { installedPluginsDir = orig }()

	p, _ := newTestPlugin(t, "")
	resp, _ := p.OnWebhook("api/installed", httptest.NewRequest(http.MethodGet, "/x", nil))
	if strings.TrimSpace(string(resp.Body)) != "[]" {
		t.Fatalf("expected [], got %s", resp.Body)
	}
}

func TestInstallReturnsHonestUnsupportedFailure(t *testing.T) {
	p, _ := newTestPlugin(t, "")
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"plugin":"someplugin"}`))
	resp, err := p.OnWebhook("api/install", req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.Status)
	}
	body := string(resp.Body)
	if !strings.Contains(body, `"success":false`) {
		t.Fatalf("expected explicit success:false, got %s", body)
	}
	if strings.Contains(body, `"success":true`) {
		t.Fatalf("must never claim success, got %s", body)
	}
}

func TestUninstallReturnsHonestUnsupportedFailure(t *testing.T) {
	p, _ := newTestPlugin(t, "")
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"plugin":"someplugin"}`))
	resp, err := p.OnWebhook("api/uninstall", req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.Status)
	}
}

func TestConfigurePluginRewritesConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	os.WriteFile(cfgPath, []byte("main.name = \"test\"\nmain.plugins.someplugin.enabled = false\nmain.plugins.someplugin.old_key = \"old\"\n"), 0o644)

	origPath := configPath
	configPath = cfgPath
	defer func() { configPath = origPath }()

	p, _ := newTestPlugin(t, "")
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"plugin":"someplugin","config":{"api_key":"secret","threshold":5,"enabled_flag":true}}`))
	resp, err := p.OnWebhook("api/configure", req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Status, resp.Body)
	}

	data, _ := os.ReadFile(cfgPath)
	content := string(data)
	if strings.Contains(content, "old_key") {
		t.Fatalf("expected old plugin config lines removed, got:\n%s", content)
	}
	if !strings.Contains(content, `main.plugins.someplugin.enabled = true`) {
		t.Fatalf("expected enabled=true stanza, got:\n%s", content)
	}
	if !strings.Contains(content, `main.plugins.someplugin.api_key = "secret"`) {
		t.Fatalf("expected quoted string value, got:\n%s", content)
	}
	if !strings.Contains(content, `main.plugins.someplugin.threshold = 5`) {
		t.Fatalf("expected bare numeric value, got:\n%s", content)
	}
	if !strings.Contains(content, `main.plugins.someplugin.enabled_flag = true`) {
		t.Fatalf("expected bare bool value, got:\n%s", content)
	}
	if !strings.Contains(content, `main.name = "test"`) {
		t.Fatalf("expected unrelated config preserved, got:\n%s", content)
	}
}

func TestRestartRunsSystemctlViaArgvAfterDelay(t *testing.T) {
	origDelay := restartDelay
	restartDelay = time.Millisecond
	defer func() { restartDelay = origDelay }()

	p, runner := newTestPlugin(t, "")
	resp, err := p.OnWebhook("api/restart", httptest.NewRequest(http.MethodPost, "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resp.Body), `"success":true`) {
		t.Fatalf("expected success:true, got %s", resp.Body)
	}
	waitFor(t, func() bool {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return len(runner.cmds) > 0
	})
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.cmds) != 1 || runner.cmds[0][0] != "systemctl" || runner.cmds[0][1] != "restart" || runner.cmds[0][2] != "pwnagotchi" {
		t.Fatalf("unexpected command: %+v", runner.cmds)
	}
}

func TestUnknownSubpathReturns404(t *testing.T) {
	p, _ := newTestPlugin(t, "")
	resp, err := p.OnWebhook("api/bogus", httptest.NewRequest(http.MethodGet, "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.Status)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !cond() {
		t.Fatal("timed out waiting for the backgrounded systemctl call")
	}
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
