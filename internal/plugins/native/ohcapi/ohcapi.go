// Package ohcapi is the native Go port of
// pwnagotchi/plugins/default/ohcapi.py: uploads WPA/WPA2 handshakes to
// OnlineHashCrack.com using their v2 API (no dashboard), converting each
// captured .pcap to a .22000 hash file via the real hcxpcapngtool CLI
// first.
//
// Original Python author: Rohan Dayaram (see ohcapi.py's own __author__
// field, left untouched). This Go port is by raf181.
package ohcapi

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

const (
	addTasksURL       = "https://api.onlinehashcrack.com/v2"
	internetCheckURL  = "https://www.google.com"
	webhookRedirect   = "https://www.onlinehashcrack.com"
	defaultSleep      = time.Hour
	batchSize         = 50
	hcxTimeout        = 5 * time.Minute
	internetCheckTime = 5 * time.Second
	uploadTimeout     = 30 * time.Second
)

// reportPath is where uploaded/processed state persists — overridable
// for tests, matching this port's established package-level-var override
// pattern (e.g. internal/wpasec.DBPath).
var reportPath = "/etc/pwnagotchi/handshakes/.ohc_uploads"

// addTasksURLVar/internetCheckURLVar/webhookRedirectVar are overridable
// for tests so they never hit a real external host (see
// internetCheckURL/addTasksURL's doc comments above for the real,
// production-default values).
var (
	addTasksURLVar      = addTasksURL
	internetCheckURLVar = internetCheckURL
	webhookRedirectVar  = webhookRedirect
)

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// reportState ports the StatusFile-persisted `{'reported': [...],
// 'processed_stations': [...]}` JSON document.
type reportState struct {
	Reported          []string    `json:"reported"`
	ProcessedStations [][2]string `json:"processed_stations"`
}

// Plugin ports the ohcapi class.
type Plugin struct {
	mu sync.Mutex

	log        pluginmanager.Logger
	exec       pluginmanager.CommandRunner
	httpClient *http.Client
	clock      pluginmanager.Clock
	view       pluginmanager.ViewCapability

	ready        bool
	apiKey       string
	receiveEmail string
	sleep        time.Duration
	handshakeDir string
	lastRun      time.Time
	internetOK   bool
	skip         map[string]bool
	report       reportState
}

func New() *Plugin { return &Plugin{skip: map[string]bool{}} }

func (p *Plugin) Name() string { return "ohcapi" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "1.1.0",
		Author:      "Rohan Dayaram",
		License:     "GPL3",
		HasWebhook:  true,
		Description: "Uploads WPA/WPA2 handshakes to OnlineHashCrack.com using the new API (V2), no dashboard.",
	}
}

// OnLoad ports on_loaded: requires api_key, defaults receive_email/sleep.
func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.log = caps.Log
	p.exec = caps.Exec
	p.view = caps.View
	p.clock = caps.Clock
	if p.clock == nil {
		p.clock = realClock{}
	}
	p.httpClient = caps.HTTPClient
	if p.httpClient == nil {
		p.httpClient = http.DefaultClient
	}

	p.apiKey, _ = caps.Config["api_key"].(string)
	if p.apiKey == "" {
		// caps.Log used directly (not p.logf): OnLoad already holds p.mu
		// via the deferred unlock above, and logf's own locking would
		// otherwise self-deadlock on this non-reentrant mutex.
		if caps.Log != nil {
			caps.Log.Printf("OHC NewAPI: Missing required config fields: [api_key]")
		}
		return nil
	}
	p.receiveEmail = stringField(caps.Config, "receive_email", "yes")
	p.sleep = durationField(caps.Config, "sleep", defaultSleep)

	if st, err := loadReportState(); err == nil {
		p.report = st
	}

	p.ready = true
	if caps.Log != nil {
		caps.Log.Printf("OHC NewAPI: Plugin loaded and ready.")
	}
	return nil
}

// OnWebhook ports on_webhook: an unconditional 302 redirect, no matter
// the subpath/method.
func (p *Plugin) OnWebhook(subpath string, r *http.Request) (pluginmanager.WebhookResponse, error) {
	return pluginmanager.WebhookResponse{
		Status:  http.StatusFound,
		Headers: map[string]string{"Location": webhookRedirectVar},
	}, nil
}

// HandleEvent ports on_config_changed/on_internet_available/on_ui_update.
func (p *Plugin) HandleEvent(event string, args []interface{}) {
	switch event {
	case "config_changed":
		p.onConfigChanged(args)
	case "internet_available":
		p.onInternetAvailable()
	case "ui_update":
		p.onUIUpdate()
	}
}

