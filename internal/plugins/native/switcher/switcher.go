// Package switcher is the native Go port of
// pwnagotchi/plugins/default/switcher.py: a generic task scheduler that
// runs a configured list of shell commands (as a real systemd oneshot
// service) whenever a named daemon event fires, optionally rebooting
// into a "run once more, then remove itself" mode afterward.
//
// Original Python author: 33197631+dadav@users.noreply.github.com (see
// switcher.py's own __author__ field, left untouched). This Go port is
// by raf181.
//
// commands is exactly the "documented configuration field that
// intentionally represents a shell program" the migration spec calls
// out (see internal/pluginmanager.CommandRunner's doc comment): each
// configured command string is written verbatim into a generated shell
// script and executed as that real script file, never interpolated into
// a shell string built by this plugin itself.
package switcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

const (
	defaultScriptDir    = "/usr/local/bin/"
	defaultSystemdDir   = "/etc/systemd/system/"
	defaultSwitcherFlag = "/root/.switcher"
	commandTimeout      = 30 * time.Second
)

// TaskConfig is one entry of the plugin's `tasks` config map, keyed by
// the bare event name (e.g. "wifi_update", "epoch" — no "on_" prefix:
// unlike Python's dynamic getattr(plugin, f"on_{event}") dispatch, this
// port's HandleEvent already receives the bare event name directly, so
// there is no Python-only lstrip('on_') prefix quirk to reproduce).
type TaskConfig struct {
	Enabled   bool
	Commands  []string
	Reboot    bool
	Stopwatch int // minutes
}

// Plugin ports the Switcher class.
type Plugin struct {
	mu    sync.Mutex
	tasks map[string]TaskConfig

	exec pluginmanager.CommandRunner
	sys  pluginmanager.SystemCapability
	log  pluginmanager.Logger

	// Overridable for tests, matching this port's established
	// package-level-var override pattern (e.g. internal/wpasec.DBPath).
	scriptDir, systemdDir, switcherFlag string
}

// New ports Switcher.__init__.
func New() *Plugin {
	return &Plugin{scriptDir: defaultScriptDir, systemdDir: defaultSystemdDir, switcherFlag: defaultSwitcherFlag}
}

func (p *Plugin) Name() string { return "switcher" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "0.0.1",
		Author:      "33197631+dadav@users.noreply.github.com",
		License:     "GPL3",
		Description: "This plugin is a generic task scheduler.",
	}
}

// OnLoad ports on_loaded: parse options['tasks'] into the hook table.
// Real Python skips setting up any hooks at all if 'tasks' is empty —
// ported here as leaving p.tasks nil, which HandleEvent's map lookup
// already treats as "no configured task for this event" (a no-op).
func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exec = caps.Exec
	p.sys = caps.System
	p.log = caps.Log
	p.tasks = parseTasks(caps.Config)
	return nil
}

// HandleEvent ports Switcher.trigger: any event with a matching,
// enabled task config runs it.
func (p *Plugin) HandleEvent(event string, _ []interface{}) {
	p.mu.Lock()
	task, ok := p.tasks[event]
	p.mu.Unlock()
	if !ok || !task.Enabled {
		return
	}
	p.runTask(event, task)
}

// runTask ports run_task.
func (p *Plugin) runTask(name string, task TaskConfig) {
	taskServiceName := fmt.Sprintf("switcher-%s-task.service", name)
	scriptPath := filepath.Join(p.scriptDir, fmt.Sprintf("switcher-%s.sh", name))

	if err := os.MkdirAll(p.scriptDir, 0o755); err != nil {
		p.logf("cannot create script dir %s: %v", p.scriptDir, err)
		return
	}
	var script strings.Builder
	script.WriteString("#!/bin/bash\n")
	for _, cmd := range task.Commands {
		script.WriteString(cmd)
		script.WriteString("\n")
	}
	// 0o755 (not 0o644 + a separate chmod subprocess): this port sets the
	// executable bit directly on write, a real, equivalent-behavior
	// simplification over Python's write-then-`os.system("chmod a+x
	// ...")` — see docs/known-differences.md.
	if err := os.WriteFile(scriptPath, []byte(script.String()), 0o755); err != nil {
		p.logf("cannot write %s: %v", scriptPath, err)
		return
	}

	unitPath := filepath.Join(p.systemdDir, taskServiceName)
	unitContent := fmt.Sprintf(`[Unit]
Description=Executes the tasks of the pwnagotchi switcher plugin
After=pwnagotchi.service bettercap.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=-%s
ExecStart=-/bin/rm %s
ExecStart=-/bin/rm %s

[Install]
WantedBy=multi-user.target
`, scriptPath, unitPath, scriptPath)
	if err := os.WriteFile(unitPath, []byte(unitContent), 0o644); err != nil {
		p.logf("cannot write %s: %v", unitPath, err)
		return
	}

	if task.Reboot {
		p.runRebootTask(name, taskServiceName)
		return
	}

	p.systemctl("daemon-reload")
	p.systemctl("start", taskServiceName)
}

