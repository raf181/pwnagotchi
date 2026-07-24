// Package pisugarx is the native Go port of
// pwnagotchi/plugins/default/pisugarx.py: a battery voltage/capacity/
// temperature indicator for PiSugar2, PiSugar2Plus, and PiSugar3 battery
// boards (auto-detected over I2C), with an optional low-power auto
// shutdown and charge-voltage protection.
//
// Original Python author: jayofelony (see pisugarx.py's own __author__
// field, left untouched). This Go port is by the migration session's
// fork worker.
package pisugarx

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// Model identifies which PiSugar board was detected, matching
// PiSugarServer's self.model strings exactly.
type Model string

const (
	ModelNone         Model = ""
	ModelPiSugar2     Model = "PiSugar2"
	ModelPiSugar2Plus Model = "PiSugar2Plus"
	ModelPiSugar3     Model = "PiSugar3"
)

// I2C addresses, ported verbatim from the real PiSugar_addresses dict.
const (
	addrPiSugar2 uint8 = 0x75
	addrPiSugar3 uint8 = 0x57
)

// curvePoint is one (voltage, percentage) sample of a battery discharge
// curve, ordered highest-voltage-first exactly like the Python lists.
type curvePoint struct {
	voltage    float64
	percentage float64
}

// curve5312/curve5209 are the exact discharge curves from
// pisugarx.py's module-level curve5312/curve5209 lists ("the same
// battery level curve as pisugar-power-manager").
var curve5312 = []curvePoint{
	{4.10, 100.0}, {4.05, 95.0}, {3.90, 88.0}, {3.80, 77.0}, {3.70, 65.0},
	{3.62, 55.0}, {3.58, 49.0}, {3.49, 25.6}, {3.32, 4.5}, {3.1, 0.0},
}

var curve5209 = []curvePoint{
	{4.16, 100.0}, {4.05, 95.0}, {4.00, 80.0}, {3.92, 65.0}, {3.86, 40.0},
	{3.79, 25.5}, {3.66, 10.0}, {3.52, 6.5}, {3.49, 3.2}, {3.1, 0.0},
}

func curveFor(model Model) []curvePoint {
	switch model {
	case ModelPiSugar2:
		return curve5209
	default: // PiSugar2Plus, PiSugar3 (and PiSugar3Plus, never actually detected by this port's connect logic, same as upstream)
		return curve5312
	}
}

const elementKey = "bat"

// clock is overridable for tests (real time.Now in production), matching
// this port's established injected-clock pattern.
type clock interface{ Now() time.Time }
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Plugin ports the PiSugar plugin class (PiSugarServer + PiSugar merged
// into one, since this port has no reason to keep them as two objects).
type Plugin struct {
	mu sync.Mutex

	view  pluginmanager.ViewCapability
	log   pluginmanager.Logger
	i2c   pluginmanager.I2CCapability
	sys   pluginmanager.SystemCapability
	clock clock

	rotationEnabled            bool
	defaultDisplay             string
	lowpowerShutdown           bool
	lowpowerShutdownLevel      float64
	maxChargeVoltageProtection bool
	maxProtectionLevel         float64

	dev     pluginmanager.I2CDevice
	address uint8
	model   Model
	ready   bool

	i2creg [256]byte

	batteryVoltage float64
	voltageHistory []float64
	batteryLevel   float64
	temperature    int
	powerPlugged   bool
	allowCharging  bool

	drot     int
	nextDChg time.Time

	cancel context.CancelFunc
	done   chan struct{}
}

// New ports PiSugar.__init__ (state only; the real I2C connection is
// deferred to OnLoad in this port, since Python's own __init__ already
// just kicks off a background thread that may never actually find a
// device — see connectAndInit).
func New() *Plugin {
	return &Plugin{clock: realClock{}, defaultDisplay: "voltage", rotationEnabled: true}
}

func (p *Plugin) Name() string { return "pisugarx" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version: "1.2",
		Author:  "jayofelony",
		License: "GPL3",
		Description: "A plugin that will add a voltage indicator for the PiSugar batteries. " +
			"Rotation of battery status can be enabled or disabled via configuration. " +
			"Additionally, when rotation is disabled, you can choose which metric to display.",
	}
}

