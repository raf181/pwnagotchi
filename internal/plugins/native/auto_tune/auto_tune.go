// Package autotune is the native Go port of
// pwnagotchi/plugins/default/auto-tune.py: tracks per-channel/per-AP
// session statistics (a "channel histogram" of APs-per-epoch plus a set
// of named counters — associations, deauths, handshakes, missed
// joins/rejoins — bucketed by channel) and uses them to pick which extra
// channels to scan each epoch, alongside a webhook UI for viewing the
// stats and editing personality/plugin parameters live (with named
// preset save/load/delete).
//
// Original Python author: Sniffleupagus (see auto-tune.py's own
// __author__ field, left untouched). This Go port is by raf181.
package autotune

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// clock is overridable for tests, matching this port's established
// injected-clock pattern (real time.Now in production).
type clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// SupportedChannelsFunc is injectable for deterministic tests.
type SupportedChannelsFunc func() []int

// Options mirrors the plugin's own self.options defaults.
type Options struct {
	ShowHidden       bool
	ResetHistory     bool
	ExtraChannels    int
	ShowInteractions bool
	RestrictChannels []int // nil unless explicitly configured
}

func defaultOptions() Options {
	return Options{ShowHidden: false, ResetHistory: true, ExtraChannels: 15, ShowInteractions: false}
}

// apRecord mirrors one entry of self._known_aps: the last-seen AP fields
// (arbitrary, merged in verbatim from each sighting) plus the AT_*
// bookkeeping fields this plugin adds.
type apRecord struct {
	fields    map[string]interface{} // last-merged raw AP fields (hostname, mac, channel, rssi, ...)
	seen      int
	assoc     int
	deauth    int
	handshake int
	visible   bool
	lastSeen  time.Time
}

// Plugin ports the auto_tune class.
type Plugin struct {
	mu sync.Mutex

	opts    Options
	log     pluginmanager.Logger
	agent   pluginmanager.AgentCapability
	fullCfg config.Map // captured from "config_changed"; config.Map is a shared reference (see webcfg.go's doc comment), so mutating fullCfg["personality"]["channels"] here is visible daemon-wide with no extra plumbing.

	supportedChannels SupportedChannelsFunc
	clock             clock

	presetsDir string
	configPath string

	loops             int
	histogram         map[int]int // channel -> AP-seen count, summed across epochs
	chistos           map[string]map[int]int
	unscannedChannels []int
	activeChannels    []int
	knownAPs          map[string]*apRecord
}

// New ports auto_tune.__init__.
func New() *Plugin {
	home, _ := os.UserHomeDir()
	return &Plugin{
		opts:       defaultOptions(),
		clock:      realClock{},
		presetsDir: filepath.Join(home, "auto-tune-presets"),
		configPath: "/etc/pwnagotchi/config.toml",
		histogram:  map[int]int{},
		chistos:    map[string]map[int]int{"_all_actions": {-1: 0}},
		knownAPs:   map[string]*apRecord{},
	}
}

func (p *Plugin) Name() string { return "auto-tune" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "1.0.1",
		Author:      "Sniffleupagus (original), Go port by raf181",
		License:     "GPL3",
		Description: "A plugin that adjust AUTO mode parameters",
		HasWebhook:  true,
	}
}

// OnLoad ports on_loaded (default option merge) + ensures the presets
// directory exists (Python's _ensure_presets_dir, called eagerly from
// __init__).
func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.log = caps.Log
	p.agent = caps.Agent
	if caps.Agent != nil {
		p.supportedChannels = caps.Agent.SupportedChannels
	}
	p.opts = parseOptions(caps.Config)

	if err := os.MkdirAll(p.presetsDir, 0o755); err != nil {
		p.logf("failed to create presets directory %s: %v", p.presetsDir, err)
	}
	return nil
}

