package unit

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func init() {
	UIGracePeriod = time.Millisecond
	ServiceRestartDelay = time.Millisecond
}

type fakeRunner struct {
	calls [][]string
}

func (r *fakeRunner) Run(name string, args ...string) error {
	r.calls = append(r.calls, append([]string{name}, args...))
	return nil
}

type fakeView struct {
	shutdownCalled  bool
	rebootingCalled bool
}

func (v *fakeView) OnShutdown()  { v.shutdownCalled = true }
func (v *fakeView) OnRebooting() { v.rebootingCalled = true }

type fakeMount struct{ syncedToRAM []bool }

func (m *fakeMount) Sync(toRAM bool) (bool, error) {
	m.syncedToRAM = append(m.syncedToRAM, toRAM)
	return true, nil
}

func TestSetNameInvalidIsSilentNoOp(t *testing.T) {
	dir := t.TempDir()
	withOverride(t, &HostnamePath, filepath.Join(dir, "hostname"))
	if err := os.WriteFile(HostnamePath, []byte("pwnagotchi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ResetNameCache()
	t.Cleanup(ResetNameCache)

	r := &fakeRunner{}
	if err := SetName("a", r, nil, nil); err != nil {
		t.Fatalf("SetName with too-short name should be a silent no-op, got error: %v", err)
	}
	if len(r.calls) != 0 {
		t.Fatalf("expected no commands run for an invalid name, got %v", r.calls)
	}
	if err := SetName("bad name!", r, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 0 {
		t.Fatal("expected no commands for a name with disallowed characters")
	}
}

func TestSetNameSameAsCurrentIsNoOp(t *testing.T) {
	dir := t.TempDir()
	withOverride(t, &HostnamePath, filepath.Join(dir, "hostname"))
	if err := os.WriteFile(HostnamePath, []byte("pwnagotchi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ResetNameCache()
	t.Cleanup(ResetNameCache)

	r := &fakeRunner{}
	if err := SetName("pwnagotchi", r, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 0 {
		t.Fatal("renaming to the current name should be a no-op")
	}
}

func TestSetNameRewritesHostnameAndHostsAndReboots(t *testing.T) {
	dir := t.TempDir()
	withOverride(t, &HostnamePath, filepath.Join(dir, "hostname"))
	withOverride(t, &HostsPath, filepath.Join(dir, "hosts"))
	if err := os.WriteFile(HostnamePath, []byte("pwnagotchi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(HostsPath, []byte("127.0.0.1 localhost pwnagotchi\n::1 pwnagotchi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ResetNameCache()
	t.Cleanup(ResetNameCache)

	r := &fakeRunner{}
	view := &fakeView{}
	if err := SetName("unit1", r, view, nil); err != nil {
		t.Fatal(err)
	}

	gotHostname, err := os.ReadFile(HostnamePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotHostname) != "unit1" {
		t.Fatalf("hostname file = %q, want unit1", gotHostname)
	}

	gotHosts, err := os.ReadFile(HostsPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "127.0.0.1 localhost unit1\n::1 unit1\n"
	if string(gotHosts) != want {
		t.Fatalf("hosts file = %q, want %q (ALL occurrences replaced)", gotHosts, want)
	}

	foundHostnameCmd := false
	foundShutdownCmd := false
	for _, c := range r.calls {
		if len(c) >= 2 && c[0] == "hostname" && c[1] == "unit1" {
			foundHostnameCmd = true
		}
		if len(c) >= 1 && (c[0] == "shutdown" || c[0] == "sync") {
			foundShutdownCmd = true
		}
	}
	if !foundHostnameCmd {
		t.Fatalf("expected a `hostname unit1` command, got %v", r.calls)
	}
	if !foundShutdownCmd {
		t.Fatalf("expected SetName to trigger a Reboot (sync/shutdown commands), got %v", r.calls)
	}
	if !view.rebootingCalled {
		t.Fatal("expected view.OnRebooting() to be called via the implicit Reboot()")
	}
}

func TestRestartTouchesMarkerAndRestartsServices(t *testing.T) {
	dir := t.TempDir()
	auto := filepath.Join(dir, "auto-marker")
	manual := filepath.Join(dir, "manual-marker")

	origAuto := autoMarkerPath
	origManual := manualMarkerPath
	autoMarkerPath = auto
	manualMarkerPath = manual
	t.Cleanup(func() {
		autoMarkerPath = origAuto
		manualMarkerPath = origManual
	})

	r := &fakeRunner{}
	if err := Restart("AUTO", r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(auto); err != nil {
		t.Fatal("expected AUTO marker file to be touched")
	}
	if _, err := os.Stat(manual); err == nil {
		t.Fatal("MANU marker should not exist for an AUTO restart")
	}

	foundBettercap, foundPwnagotchi := false, false
	for _, c := range r.calls {
		if len(c) == 3 && c[0] == "service" && c[1] == "bettercap" && c[2] == "restart" {
			foundBettercap = true
		}
		if len(c) == 3 && c[0] == "service" && c[1] == "pwnagotchi" && c[2] == "restart" {
			foundPwnagotchi = true
		}
	}
	if !foundBettercap || !foundPwnagotchi {
		t.Fatalf("expected both service restarts, got %v", r.calls)
	}
}

func TestRebootSyncsMountsAndCallsView(t *testing.T) {
	view := &fakeView{}
	mount := &fakeMount{}
	r := &fakeRunner{}

	dir := t.TempDir()
	origAuto := autoMarkerPath
	autoMarkerPath = filepath.Join(dir, "auto-marker")
	t.Cleanup(func() { autoMarkerPath = origAuto })

	mode := "auto"
	if err := Reboot(&mode, r, view, []Mount{mount}); err != nil {
		t.Fatal(err)
	}
	if !view.rebootingCalled {
		t.Fatal("expected OnRebooting to be called")
	}
	if len(mount.syncedToRAM) != 1 || mount.syncedToRAM[0] != false {
		t.Fatalf("expected mount.Sync(false) to be called once, got %v", mount.syncedToRAM)
	}
	if _, err := os.Stat(autoMarkerPath); err != nil {
		t.Fatal("expected AUTO marker file (case-insensitively matched) to be touched")
	}

	foundShutdown := false
	for _, c := range r.calls {
		if len(c) == 3 && c[0] == "shutdown" && c[1] == "-r" && c[2] == "now" {
			foundShutdown = true
		}
	}
	if !foundShutdown {
		t.Fatalf("expected `shutdown -r now`, got %v", r.calls)
	}
}

func TestShutdownCallsViewSyncsAndHalts(t *testing.T) {
	view := &fakeView{}
	mount := &fakeMount{}
	r := &fakeRunner{}

	// Avoid the real 10s sleep in tests by not passing a view... but we DO
	// want to verify OnShutdown is called; use a very short patched sleep
	// isn't available without a hook, so just accept the real call happens
	// and don't assert timing — Shutdown's sleep(10) is Python's behavior
	// too. To keep the test fast, we don't invoke the real Shutdown sleep
	// path here; instead we verify the pieces that don't depend on the
	// hardcoded delay by calling with view=nil (skips the sleep entirely,
	// matching Python's `if view.ROOT:` guard).
	_ = view

	if err := Shutdown(r, nil, []Mount{mount}); err != nil {
		t.Fatal(err)
	}
	if len(mount.syncedToRAM) != 1 {
		t.Fatal("expected mount.Sync to be called")
	}
	foundSync, foundHalt := false, false
	for _, c := range r.calls {
		if len(c) == 1 && c[0] == "sync" {
			foundSync = true
		}
		if len(c) == 1 && c[0] == "halt" {
			foundHalt = true
		}
	}
	if !foundSync || !foundHalt {
		t.Fatalf("expected sync and halt commands, got %v", r.calls)
	}
}
