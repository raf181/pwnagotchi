// Package bttether is the native Go port of
// pwnagotchi/plugins/default/bt-tether.py: Bluetooth PAN/NAP tethering to
// a paired phone (BlueZ via bluetoothctl + a targeted dbus-send NAP
// profile connect), with on-screen status and a web UI for
// scanning/pairing/managing devices.
//
// Original Python author: wsvdmeer (see bt-tether.py's own __author__
// field, left untouched). This Go port is by raf181.
//
// # Scope of this port (read before assuming parity)
//
// bt-tether.py is ~5000 lines with a great deal of defensive/retry logic,
// an interactive-subprocess-based real-time scan, and a full
// passkey-confirmation pairing agent. This port implements the real,
// working core end-to-end (device discovery, pairing, trust, the actual
// NAP profile connection, network interface bring-up, on-screen status,
// auto-reconnect, and the full original web UI backed by real logic) but
// deliberately narrows two areas, both documented at their call sites:
//
//  1. Device discovery is a single blocking `bluetoothctl --timeout Ns
//     scan on` call (via the injected CommandRunner, which is
//     request/response only) instead of Python's interactive
//     Popen-with-piped-stdin/stdout session that streams `[NEW] Device`
//     lines to the web UI in real time during the scan window. Real-time
//     scan progress would need a new "long-running process with
//     stdin/stdout streaming" capability that doesn't exist yet in
//     pluginmanager — proposed, not implemented. Devices are still
//     discovered and returned once scanning completes.
//  2. Pairing relies on bluetoothctl's own default agent behavior
//     (works for devices that don't require an interactive passkey
//     confirmation) rather than Python's dedicated pairing-agent
//     subprocess that monitors its log for a passkey prompt and would
//     need a human to confirm on the phone. A phone that requires
//     passkey confirmation will need to be paired once via `bluetoothctl`
//     directly on the device, after which this plugin's trust/connect/
//     reconnect logic takes over exactly as it does for any other
//     trusted device.
package bttether

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "embed"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

//go:embed bt_tether.html
var htmlSource string

var htmlTemplate = template.Must(template.New("bt-tether").Parse(htmlSource))

// State mirrors the real plugin's STATE_* class constants.
type State string

const (
	StateIdle          State = "IDLE"
	StateInitializing  State = "INITIALIZING"
	StateScanning      State = "SCANNING"
	StatePairing       State = "PAIRING"
	StateTrusting      State = "TRUSTING"
	StateConnecting    State = "CONNECTING"
	StateConnected     State = "CONNECTED"
	StateReconnecting  State = "RECONNECTING"
	StateDisconnecting State = "DISCONNECTING"
	StateUntrusting    State = "UNTRUSTING"
	StateDisconnected  State = "DISCONNECTED"
	StateError         State = "ERROR"
)

// NAPUUID is the Bluetooth NAP (Network Access Point) profile UUID —
// ported verbatim from the real plugin's NAP_UUID constant.
const NAPUUID = "00001116-0000-1000-8000-00805f9b34fb"

const (
	defaultScanDuration             = 30 * time.Second
	defaultReconnectInterval        = 60 * time.Second
	defaultReconnectFailureCooldown = 300 * time.Second
	maxReconnectFailures            = 5
	uiLogMaxLen                     = 100
	btCommandTimeout                = 10 * time.Second
	dbusConnectTimeout              = 30 * time.Second
)

// bluetoothAdapter is the BlueZ adapter object path segment this port
// assumes (a real Pwnagotchi's onboard BCM43430A1 registers as hci0 —
// the standard, only adapter on this hardware). Device object paths for
// dbus-send are derived deterministically from this + the MAC address
// rather than resolved via a real GetManagedObjects query, avoiding a
// dependency on a Go D-Bus client library for reply parsing — see
// napConnect's doc comment.
const bluetoothAdapter = "hci0"

// logEntry is one line in the UI-visible log ring buffer.
type logEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// Plugin ports the BTTetherHelper class.
type Plugin struct {
	mu sync.Mutex

	// capabilities
	exec       pluginmanager.CommandRunner
	httpClient *http.Client
	view       pluginmanager.ViewCapability
	log        pluginmanager.Logger
	clock      pluginmanager.Clock

	// config
	showOnScreen             bool
	showMiniStatus           bool
	miniStatusPos            [2]int
	showDetailedStatus       bool
	detailedStatusPos        [2]int
	autoReconnect            bool
	reconnectInterval        time.Duration
	reconnectFailureCooldown time.Duration
	scanDuration             time.Duration

	phoneMAC string
	status   State
	message  string

	connectionInProgress bool
	disconnecting        bool
	untrusting           bool
	initializing         bool

	reconnectFailureCount int
	cooldownUntil         time.Time

	scanning   bool
	discovered map[string]DiscoveredDevice

	uiLogs []logEntry

	stop chan struct{}
	done chan struct{}
}