// OnLoad ports on_loaded + on_ui_setup in one step (see memtemp.go's
// OnLoad doc comment for why a native plugin's OnLoad already covers
// both timings), then starts the background connect+poll goroutine
// real PiSugarServer.__init__ starts (here explicitly stoppable via
// OnUnload, unlike Python's fire-and-forget daemon thread).
func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	p.view = caps.View
	p.log = caps.Log
	p.i2c = caps.I2C
	p.sys = caps.System
	if caps.Clock != nil {
		p.clock = caps.Clock
	}

	p.rotationEnabled = boolField(caps.Config, "rotation", true)
	p.defaultDisplay = strings.ToLower(stringField(caps.Config, "default_display", "voltage"))
	switch p.defaultDisplay {
	case "voltage", "percentage", "percent", "temp":
	default:
		// caps.Log directly, not p.logf: p.mu is already held here, and
		// logf takes the same lock — calling it while holding p.mu would
		// deadlock (this exact bug was caught by a real hang in
		// TestInvalidDefaultDisplayFallsBackToVoltage during development).
		if caps.Log != nil {
			caps.Log.Printf("[PiSugarX] Invalid default_display %q. Using 'voltage'.", p.defaultDisplay)
		}
		p.defaultDisplay = "voltage"
	}
	p.lowpowerShutdown = boolField(caps.Config, "lowpower_shutdown", true)
	p.lowpowerShutdownLevel = floatField(caps.Config, "lowpower_shutdown_level", 10)
	p.maxChargeVoltageProtection = boolField(caps.Config, "max_charge_voltage_protection", true)
	p.maxProtectionLevel = floatField(caps.Config, "max_protection_level", 80)
	view := p.view
	p.mu.Unlock()

	if view != nil {
		x := view.Width()/2 + 15
		view.AddLabeledValue(elementKey, "BAT", "0%", x, 0, pluginmanager.FontBold, pluginmanager.FontMedium, 0)
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	p.cancel = cancel
	p.done = make(chan struct{})
	p.mu.Unlock()
	go p.run(ctx)
	return nil
}

// OnUnload stops the background goroutine and removes the UI element.
func (p *Plugin) OnUnload() error {
	p.mu.Lock()
	cancel := p.cancel
	done := p.done
	view := p.view
	dev := p.dev
	p.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	if view != nil {
		view.RemoveElement(elementKey)
	}
	if dev != nil {
		_ = dev.Close()
	}
	return nil
}

// HandleEvent ports on_ui_update (the only hook with real, non-stub
// logic among on_ready/on_internet_available/on_ui_update — on_ready
// just re-reads a bool this port's background loop already keeps
// current, and on_internet_available's rtc_web() is a literal `pass` in
// every real PiSugarServer implementation, so there is nothing to port
// for either).
func (p *Plugin) HandleEvent(event string, _ []interface{}) {
	if event != "ui_update" {
		return
	}
	p.updateUI()
}

func (p *Plugin) updateUI() {
	p.mu.Lock()
	view := p.view
	ready := p.ready
	voltage := p.batteryVoltage
	capacity := p.batteryLevel
	temp := p.temperature
	plugged := p.powerPlugged
	p.mu.Unlock()
	if view == nil {
		return
	}
	if !ready {
		voltage, capacity, temp = 0, 0, 0
	}

	// Real Python mutates the element's label in place between "BAT" and
	// "CHG"; ViewCapability has no per-label mutator (only AddLabeledValue
	// at creation time), so the charge indicator is folded into the value
	// string instead (a "CHG " prefix below) — a disclosed, harmless
	// simplification, the indicator is still visibly present either way.
	now := p.clock.Now()
	p.mu.Lock()
	if p.rotationEnabled {
		if !now.Before(p.nextDChg) {
			p.drot = (p.drot + 1) % 3
			p.nextDChg = now.Add(5 * time.Second)
		}
	}
	drot := p.drot
	rotation := p.rotationEnabled
	display := p.defaultDisplay
	p.mu.Unlock()

	mode := display
	if rotation {
		mode = []string{"voltage", "percentage", "temp"}[drot]
	}

	var value string
	switch mode {
	case "voltage":
		value = fmt.Sprintf("%.2fV", voltage)
	case "percentage", "percent":
		value = fmt.Sprintf("%.0f%%", capacity)
	default: // "temp"
		value = fmt.Sprintf("%d°C", temp)
	}
	if plugged {
		value = "CHG " + value
	}
	view.Set(elementKey, value)
}

