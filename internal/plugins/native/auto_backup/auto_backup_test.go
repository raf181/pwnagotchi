package autobackup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
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

// fakeRunner actually shells out to the real `tar` binary (present on any
// Linux dev/CI host) so tests verify a real archive was produced, not
// just that a command was "recorded".
type fakeRunner struct {
	mu   sync.Mutex
	cmds []recordedCmd
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := osexec.CommandContext(ctx, name, args...).CombinedOutput()
	f.mu.Lock()
	f.cmds = append(f.cmds, recordedCmd{name, args})
	f.mu.Unlock()
	return out, err
}

func (f *fakeRunner) snapshot() []recordedCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedCmd(nil), f.cmds...)
}

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

type fakeView struct {
	mu     sync.Mutex
	values map[string]string
}

func newFakeView() *fakeView { return &fakeView{values: map[string]string{}} }
func (f *fakeView) get(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.values[key]
}
func (f *fakeView) Set(key, value string) {
	f.mu.Lock()
	f.values[key] = value
	f.mu.Unlock()
}
func (f *fakeView) Update(bool)                                                          {}
func (f *fakeView) Kind() string                                                         { return "dummydisplay" }
func (f *fakeView) HasElement(string) bool                                               { return false }
func (f *fakeView) RemoveElement(string)                                                 {}
func (f *fakeView) AddText(string, string, int, int, pluginmanager.FontStyle, bool, int) {}
func (f *fakeView) AddLabeledValue(string, string, string, int, int, pluginmanager.FontStyle, pluginmanager.FontStyle, int) {
}
func (f *fakeView) OnUploading(string) {}
func (f *fakeView) OnNormal()          {}
func (f *fakeView) Width() int         { return 250 }
func (f *fakeView) Height() int        { return 122 }

var _ pluginmanager.ViewCapability = (*fakeView)(nil)
var _ pluginmanager.CommandRunner = (*fakeRunner)(nil)
var _ pluginmanager.Clock = (*fakeClock)(nil)

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", timeout)
	}
}

func newTestPlugin(t *testing.T, cfg config.Map) (*Plugin, *fakeRunner, *fakeClock, *fakeView) {
	t.Helper()
	runner := &fakeRunner{}
	clock := &fakeClock{now: time.Now()}
	view := newFakeView()
	p := New()
	p.statusFile = filepath.Join(t.TempDir(), ".auto-backup")
	if err := p.OnLoad(pluginmanager.Capabilities{Config: cfg, Exec: runner, Clock: clock, View: view}); err != nil {
		t.Fatal(err)
	}
	return p, runner, clock, view
}

func TestOnLoadRequiresBackupLocation(t *testing.T) {
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{Config: config.Map{}}); err != nil {
		t.Fatal(err)
	}
	if p.ready {
		t.Fatal("expected plugin to remain not-ready without backup_location")
	}
}

func TestOnLoadAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	p, _, _, _ := newTestPlugin(t, config.Map{"backup_location": dir})
	if !p.ready {
		t.Fatal("expected ready")
	}
	if p.intervalSeconds != defaultIntervalSeconds || p.maxBackups != defaultMaxBackups {
		t.Fatalf("expected default interval/max, got %d/%d", p.intervalSeconds, p.maxBackups)
	}
	if len(p.commands) != 2 || p.commands[0] != "tar" || p.commands[1] != "czf" {
		t.Fatalf("expected default tar czf commands, got %v", p.commands)
	}
}

