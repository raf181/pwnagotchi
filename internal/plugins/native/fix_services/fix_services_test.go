package fixservices

import (
	"context"
	"os"
	"path/filepath"
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
	outputs map[string]string
	errFor  map[string]bool
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{outputs: map[string]string{}, errFor: map[string]bool{}}
}

func key(name string, args []string) string {
	s := name
	for _, a := range args {
		s += " " + a
	}
	return s
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.cmds = append(f.cmds, recordedCmd{name, args})
	f.mu.Unlock()
	k := key(name, args)
	if f.errFor[k] {
		return nil, context.DeadlineExceeded
	}
	return []byte(f.outputs[k]), nil
}

func (f *fakeRunner) snapshot() []recordedCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedCmd(nil), f.cmds...)
}

func (f *fakeRunner) hasCmd(name string, args ...string) bool {
	for _, c := range f.snapshot() {
		if c.name != name || len(c.args) != len(args) {
			continue
		}
		match := true
		for i := range args {
			if c.args[i] != args[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

type fakeAgent struct {
	mu     sync.Mutex
	runs   []string
	failOn map[string]bool
}

func newFakeAgent() *fakeAgent { return &fakeAgent{failOn: map[string]bool{}} }

func (a *fakeAgent) Run(cmd string, verbose bool) (interface{}, error) {
	a.mu.Lock()
	a.runs = append(a.runs, cmd)
	fail := a.failOn[cmd]
	a.mu.Unlock()
	if fail {
		return nil, context.DeadlineExceeded
	}
	return map[string]interface{}{"success": true}, nil
}
func (a *fakeAgent) Session(string) (interface{}, error) { return nil, nil }
func (a *fakeAgent) IsModuleRunning(string) bool         { return false }
func (a *fakeAgent) StartModule(string)                  {}
func (a *fakeAgent) RestartModule(string)                {}

func (a *fakeAgent) snapshot() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.runs...)
}

type fakeView struct {
	mu     sync.Mutex
	status string
}

func (v *fakeView) Set(key, value string) {
	if key == "status" {
		v.mu.Lock()
		v.status = value
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
	mu           sync.Mutex
	restartCalls []string
	rebootCalls  int
}

func (s *fakeSystem) Shutdown() error { return nil }
func (s *fakeSystem) Reboot(mode string) error {
	s.mu.Lock()
	s.rebootCalls++
	s.mu.Unlock()
	return nil
}
func (s *fakeSystem) Restart(mode string) error {
	s.mu.Lock()
	s.restartCalls = append(s.restartCalls, mode)
	s.mu.Unlock()
	return nil
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

func setupWlan0(t *testing.T, driver string) {
	t.Helper()
	dir := t.TempDir()
	netClassDir = dir
	t.Cleanup(func() { netClassDir = "/sys/class/net" })

	wlan0 := filepath.Join(dir, "wlan0")
	if err := os.MkdirAll(filepath.Join(wlan0, "device"), 0o755); err != nil {
		t.Fatal(err)
	}
	if driver != "" {
		driverDir := filepath.Join(dir, "realdriver", driver)
		if err := os.MkdirAll(driverDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(driverDir, filepath.Join(wlan0, "device", "driver")); err != nil {
			t.Fatal(err)
		}
	}
}

func newTestPlugin(t *testing.T, driver string) (*Plugin, *fakeRunner, *fakeAgent, *fakeView, *fakeSystem, *fakeClock) {
	t.Helper()
	setupWlan0(t, driver)
	runner := newFakeRunner()
	agent := newFakeAgent()
	view := &fakeView{}
	sys := &fakeSystem{}
	clock := &fakeClock{now: time.Now()}
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{
		Exec:   runner,
		Agent:  agent,
		View:   view,
		System: sys,
		Clock:  clock,
	}); err != nil {
		t.Fatal(err)
	}
	return p, runner, agent, view, sys, clock
}

func TestOnLoadEnabledWithBrcmfmacDriver(t *testing.T) {
	p, _, _, _, _, _ := newTestPlugin(t, "brcmfmac")
	if p.isDisabled {
		t.Fatal("expected plugin enabled with brcmfmac driver")
	}
}

func TestOnLoadDisabledWithExternalDriver(t *testing.T) {
	p, _, _, _, _, _ := newTestPlugin(t, "rtl8188eu")
	if !p.isDisabled {
		t.Fatal("expected plugin disabled with a non-brcmfmac driver")
	}
}

func TestOnLoadDisabledWithNoWlan0(t *testing.T) {
	dir := t.TempDir()
	netClassDir = dir
	defer func() { netClassDir = "/sys/class/net" }()
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{Exec: newFakeRunner()}); err != nil {
		t.Fatal(err)
	}
	if !p.isDisabled {
		t.Fatal("expected plugin disabled when wlan0 doesn't exist")
	}
}

func TestHandleEventNoOpWhenDisabled(t *testing.T) {
	p, runner, _, _, _, _ := newTestPlugin(t, "rtl8188eu")
	p.HandleEvent("ready", nil)
	if len(runner.snapshot()) != 0 {
		t.Fatal("expected no commands run while disabled")
	}
}

func TestOnReadyTriggersRecoveryWhenInterfaceMissing(t *testing.T) {
	p, runner, agent, _, _, _ := newTestPlugin(t, "brcmfmac")
	runner.errFor[key("ip", []string{"link", "show", "wlan0mon"})] = true
	p.HandleEvent("ready", nil)

	if !runner.hasCmd("monstop") {
		t.Fatal("expected recovery to stop the monitor interface")
	}
	if len(agent.snapshot()) == 0 {
		t.Fatal("expected recovery to interact with bettercap")
	}
}

func TestOnReadyNoOpWhenInterfaceUp(t *testing.T) {
	p, runner, _, _, _, _ := newTestPlugin(t, "brcmfmac")
	p.HandleEvent("ready", nil)
	if runner.hasCmd("monstop") {
		t.Fatal("expected no recovery when the interface check succeeds")
	}
}

func TestBcapSysLogFlipsReconOnChannelHopError(t *testing.T) {
	p, _, agent, view, _, _ := newTestPlugin(t, "brcmfmac")
	event := map[string]interface{}{
		"data": map[string]interface{}{"Message": "wifi error while hopping to channel 6"},
	}
	p.HandleEvent("bcap_sys_log", []interface{}{nil, event})

	runs := agent.snapshot()
	if len(runs) != 1 || runs[0] != "wifi.recon off; wifi.recon on" {
		t.Fatalf("expected a single recon-flip command, got %v", runs)
	}
	view.mu.Lock()
	status := view.status
	view.mu.Unlock()
	if status != "Wifi recon flipped!" {
		t.Fatalf("status = %q, want %q", status, "Wifi recon flipped!")
	}
}

func TestBcapSysLogIgnoresUnrelatedMessages(t *testing.T) {
	p, _, agent, _, _, _ := newTestPlugin(t, "brcmfmac")
	event := map[string]interface{}{"data": map[string]interface{}{"Message": "nothing interesting here"}}
	p.HandleEvent("bcap_sys_log", []interface{}{nil, event})
	if len(agent.snapshot()) != 0 {
		t.Fatal("expected no action for an unrelated log message")
	}
}

func TestOnEpochPatternIfaceValidationTriggersMonRestartAndDaemonRestart(t *testing.T) {
	p, runner, _, view, sys, clock := newTestPlugin(t, "brcmfmac")
	runner.outputs[key("journalctl", []string{"-n10", "-k"})] = "ieee80211 phy0: brcmf_cfg80211_add_iface: iface validation failed: err=-95"
	clock.advance(200 * time.Second)

	p.HandleEvent("epoch", nil)

	if !runner.hasCmd("monstop") || !runner.hasCmd("monstart") {
		t.Fatalf("expected monstop+monstart, got %+v", runner.snapshot())
	}
	sys.mu.Lock()
	restarts := sys.restartCalls
	sys.mu.Unlock()
	if len(restarts) != 1 || restarts[0] != "AUTO" {
		t.Fatalf("expected one AUTO restart, got %v", restarts)
	}
	view.mu.Lock()
	status := view.status
	view.mu.Unlock()
	if status != "Wifi channel stuck. Restarting recon." {
		t.Fatalf("unexpected status: %q", status)
	}
}

func TestOnEpochPatternConcurrentMapRestartsBettercapAndDaemon(t *testing.T) {
	p, runner, _, _, sys, clock := newTestPlugin(t, "brcmfmac")
	runner.outputs[key("tail", []string{"-n", "10", pwnagotchiLog})] = "fatal error: concurrent map iteration and map write"
	clock.advance(200 * time.Second)

	p.HandleEvent("epoch", nil)

	if !runner.hasCmd("systemctl", "restart", "bettercap") {
		t.Fatal("expected systemctl restart bettercap")
	}
	sys.mu.Lock()
	restarts := sys.restartCalls
	sys.mu.Unlock()
	if len(restarts) != 1 {
		t.Fatalf("expected one daemon restart, got %v", restarts)
	}
}

// TestOnEpochRateLimitGatesOnLastRecoveryAttempt ports the real (if
// imperfect) rate-limit semantics exactly: on_epoch's `time.time() -
// self.LASTTRY > 180` gate is only ever armed by
// _tryTurningItOffAndOnAgain (called from on_ready/a failed recon flip),
// never by on_epoch's own direct-remediation branches (monstart/
// monstop/systemctl restart) — grepped the real fix_services.py to
// confirm none of the 7 pattern branches update LASTTRY themselves. So a
// recent _tryTurningItOffAndOnAgain run suppresses the next epoch scan...
func TestOnEpochRateLimitGatesOnLastRecoveryAttempt(t *testing.T) {
	p, runner, _, _, _, clock := newTestPlugin(t, "brcmfmac")
	runner.outputs[key("journalctl", []string{"-n10", "-k"})] = "ieee80211 phy0: brcmf_cfg80211_add_iface: iface validation failed: err=-95"

	p.mu.Lock()
	p.lastTry = clock.Now()
	p.mu.Unlock()

	p.HandleEvent("epoch", nil)
	if runner.hasCmd("monstop") {
		t.Fatal("expected the scan to be suppressed right after a recent recovery attempt")
	}
}

// ...but a direct-remediation branch (this one doesn't call
// _tryTurningItOffAndOnAgain) does NOT itself re-arm the gate, so a
// second epoch tick before any real recovery attempt re-fires the same
// remediation. This is a confirmed real upstream quirk, not a Go-port
// bug — preserved faithfully rather than "fixed" unilaterally.
func TestOnEpochWithoutRecoveryDoesNotSelfSuppress(t *testing.T) {
	p, runner, _, _, _, clock := newTestPlugin(t, "brcmfmac")
	runner.outputs[key("journalctl", []string{"-n10", "-k"})] = "ieee80211 phy0: brcmf_cfg80211_add_iface: iface validation failed: err=-95"
	clock.advance(200 * time.Second)

	p.HandleEvent("epoch", nil)
	first := len(runner.snapshot())

	p.HandleEvent("epoch", nil)
	second := len(runner.snapshot())
	if second <= first {
		t.Fatalf("expected the pattern to re-fire on a second tick (real upstream behavior): first=%d second=%d", first, second)
	}
}

func TestTryTurningItOffAndOnAgainRebootsAfterThreeFailedReloads(t *testing.T) {
	p, runner, _, _, sys, _ := newTestPlugin(t, "brcmfmac")
	runner.errFor[key("sudo", []string{"modprobe", "-r", "brcmfmac"})] = true

	p.tryTurningItOffAndOnAgain()

	sys.mu.Lock()
	reboots := sys.rebootCalls
	sys.mu.Unlock()
	if reboots != 1 {
		t.Fatalf("expected exactly 1 reboot after 3 failed reload attempts, got %d", reboots)
	}
}

func TestTryTurningItOffAndOnAgainSucceedsAndResumesRecon(t *testing.T) {
	p, runner, agent, view, sys, _ := newTestPlugin(t, "brcmfmac")
	_ = runner

	p.tryTurningItOffAndOnAgain()

	sys.mu.Lock()
	reboots := sys.rebootCalls
	sys.mu.Unlock()
	if reboots != 0 {
		t.Fatal("expected no reboot on a successful reload")
	}
	runs := agent.snapshot()
	found := false
	for _, r := range runs {
		if r == "wifi.clear; wifi.recon on" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected recon to be resumed, got %v", runs)
	}
	view.mu.Lock()
	status := view.status
	view.mu.Unlock()
	if status != "I can see again! (probably)" {
		t.Fatalf("unexpected final status: %q", status)
	}
}

func TestTryTurningItOffAndOnAgainIgnoresDuplicateAttempt(t *testing.T) {
	p, runner, _, _, _, clock := newTestPlugin(t, "brcmfmac")
	p.isReloadingMon = true
	p.lastTry = clock.Now()

	p.tryTurningItOffAndOnAgain()

	if len(runner.snapshot()) != 0 {
		t.Fatal("expected the duplicate-in-progress attempt to be ignored entirely")
	}
}

func TestExternalAdapterFallsBackToLsmodWhenNoDriverSymlink(t *testing.T) {
	dir := t.TempDir()
	netClassDir = dir
	defer func() { netClassDir = "/sys/class/net" }()
	if err := os.MkdirAll(filepath.Join(dir, "wlan0"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := newFakeRunner()
	runner.outputs[key("lsmod", nil)] = "Module\nbrcmfmac 200000 1\n"
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{Exec: runner}); err != nil {
		t.Fatal(err)
	}
	if p.isDisabled {
		t.Fatal("expected plugin enabled when lsmod shows brcmfmac loaded")
	}
}