// DiscoveredDevice mirrors one entry of the real plugin's discovered/
// trusted-device dicts.
type DiscoveredDevice struct {
	MAC       string `json:"mac"`
	Name      string `json:"name"`
	Type      string `json:"type,omitempty"`
	Paired    bool   `json:"paired"`
	Trusted   bool   `json:"trusted"`
	Connected bool   `json:"connected"`
	HasNAP    bool   `json:"has_nap"`
}

// ConnectionStatus mirrors _get_full_connection_status's real JSON shape.
type ConnectionStatus struct {
	Paired                bool   `json:"paired"`
	Trusted               bool   `json:"trusted"`
	Connected             bool   `json:"connected"`
	PANActive             bool   `json:"pan_active"`
	Interface             string `json:"interface"`
	IPAddress             string `json:"ip_address"`
	DefaultRouteInterface string `json:"default_route_interface"`
}

// New ports BTTetherHelper.__init__ (on_loaded's field initialization).
func New() *Plugin {
	return &Plugin{
		status:     StateIdle,
		message:    "Ready",
		discovered: map[string]DiscoveredDevice{},
	}
}

func (p *Plugin) Name() string { return "bt-tether" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "1.2.5",
		Author:      "wsvdmeer (original), Go port by raf181",
		License:     "GPL3",
		Description: "Guided Bluetooth tethering with user instructions",
		HasWebhook:  true,
	}
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// OnLoad ports on_loaded (config parsing + state init). Real device
// initialization (bluetooth service restart, auto-connect to a trusted
// device) is Python's on_ready/_initialize_bluetooth_services, ported as
// HandleEvent("ready", ...) below — a native plugin's OnLoad already
// covers timing equivalent to on_loaded; "ready" still needs its own
// event since real initialization is comparatively slow/asynchronous and
// Python explicitly defers it past on_loaded for that reason.
func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.exec = caps.Exec
	p.httpClient = caps.HTTPClient
	if p.httpClient == nil {
		p.httpClient = http.DefaultClient
	}
	p.view = caps.View
	p.log = caps.Log
	p.clock = caps.Clock
	if p.clock == nil {
		p.clock = realClock{}
	}

	cfg := caps.Config
	p.showOnScreen = boolField(cfg, "show_on_screen", true)
	p.showMiniStatus = boolField(cfg, "show_mini_status", true)
	p.miniStatusPos = intPairField(cfg, "mini_status_position", [2]int{110, 0})
	p.showDetailedStatus = boolField(cfg, "show_detailed_status", true)
	p.detailedStatusPos = intPairField(cfg, "detailed_status_position", [2]int{0, 82})
	p.autoReconnect = boolField(cfg, "auto_reconnect", true)
	p.reconnectInterval = durationField(cfg, "reconnect_interval", defaultReconnectInterval)
	p.reconnectFailureCooldown = durationField(cfg, "reconnect_failure_cooldown", defaultReconnectFailureCooldown)
	p.scanDuration = defaultScanDuration
	p.phoneMAC = strings.ToUpper(stringField(cfg, "mac", ""))
	p.initializing = true

	if p.view != nil && p.showOnScreen {
		if p.showMiniStatus {
			p.view.AddText("bt_tether_mini", "-", p.miniStatusPos[0], p.miniStatusPos[1], pluginmanager.FontBold, false, 0)
		}
		if p.showDetailedStatus {
			p.view.AddText("bt_tether_detail", "", p.detailedStatusPos[0], p.detailedStatusPos[1], pluginmanager.FontSmall, false, 0)
		}
	}

	p.logfLocked("INFO", "Plugin configuration loaded")
	return nil
}

// OnUnload ports on_unload: stop the reconnect monitor and remove any
// on-screen elements.
func (p *Plugin) OnUnload() error {
	p.mu.Lock()
	stop := p.stop
	done := p.done
	view := p.view
	p.mu.Unlock()

	if stop != nil {
		close(stop)
		if done != nil {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
		}
	}
	if view != nil {
		view.RemoveElement("bt_tether_mini")
		view.RemoveElement("bt_tether_detail")
	}
	return nil
}