func TestManualBackupCreatesRealArchiveWithConfiguredFiles(t *testing.T) {
	backupDir := t.TempDir()
	srcDir := t.TempDir()
	f1 := filepath.Join(srcDir, "settings.yaml")
	f2 := filepath.Join(srcDir, "secret.json")
	os.WriteFile(f1, []byte("main:\n  name: test\n"), 0o644)
	os.WriteFile(f2, []byte(`{"a":1}`), 0o644)

	p, runner, _, view := newTestPlugin(t, config.Map{
		"backup_location": backupDir,
		"files":           []interface{}{f1, f2},
	})

	result := p.ManualBackup()
	if !strings.Contains(result, "Backup started") {
		t.Fatalf("unexpected manual backup result: %q", result)
	}

	// Wait for the backup to fully complete (not just "the file exists"):
	// the real `tar` process creates the file as soon as it opens it for
	// writing, well before it finishes/closes it, so a glob-existence
	// check alone races with tar still writing. "Backup done!" is only
	// set after exec.Run (which blocks until tar exits) returns.
	waitFor(t, 5*time.Second, func() bool { return view.get("status") == "Backup done!" })

	matches, _ := filepath.Glob(filepath.Join(backupDir, "*-backup-*.tar.gz"))
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 archive, got %v", matches)
	}
	archivePath := matches[0]

	names := readTarGzNames(t, archivePath)
	foundF1, foundF2 := false, false
	for _, n := range names {
		if strings.HasSuffix(n, "settings.yaml") {
			foundF1 = true
		}
		if strings.HasSuffix(n, "secret.json") {
			foundF2 = true
		}
	}
	if !foundF1 || !foundF2 {
		t.Fatalf("expected both files in archive, got entries: %v", names)
	}

	cmds := runner.snapshot()
	if len(cmds) != 1 || cmds[0].name != "tar" {
		t.Fatalf("expected exactly one real tar invocation, got %+v", cmds)
	}
}

func TestManualBackupNoFilesToBackup(t *testing.T) {
	backupDir := t.TempDir()
	p, runner, _, _ := newTestPlugin(t, config.Map{
		"backup_location": backupDir,
		"files":           []interface{}{filepath.Join(t.TempDir(), "does-not-exist")},
	})
	result := p.ManualBackup()
	if result != "No files to backup" {
		t.Fatalf("expected 'No files to backup', got %q", result)
	}
	if len(runner.snapshot()) != 0 {
		t.Fatal("expected no tar invocation when nothing exists to back up")
	}
}

func TestManualBackupAlreadyInProgress(t *testing.T) {
	p, _, _, _ := newTestPlugin(t, config.Map{"backup_location": t.TempDir()})
	p.mu.Lock()
	p.backupInProgress = true
	p.mu.Unlock()
	if got := p.ManualBackup(); got != "Backup already in progress" {
		t.Fatalf("expected in-progress message, got %q", got)
	}
}

func TestCleanupOldBackupsKeepsOnlyMostRecent(t *testing.T) {
	backupDir := t.TempDir()
	p, _, _, _ := newTestPlugin(t, config.Map{"backup_location": backupDir, "max_backups_to_keep": int64(2)})

	names := []string{"a", "b", "c", "d"}
	for i, n := range names {
		path := filepath.Join(backupDir, p.hostname+"-backup-"+n+".tar.gz")
		os.WriteFile(path, []byte("x"), 0o644)
		mt := time.Now().Add(time.Duration(i) * time.Minute)
		os.Chtimes(path, mt, mt)
	}

	p.cleanupOldBackups()

	matches, _ := filepath.Glob(filepath.Join(backupDir, "*-backup-*.tar.gz"))
	if len(matches) != 2 {
		t.Fatalf("expected 2 backups kept, got %d: %v", len(matches), matches)
	}
	for _, m := range matches {
		if strings.Contains(m, "-backup-a.") || strings.Contains(m, "-backup-b.") {
			t.Fatalf("expected the two oldest backups deleted, but found %s", m)
		}
	}
}

func TestIsBackupDueRespectsInterval(t *testing.T) {
	p, _, clock, _ := newTestPlugin(t, config.Map{"backup_location": t.TempDir(), "interval_seconds": int64(3600)})

	if !p.isBackupDueLocked() {
		t.Fatal("expected due=true when no status file exists yet")
	}

	touch(p.statusFile, clock.Now())
	if p.isBackupDueLocked() {
		t.Fatal("expected due=false immediately after a backup")
	}

	clock.advance(30 * time.Minute)
	if p.isBackupDueLocked() {
		t.Fatal("expected due=false at half the interval")
	}

	clock.advance(31 * time.Minute)
	if !p.isBackupDueLocked() {
		t.Fatal("expected due=true once the interval has elapsed")
	}
}