func (p *Plugin) onConfigChanged(args []interface{}) {
	if len(args) < 1 {
		return
	}
	fullCfg, ok := args[0].(config.Map)
	if !ok {
		return
	}
	bettercapCfg, _ := fullCfg["bettercap"].(config.Map)
	handshakeDir, _ := bettercapCfg["handshakes"].(string)
	p.mu.Lock()
	p.handshakeDir = handshakeDir
	p.mu.Unlock()
}

// onInternetAvailable ports on_internet_available: run immediately, once,
// whenever the daemon signals connectivity just came up.
func (p *Plugin) onInternetAvailable() {
	p.mu.Lock()
	ready := p.ready
	p.mu.Unlock()
	if !ready {
		return
	}
	p.mu.Lock()
	p.internetOK = true
	p.mu.Unlock()
	p.runTasks()
	p.mu.Lock()
	p.lastRun = p.clock.Now()
	p.mu.Unlock()
}

// onUIUpdate ports on_ui_update: re-check connectivity by pinging a real
// host (overridable for tests), then re-run tasks every `sleep` interval.
func (p *Plugin) onUIUpdate() {
	p.mu.Lock()
	ready := p.ready
	client := p.httpClient
	p.mu.Unlock()
	if !ready {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), internetCheckTime)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, internetCheckURLVar, nil)
	resp, err := client.Do(req)
	online := err == nil && resp != nil && resp.StatusCode == http.StatusOK
	if resp != nil {
		resp.Body.Close()
	}

	p.mu.Lock()
	p.internetOK = online
	sleep := p.sleep
	last := p.lastRun
	now := p.clock.Now()
	due := online && now.Sub(last) >= sleep
	p.mu.Unlock()

	if due {
		p.runTasks()
		p.mu.Lock()
		p.lastRun = now
		p.mu.Unlock()
	}
}

// runTasks ports _run_tasks: find new .pcap files, extract+upload hashes,
// persist reported/processed-station state.
func (p *Plugin) runTasks() {
	p.mu.Lock()
	handshakeDir := p.handshakeDir
	apiKey := p.apiKey
	receiveEmail := p.receiveEmail
	view := p.view
	p.mu.Unlock()
	if handshakeDir == "" {
		return
	}

	entries, err := os.ReadDir(handshakeDir)
	if err != nil {
		p.logf("OHC NewAPI: cannot list %s: %v", handshakeDir, err)
		return
	}

	p.mu.Lock()
	reported := map[string]bool{}
	for _, r := range p.report.Reported {
		reported[r] = true
	}
	processedStations := map[[2]string]bool{}
	for _, s := range p.report.ProcessedStations {
		processedStations[s] = true
	}
	p.mu.Unlock()

	var newPcaps []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pcap") {
			continue
		}
		full := filepath.Join(handshakeDir, e.Name())
		hccapx := strings.TrimSuffix(full, ".pcap") + ".22000"
		if _, err := os.Stat(hccapx); err == nil {
			continue // already extracted+uploaded previously
		}
		p.mu.Lock()
		skipped := p.skip[full]
		p.mu.Unlock()
		if reported[full] || skipped {
			continue
		}
		newPcaps = append(newPcaps, full)
	}
	if len(newPcaps) == 0 {
		p.logf("OHC NewAPI: No new PCAP files to process.")
		return
	}
	p.logf("OHC NewAPI: Processing %d new PCAP handshakes.", len(newPcaps))

	var allHashes []string
	var successfullyExtracted []string
	essidBssidMap := map[string][2]string{}

	for _, pcapPath := range newPcaps {
		hashes := p.extractHashesFromHandshake(pcapPath)
		if len(hashes) == 0 {
			p.mu.Lock()
			p.skip[pcapPath] = true
			p.mu.Unlock()
			continue
		}
		essid, bssid := extractEssidBssidFromHash(hashes[0])
		station := [2]string{essid, bssid}
		if processedStations[station] {
			p.mu.Lock()
			p.skip[pcapPath] = true
			p.mu.Unlock()
			continue
		}
		allHashes = append(allHashes, hashes...)
		successfullyExtracted = append(successfullyExtracted, pcapPath)
		essidBssidMap[pcapPath] = station
	}

	if len(allHashes) == 0 {
		p.logf("OHC NewAPI: No hashes were extracted from the new pcaps. Nothing to upload.")
		return
	}

	uploadSuccess := true
	for i := 0; i < len(allHashes); i += batchSize {
		end := i + batchSize
		if end > len(allHashes) {
			end = len(allHashes)
		}
		batch := allHashes[i:end]
		if view != nil {
			view.OnUploading(fmt.Sprintf("onlinehashcrack.com (%d/%d)", end, len(allHashes)))
		}
		if !p.addTasks(batch, apiKey, receiveEmail) {
			uploadSuccess = false
			break
		}
	}

	if uploadSuccess {
		p.mu.Lock()
		for _, path := range successfullyExtracted {
			p.report.Reported = append(p.report.Reported, path)
			p.report.ProcessedStations = append(p.report.ProcessedStations, essidBssidMap[path])
		}
		st := p.report
		p.mu.Unlock()
		if err := saveReportState(st); err != nil {
			p.logf("OHC NewAPI: failed to persist report state: %v", err)
		}
		p.logf("OHC NewAPI: Successfully reported all new handshakes.")
	} else {
		p.mu.Lock()
		for _, path := range successfullyExtracted {
			p.skip[path] = true
		}
		p.mu.Unlock()
		p.logf("OHC NewAPI: Failed to upload tasks, added to skip list.")
	}

	if view != nil {
		view.OnNormal()
	}
}