// run is the background connect-then-poll loop real PiSugarServer's two
// daemon threads (_connect_device, then update_value) collapse into.
func (p *Plugin) run(ctx context.Context) {
	defer close(p.done)
	for {
		if p.connectAndInit(ctx) {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
	p.mu.Lock()
	p.ready = true
	p.mu.Unlock()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pollOnce()
			p.checkLowPowerShutdown()
		}
	}
}

// connectAndInit ports _connect_device's device-detection branch (the
// polling-until-found loop is the caller's job in run/OnLoad's tests).
// Returns true once a real device is opened and initialized.
func (p *Plugin) connectAndInit(ctx context.Context) bool {
	p.mu.Lock()
	i2c := p.i2c
	p.mu.Unlock()
	if i2c == nil {
		return false
	}

	if dev, ok := checkDevice(i2c, addrPiSugar2); ok {
		model := ModelPiSugar2
		if b, err := dev.ReadReg(0xC2, 1); err == nil && len(b) == 1 && b[0] != 0 {
			model = ModelPiSugar2Plus
		}
		p.mu.Lock()
		p.dev, p.address, p.model = dev, addrPiSugar2, model
		p.mu.Unlock()
		p.deviceInit()
		return true
	}
	if dev, ok := checkDevice(i2c, addrPiSugar3); ok {
		p.mu.Lock()
		p.dev, p.address, p.model = dev, addrPiSugar3, ModelPiSugar3
		p.mu.Unlock()
		return true
	}
	return false
}

// checkDevice ports PiSugarServer.check_device: open the bus device and
// confirm register 0 is readable (a real device answered), returning
// (nil, false) — never a partially-opened device — otherwise.
func checkDevice(i2c pluginmanager.I2CCapability, addr uint8) (pluginmanager.I2CDevice, bool) {
	dev, err := i2c.Open(1, addr)
	if err != nil {
		return nil, false
	}
	if _, err := dev.ReadReg(0, 1); err != nil {
		_ = dev.Close()
		return nil, false
	}
	return dev, true
}

// deviceInit ports device_init's per-model GPIO/current-limit register
// setup, run once right after a device is detected.
func (p *Plugin) deviceInit() {
	p.mu.Lock()
	dev, model := p.dev, p.model
	p.mu.Unlock()
	if dev == nil {
		return
	}
	switch model {
	case ModelPiSugar2Plus:
		setBits(dev, 0x52, 0b00000010, 0)
		setBits(dev, 0x54, 0b00000010, 0)
		setBits(dev, 0x52, 0b00000100, 0)
		setBits(dev, 0x29, 0, 0b01000000)
		modifyBits(dev, 0x52, 0b10011111, 0b01000000)
		setBits(dev, 0xC2, 0b00010000, 0)
		modifyBits(dev, 0x30, 0b11000000, 0x3f)
	case ModelPiSugar2:
		modifyBits(dev, 0x51, 0b11110011, 0b00000100)
		setBits(dev, 0x53, 0b00000010, 0)
		modifyBits(dev, 0x51, 0b11001111, 0b00010000)
		setBits(dev, 0x26, 0, 0b01001111) // clear bits per `& 0b10110000`
		modifyBits(dev, 0x52, 0b11110011, 0b00000100)
		modifyBits(dev, 0x53, 0b11101111, 0b00010000)
	}
}