// HandleEvent ports every on_<event> hook.
func (p *Plugin) HandleEvent(event string, args []interface{}) {
	switch event {
	case "config_changed":
		p.onConfigChanged(args)
	case "ready":
		p.onReady()
	case "wifi_update":
		p.onWifiUpdate(args)
	case "epoch":
		p.onEpoch()
	case "association":
		p.onAssociation(args)
	case "deauthentication":
		p.onDeauthentication(args)
	case "handshake":
		p.onHandshake(args)
	case "bcap_wifi_ap_new":
		p.onBcapWifiAPNew(args)
	case "bcap_wifi_ap_lost":
		p.onBcapWifiAPLost(args)
		// on_channel_hop/on_bcap_wifi_client_new/lost are real no-ops in
		// Python too (channel_hop: `pass`; the two client hooks only read
		// event data into locals that are never used) — nothing to port.
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
	p.mu.Lock()
	p.fullCfg = fullCfg
	p.mu.Unlock()
}

// onReady ports on_ready: reset_history clears both in-process interaction
// history and bettercap's recon state.
func (p *Plugin) onReady() {
	p.mu.Lock()
	reset := p.opts.ResetHistory
	agent := p.agent
	p.mu.Unlock()
	if !reset || agent == nil {
		return
	}
	agent.ResetHistory()
	_, _ = agent.Run("wifi.recon clear", false)
	_, _ = agent.Run("wifi.clear", false)
}

func (p *Plugin) onWifiUpdate(args []interface{}) {
	if len(args) < 2 {
		return
	}
	aps := asAPSlice(args[1])

	p.mu.Lock()
	defer p.mu.Unlock()
	p.loops++
	var active []int
	for _, ap := range aps {
		p.markAPSeenLocked(ap, "wifi_update")
		ch := channelOf(ap)
		if !containsInt(active, ch) {
			active = append(active, ch)
			p.unscannedChannels = removeInt(p.unscannedChannels, ch)
		}
		p.histogram[ch]++
	}
	p.activeChannels = active
}

// onEpoch ports on_epoch's channel-selection logic.
func (p *Plugin) onEpoch() {
	p.mu.Lock()
	defer p.mu.Unlock()

	nextChannels := append([]int(nil), p.activeChannels...)
	n := p.opts.ExtraChannels
	if len(p.unscannedChannels) == 0 {
		switch {
		case p.opts.RestrictChannels != nil:
			p.unscannedChannels = append([]int(nil), p.opts.RestrictChannels...)
		case p.supportedChannels != nil:
			p.unscannedChannels = append([]int(nil), p.supportedChannels()...)
		default:
			p.logf("auto-tune: no restrict_channels or supported channels available; cannot repopulate the unscanned-channel pool")
		}
	}
	for i := 0; i < n && len(p.unscannedChannels) > 0; i++ {
		idx := rand.Intn(len(p.unscannedChannels))
		ch := p.unscannedChannels[idx]
		p.unscannedChannels = append(p.unscannedChannels[:idx], p.unscannedChannels[idx+1:]...)
		nextChannels = append(nextChannels, ch)
	}

	if p.fullCfg != nil {
		if personality, ok := p.fullCfg["personality"].(config.Map); ok {
			channels := make([]interface{}, len(nextChannels))
			for i, c := range nextChannels {
				channels[i] = c
			}
			personality["channels"] = channels
		}
	}
}

func (p *Plugin) onAssociation(args []interface{}) {
	if len(args) < 2 {
		return
	}
	ap, ok := args[1].(map[string]interface{})
	if !ok {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.incrementChistoLocked("Associations", channelOf(ap), 1)
	p.markAPSeenLocked(ap, "assoc")
}

func (p *Plugin) onDeauthentication(args []interface{}) {
	if len(args) < 2 {
		return
	}
	ap, ok := args[1].(map[string]interface{})
	if !ok {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.incrementChistoLocked("Deauths", channelOf(ap), 1)
	p.markAPSeenLocked(ap, "deauth")
}

// onHandshake ports on_handshake. Real agent.py can emit a plain BSSID
// string instead of a map for access_point when the AP wasn't found in
// the current session (a confirmed real upstream behavior, not a Go-port
// gap — see internal/plugins/native/cache's HandleEvent "handshake" case
// for the verified source reference); this port skips cleanly rather
// than indexing a non-map, matching Python's own `except Exception` catch
// around the whole handler body.
func (p *Plugin) onHandshake(args []interface{}) {
	if len(args) < 3 {
		return
	}
	ap, ok := args[2].(map[string]interface{})
	if !ok {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.incrementChistoLocked("Handshakes", channelOf(ap), 1)
	p.markAPSeenLocked(ap, "handshake")
}

func (p *Plugin) onBcapWifiAPNew(args []interface{}) {
	if len(args) < 2 {
		return
	}
	event, ok := args[1].(map[string]interface{})
	if !ok {
		return
	}
	ap, ok := event["data"].(map[string]interface{})
	if !ok {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.markAPSeenLocked(ap, "")
}

func (p *Plugin) onBcapWifiAPLost(args []interface{}) {
	if len(args) < 2 {
		return
	}
	event, ok := args[1].(map[string]interface{})
	if !ok {
		return
	}
	ap, ok := event["data"].(map[string]interface{})
	if !ok {
		return
	}
	channel := channelOf(ap)
	apID := apIdentity(ap)

	p.mu.Lock()
	defer p.mu.Unlock()
	rec, known := p.knownAPs[apID]
	switch {
	case !known:
		p.incrementChistoLocked("Missed joins", channel, 1)
	case !rec.visible:
		p.incrementChistoLocked("Missed rejoins", channel, 1)
	default:
		rec.visible = false
		p.incrementChistoLocked("Current APs", channel, -1)
	}
}

// incrementChistoLocked ports incrementChisto. Caller holds p.mu.
func (p *Plugin) incrementChistoLocked(stat string, channel, count int) {
	if _, ok := p.chistos[stat]; !ok {
		p.chistos[stat] = map[int]int{-1: 0}
	}
	p.chistos[stat][channel] += count

	if _, ok := p.chistos["_all_actions"][channel]; !ok {
		p.chistos["_all_actions"][channel] = 1
	} else {
		p.chistos["_all_actions"][channel]++
	}

	p.chistos[stat][-1] += count
	p.chistos["_all_actions"][-1]++
}

// markAPSeenLocked ports markAPSeen. Caller holds p.mu.
func (p *Plugin) markAPSeenLocked(ap map[string]interface{}, context string) {
	apID := apIdentity(ap)
	channel := channelOf(ap)
	tag := "seen"
	if context != "" {
		tag = context
	}

	rec, known := p.knownAPs[apID]
	if !known {
		rec = &apRecord{fields: copyMap(ap), visible: true}
		rec.bump(tag, 1)
		rec.seen = 1
		p.knownAPs[apID] = rec
		p.incrementChistoLocked("Unique APs", channel, 1)
		p.incrementChistoLocked("Current APs", channel, 1)
	} else {
		for k, v := range ap {
			rec.fields[k] = v
		}
		if !rec.visible {
			rec.visible = true
			rec.seen++
			p.incrementChistoLocked("Current APs", channel, 1)
		}
		rec.bump(tag, 1)
	}
	rec.lastSeen = p.now()
}

func (r *apRecord) bump(tag string, n int) {
	switch tag {
	case "seen":
		r.seen += n
	case "assoc":
		r.assoc += n
	case "deauth":
		r.deauth += n
	case "handshake":
		r.handshake += n
	}
}

func (p *Plugin) now() time.Time {
	if p.clock != nil {
		return p.clock.Now()
	}
	return time.Now()
}

func (p *Plugin) logf(format string, args ...interface{}) {
	if p.log != nil {
		p.log.Printf(format, args...)
	}
}

// -- helpers over the generic AP map shape (matches internal/plugins/native/cache's asAPSlice) --

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

func channelOf(ap map[string]interface{}) int {
	switch v := ap["channel"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func normalizeName(name string) string {
	if name == "" {
		return "EMPTY"
	}
	if name == "<hidden>" {
		return "HIDDEN"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func apIdentity(ap map[string]interface{}) string {
	hostname, _ := ap["hostname"].(string)
	mac, _ := ap["mac"].(string)
	return normalizeName(hostname) + "-" + normalizeName(mac)
}

func copyMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func removeInt(s []int, v int) []int {
	out := s[:0:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func parseOptions(cfg config.Map) Options {
	o := defaultOptions()
	if cfg == nil {
		return o
	}
	if v, ok := cfg["show_hidden"].(bool); ok {
		o.ShowHidden = v
	}
	if v, ok := cfg["reset_history"].(bool); ok {
		o.ResetHistory = v
	}
	if v := intField(cfg, "extra_channels", -1); v >= 0 {
		o.ExtraChannels = v
	}
	if v, ok := cfg["show_interactions"].(bool); ok {
		o.ShowInteractions = v
	}
	if raw, ok := cfg["restrict_channels"].([]interface{}); ok {
		chans := make([]int, 0, len(raw))
		for _, v := range raw {
			switch n := v.(type) {
			case int:
				chans = append(chans, n)
			case int64:
				chans = append(chans, int(n))
			case float64:
				chans = append(chans, int(n))
			}
		}
		o.RestrictChannels = chans
	}
	return o
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
var _ pluginmanager.WebhookHandler = (*Plugin)(nil)
