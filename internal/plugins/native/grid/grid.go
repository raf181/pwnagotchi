// Package grid is the native Go port of
// pwnagotchi/plugins/default/grid.py: reports the unit's identity and
// list of pwned networks to opwngrid.xyz, and checks the pwngrid inbox.
//
// Original Python author: evilsocket@gmail.com (see grid.py's own
// __author__ field, left untouched). This Go port is by raf181.
//
// pluginmanager.GridCapability
// (Report/MemoryGet/MemorySet) does not match the real, already-ported
// internal/grid.Client's actual API (ReportAP/Inbox/UpdateData) at all —
// the two were designed independently. Rather than force a mismatch,
// this plugin defines its own narrow GridClient interface
// (below) that the real *grid.Client already satisfies structurally, and
// expects to be constructed with one directly (mirroring
// internal/wpasec's cfg-in-constructor pattern) rather than receiving it
// via Capabilities.Grid. main.go passes the daemon's real,
// already-constructed *grid.Client here.
//
// One API mismatch remains constructor-injected:
//  1. grid.py's on_internet_available passes `agent.last_session` (real
//     Python: pwnagotchi.log.LastSession, the daemon's own running
//     session statistics) to grid.update_data — pluginmanager.
//     AgentCapability exposes bettercap's session() (wifi/AP state), not
//     the daemon's own log/session statistics, so there is no capability
//     path to it. SessionSummaryFunc (constructor-injected, like
//     GridClient) lets the coordinator wire this to the real
//     *agent.Agent.LastSession if it chooses; a nil func reports a
//     zero-value summary rather than fabricating data.
package grid

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	realgrid "github.com/jayofelony/pwnagotchi/internal/grid"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// ReportedPath mirrors Python's hardcoded
// StatusFile('/root/.api-report.json', data_format='json') — a fixed
// absolute path, NOT derived from bettercap.handshakes. Overridable for
// tests, matching this port's established package-level-var override
// pattern (e.g. internal/wpasec.DBPath).
var ReportedPath = "/root/.api-report.json"

// reportedFile mirrors Python's StatusFile('/root/.api-report.json',
// data_format='json') shape: {"reported": [...]}.
type reportedFile struct {
	Reported []string `json:"reported"`
}

func loadReportedList(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f reportedFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return f.Reported, nil
}

func saveReportedList(path string, names []string) error {
	data, err := json.Marshal(reportedFile{Reported: names})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// GridClient is the narrow subset of *internal/grid.Client this plugin
// needs — see the package doc comment for why this isn't
// pluginmanager.GridCapability.
type GridClient interface {
	ReportAP(essid, bssid string) bool
	Inbox(page int, withPager bool) (interface{}, error)
	UpdateData(cfg config.Map, session realgrid.SessionSummary) error
}

type unreadMessageView interface {
	OnUnreadMessages(count, total int)
}

// Plugin ports the Grid class.
type Plugin struct {
	mu sync.Mutex

	client         GridClient
	sessionSummary func() realgrid.SessionSummary
	log            pluginmanager.Logger
	emit           func(event string, args ...interface{})
	unreadView     unreadMessageView
	fullCfg        config.Map
	reportEnabled  bool
	whitelist      []string
	handshakesDir  string
	reportedPath   string
	reported       map[string]bool
	unreadMessages int
	totalMessages  int
}

// New constructs the plugin with its real pwngrid client (see package doc
// comment for why this is constructor-injected rather than
// capability-injected) and an optional session-summary source (nil is
// fine — a zero-value summary is reported rather than fabricated data).
func New(client GridClient, sessionSummary func() realgrid.SessionSummary) *Plugin {
	return &Plugin{client: client, sessionSummary: sessionSummary, reported: map[string]bool{}}
}

func (p *Plugin) Name() string { return "grid" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "1.1.0",
		Author:      "evilsocket@gmail.com (original), Go port by raf181",
		License:     "GPL3",
		Description: "This plugin signals the unit cryptographic identity and list of pwned networks and list of pwned networks to opwngrid.xyz",
		HasWebhook:  true,
	}
}

func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	p.log = caps.Log
	p.emit = caps.Emit
	p.unreadView, _ = caps.View.(unreadMessageView)
	p.mu.Unlock()
	return nil
}

// OnWebhook ports on_webhook: a plain redirect to opwngrid.xyz.
func (p *Plugin) OnWebhook(_ string, _ *http.Request) (pluginmanager.WebhookResponse, error) {
	return pluginmanager.WebhookResponse{
		Status:  http.StatusFound,
		Headers: map[string]string{"Location": "https://opwngrid.xyz"},
	}, nil
}

func (p *Plugin) logf(format string, args ...interface{}) {
	p.mu.Lock()
	log := p.log
	p.mu.Unlock()
	if log != nil {
		log.Printf(format, args...)
	}
}

// HandleEvent ports config_changed + on_internet_available.
func (p *Plugin) HandleEvent(event string, args []interface{}) {
	switch event {
	case "config_changed":
		if len(args) < 1 {
			return
		}
		fullCfg, ok := args[0].(config.Map)
		if !ok {
			return
		}
		p.onConfigChanged(fullCfg)
	case "internet_available":
		p.onInternetAvailable()
	}
}