// runRebootTask ports run_task's `if 'reboot' in options...` branch: an
// indication flag file, ConditionPathExists dropins on pwnagotchi.service/
// bettercap.service (so they don't start again until the flag is
// cleared), a self-cleaning timer, then a real reboot.
func (p *Plugin) runRebootTask(name, taskServiceName string) {
	if err := touch(p.switcherFlag); err != nil {
		p.logf("cannot create %s: %v", p.switcherFlag, err)
		return
	}

	if err := p.systemdDropin("pwnagotchi.service", fmt.Sprintf("[Unit]\nConditionPathExists=!%s\n", p.switcherFlag)); err != nil {
		p.logf("dropin for pwnagotchi.service: %v", err)
		return
	}
	if err := p.systemdDropin("bettercap.service", fmt.Sprintf("[Unit]\nConditionPathExists=!%s\n", p.switcherFlag)); err != nil {
		p.logf("dropin for bettercap.service: %v", err)
		return
	}

	timerPath := filepath.Join(p.systemdDir, "switcher-reboot.timer")
	if err := p.systemdDropin(taskServiceName, fmt.Sprintf("[Service]\nExecStart=-/bin/rm %s\nExecStart=-/bin/rm %s\n", p.switcherFlag, timerPath)); err != nil {
		p.logf("dropin for %s: %v", taskServiceName, err)
		return
	}

	stopwatch := 1
	p.mu.Lock()
	if t, ok := p.tasks[name]; ok && t.Stopwatch > 0 {
		stopwatch = t.Stopwatch
	}
	p.mu.Unlock()
	timerContent := fmt.Sprintf(`[Unit]
Description=Reboot when time is up
ConditionPathExists=%s

[Timer]
OnBootSec=%dm
Unit=reboot.target

[Install]
WantedBy=timers.target
`, p.switcherFlag, stopwatch)
	if err := os.WriteFile(timerPath, []byte(timerContent), 0o644); err != nil {
		p.logf("cannot write %s: %v", timerPath, err)
		return
	}

	p.systemctl("daemon-reload")
	p.systemctl("enable", "switcher-reboot.timer")
	p.systemctl("enable", taskServiceName)

	if p.sys != nil {
		if err := p.sys.Reboot(""); err != nil {
			p.logf("reboot failed: %v", err)
		}
	}
}

// systemdDropin ports systemd_dropin: writes a `switcher.conf` drop-in
// under <name>.service.d/ then reloads systemd.
func (p *Plugin) systemdDropin(name, content string) error {
	if !strings.HasSuffix(name, ".service") {
		name += ".service"
	}
	dropinDir := filepath.Join(p.systemdDir, name+".d")
	if err := os.MkdirAll(dropinDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dropinDir, "switcher.conf"), []byte(content), 0o644); err != nil {
		return err
	}
	p.systemctl("daemon-reload")
	return nil
}

// systemctl ports the module-level systemctl() helper: a real argv
// (never shell-string) systemctl invocation via the injected
// CommandRunner.
func (p *Plugin) systemctl(args ...string) {
	if p.exec == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	if _, err := p.exec.Run(ctx, "systemctl", args...); err != nil {
		p.logf("systemctl %s: %v", strings.Join(args, " "), err)
	}
}

func (p *Plugin) logf(format string, args ...interface{}) {
	if p.log != nil {
		p.log.Printf(format, args...)
	}
}

func touch(path string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// parseTasks reads config['main']['plugins']['switcher']['tasks'] (a
// config.Map keyed by event name) into the typed task table.
func parseTasks(cfg config.Map) map[string]TaskConfig {
	raw, _ := cfg["tasks"].(config.Map)
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]TaskConfig, len(raw))
	for name, v := range raw {
		taskMap, ok := v.(config.Map)
		if !ok {
			continue
		}
		tc := TaskConfig{
			Enabled:   boolField(taskMap, "enabled"),
			Reboot:    boolField(taskMap, "reboot"),
			Stopwatch: intField(taskMap, "stopwatch", 1),
		}
		if cmds, ok := taskMap["commands"].([]interface{}); ok {
			for _, c := range cmds {
				if s, ok := c.(string); ok {
					tc.Commands = append(tc.Commands, s)
				}
			}
		}
		out[name] = tc
	}
	return out
}

func boolField(m config.Map, key string) bool {
	b, _ := m[key].(bool)
	return b
}

func intField(m config.Map, key string, def int) int {
	switch v := m[key].(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
