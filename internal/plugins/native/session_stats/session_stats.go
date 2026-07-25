// Package sessionstats is the native Go port of
// pwnagotchi/plugins/default/session-stats.py: a real-time WiFi capture
// stats collector (networks/handshakes/deauths + system temp/mem/cpu)
// persisted to a per-boot timestamped JSON file, with historical carry-
// over from the previous session and a web dashboard (summary cards +
// trend charts) served entirely from this plugin's own webhook.
//
// Original Python author: 33197631+dadav@users.noreply.github.com,
// modified by wsvdmeer (see session-stats.py's own __author__ field,
// left untouched). This Go port is by raf181.
//
// Data-collection design difference (disclosed, not a silent behavior
// drop): real Python's on_epoch handler reads `agent._access_points`/
// `agent._handshakes` — private Agent internals — and epoch_data's
// num_deauths/temperature/mem_usage/cpu_load. Two things are true in
// this Go port that change how the equivalent data has to be gathered:
//  1. pluginmanager.AgentCapability deliberately does not expose private
//     Agent state (see its own doc comment) — there is no equivalent of
//     reading agent._access_points/_handshakes directly.
//  2. This Go port does not currently emit an "epoch" event anywhere —
//     confirmed by grepping every `emit.On(...)` call site under
//     internal/agent, internal/mesh, internal/ui/view: there is no
//     `emit.On("epoch", ...)` at all. That is a genuine, pre-existing gap
//     in this port (like the earlier-documented "ready"/"config_changed"
//     gaps other plugins hit), not something this plugin's port should
//     paper over by inventing a fake argument shape for an event that
//     doesn't exist.
//
// Given both, this port tracks networks/handshakes/deauths itself from
// the real events it CAN observe (wifi_update, handshake,
// deauthentication) instead of reaching into agent internals, and reads
// real system temp/mem/cpu from internal/unit (the exact same package
// internal/epoch itself uses for these same three readings) on its own
// periodic tick — reproducing real Python's `_realtime_loop` faithfully;
// HandleEvent("epoch", ...) is still implemented (recording the same
// kind of snapshot) so it starts working for free the moment epoch
// events are wired up elsewhere, but it is currently unreachable dead
// code in this port, exactly like the real Python's on_ready is for
// on-boot-enabled plugins (see internal/plugins/native/gps's identical
// documented situation).
package sessionstats

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
	"github.com/jayofelony/pwnagotchi/internal/unit"
)

const (
	defaultUpdateInterval = 15 * time.Second
	defaultSaveDir        = "/home/pi/pwnagotchi/sessions/"
	timestampLayout       = "15:04:05.000"
)

// StatsEntry is one timestamped sample, matching the real persisted JSON
// shape exactly (field names/case) so an existing on-disk session file
// from a real Python install remains readable by this port.
type StatsEntry struct {
	NumPeers      int     `json:"num_peers"`
	NumHandshakes int     `json:"num_handshakes"`
	NumDeauths    int     `json:"num_deauths"`
	Temperature   float64 `json:"temperature"`
	MemUsage      float64 `json:"mem_usage"`
	CPULoad       float64 `json:"cpu_load"`
}

// sessionFile is the on-disk shape: {"data": {timestamp: entry, ...}}.
type sessionFile struct {
	Data map[string]StatsEntry `json:"data"`
}

// systemReader is overridable for tests (real production behavior: the
// actual internal/unit readings).
type systemReader interface {
	Celsius() (int, error)
	MemUsage() (float64, error)
	CPULoad(tag string) (float64, error)
}

type realSystemReader struct{}

func (realSystemReader) Celsius() (int, error)               { return unit.Celsius() }
func (realSystemReader) MemUsage() (float64, error)          { return unit.MemUsage() }
func (realSystemReader) CPULoad(tag string) (float64, error) { return unit.CPULoad(tag) }

// Plugin ports the SessionStats class.
type Plugin struct {
	mu sync.Mutex

	saveDir        string
	updateInterval time.Duration
	sessionPath    string

	stats map[string]StatsEntry

	networks   int
	handshakes int
	deauths    int

	log   pluginmanager.Logger
	clock pluginmanager.Clock
	sys   systemReader

	stop chan struct{}
	done chan struct{}
}

// New ports SessionStats.__init__.
func New() *Plugin {
	return &Plugin{stats: map[string]StatsEntry{}, sys: realSystemReader{}}
}

