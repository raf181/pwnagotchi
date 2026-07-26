// Package gps is the native Go port of
// pwnagotchi/plugins/default/gps.py: enables bettercap's real GPS module
// against a configured device, saves coordinates alongside each captured
// handshake, and shows lat/long/alt on screen.
//
// Original Python author: evilsocket@gmail.com (see gps.py's own
// __author__ field, left untouched). This Go port is by raf181.
package gps

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

const (
	lineSpacingDefault = 10
	labelSpacing       = 0
)

// deviceExists is overridable for tests (real production behavior: a
// real device path exists, or the configured device string is a
// gpsd-style "host:port" address — matching Python's
// `os.path.exists(device) or ":" in device` check exactly).
var deviceExists = func(device string) bool {
	if strings.Contains(device, ":") {
		return true
	}
	_, err := os.Stat(device)
	return err == nil
}

type pos struct{ x, y int }

// Coordinates mirrors the real bettercap session's `gps` object shape.
type Coordinates struct {
	Latitude  float64
	Longitude float64
	Altitude  float64
}

// valid ports the real `all([Latitude, Longitude])` truthiness check:
// Python's `all()` treats 0.0 as falsy, so an all-zero (unfixed) GPS
// reading is deliberately treated as "no location" — not a real
// difference, just Go not having Python's truthiness rules built in.
func (c Coordinates) valid() bool {
	return c.Latitude != 0 && c.Longitude != 0
}

// Plugin ports the GPS class.
type Plugin struct {
	mu          sync.Mutex
	device      string
	speed       string
	lineSpacing int

	agent pluginmanager.AgentCapability
	view  pluginmanager.ViewCapability
	log   pluginmanager.Logger

	running     bool
	coordinates Coordinates
	hasCoords   bool
}

func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string { return "gps" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "1.0.1",
		Author:      "evilsocket@gmail.com (original), Go port by raf181",
		License:     "GPL3",
		Description: "Save GPS coordinates whenever an handshake is captured.",
	}
}

// OnLoad ports on_loaded + on_ui_setup in one step (see memtemp.go's
// OnLoad doc comment for why a native plugin's OnLoad already coincides
// with Python's on_ui_setup timing).
func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.agent = caps.Agent
	p.view = caps.View
	p.log = caps.Log
	p.device = stringField(caps.Config, "device", "")
	p.speed = stringField(caps.Config, "speed", "9600")
	p.lineSpacing = intField(caps.Config, "linespacing", lineSpacingDefault)

	latPos, lonPos, altPos := p.positions(caps.Config)
	if p.view == nil {
		return nil
	}
	p.view.AddLabeledValue("latitude", "lat:", "-", latPos.x, latPos.y, pluginmanager.FontSmall, pluginmanager.FontSmall, labelSpacing)
	p.view.AddLabeledValue("longitude", "long:", "-", lonPos.x, lonPos.y, pluginmanager.FontSmall, pluginmanager.FontSmall, labelSpacing)
	p.view.AddLabeledValue("altitude", "alt:", "-", altPos.x, altPos.y, pluginmanager.FontSmall, pluginmanager.FontSmall, labelSpacing)
	return nil
}

func (p *Plugin) OnUnload() error {
	p.mu.Lock()
	view := p.view
	p.mu.Unlock()
	if view == nil {
		return nil
	}
	view.RemoveElement("latitude")
	view.RemoveElement("longitude")
	view.RemoveElement("altitude")
	return nil
}

// HandleEvent ports on_ready/on_handshake/on_ui_update.
//
// NOTE on "ready": real Python's plugins.load() (the normal startup
// path) only ever calls on('loaded') and on('config_changed', config) —
// grepped the whole real pwnagotchi/plugins/__init__.py to confirm.
// on('ready', ...) is ONLY fired by toggle_plugin (a runtime web-UI
// enable), never for a plugin that was already enabled in config.toml at
// boot. That means on_ready's entire bettercap-GPS-enable logic is real,
// confirmed dead code for the overwhelmingly common case of a plugin
// enabled from boot — a genuine upstream behavior gap, not a Go-port
// bug. This port implements HandleEvent("ready", ...) faithfully anyway
// (so it works correctly in the one case Python's does: a runtime
// toggle), rather than "fixing" a behavior difference that isn't this
// migration's call to make unilaterally.
func (p *Plugin) HandleEvent(event string, args []interface{}) {
	switch event {
	case "ready":
		p.onReady()
	case "handshake":
		if len(args) < 2 {
			return
		}
		filename, _ := args[1].(string)
		p.onHandshake(filename)
	case "ui_update":
		p.onUIUpdate()
	}
}

