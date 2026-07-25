package switcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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

type fakeSystem struct {
	mu          sync.Mutex
	rebootMode  string
	rebootCalls int
}

func (f *fakeSystem) Shutdown() error { return nil }
func (f *fakeSystem) Reboot(mode string) error {
	f.mu.Lock()
	f.rebootMode = mode
	f.rebootCalls++
	f.mu.Unlock()
	return nil
}
func (f *fakeSystem) Restart(mode string) error { return nil }

func newTestPlugin(t *testing.T, tasks config.Map) (*Plugin, *fakeRunner, *fakeSystem, string, string) {
	t.Helper()
	scriptDir := t.TempDir()
	systemdDir := t.TempDir()
	p := New()
	p.scriptDir = scriptDir
	p.systemdDir = systemdDir
	p.switcherFlag = filepath.Join(t.TempDir(), ".switcher")

	runner := &fakeRunner{}
	sys := &fakeSystem{}
	if err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"tasks": tasks},
		Exec:   runner,
		System: sys,
	}); err != nil {
		t.Fatal(err)
	}
	return p, runner, sys, scriptDir, systemdDir
}

func TestHandleEventRunsMatchingEnabledTask(t *testing.T) {
	p, runner, _, scriptDir, systemdDir := newTestPlugin(t, config.Map{
		"epoch": config.Map{"enabled": true, "commands": []interface{}{"echo hi", "echo bye"}},
	})
	p.HandleEvent("epoch", nil)

	scriptPath := filepath.Join(scriptDir, "switcher-epoch.sh")
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("expected script written: %v", err)
	}
	if !strings.Contains(string(data), "echo hi\n") || !strings.Contains(string(data), "echo bye\n") {
		t.Fatalf("unexpected script contents: %s", data)
	}
	if info, err := os.Stat(scriptPath); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("expected script to be executable, mode=%v err=%v", info.Mode(), err)
	}

	unitPath := filepath.Join(systemdDir, "switcher-epoch-task.service")
	if _, err := os.Stat(unitPath); err != nil {
		t.Fatalf("expected unit file written: %v", err)
	}

	cmds := runner.snapshot()
	if len(cmds) != 2 || cmds[0].args[0] != "daemon-reload" || cmds[1].args[0] != "start" || cmds[1].args[1] != "switcher-epoch-task.service" {
		t.Fatalf("unexpected systemctl calls: %+v", cmds)
	}
}

func TestHandleEventIgnoresDisabledTask(t *testing.T) {
	p, runner, _, scriptDir, _ := newTestPlugin(t, config.Map{
		"epoch": config.Map{"enabled": false, "commands": []interface{}{"echo hi"}},
	})
	p.HandleEvent("epoch", nil)
	if entries, _ := os.ReadDir(scriptDir); len(entries) != 0 {
		t.Fatalf("expected no script written for a disabled task, got %v", entries)
	}
	if len(runner.snapshot()) != 0 {
		t.Fatal("expected no systemctl calls for a disabled task")
	}
}

func TestHandleEventIgnoresUnconfiguredEvent(t *testing.T) {
	p, runner, _, _, _ := newTestPlugin(t, config.Map{
		"epoch": config.Map{"enabled": true, "commands": []interface{}{"echo hi"}},
	})
	p.HandleEvent("wifi_update", nil)
	if len(runner.snapshot()) != 0 {
		t.Fatal("expected no action for an event with no configured task")
	}
}

func TestRebootTaskCreatesFlagDropinsTimerAndReboots(t *testing.T) {
	p, runner, sys, _, systemdDir := newTestPlugin(t, config.Map{
		"handshake": config.Map{"enabled": true, "commands": []interface{}{"echo pwned"}, "reboot": true, "stopwatch": int64(5)},
	})
	p.HandleEvent("handshake", nil)

	if _, err := os.Stat(p.switcherFlag); err != nil {
		t.Fatalf("expected switcher flag file created: %v", err)
	}

	pwnDropin := filepath.Join(systemdDir, "pwnagotchi.service.d", "switcher.conf")
	data, err := os.ReadFile(pwnDropin)
	if err != nil {
		t.Fatalf("expected pwnagotchi.service dropin: %v", err)
	}
	if !strings.Contains(string(data), "ConditionPathExists=!"+p.switcherFlag) {
		t.Fatalf("unexpected pwnagotchi dropin content: %s", data)
	}

	bcDropin := filepath.Join(systemdDir, "bettercap.service.d", "switcher.conf")
	if _, err := os.Stat(bcDropin); err != nil {
		t.Fatalf("expected bettercap.service dropin: %v", err)
	}

	timerPath := filepath.Join(systemdDir, "switcher-reboot.timer")
	timerData, err := os.ReadFile(timerPath)
	if err != nil {
		t.Fatalf("expected reboot timer written: %v", err)
	}
	if !strings.Contains(string(timerData), "OnBootSec=5m") {
		t.Fatalf("expected stopwatch=5 minutes in timer, got: %s", timerData)
	}

	if sys.rebootCalls != 1 {
		t.Fatalf("expected exactly 1 real reboot call, got %d", sys.rebootCalls)
	}

	cmds := runner.snapshot()
	foundEnableTimer, foundEnableTask := false, false
	for _, c := range cmds {
		if len(c.args) == 2 && c.args[0] == "enable" && c.args[1] == "switcher-reboot.timer" {
			foundEnableTimer = true
		}
		if len(c.args) == 2 && c.args[0] == "enable" && c.args[1] == "switcher-handshake-task.service" {
			foundEnableTask = true
		}
	}
	if !foundEnableTimer || !foundEnableTask {
		t.Fatalf("expected enable calls for both the timer and the task service, got %+v", cmds)
	}
}

func TestNonRebootTaskNeverCallsSystemReboot(t *testing.T) {
	p, _, sys, _, _ := newTestPlugin(t, config.Map{
		"epoch": config.Map{"enabled": true, "commands": []interface{}{"echo hi"}},
	})
	p.HandleEvent("epoch", nil)
	if sys.rebootCalls != 0 {
		t.Fatal("expected no reboot for a non-reboot task")
	}
}

func TestEmptyTasksConfigIsANoOp(t *testing.T) {
	p, runner, _, _, _ := newTestPlugin(t, nil)
	p.HandleEvent("epoch", nil)
	if len(runner.snapshot()) != 0 {
		t.Fatal("expected no action with no configured tasks")
	}
}