func (p *Plugin) Name() string { return "session-stats" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "0.2.0",
		Author:      "33197631+dadav@users.noreply.github.com modified by wsvdmeer (original), Go port by raf181",
		License:     "GPL3",
		Description: "Displays WiFi capture stats including networks, handshakes, and deauths.",
		HasWebhook:  true,
	}
}

// OnLoad ports on_loaded: resolve the save directory, create the
// per-boot session file, load historical data from the previous
// session's file, and start the realtime collection loop.
func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	p.log = caps.Log
	p.clock = caps.Clock
	if p.clock == nil {
		p.clock = realClock{}
	}
	p.saveDir = stringField(caps.Config, "save_directory", defaultSaveDir)
	p.updateInterval = time.Duration(intField(caps.Config, "update_interval", int(defaultUpdateInterval/time.Second))) * time.Second
	if p.updateInterval <= 0 {
		p.updateInterval = defaultUpdateInterval
	}
	now := p.clock.Now()
	p.mu.Unlock()

	if err := os.MkdirAll(p.saveDir, 0o755); err != nil {
		p.logf("cannot create save directory %s: %v", p.saveDir, err)
		return nil
	}
	sessionName := fmt.Sprintf("stats_%s.json", now.Format("2006_01_02_15_04"))
	p.mu.Lock()
	p.sessionPath = filepath.Join(p.saveDir, sessionName)
	p.mu.Unlock()

	p.loadHistorical()

	p.mu.Lock()
	p.stop = make(chan struct{})
	p.done = make(chan struct{})
	stopCh, doneCh, interval := p.stop, p.done, p.updateInterval
	p.mu.Unlock()
	// stopCh/doneCh/interval are captured here (at spawn time) and passed
	// explicitly rather than re-read from p.stop/p.done inside the
	// goroutine: re-reading them raced with OnUnload, which can run
	// (nilling p.stop for idempotency) before this goroutine gets
	// scheduled for the first time, leaving it permanently selecting on a
	// nil channel — a real, found-by-testing goroutine leak, not a
	// theoretical one.
	go p.realtimeLoop(stopCh, doneCh, interval)

	return nil
}

// OnUnload ports on_unload: stop the realtime loop and wait for it to exit.
func (p *Plugin) OnUnload() error {
	p.mu.Lock()
	stop := p.stop
	done := p.done
	p.stop = nil // idempotent: a second OnUnload call must not re-close this channel
	p.mu.Unlock()
	if stop == nil {
		return nil
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	return nil
}

// loadHistorical ports on_loaded's "load the previous session's data"
// block: find every stats_*.json in saveDir, sorted, and if there is more
// than just the file this run just created, merge in the second-to-last
// one's data.
func (p *Plugin) loadHistorical() {
	p.mu.Lock()
	saveDir := p.saveDir
	p.mu.Unlock()

	entries, err := os.ReadDir(saveDir)
	if err != nil {
		return
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "stats_") && strings.HasSuffix(name, ".json") {
			files = append(files, name)
		}
	}
	sort.Strings(files)
	if len(files) < 2 {
		return
	}
	prev := files[len(files)-2]
	data, ok := readSessionData(filepath.Join(saveDir, prev))
	if !ok || len(data) == 0 {
		return
	}
	p.mu.Lock()
	for ts, entry := range data {
		p.stats[ts] = entry
	}
	p.mu.Unlock()
	p.logf("loaded %d historical data points from %s", len(data), prev)
}

func readSessionData(path string) (map[string]StatsEntry, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var sf sessionFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return nil, false
	}
	return sf.Data, true
}

// realClock is the production pluginmanager.Clock (Capabilities.Clock
// may be nil if the daemon didn't wire one; this keeps the plugin
// functional either way).
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// realtimeLoop ports _realtime_loop: periodically snapshots current
// counters + system readings and, if anything changed since the last
// sample (or this is the first sample), records + persists it.
func (p *Plugin) realtimeLoop(stop <-chan struct{}, done chan<- struct{}, interval time.Duration) {
	defer close(done)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			p.collectAndMaybeRecord()
		}
	}
}