func (p *Plugin) onReady() {
	p.mu.Lock()
	device := p.device
	speed := p.speed
	agent := p.agent
	p.mu.Unlock()

	if agent == nil || !deviceExists(device) {
		p.logf("no GPS detected")
		return
	}

	p.logf("enabling bettercap's gps module for %s", device)
	_, _ = agent.Run("gps off", false) // matches Python's best-effort `except Exception: pass`
	if _, err := agent.Run(fmt.Sprintf("set gps.device %s", device), true); err != nil {
		p.logf("gps: %v", err)
		return
	}
	if _, err := agent.Run(fmt.Sprintf("set gps.baudrate %s", speed), true); err != nil {
		p.logf("gps: %v", err)
		return
	}
	if _, err := agent.Run("gps on", true); err != nil {
		p.logf("gps: %v", err)
		return
	}
	p.logf("bettercap gps module enabled on %s", device)
	p.mu.Lock()
	p.running = true
	p.mu.Unlock()
}

func (p *Plugin) onHandshake(filename string) {
	p.mu.Lock()
	running := p.running
	agent := p.agent
	p.mu.Unlock()
	if !running || agent == nil || filename == "" {
		return
	}

	raw, err := agent.Session("")
	if err != nil {
		return
	}
	sessionMap, ok := raw.(map[string]interface{})
	if !ok {
		return
	}
	gpsMap, ok := sessionMap["gps"].(map[string]interface{})
	if !ok {
		return
	}
	coords := Coordinates{
		Latitude:  toFloat(gpsMap["Latitude"]),
		Longitude: toFloat(gpsMap["Longitude"]),
		Altitude:  toFloat(gpsMap["Altitude"]),
	}

	p.mu.Lock()
	p.coordinates = coords
	p.hasCoords = true
	p.mu.Unlock()

	if !coords.valid() {
		p.logf("not saving GPS. Couldn't find location.")
		return
	}
	gpsFilename := strings.TrimSuffix(filename, ".pcap") + ".gps.json"
	if strings.HasSuffix(filename, ".pcap") {
		gpsFilename = filename[:len(filename)-len(".pcap")] + ".gps.json"
	}
	data, err := json.Marshal(map[string]float64{
		"Latitude":  coords.Latitude,
		"Longitude": coords.Longitude,
		"Altitude":  coords.Altitude,
	})
	if err != nil {
		return
	}
	if err := os.WriteFile(gpsFilename, data, 0o644); err != nil {
		p.logf("cannot write %s: %v", gpsFilename, err)
	}
}

func (p *Plugin) onUIUpdate() {
	p.mu.Lock()
	view := p.view
	coords := p.coordinates
	has := p.hasCoords
	p.mu.Unlock()
	if view == nil || !has || !coords.valid() {
		return
	}
	view.Set("latitude", fmt.Sprintf("%.4f ", coords.Latitude))
	view.Set("longitude", fmt.Sprintf("%.4f ", coords.Longitude))
	view.Set("altitude", fmt.Sprintf("%.1fm ", coords.Altitude))
}

// positions ports on_ui_setup's explicit-position-or-per-display-default
// logic.
func (p *Plugin) positions(cfg config.Map) (lat, lon, alt pos) {
	lineSpacing := p.lineSpacing
	if raw := stringField(cfg, "position", ""); raw != "" {
		parts := strings.Split(raw, ",")
		if len(parts) == 2 {
			x, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
			y, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err1 == nil && err2 == nil {
				return pos{x + 5, y}, pos{x, y + lineSpacing}, pos{x + 5, y + 2*lineSpacing}
			}
		}
	}

	kind := ""
	if p.view != nil {
		kind = p.view.Kind()
	}
	switch kind {
	case "waveshare_2":
		return pos{127, 74}, pos{122, 84}, pos{127, 94}
	case "waveshare_1":
		return pos{130, 70}, pos{125, 80}, pos{130, 90}
	case "inky":
		return pos{127, 60}, pos{122, 70}, pos{127, 80}
	case "waveshare144lcd":
		return pos{67, 73}, pos{62, 83}, pos{67, 93}
	case "dfrobot_v2":
		return pos{127, 74}, pos{122, 84}, pos{127, 94}
	case "waveshare2in7":
		return pos{6, 120}, pos{1, 135}, pos{6, 150}
	default:
		return pos{127, 51}, pos{122, 61}, pos{127, 71}
	}
}

func toFloat(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	default:
		return 0
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

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.Unloader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