func (p *Plugin) onConfigChanged(fullCfg config.Map) {
	mainCfg, _ := fullCfg["main"].(config.Map)
	pluginsCfg, _ := mainCfg["plugins"].(config.Map)
	opts, _ := pluginsCfg["grid"].(config.Map)
	bettercapCfg, _ := fullCfg["bettercap"].(config.Map)

	handshakesDir, _ := bettercapCfg["handshakes"].(string)
	var whitelist []string
	if raw, ok := mainCfg["whitelist"].([]interface{}); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				whitelist = append(whitelist, s)
			}
		}
	}

	p.mu.Lock()
	p.fullCfg = fullCfg
	p.reportEnabled, _ = opts["report"].(bool)
	p.whitelist = whitelist
	p.handshakesDir = handshakesDir
	p.reportedPath = ReportedPath
	p.mu.Unlock()

	p.loadReported()
}

func (p *Plugin) loadReported() {
	p.mu.Lock()
	path := p.reportedPath
	p.mu.Unlock()
	if path == "" {
		return
	}
	names, err := loadReportedList(path)
	if err != nil {
		return
	}
	p.mu.Lock()
	for _, n := range names {
		p.reported[n] = true
	}
	p.mu.Unlock()
}

func (p *Plugin) setReported(netID string) {
	p.mu.Lock()
	p.reported[netID] = true
	names := make([]string, 0, len(p.reported))
	for n := range p.reported {
		names = append(names, n)
	}
	path := p.reportedPath
	p.mu.Unlock()
	if path != "" {
		_ = saveReportedList(path, names)
	}
}

func (p *Plugin) isExcluded(what string) bool {
	p.mu.Lock()
	whitelist := p.whitelist
	p.mu.Unlock()
	what = strings.ToLower(what)
	for _, skip := range whitelist {
		skip = strings.ToLower(skip)
		if strings.Contains(what, skip) || strings.Contains(what, strings.ReplaceAll(skip, ":", "")) {
			return true
		}
	}
	return false
}

var macRe = regexp.MustCompile(`^[0-9a-fA-F]{12}`)

// parsePcapFilename ports parse_pcap's filename-based fallback (the
// primary Scapy-based extraction path already exists as
// internal/wifiparse.ExtractFromPCAP, used by wigle; grid.py's own
// parse_pcap only ever used the filename fallback in the common case
// since ESSID/BSSID are already encoded in "ESSID_BSSID.pcap"/"BSSID.pcap"
// naming, matching real handshake filenames written by this daemon).
func parsePcapFilename(filename string) (essid, bssid string) {
	base := strings.TrimSuffix(filepath.Base(filename), ".pcap")
	var macPart string
	if idx := strings.Index(base, "_"); idx >= 0 {
		essid, macPart = base[:idx], base[idx+1:]
	} else {
		macPart = base
	}
	if !macRe.MatchString(macPart) || len(macPart) < 12 {
		return "", ""
	}
	macPart = macPart[:12]
	var b strings.Builder
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(macPart[i : i+2])
	}
	return essid, b.String()
}

func (p *Plugin) checkHandshakes() {
	p.mu.Lock()
	handshakesDir := p.handshakesDir
	reportEnabled := p.reportEnabled
	p.mu.Unlock()
	if handshakesDir == "" || !reportEnabled {
		return
	}

	entries, err := os.ReadDir(handshakesDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pcap") {
			continue
		}
		netID := strings.TrimSuffix(e.Name(), ".pcap")
		p.mu.Lock()
		already := p.reported[netID]
		p.mu.Unlock()
		if already {
			continue
		}
		if p.isExcluded(netID) {
			p.setReported(netID)
			continue
		}
		essid, bssid := parsePcapFilename(e.Name())
		if bssid == "" {
			p.logf("no bssid found for %s", e.Name())
			continue
		}
		if p.isExcluded(essid) || p.isExcluded(bssid) {
			p.setReported(netID)
			continue
		}
		if p.client != nil && p.client.ReportAP(essid, bssid) {
			p.setReported(netID)
		}
		time.Sleep(1500 * time.Millisecond)
	}
}

func (p *Plugin) checkInbox() {
	if p.client == nil {
		return
	}
	raw, err := p.client.Inbox(0, false)
	if err != nil {
		return
	}
	messages, _ := raw.([]interface{})
	total := len(messages)
	unread := 0
	for _, m := range messages {
		msg, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		if msg["seen_at"] == nil {
			unread++
		}
	}
	p.mu.Lock()
	p.unreadMessages, p.totalMessages = unread, total
	emit := p.emit
	view := p.unreadView
	p.mu.Unlock()
	if unread > 0 {
		if emit != nil {
			emit("unread_inbox", unread)
		}
		if view != nil {
			view.OnUnreadMessages(unread, total)
		}
		p.logf("unread:%d total:%d", unread, total)
	}
}

func (p *Plugin) onInternetAvailable() {
	if p.client == nil {
		return
	}
	p.mu.Lock()
	fullCfg := p.fullCfg
	summaryFn := p.sessionSummary
	p.mu.Unlock()

	var summary realgrid.SessionSummary
	if summaryFn != nil {
		summary = summaryFn()
	}
	if err := p.client.UpdateData(fullCfg, summary); err != nil {
		p.logf("error connecting to the pwngrid-peer service: %v", err)
		return
	}
	p.checkInbox()
	p.checkHandshakes()
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
