// Package autobackup is the native Go port of
// pwnagotchi/plugins/default/auto_backup.py: periodically (and on
// manual web-triggered request) archives a configured set of
// files/directories into a timestamped tar.gz under a configured backup
// location, rotating away the oldest backups beyond a keep-count.
//
// Original Python author: WPA2 (see auto_backup.py's own __author__
// field, left untouched). This Go port is by raf181.
//
// The real plugin already shells out to a real `tar` binary with
// shell=False (argv, not a shell string) — this port preserves that
// exactly via the injected CommandRunner, rather than reimplementing tar
// in Go: real Python is not working around a missing library here, it is
// deliberately using the same tar package Debian/Raspberry Pi OS already
// ships, and doing anything else would risk subtly different archive
// semantics (permissions, symlink handling, GNU tar extensions) for no
// benefit.
package autobackup

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

var defaultFiles = []string{
	"/root/settings.yaml",
	"/root/client_secrets.json",
	"/root/.api-report.json",
	"/root/.ssh",
	"/root/.bashrc",
	"/root/.profile",
	"/root/peers",
	"/etc/pwnagotchi/",
	"/usr/local/share/pwnagotchi/custom-plugins",
	"/etc/ssh/",
	"/home/pi/handshakes/",
	"/home/pi/.bashrc",
	"/home/pi/.profile",
	"/home/pi/.wpa_sec_uploads",
}

var defaultExclude = []string{
	"/etc/pwnagotchi/logs/*",
	"*.bak",
	"*.tmp",
}

const (
	defaultIntervalSeconds = 60 * 60
	defaultMaxBackups      = 3
	defaultStatusFile      = "/root/.auto-backup"
	commandTimeout         = 5 * time.Minute
)

// Plugin ports the AutoBackup class.
type Plugin struct {
	mu sync.Mutex

	ready            bool
	hostname         string
	files            []string
	intervalSeconds  int
	maxBackups       int
	exclude          []string
	include          []string
	commands         []string
	backupLocation   string
	statusFile       string // overridable for tests, matches Python's self.status_file
	backupInProgress bool
	tries            int

	exec  pluginmanager.CommandRunner
	clock pluginmanager.Clock
	view  pluginmanager.ViewCapability
	log   pluginmanager.Logger
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// New ports AutoBackup.__init__.
func New() *Plugin {
	hostname, _ := os.Hostname()
	return &Plugin{
		hostname:   hostname,
		statusFile: defaultStatusFile,
	}
}

func (p *Plugin) Name() string { return "auto_backup" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "2.2",
		Author:      "WPA2 (original), Go port by raf181",
		License:     "GPL3",
		Description: "Backs up Pwnagotchi configuration and data, keeping recent backups.",
		HasWebhook:  true,
	}
}

// OnLoad ports on_loaded: validates backup_location, reads config with
// internal defaults (never mutating the config map itself, matching
// Python's own "DO NOT modify self.options" comment).
func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.exec = caps.Exec
	p.view = caps.View
	p.log = caps.Log
	p.clock = caps.Clock
	if p.clock == nil {
		p.clock = realClock{}
	}

	loc := stringField(caps.Config, "backup_location", "")
	if loc == "" {
		p.logf("Option 'backup_location' is not set.")
		return nil
	}
	p.backupLocation = loc

	p.files = stringSliceFieldOrDefault(caps.Config, "files", defaultFiles)
	p.intervalSeconds = intField(caps.Config, "interval_seconds", defaultIntervalSeconds)
	p.maxBackups = intField(caps.Config, "max_backups_to_keep", defaultMaxBackups)
	p.exclude = stringSliceFieldOrDefault(caps.Config, "exclude", defaultExclude)
	p.include = stringSliceFieldOrDefault(caps.Config, "include", nil)

	commands := stringSliceFieldOrDefault(caps.Config, "commands", []string{"tar", "czf"})
	if len(commands) == 0 {
		commands = []string{"tar", "czf"}
	}
	p.commands = commands

	p.ready = true
	p.logf("Plugin loaded for host '%s'. Interval: %ds, Backups kept: %d", p.hostname, p.intervalSeconds, p.maxBackups)
	return nil
}