// HandleEvent ports on_ready (real Bluetooth service init + auto-connect)
// and on_ui_update (on-screen status refresh).
func (p *Plugin) HandleEvent(event string, _ []interface{}) {
	switch event {
	case "ready":
		p.onReady()
	case "ui_update":
		p.refreshOnScreenStatus()
	}
}

// onReady ports _initialize_bluetooth_services: real bluetooth restart is
// left to the host's own systemd units (out of scope for a plugin to
// restart system services it doesn't own the lifecycle of beyond what
// SystemCapability already exposes) — this port focuses on the
// tethering-specific initialization: start the reconnect monitor and
// attempt to auto-connect to the best already-trusted device.
func (p *Plugin) onReady() {
	p.mu.Lock()
	if p.stop != nil {
		p.mu.Unlock()
		return // already initialized
	}
	p.stop = make(chan struct{})
	p.done = make(chan struct{})
	autoReconnect := p.autoReconnect
	p.mu.Unlock()

	if autoReconnect {
		go p.monitorLoop()
	}

	best, err := p.findBestDeviceToConnect(context.Background())
	p.mu.Lock()
	p.initializing = false
	p.mu.Unlock()
	if err != nil || best == nil {
		p.logf("INFO", "No trusted devices found. Pair a device via web UI.")
		return
	}
	p.logf("INFO", fmt.Sprintf("Found trusted device: %s, starting connection...", best.Name))
	p.mu.Lock()
	p.phoneMAC = best.MAC
	p.mu.Unlock()
	go p.connectDevice(context.Background(), *best)
}

func (p *Plugin) logfLocked(level, msg string) {
	if p.log != nil {
		p.log.Printf("[%s] %s", level, msg)
	}
	entry := logEntry{Time: p.now().Format(time.RFC3339), Level: level, Message: msg}
	p.uiLogs = append(p.uiLogs, entry)
	if len(p.uiLogs) > uiLogMaxLen {
		p.uiLogs = p.uiLogs[len(p.uiLogs)-uiLogMaxLen:]
	}
}

func (p *Plugin) logf(level, msg string) {
	p.mu.Lock()
	p.logfLocked(level, msg)
	p.mu.Unlock()
}

func (p *Plugin) now() time.Time {
	if p.clock != nil {
		return p.clock.Now()
	}
	return time.Now()
}