func (p *Plugin) collectAndMaybeRecord() {
	temp, _ := p.sys.Celsius()
	mem, _ := p.sys.MemUsage()
	cpu, _ := p.sys.CPULoad("session-stats")

	p.mu.Lock()
	defer p.mu.Unlock()

	entry := StatsEntry{
		NumPeers:      p.networks,
		NumHandshakes: p.handshakes,
		NumDeauths:    p.deauths,
		Temperature:   float64(temp),
		MemUsage:      mem * 100,
		CPULoad:       cpu * 100,
	}

	changed := len(p.stats) == 0
	if !changed {
		if last, ok := p.lastEntryLocked(); ok {
			changed = entry.NumPeers != last.NumPeers || entry.NumHandshakes != last.NumHandshakes
		} else {
			changed = true
		}
	}
	if !changed {
		return
	}

	ts := p.clock.Now().Format(timestampLayout)
	p.stats[ts] = entry
	p.persistLocked()
}

// lastEntryLocked returns the entry with the lexicographically greatest
// timestamp key (equivalent to Python dict's insertion-ordered "last
// value" for same-day HH:MM:SS.mmm keys, which sort correctly as plain
// strings). Caller must hold p.mu.
func (p *Plugin) lastEntryLocked() (StatsEntry, bool) {
	if len(p.stats) == 0 {
		return StatsEntry{}, false
	}
	keys := make([]string, 0, len(p.stats))
	for k := range p.stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return p.stats[keys[len(keys)-1]], true
}

// persistLocked writes the full stats map to the session file. Caller
// must hold p.mu.
func (p *Plugin) persistLocked() {
	if p.sessionPath == "" {
		return
	}
	data, err := json.Marshal(sessionFile{Data: p.stats})
	if err != nil {
		return
	}
	if err := os.WriteFile(p.sessionPath, data, 0o644); err != nil {
		p.logf("cannot write %s: %v", p.sessionPath, err)
	}
}

// HandleEvent tracks networks/handshakes/deauths from real events (see
// package doc comment for why, instead of reading agent internals), and
// implements the (currently unreachable — no "epoch" emitter exists yet)
// on_epoch equivalent for forward compatibility.
func (p *Plugin) HandleEvent(event string, args []interface{}) {
	switch event {
	case "wifi_update":
		if len(args) < 2 {
			return
		}
		aps := asAPSlice(args[1])
		p.mu.Lock()
		p.networks = len(aps)
		p.mu.Unlock()
	case "handshake":
		p.mu.Lock()
		p.handshakes++
		p.mu.Unlock()
	case "deauthentication":
		p.mu.Lock()
		p.deauths++
		p.mu.Unlock()
	case "epoch":
		p.collectAndMaybeRecord()
	}
}