// HandleEvent ports on_ui_update's periodic due-check (see cache.go for
// the same "gate a periodic action behind ui_update" pattern this port
// already established) instead of Python's own persistent
// threading.Thread scheduler loop sleeping 60s at a time — functionally
// equivalent (both just periodically re-check "is a backup due"), but
// reuses the daemon's existing render-tick event instead of spawning a
// dedicated always-running goroutine of its own.
func (p *Plugin) HandleEvent(event string, _ []interface{}) {
	if event != "ui_update" {
		return
	}
	p.mu.Lock()
	ready := p.ready
	inProgress := p.backupInProgress
	tries := p.tries
	due := ready && !inProgress && tries < 3 && p.isBackupDueLocked()
	p.mu.Unlock()
	if !due {
		return
	}
	p.startBackup()
}

// isBackupDueLocked must be called with p.mu held.
func (p *Plugin) isBackupDueLocked() bool {
	info, err := os.Stat(p.statusFile)
	if err != nil {
		return true
	}
	return p.clock.Now().Sub(info.ModTime()) >= time.Duration(p.intervalSeconds)*time.Second
}

// OnWebhook ports on_webhook: GET renders a status/config page with a
// manual-backup form, POST "backup" triggers one.
func (p *Plugin) OnWebhook(subpath string, r *http.Request) (pluginmanager.WebhookResponse, error) {
	switch {
	case r.Method == http.MethodGet && (subpath == "" || subpath == "/"):
		return pluginmanager.WebhookResponse{Status: http.StatusOK, Body: []byte(p.statusPageHTML())}, nil
	case r.Method == http.MethodPost && (subpath == "backup" || subpath == "/backup"):
		result := p.ManualBackup()
		body := fmt.Sprintf(`<html><head><title>AUTO Backup</title></head><body><h1>AUTO Backup</h1><p><b>%s</b></p><a href="/plugins/auto_backup/">Back</a></body></html>`, result)
		return pluginmanager.WebhookResponse{Status: http.StatusOK, Body: []byte(body)}, nil
	default:
		return pluginmanager.WebhookResponse{Status: http.StatusNotFound, Body: []byte("Not found")}, nil
	}
}

func (p *Plugin) statusPageHTML() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	status := "Ready"
	if p.backupInProgress {
		status = "Backup in progress..."
	}
	loc := p.backupLocation
	if loc == "" {
		loc = "Not set"
	}
	include := "None"
	if len(p.include) > 0 {
		include = strings.Join(p.include, ", ")
	}
	var b strings.Builder
	b.WriteString(`<html><head><title>AUTO Backup</title></head><body>`)
	b.WriteString("<h1>AUTO Backup</h1>")
	b.WriteString("<p>Status: <b>" + status + "</b></p>")
	b.WriteString(`<form method="POST" action="/plugins/auto_backup/backup">`)
	b.WriteString(`<input type="submit" value="Start Manual Backup" class="btn primary"></form><hr>`)
	b.WriteString("<h2>Configuration</h2>")
	b.WriteString(`<table border="1" cellpadding="5">`)
	b.WriteString("<tr><td><b>Backup Location:</b></td><td>" + loc + "</td></tr>")
	b.WriteString("<tr><td><b>Interval:</b></td><td>" + strconv.Itoa(p.intervalSeconds/60) + " minutes</td></tr>")
	b.WriteString("<tr><td><b>Max Backups:</b></td><td>" + strconv.Itoa(p.maxBackups) + "</td></tr>")
	b.WriteString("<tr><td><b>Include Paths:</b></td><td>" + include + "</td></tr>")
	b.WriteString("</table></body></html>")
	return b.String()
}

// ManualBackup ports manual_backup.
func (p *Plugin) ManualBackup() string {
	p.mu.Lock()
	if p.backupInProgress {
		p.mu.Unlock()
		return "Backup already in progress"
	}
	files := p.existingBackupFilesLocked()
	p.mu.Unlock()
	if len(files) == 0 {
		return "No files to backup"
	}
	p.startBackup()
	return "Backup started - check logs for details"
}

// existingBackupFilesLocked must be called with p.mu held.
func (p *Plugin) existingBackupFilesLocked() []string {
	var out []string
	for _, f := range p.files {
		if _, err := os.Stat(f); err == nil {
			out = append(out, f)
		}
	}
	for _, f := range p.include {
		if _, err := os.Stat(f); err == nil {
			out = append(out, f)
		}
	}
	return out
}