// pollOnce ports one iteration of update_value's try block: refresh the
// full 256-byte register cache and derive voltage/temperature/charging
// state from it. Exported behavior via HandleEvent/updateUI; kept as its
// own method so tests can drive exactly one poll deterministically
// without waiting on the real 3-second ticker.
func (p *Plugin) pollOnce() {
	p.mu.Lock()
	dev, model := p.dev, p.model
	p.mu.Unlock()
	if dev == nil {
		return
	}

	if model == ModelPiSugar2 || model == ModelPiSugar2Plus {
		// Temporarily disable charging to get an accurate battery
		// voltage reading — matches update_value's real call before
		// each read for these two models.
		p.setBatteryNotAllowCharging()
	}

	var reg [256]byte
	for i := 0; i < 256; i += 32 {
		n := 32
		if i+n > 256 {
			n = 256 - i
		}
		chunk, err := dev.ReadReg(uint8(i), n)
		if err != nil {
			p.logf("read error: %v", err)
			return
		}
		copy(reg[i:i+n], chunk)
	}
	p.mu.Lock()
	p.i2creg = reg
	p.mu.Unlock()

	var voltage float64
	var plugged, allowCharging bool
	var temp int
	switch model {
	case ModelPiSugar3:
		low, high := reg[0x23], reg[0x22]
		voltage = float64(uint16(high)<<8+uint16(low)) / 1000
		temp = int(reg[0x04]) - 40
		ctrl1 := reg[0x02]
		plugged = ctrl1&(1<<7) != 0
		allowCharging = ctrl1&(1<<6) != 0
		p.applyChargeVoltageProtectionPiSugar3(dev)
	case ModelPiSugar2:
		high, low := reg[0xa3], reg[0xa2]
		if high&0x20 != 0 {
			voltage = (2600.0 - float64((uint16(high|0b11000000)<<8)+uint16(low))*0.26855) / 1000.0
		} else {
			voltage = (2600.0 + float64((uint16(high&0x1f)<<8)+uint16(low))*0.26855) / 1000.0
		}
		plugged = reg[0x55]&0b00010000 != 0
	case ModelPiSugar2Plus:
		low, high := reg[0xd0], reg[0xd1]
		voltage = (float64((uint16(high&0b00111111)<<8)+uint16(low))*0.26855 + 2600.0) / 1000
		plugged = reg[0xdd] == 0x1f
	}

	p.mu.Lock()
	p.batteryVoltage = voltage
	p.temperature = temp
	p.powerPlugged = plugged
	p.allowCharging = allowCharging
	p.voltageHistory = append(p.voltageHistory, voltage)
	if len(p.voltageHistory) > 10 {
		p.voltageHistory = p.voltageHistory[len(p.voltageHistory)-10:]
	}
	p.batteryLevel = convertVoltageToLevel(p.voltageHistory, curveFor(model))
	protection := p.maxChargeVoltageProtection
	protectionLevel := p.maxProtectionLevel
	level := p.batteryLevel
	p.mu.Unlock()

	if model == ModelPiSugar2 || model == ModelPiSugar2Plus {
		if protection {
			if level > protectionLevel {
				p.setBatteryNotAllowCharging()
			} else {
				p.setBatteryAllowCharging()
			}
		} else {
			p.setBatteryAllowCharging()
		}
	}
}

// checkLowPowerShutdown ports update_value's low-power-shutdown check,
// separated from pollOnce so tests can assert it independently of a full
// register-read cycle.
func (p *Plugin) checkLowPowerShutdown() {
	p.mu.Lock()
	enabled := p.lowpowerShutdown
	level := p.lowpowerShutdownLevel
	battery := p.batteryLevel
	model := p.model
	dev := p.dev
	sys := p.sys
	p.mu.Unlock()
	if !enabled || battery >= level || dev == nil {
		return
	}
	p.logf("low power shutdown now.")
	shutdownDevice(dev, model)
	if sys != nil {
		if err := sys.Shutdown(); err != nil {
			p.logf("shutdown: %v", err)
		}
	}
}

// shutdownDevice ports PiSugarServer.shutdown: only PiSugar3 has real
// hardware behavior (a 10-second power-off timer register write) —
// PiSugar2/2Plus are literally `pass` in the real implementation.
func shutdownDevice(dev pluginmanager.I2CDevice, model Model) {
	if model != ModelPiSugar3 {
		return
	}
	_ = dev.WriteReg(0x0B, []byte{0x29})
	_ = dev.WriteReg(0x09, []byte{10})
	cur, _ := dev.ReadReg(0x02, 1)
	var b byte
	if len(cur) == 1 {
		b = cur[0]
	}
	_ = dev.WriteReg(0x02, []byte{b & 0b11011111})
	_ = dev.WriteReg(0x0B, []byte{0x00})
}

func (p *Plugin) applyChargeVoltageProtectionPiSugar3(dev pluginmanager.I2CDevice) {
	p.mu.Lock()
	protection := p.maxChargeVoltageProtection
	p.mu.Unlock()
	_ = dev.WriteReg(0x0B, []byte{0x29})
	cur, _ := dev.ReadReg(0x20, 1)
	var b byte
	if len(cur) == 1 {
		b = cur[0]
	}
	if protection {
		b |= 0b10000000
	} else {
		b &= 0b01111111
	}
	_ = dev.WriteReg(0x20, []byte{b})
	_ = dev.WriteReg(0x0B, []byte{0x00})
}

