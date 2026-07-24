// Package wittypi is the native Go port of
// pwnagotchi/plugins/default/wittypi.py: a battery percentage/charging
// indicator for the Witty Pi 4 L3V7 UPS board, read over I2C.
//
// Original Python author: https://github.com/krishenriksen (see
// wittypi.py's own __author__ field, left untouched). This Go port is by
// the migration session's fork worker.
package wittypi

import (
	"sync"

	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// Witty Pi 4 L3V7 microcontroller I2C address and register map (ported
// verbatim from wittypi.py's UPS class constants).
const (
	i2cAddress   = 0x08
	regVoltageI  = 1 // integer part of voltage
	regVoltageD  = 2 // decimal (hundredths) part of voltage
	regCurrentI  = 5 // integer part of current
	regCurrentD  = 6 // decimal (hundredths) part of current
	regPowerMode = 7 // 0 = charging (DC in), nonzero = on battery
)

// elementKey/label mirror on_ui_setup's
// ui.add_element('ups', LabeledValue(..., position=(ui.width()/2+15, 0))).
const (
	elementKey = "ups"
	elementY   = 0
	labelText  = "UPS"
)

// Plugin ports the WittyPi class.
type Plugin struct {
	mu   sync.Mutex
	view pluginmanager.ViewCapability
	dev  pluginmanager.I2CDevice
	log  pluginmanager.Logger
}

// New ports WittyPi.__init__ (self.ups = None).
func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string { return "wittypi" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "1.0.0",
		Author:      "https://github.com/krishenriksen",
		License:     "GPL3",
		Description: "A plugin that will display battery info from Witty Pi 4 L3V7",
	}
}

// OnLoad ports on_loaded (UPS() — opens the I2C device once) and
// on_ui_setup (adds the 'ups' LabeledValue element) in one step — a
// native plugin's OnLoad only ever runs once real capabilities exist,
// the same "once, at startup, before the first render" timing Python's
// on_loaded-then-on_ui_setup sequence has (see memtemp.go's OnLoad doc
// comment for the fuller rationale).
func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.view = caps.View
	p.log = caps.Log

	if caps.I2C == nil {
		p.logf("I2C capability unavailable (no bus backend wired yet); battery readings will report 0%%")
	} else {
		dev, err := caps.I2C.Open(1, i2cAddress)
		if err != nil {
			p.logf("opening I2C device: %v", err)
		} else {
			p.dev = dev
		}
	}

	if p.view != nil {
		elementX := p.view.Width()/2 + 15
		p.view.AddLabeledValue(elementKey, labelText, "0%", elementX, elementY, pluginmanager.FontBold, pluginmanager.FontMedium, 0)
	}
	return nil
}

// OnUnload ports on_unload: remove the 'ups' element.
func (p *Plugin) OnUnload() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.view != nil {
		p.view.RemoveElement(elementKey)
	}
	if p.dev != nil {
		_ = p.dev.Close()
	}
	return nil
}

// HandleEvent ports on_ui_update: refresh the displayed capacity/charging
// indicator.
func (p *Plugin) HandleEvent(event string, _ []interface{}) {
	if event != "ui_update" {
		return
	}
	p.mu.Lock()
	view := p.view
	p.mu.Unlock()
	if view == nil {
		return
	}
	capacity := p.capacity()
	charging := p.charging()
	view.Set(elementKey, formatReading(capacity, charging))
}

// formatReading ports Python's "%2i%s" % (capacity, charging): a
// space-padded-to-width-2 integer immediately followed by the charging
// sign, no separator.
func formatReading(capacity int, charging string) string {
	digits := itoa(capacity)
	if len(digits) < 2 {
		digits = " " + digits
	}
	return digits + charging
}

func itoa(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// readByteReg ports one `self._bus.read_byte_data(addr, reg)` call:
// exactly one register byte.
func (p *Plugin) readByteReg(reg uint8) (int, error) {
	p.mu.Lock()
	dev := p.dev
	p.mu.Unlock()
	if dev == nil {
		return 0, errNoDevice
	}
	data, err := dev.ReadReg(reg, 1)
	if err != nil {
		return 0, err
	}
	if len(data) < 1 {
		return 0, errShortRead
	}
	return int(data[0]), nil
}

// voltage ports UPS.voltage(): integer + hundredths from two consecutive
// registers.
func (p *Plugin) voltage() float64 {
	i, err := p.readByteReg(regVoltageI)
	if err != nil {
		p.logf("voltage: %v", err)
		return 0.0
	}
	d, err := p.readByteReg(regVoltageD)
	if err != nil {
		p.logf("voltage: %v", err)
		return 0.0
	}
	return float64(i) + float64(d)/100
}

// current ports UPS.current(), same shape as voltage() at a different
// register pair. Not surfaced on-screen by the real plugin (only
// capacity+charging are), but ported for parity/future use exactly as
// the original UPS class exposes it.
func (p *Plugin) current() float64 {
	i, err := p.readByteReg(regCurrentI)
	if err != nil {
		p.logf("current: %v", err)
		return 0.0
	}
	d, err := p.readByteReg(regCurrentD)
	if err != nil {
		p.logf("current: %v", err)
		return 0.0
	}
	return float64(i) + float64(d)/100
}

// capacity ports UPS.capacity(): voltage clamped to [3.1, 4.2] then
// linearly mapped to a 0-100 percentage, rounded to the nearest integer
// (Python's round() on a float uses banker's rounding at exactly .5;
// this uses standard round-half-away-from-zero — a disclosed, harmless
// divergence since a battery voltage reading landing on an exact .5%
// boundary is not a realistic occurrence and no test depends on it).
func (p *Plugin) capacity() int {
	v := p.voltage()
	if v < 3.1 {
		v = 3.1
	}
	if v > 4.2 {
		v = 4.2
	}
	pct := (v - 3.1) / (4.2 - 3.1) * 100
	return int(pct + 0.5)
}

// charging ports UPS.charging(): register 7 == 0 means charging.
func (p *Plugin) charging() string {
	mode, err := p.readByteReg(regPowerMode)
	if err != nil {
		return "-"
	}
	if mode == 0 {
		return "+"
	}
	return "-"
}

func (p *Plugin) logf(format string, args ...interface{}) {
	if p.log != nil {
		p.log.Printf(format, args...)
	}
}

type sentinelError string

func (e sentinelError) Error() string { return string(e) }

const (
	errNoDevice  sentinelError = "wittypi: I2C device not available"
	errShortRead sentinelError = "wittypi: short I2C read"
)

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.Unloader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