// extractHashesFromHandshake ports _extract_hashes_from_handshake: runs
// the real hcxpcapngtool CLI (argv form, never a shell string) and reads
// back the resulting .22000 lines.
func (p *Plugin) extractHashesFromHandshake(pcapPath string) []string {
	hccapxPath := strings.TrimSuffix(pcapPath, ".pcap") + ".22000"
	p.mu.Lock()
	exec := p.exec
	p.mu.Unlock()
	if exec != nil {
		ctx, cancel := context.WithTimeout(context.Background(), hcxTimeout)
		_, _ = exec.Run(ctx, "hcxpcapngtool", "-o", hccapxPath, pcapPath)
		cancel()
	}

	info, err := os.Stat(hccapxPath)
	if err != nil || info.Size() == 0 {
		p.logf("OHC NewAPI: Failed to extract hashes from %s", pcapPath)
		if err == nil {
			os.Remove(hccapxPath)
		}
		return nil
	}
	data, err := os.ReadFile(hccapxPath)
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// addTasks ports _add_tasks: POST a batch of 22000 hash lines as JSON.
func (p *Plugin) addTasks(hashes []string, apiKey, receiveEmail string) bool {
	var clean []string
	for _, h := range hashes {
		h = strings.TrimSpace(h)
		if h != "" {
			clean = append(clean, h)
		}
	}
	if len(clean) == 0 {
		return true
	}

	payload := map[string]interface{}{
		"api_key":       apiKey,
		"agree_terms":   "yes",
		"action":        "add_tasks",
		"algo_mode":     22000,
		"hashes":        clean,
		"receive_email": receiveEmail,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), uploadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, addTasksURLVar, bytes.NewReader(data))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")

	p.mu.Lock()
	client := p.httpClient
	p.mu.Unlock()
	resp, err := client.Do(req)
	if err != nil {
		p.logf("OHC NewAPI: Exception while adding tasks -> %v", err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		p.logf("OHC NewAPI: Exception while adding tasks -> HTTP %d", resp.StatusCode)
		return false
	}
	var out interface{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	p.logf("OHC NewAPI: Add tasks response: %v", out)
	return true
}

// extractEssidBssidFromHash ports _extract_essid_bssid_from_hash: a
// 22000-format hash line, split on '*', field 5 = hex-encoded ESSID,
// field 3 = 12-hex-char AP MAC without colons. Never panics on malformed/
// short input — this is untrusted external tool output.
func extractEssidBssidFromHash(hashLine string) (essid, bssid string) {
	essid = "unknown_ESSID"
	bssid = "00:00:00:00:00:00"

	parts := strings.Split(strings.TrimSpace(hashLine), "*")
	if len(parts) > 5 {
		if decoded, err := hex.DecodeString(parts[5]); err == nil {
			essid = string(decoded)
		}
	}
	if len(parts) > 3 {
		apmac := parts[3]
		if len(apmac) == 12 {
			var b strings.Builder
			for i := 0; i < 12; i += 2 {
				if i > 0 {
					b.WriteByte(':')
				}
				b.WriteString(apmac[i : i+2])
			}
			bssid = b.String()
		}
	}
	return essid, bssid
}

func loadReportState() (reportState, error) {
	data, err := os.ReadFile(reportPath)
	if err != nil {
		return reportState{}, err
	}
	var st reportState
	if err := json.Unmarshal(data, &st); err != nil {
		return reportState{}, err
	}
	return st, nil
}

func saveReportState(st reportState) error {
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(reportPath, data, 0o644)
}

func (p *Plugin) logf(format string, args ...interface{}) {
	p.mu.Lock()
	log := p.log
	p.mu.Unlock()
	if log != nil {
		log.Printf(format, args...)
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

func durationField(m config.Map, key string, def time.Duration) time.Duration {
	if m == nil {
		return def
	}
	switch v := m[key].(type) {
	case int64:
		return time.Duration(v) * time.Second
	case float64:
		return time.Duration(v) * time.Second
	case int:
		return time.Duration(v) * time.Second
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return def
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
var _ pluginmanager.WebhookHandler = (*Plugin)(nil)