func TestHandleEventUiUpdateTriggersBackupWhenDue(t *testing.T) {
	backupDir := t.TempDir()
	f := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(f, []byte("x"), 0o644)

	p, runner, _, _ := newTestPlugin(t, config.Map{
		"backup_location":  backupDir,
		"files":            []interface{}{f},
		"interval_seconds": int64(3600),
	})
	p.HandleEvent("ui_update", nil)
	waitFor(t, 5*time.Second, func() bool { return len(runner.snapshot()) == 1 })
}

func TestHandleEventUiUpdateSkipsWhenNotDue(t *testing.T) {
	backupDir := t.TempDir()
	p, runner, clock, _ := newTestPlugin(t, config.Map{"backup_location": backupDir, "interval_seconds": int64(3600)})
	touch(p.statusFile, clock.Now())

	p.HandleEvent("ui_update", nil)
	time.Sleep(50 * time.Millisecond)
	if len(runner.snapshot()) != 0 {
		t.Fatal("expected no backup triggered before the interval elapses")
	}
}

func TestHandleEventIgnoresOtherEvents(t *testing.T) {
	p, runner, _, _ := newTestPlugin(t, config.Map{"backup_location": t.TempDir()})
	p.HandleEvent("epoch", nil)
	time.Sleep(20 * time.Millisecond)
	if len(runner.snapshot()) != 0 {
		t.Fatal("expected no action for non-ui_update events")
	}
}

func TestOnWebhookGetRendersStatusPage(t *testing.T) {
	p, _, _, _ := newTestPlugin(t, config.Map{"backup_location": "/some/dir"})
	req := httptest.NewRequest("GET", "/plugins/auto_backup/", nil)
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "test-token"})
	resp, err := p.OnWebhook("", req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 || !strings.Contains(string(resp.Body), "AUTO Backup") {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if !strings.Contains(string(resp.Body), "/some/dir") {
		t.Fatalf("expected backup location in status page, got: %s", resp.Body)
	}
	if !strings.Contains(string(resp.Body), `name="csrf_token" value="test-token"`) {
		t.Fatalf("expected CSRF token in backup form, got: %s", resp.Body)
	}
}

func TestStatusPageEscapesConfiguredPaths(t *testing.T) {
	p, _, _, _ := newTestPlugin(t, config.Map{"backup_location": `<script>alert(1)</script>`})
	resp, err := p.OnWebhook("", httptest.NewRequest(http.MethodGet, "/plugins/auto_backup/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(resp.Body), "<script>") {
		t.Fatalf("configured path was rendered as HTML: %s", resp.Body)
	}
}

func TestOnWebhookPostTriggersManualBackup(t *testing.T) {
	f := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(f, []byte("x"), 0o644)
	p, runner, _, _ := newTestPlugin(t, config.Map{"backup_location": t.TempDir(), "files": []interface{}{f}})

	req := httptest.NewRequest("POST", "/plugins/auto_backup/backup", nil)
	resp, err := p.OnWebhook("backup", req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 || !strings.Contains(string(resp.Body), "Backup started") {
		t.Fatalf("unexpected response: %s", resp.Body)
	}
	waitFor(t, 5*time.Second, func() bool { return len(runner.snapshot()) == 1 })
}

func TestOnWebhookUnknownPathReturns404(t *testing.T) {
	p, _, _, _ := newTestPlugin(t, config.Map{"backup_location": t.TempDir()})
	req := httptest.NewRequest("GET", "/plugins/auto_backup/nope", nil)
	resp, err := p.OnWebhook("nope", req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 404 {
		t.Fatalf("expected 404, got %d", resp.Status)
	}
}

// readTarGzNames extracts the list of entry names from a real tar.gz
// file, proving a genuine archive (not just a placeholder file) was
// produced by the real `tar` invocation.
func readTarGzNames(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, hdr.Name)
	}
	return names
}