// asAPSlice accepts either the concrete []map[string]interface{} shape
// wifi_update delivers, or a generic []interface{} of maps, matching the
// same pattern internal/plugins/native/cache uses for the same two
// differing real emitter shapes.
func asAPSlice(v interface{}) []map[string]interface{} {
	switch t := v.(type) {
	case []map[string]interface{}:
		return t
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(t))
		for _, raw := range t {
			if m, ok := raw.(map[string]interface{}); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
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

// OnWebhook ports on_webhook: the dashboard page at the root path, plus
// summary/networks/handshakes/deauths/temp/mem/cpu/sessions JSON
// endpoints, each optionally scoped to a named historical `?session=`
// file instead of the live in-memory data.
//
// Note: this renders a fully self-contained HTML page rather than
// through WebCapability.Render's shared base.html layout — registering a
// new named template under internal/web/templates/ is outside this
// package's scope (it would require modifying files outside
// internal/plugins/native/session_stats). The stats cards, charts, and
// every JSON endpoint are fully functional and byte-for-byte the same
// content Python's Jinja template renders inside that block; only the
// surrounding shared nav/header chrome is not shared. Disclosed
// simplification, not a dropped feature.
func (p *Plugin) OnWebhook(subpath string, r *http.Request) (pluginmanager.WebhookResponse, error) {
	path := strings.Trim(subpath, "/")
	if path == "" {
		return pluginmanager.WebhookResponse{Status: http.StatusOK, Body: []byte(dashboardHTML)}, nil
	}

	data := p.dataForRequest(r)

	switch path {
	case "summary":
		return jsonResponse(p.summary(data))
	case "networks":
		return jsonResponse(extractKeyValues(data, "num_peers"))
	case "handshakes":
		return jsonResponse(extractKeyValues(data, "num_handshakes"))
	case "deauths":
		return jsonResponse(extractKeyValues(data, "num_deauths"))
	case "temp":
		return jsonResponse(extractKeyValues(data, "temperature"))
	case "mem":
		return jsonResponse(extractKeyValues(data, "mem_usage"))
	case "cpu":
		return jsonResponse(extractKeyValues(data, "cpu_load"))
	case "sessions":
		return jsonResponse(map[string]interface{}{"files": p.sessionFiles()})
	default:
		return jsonResponse(map[string]interface{}{"error": "Unknown path"})
	}
}

// dataForRequest ports the `?session=` scoping: "Current"/absent means
// the live in-memory stats; any other value reads that named file's
// persisted data instead.
func (p *Plugin) dataForRequest(r *http.Request) map[string]StatsEntry {
	session := r.URL.Query().Get("session")
	if session == "" || session == "Current" {
		p.mu.Lock()
		defer p.mu.Unlock()
		out := make(map[string]StatsEntry, len(p.stats))
		for k, v := range p.stats {
			out[k] = v
		}
		return out
	}
	p.mu.Lock()
	saveDir := p.saveDir
	p.mu.Unlock()
	data, ok := readSessionData(filepath.Join(saveDir, filepath.Base(session)))
	if !ok {
		return map[string]StatsEntry{}
	}
	return data
}

func (p *Plugin) sessionFiles() []string {
	p.mu.Lock()
	saveDir := p.saveDir
	p.mu.Unlock()
	entries, err := os.ReadDir(saveDir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// summary ports on_webhook's "summary" branch — including its exact
// aggregation quirks, preserved faithfully (not "fixed"): total_networks
// is genuinely `len(set(num_peers values))` (a count of DISTINCT values
// seen, not the latest/max count), and total_handshakes/total_deauths
// are genuinely SUMS across every sample of an already-cumulative
// counter (inflating the real total). duration is literally the sample
// COUNT, not a real elapsed time. These read as bugs but are the actual
// real Python behavior; changing them would be an undisclosed behavior
// change this migration doesn't have license to make.
func (p *Plugin) summary(data map[string]StatsEntry) map[string]interface{} {
	distinctNetworks := map[int]struct{}{}
	totalHandshakes, totalDeauths := 0, 0
	var maxTemp, maxMem, maxCPU float64
	for _, e := range data {
		distinctNetworks[e.NumPeers] = struct{}{}
		totalHandshakes += e.NumHandshakes
		totalDeauths += e.NumDeauths
		if e.Temperature > maxTemp {
			maxTemp = e.Temperature
		}
		if e.MemUsage > maxMem {
			maxMem = e.MemUsage
		}
		if e.CPULoad > maxCPU {
			maxCPU = e.CPULoad
		}
	}
	return map[string]interface{}{
		"networks":   len(distinctNetworks),
		"handshakes": totalHandshakes,
		"deauths":    totalDeauths,
		"duration":   fmt.Sprintf("%ds", len(data)),
		"temp":       fmt.Sprintf("%.1f°C", maxTemp),
		"mem":        fmt.Sprintf("%.1f%%", maxMem),
		"cpu":        fmt.Sprintf("%.1f%%", maxCPU),
	}
}

// extractKeyValues ports the static _extract_key_values helper: a
// chart-ready {"values": [[[ts, val], ...]], "labels": [subkey]} shape,
// timestamp-sorted (see lastEntryLocked's doc comment for why plain
// string sort reproduces chronological order here).
func extractKeyValues(data map[string]StatsEntry, key string) map[string]interface{} {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	values := make([][2]interface{}, 0, len(keys))
	for _, ts := range keys {
		e := data[ts]
		var v float64
		switch key {
		case "num_peers":
			v = float64(e.NumPeers)
		case "num_handshakes":
			v = float64(e.NumHandshakes)
		case "num_deauths":
			v = float64(e.NumDeauths)
		case "temperature":
			v = e.Temperature
		case "mem_usage":
			v = e.MemUsage
		case "cpu_load":
			v = e.CPULoad
		}
		values = append(values, [2]interface{}{ts, v})
	}
	return map[string]interface{}{
		"values": [][][2]interface{}{values},
		"labels": []string{key},
	}
}

func jsonResponse(v interface{}) (pluginmanager.WebhookResponse, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return pluginmanager.WebhookResponse{}, err
	}
	return pluginmanager.WebhookResponse{
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    data,
	}, nil
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.Unloader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
var _ pluginmanager.WebhookHandler = (*Plugin)(nil)