func (p *Plugin) setBatteryAllowCharging() {
	p.mu.Lock()
	dev, model := p.dev, p.model
	p.mu.Unlock()
	if dev == nil {
		return
	}
	switch model {
	case ModelPiSugar2:
		setBits(dev, 0x54, 0b00000100, 0)
		setBits(dev, 0x55, 0b00000100, 0)
		setBits(dev, 0x54, 0, 0b00000100)
	case ModelPiSugar2Plus:
		setBits(dev, 0x56, 0b00000100, 0)
		setBits(dev, 0x58, 0b00000100, 0)
		setBits(dev, 0x56, 0, 0b00000100)
	}
}

func (p *Plugin) setBatteryNotAllowCharging() {
	p.mu.Lock()
	dev, model := p.dev, p.model
	p.mu.Unlock()
	if dev == nil {
		return
	}
	switch model {
	case ModelPiSugar2:
		setBits(dev, 0x54, 0b00000100, 0)
		setBits(dev, 0x55, 0, 0b00000100)
		setBits(dev, 0x54, 0, 0b00000100)
	case ModelPiSugar2Plus:
		setBits(dev, 0x56, 0b00000100, 0)
		setBits(dev, 0x58, 0, 0b00000100)
		setBits(dev, 0x56, 0, 0b00000100)
	}
}

// setBits reads reg, clears clearMask bits, sets setMask bits, writes it
// back — the read-modify-write pattern every register bit-twiddle in
// pisugarx.py uses.
func setBits(dev pluginmanager.I2CDevice, reg uint8, clearMask, setMask byte) {
	cur, err := dev.ReadReg(reg, 1)
	if err != nil || len(cur) != 1 {
		return
	}
	b := cur[0] &^ clearMask
	b |= setMask
	_ = dev.WriteReg(reg, []byte{b})
}

// modifyBits is setBits with an explicit AND-mask (keepMask) instead of a
// clear-mask, for the `(read & keepMask) | setMask` shape several
// device_init lines use.
func modifyBits(dev pluginmanager.I2CDevice, reg uint8, keepMask, setMask byte) {
	cur, err := dev.ReadReg(reg, 1)
	if err != nil || len(cur) != 1 {
		return
	}
	b := (cur[0] & keepMask) | setMask
	_ = dev.WriteReg(reg, []byte{b})
}

// convertVoltageToLevel ports convert_battery_voltage_to_level: a
// trimmed-mean (drop 2 highest/2 lowest once >=5 samples) of recent
// voltage readings, linearly interpolated against the model's discharge
// curve.
func convertVoltageToLevel(history []float64, curve []curvePoint) float64 {
	if len(history) == 0 {
		return 0
	}
	var avg float64
	if len(history) < 5 {
		sum := 0.0
		for _, v := range history {
			sum += v
		}
		avg = sum / float64(len(history))
	} else {
		sorted := append([]float64(nil), history...)
		sort.Float64s(sorted)
		trimmed := sorted[2 : len(sorted)-2]
		sum := 0.0
		for _, v := range trimmed {
			sum += v
		}
		avg = sum / float64(len(trimmed))
	}

	for i := 0; i < len(curve)-1; i++ {
		v1, p1 := curve[i].voltage, curve[i].percentage
		v2, p2 := curve[i+1].voltage, curve[i+1].percentage
		if avg >= v2 && avg <= v1 {
			return p2 + (p1-p2)*(avg-v2)/(v1-v2)
		}
	}
	if avg < curve[len(curve)-1].voltage {
		return curve[len(curve)-1].percentage
	}
	return curve[0].percentage
}