// startBackup runs the backup in a background goroutine, mirroring
// Python's threading.Thread(target=self._run_backup_thread, ...).
func (p *Plugin) startBackup() {
	p.mu.Lock()
	files := p.existingBackupFilesLocked()
	if len(files) == 0 {
		p.mu.Unlock()
		p.logf("No files to backup exist")
		return
	}
	p.backupInProgress = true
	p.mu.Unlock()

	go p.runBackup(files)
}

// runBackup ports _run_backup_thread.
func (p *Plugin) runBackup(files []string) {
	defer func() {
		p.mu.Lock()
		p.backupInProgress = false
		p.mu.Unlock()
	}()

	p.mu.Lock()
	loc := p.backupLocation
	hostname := p.hostname
	commands := append([]string(nil), p.commands...)
	exclude := append([]string(nil), p.exclude...)
	view := p.view
	p.mu.Unlock()

	if err := os.MkdirAll(loc, 0o755); err != nil {
		p.logf("Failed to create backup directory: %v", err)
		p.recordFailure()
		return
	}

	timestamp := p.clock.Now().Format("20060102-150405")
	backupFile := filepath.Join(loc, fmt.Sprintf("%s-backup-%s.tar.gz", hostname, timestamp))

	if view != nil {
		view.Set("status", "Backing up...")
		view.Update(false)
	}

	p.logf("Starting backup to %s...", backupFile)

	if p.exec == nil {
		p.logf("no CommandRunner available, cannot run backup")
		p.recordFailure()
		return
	}

	args := append([]string(nil), commands[1:]...)
	args = append(args, backupFile)
	for _, pattern := range exclude {
		args = append(args, "--exclude="+pattern)
	}
	args = append(args, files...)

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	if out, err := p.exec.Run(ctx, commands[0], args...); err != nil {
		p.logf("Backup command failed: %v (%s)", err, string(out))
		p.recordFailure()
		return
	}

	p.logf("Backup successful: %s", backupFile)
	p.cleanupOldBackups()

	if view != nil {
		view.Set("status", "Backup done!")
		view.Update(false)
	}

	if err := touch(p.statusFile, p.clock.Now()); err != nil {
		p.logf("updating status file: %v", err)
	}

	p.mu.Lock()
	p.tries = 0
	p.mu.Unlock()
}

func (p *Plugin) recordFailure() {
	p.mu.Lock()
	p.tries++
	p.mu.Unlock()
}

// cleanupOldBackups ports _cleanup_old_backups.
func (p *Plugin) cleanupOldBackups() {
	p.mu.Lock()
	loc := p.backupLocation
	hostname := p.hostname
	maxKeep := p.maxBackups
	p.mu.Unlock()

	pattern := filepath.Join(loc, hostname+"-backup-*.tar.gz")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return
	}

	type fileInfo struct {
		path string
		mod  time.Time
	}
	infos := make([]fileInfo, 0, len(matches))
	for _, m := range matches {
		st, err := os.Stat(m)
		if err != nil {
			continue
		}
		infos = append(infos, fileInfo{m, st.ModTime()})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].mod.Before(infos[j].mod) })

	if len(infos) <= maxKeep {
		return
	}
	toDelete := infos[:len(infos)-maxKeep]
	p.logf("Found %d backups, keeping %d, deleting %d old backup(s)...", len(infos), maxKeep, len(toDelete))
	for _, f := range toDelete {
		if err := os.Remove(f.path); err != nil {
			p.logf("Failed to delete %s: %v", f.path, err)
		} else {
			p.logf("Deleted: %s", filepath.Base(f.path))
		}
	}
}

func touch(path string, t time.Time) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		f.Close()
	}
	return os.Chtimes(path, t, t)
}

func (p *Plugin) logf(format string, args ...interface{}) {
	if p.log != nil {
		p.log.Printf(format, args...)
	}
}

func stringField(m config.Map, key, def string) string {
	if m == nil {
		return def
	}
	if s, ok := m[key].(string); ok && s != "" {
		return s
	}
	return def
}

func intField(m config.Map, key string, def int) int {
	if m == nil {
		return def
	}
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

func stringSliceFieldOrDefault(m config.Map, key string, def []string) []string {
	if m == nil {
		return def
	}
	raw, ok := m[key]
	if !ok {
		return def
	}
	switch v := raw.(type) {
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{v}
	default:
		return def
	}
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
var _ pluginmanager.WebhookHandler = (*Plugin)(nil)