func (p *Plugin) setStatus(status State, message string) {
	p.mu.Lock()
	p.status = status
	p.message = message
	p.mu.Unlock()
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

func boolField(m config.Map, key string, def bool) bool {
	if m == nil {
		return def
	}
	if b, ok := m[key].(bool); ok {
		return b
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
	}
	return def
}

func intPairField(m config.Map, key string, def [2]int) [2]int {
	if m == nil {
		return def
	}
	raw, ok := m[key].([]interface{})
	if !ok || len(raw) != 2 {
		return def
	}
	x, ok1 := toInt(raw[0])
	y, ok2 := toInt(raw[1])
	if !ok1 || !ok2 {
		return def
	}
	return [2]int{x, y}
}

func toInt(v interface{}) (int, bool) {
	switch t := v.(type) {
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case int:
		return t, true
	}
	return 0, false
}

var macPattern = regexp.MustCompile(`^([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}$`)

func validMAC(mac string) bool { return macPattern.MatchString(mac) }

func normalizeMAC(mac string) string { return strings.ToUpper(strings.TrimSpace(mac)) }

// screenStatusLetter ports updateStatusDisplay's JS screenStatus (C/N/P/D)
// logic, computed server-side for the mini status element.
func screenStatusLetter(s ConnectionStatus) string {
	switch {
	case s.PANActive:
		return "C"
	case s.Connected:
		return "N"
	case s.Paired:
		return "P"
	default:
		return "D"
	}
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.Unloader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
var _ pluginmanager.WebhookHandler = (*Plugin)(nil)

// jsonResponse builds a pluginmanager.WebhookResponse from a JSON-
// marshalable value, mirroring Flask's jsonify(...).
func jsonResponse(v interface{}) pluginmanager.WebhookResponse {
	data, err := json.Marshal(v)
	if err != nil {
		return pluginmanager.WebhookResponse{Status: http.StatusInternalServerError, Body: []byte(`{"error":"marshal failure"}`)}
	}
	return pluginmanager.WebhookResponse{
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    data,
	}
}

func htmlResponse(body []byte) pluginmanager.WebhookResponse {
	return pluginmanager.WebhookResponse{
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "text/html; charset=utf-8"},
		Body:    body,
	}
}

func methodNotAllowed(method string) pluginmanager.WebhookResponse {
	return pluginmanager.WebhookResponse{
		Status:  http.StatusMethodNotAllowed,
		Headers: map[string]string{"Allow": method},
		Body:    []byte("Method Not Allowed"),
	}
}

// OnWebhook ports on_webhook: dispatches every real route the bundled
// bt-tether.py serves under /plugins/bt-tether/...
func (p *Plugin) OnWebhook(subpath string, r *http.Request) (pluginmanager.WebhookResponse, error) {
	ctx := r.Context()
	clean := strings.TrimPrefix(subpath, "/")

	switch clean {
	case "":
		if r.Method != http.MethodGet {
			return methodNotAllowed(http.MethodGet), nil
		}
		p.mu.Lock()
		mac := p.phoneMAC
		p.mu.Unlock()
		csrf := ""
		if cookie, err := r.Cookie("csrf_token"); err == nil {
			csrf = cookie.Value
		}
		var buf bytes.Buffer
		if err := htmlTemplate.Execute(&buf, struct{ Version, MAC, CSRF string }{p.Metadata().Version, mac, csrf}); err != nil {
			return pluginmanager.WebhookResponse{Status: http.StatusInternalServerError}, nil
		}
		return htmlResponse(buf.Bytes()), nil

	case "status":
		if r.Method != http.MethodGet {
			return methodNotAllowed(http.MethodGet), nil
		}
		p.mu.Lock()
		resp := map[string]interface{}{
			"status":                 string(p.status),
			"message":                p.message,
			"mac":                    p.phoneMAC,
			"disconnecting":          p.disconnecting,
			"untrusting":             p.untrusting,
			"initializing":           p.initializing,
			"connection_in_progress": p.connectionInProgress,
		}
		p.mu.Unlock()
		return jsonResponse(resp), nil

	case "trusted-devices":
		if r.Method != http.MethodGet {
			return methodNotAllowed(http.MethodGet), nil
		}
		devices, _ := p.trustedDevices(ctx)
		return jsonResponse(map[string]interface{}{"devices": devices}), nil

	case "connect":
		if r.Method != http.MethodPost {
			return methodNotAllowed(http.MethodPost), nil
		}
		mac := normalizeMAC(r.URL.Query().Get("mac"))
		return p.handleConnect(ctx, mac), nil

	case "pair-device":
		if r.Method != http.MethodPost {
			return methodNotAllowed(http.MethodPost), nil
		}
		mac := normalizeMAC(r.URL.Query().Get("mac"))
		name := r.URL.Query().Get("name")
		return p.handlePairDevice(mac, name), nil

	case "disconnect":
		if r.Method != http.MethodPost {
			return methodNotAllowed(http.MethodPost), nil
		}
		mac := normalizeMAC(r.URL.Query().Get("mac"))
		return p.handleDisconnect(mac), nil

	case "unpair":
		if r.Method != http.MethodPost {
			return methodNotAllowed(http.MethodPost), nil
		}
		mac := normalizeMAC(r.URL.Query().Get("mac"))
		if !validMAC(mac) {
			return jsonResponse(map[string]interface{}{"success": false, "message": "Invalid MAC"}), nil
		}
		ok, msg := p.unpairDevice(ctx, mac)
		return jsonResponse(map[string]interface{}{"success": ok, "message": msg}), nil

	case "pair-status":
		if r.Method != http.MethodGet {
			return methodNotAllowed(http.MethodGet), nil
		}
		mac := normalizeMAC(r.URL.Query().Get("mac"))
		if !validMAC(mac) {
			return jsonResponse(map[string]interface{}{"paired": false, "connected": false}), nil
		}
		status, _ := p.currentStatus(ctx, mac)
		return jsonResponse(status), nil

	case "scan":
		if r.Method != http.MethodPost {
			return methodNotAllowed(http.MethodPost), nil
		}
		return p.handleScan(), nil

	case "scan-progress":
		if r.Method != http.MethodGet {
			return methodNotAllowed(http.MethodGet), nil
		}
		p.mu.Lock()
		devices := make([]DiscoveredDevice, 0, len(p.discovered))
		for _, d := range p.discovered {
			devices = append(devices, d)
		}
		scanning := p.scanning
		p.mu.Unlock()
		return jsonResponse(map[string]interface{}{"scanning": scanning, "devices": devices, "count": len(devices)}), nil

	case "connection-status":
		if r.Method != http.MethodGet {
			return methodNotAllowed(http.MethodGet), nil
		}
		mac := normalizeMAC(r.URL.Query().Get("mac"))
		if !validMAC(mac) {
			return jsonResponse(ConnectionStatus{}), nil
		}
		status, _ := p.fullConnectionStatus(ctx, mac)
		return jsonResponse(status), nil

	case "test-internet":
		if r.Method != http.MethodGet {
			return methodNotAllowed(http.MethodGet), nil
		}
		ok, detail := p.testInternetConnectivity(ctx)
		return jsonResponse(map[string]interface{}{"success": ok, "message": detail}), nil

	case "logs":
		if r.Method != http.MethodGet {
			return methodNotAllowed(http.MethodGet), nil
		}
		p.mu.Lock()
		logs := append([]logEntry(nil), p.uiLogs...)
		p.mu.Unlock()
		return jsonResponse(map[string]interface{}{"logs": logs}), nil
	}

	return pluginmanager.WebhookResponse{Status: http.StatusNotFound, Body: []byte("Not Found")}, nil
}

func (p *Plugin) handleConnect(ctx context.Context, mac string) pluginmanager.WebhookResponse {
	if mac != "" && validMAC(mac) {
		p.mu.Lock()
		p.phoneMAC = mac
		p.mu.Unlock()
		go p.connectDevice(context.Background(), DiscoveredDevice{MAC: mac, Name: mac})
		return jsonResponse(map[string]interface{}{"success": true, "message": fmt.Sprintf("Connection started to %s", mac)})
	}
	best, err := p.findBestDeviceToConnect(ctx)
	if err != nil || best == nil {
		return jsonResponse(map[string]interface{}{"success": false, "message": "No suitable devices found - pair a device first or set MAC address"})
	}
	p.mu.Lock()
	p.phoneMAC = best.MAC
	p.mu.Unlock()
	go p.connectDevice(context.Background(), *best)
	return jsonResponse(map[string]interface{}{"success": true, "message": fmt.Sprintf("Connection started to %s (%s)", best.Name, best.MAC)})
}

func (p *Plugin) handlePairDevice(mac, name string) pluginmanager.WebhookResponse {
	if mac == "" || !validMAC(mac) {
		return jsonResponse(map[string]interface{}{"success": false, "message": "Invalid MAC address"})
	}
	p.mu.Lock()
	if p.connectionInProgress {
		p.mu.Unlock()
		return jsonResponse(map[string]interface{}{"success": false, "message": "Connection already in progress"})
	}
	p.phoneMAC = mac
	p.connectionInProgress = true
	p.mu.Unlock()

	if name == "" {
		name = "Unknown Device"
	}
	go p.connectDevice(context.Background(), DiscoveredDevice{MAC: mac, Name: name, HasNAP: true})
	return jsonResponse(map[string]interface{}{"success": true, "message": fmt.Sprintf("Pairing started with %s", mac)})
}

func (p *Plugin) handleDisconnect(mac string) pluginmanager.WebhookResponse {
	if mac == "" || !validMAC(mac) {
		return jsonResponse(map[string]interface{}{"success": false, "message": "Invalid MAC"})
	}
	p.mu.Lock()
	p.disconnecting = true
	p.mu.Unlock()
	go func() {
		p.disconnectDevice(context.Background(), mac)
		p.mu.Lock()
		p.disconnecting = false
		p.connectionInProgress = false
		p.mu.Unlock()
	}()
	return jsonResponse(map[string]interface{}{"success": true, "message": "Disconnect started"})
}

func (p *Plugin) handleScan() pluginmanager.WebhookResponse {
	p.mu.Lock()
	if p.scanning {
		devices := make([]DiscoveredDevice, 0, len(p.discovered))
		for _, d := range p.discovered {
			devices = append(devices, d)
		}
		p.mu.Unlock()
		return jsonResponse(map[string]interface{}{"devices": devices, "scanning": true})
	}
	p.scanning = true
	p.discovered = map[string]DiscoveredDevice{}
	p.mu.Unlock()

	go func() {
		devices, err := p.scanDevices(context.Background())
		p.mu.Lock()
		if err == nil {
			p.discovered = map[string]DiscoveredDevice{}
			for _, d := range devices {
				p.discovered[d.MAC] = d
			}
		}
		p.scanning = false
		p.mu.Unlock()
	}()
	return jsonResponse(map[string]interface{}{"devices": []DiscoveredDevice{}, "scanning": true})
}

// durationSeconds formats a time.Duration as a plain integer second count
// string, used for CLI arguments like bluetoothctl --timeout.
func durationSeconds(d time.Duration) string {
	return strconv.Itoa(int(d / time.Second))
}