func (p *Plugin) logf(format string, args ...interface{}) {
	p.mu.Lock()
	log := p.log
	p.mu.Unlock()
	if log != nil {
		log.Printf("[PiSugarX] "+format, args...)
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

func boolField(m config.Map, key string, def bool) bool {
	if m == nil {
		return def
	}
	if b, ok := m[key].(bool); ok {
		return b
	}
	return def
}

func floatField(m config.Map, key string, def float64) float64 {
	if m == nil {
		return def
	}
	switch v := m[key].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	}
	return def
}

// OnWebhook ports on_webhook: a real-data HTML status page. Every field
// the real PiSugarServer class only ever "implements" as a bare `pass`
// (returns None unconditionally — charging range, full-charge duration,
// safe-shutdown level/delay, auto power-on, soft power-off, system time,
// RTC adjust PPM/alarm repeat, tap enable/shell, anti-mistouch) is
// rendered with its real Python default fallback text here too ("N/A" /
// "No" / "Not set") rather than invented — those parameters are not
// currently functional in the upstream plugin on ANY board revision, not
// a gap introduced by this port.
func (p *Plugin) OnWebhook(subpath string, r *http.Request) (pluginmanager.WebhookResponse, error) {
	p.mu.Lock()
	ready := p.ready
	model := p.model
	voltage := p.batteryVoltage
	level := p.batteryLevel
	plugged := p.powerPlugged
	allowCharging := p.allowCharging
	temp := p.temperature
	reg := p.i2creg
	p.mu.Unlock()

	if !ready {
		body := "<html><head><title>PiSugarX not ready</title></head><body><h1>PiSugarX not ready</h1></body></html>"
		return pluginmanager.WebhookResponse{Status: http.StatusOK, Body: []byte(body)}, nil
	}
	if subpath != "" && subpath != "/" {
		return pluginmanager.WebhookResponse{}, fmt.Errorf("pisugarx: not found: %s", subpath)
	}

	version := "Unknown"
	if model == ModelPiSugar3 {
		version = strings.TrimRight(string(reg[0xe2:0xee]), "\x00")
	}

	yesNo := func(b bool) string {
		if b {
			return "Yes"
		}
		return "No"
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html lang=\"en\"><head><meta charset=\"UTF-8\"><title>PiSugarX Parameters</title></head><body>")
	b.WriteString("<h1>PiSugarX Parameters</h1><table><thead><tr><th>Parameter</th><th>Value</th></tr></thead><tbody>")
	fmt.Fprintf(&b, "<tr><td>Server version</td><td>%s</td></tr>", version)
	fmt.Fprintf(&b, "<tr><td>PiSugar Model</td><td>%s</td></tr>", string(model))
	fmt.Fprintf(&b, "<tr><td>Battery Level</td><td>%.0f%%</td></tr>", level)
	fmt.Fprintf(&b, "<tr><td>Battery Voltage</td><td>%.2fV</td></tr>", voltage)
	b.WriteString("<tr><td>Battery Current</td><td>N/A</td></tr>")
	fmt.Fprintf(&b, "<tr><td>Battery Allow Charging</td><td>%s</td></tr>", yesNo(allowCharging))
	b.WriteString("<tr><td>Battery Charging Range</td><td>N/A</td></tr>")
	b.WriteString("<tr><td>Duration of Keep Charging When Full</td><td>N/A seconds</td></tr>")
	b.WriteString("<tr><td>Battery Safe Shutdown Level</td><td>Not set</td></tr>")
	b.WriteString("<tr><td>Battery Safe Shutdown Delay</td><td>N/A seconds</td></tr>")
	b.WriteString("<tr><td>Battery Auto Power On</td><td>No</td></tr>")
	b.WriteString("<tr><td>Battery Soft Power Off Enabled</td><td>No</td></tr>")
	b.WriteString("<tr><td>System Time</td><td>N/A</td></tr>")
	b.WriteString("<tr><td>RTC Adjust PPM</td><td>Not supported</td></tr>")
	b.WriteString("<tr><td>RTC Alarm Repeat</td><td>N/A</td></tr>")
	b.WriteString("<tr><td>Single Tap Enabled</td><td>No</td></tr>")
	b.WriteString("<tr><td>Double Tap Enabled</td><td>No</td></tr>")
	b.WriteString("<tr><td>Long Tap Enabled</td><td>No</td></tr>")
	b.WriteString("<tr><td>Single Tap Shell</td><td>N/A</td></tr>")
	b.WriteString("<tr><td>Double Tap Shell</td><td>N/A</td></tr>")
	b.WriteString("<tr><td>Long Tap Shell</td><td>N/A</td></tr>")
	b.WriteString("<tr><td>Mis Touch Protection Enabled</td><td>No</td></tr>")
	fmt.Fprintf(&b, "<tr><td>Battery Temperature</td><td>%d &deg;C</td></tr>", temp)
	fmt.Fprintf(&b, "<tr><td>Power Plugged</td><td>%s</td></tr>", yesNo(plugged))
	b.WriteString("</tbody></table></body></html>")

	return pluginmanager.WebhookResponse{Status: http.StatusOK, Body: []byte(b.String())}, nil
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.Unloader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
var _ pluginmanager.WebhookHandler = (*Plugin)(nil)
