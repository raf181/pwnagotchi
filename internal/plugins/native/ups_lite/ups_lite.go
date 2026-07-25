// Package upslite is the native Go port of
// pwnagotchi/plugins/default/ups_lite.py: a battery percentage indicator
// for the UPS Lite v1.3 board (CW2015 fuel-gauge chip over I2C, plus a
// GPIO pin for charging status).
//
// Original Python author: marbasec (see ups_lite.py's own __author__
// field, left untouched). This Go port is by the migration session's
// fork worker.
package upslite

import (
	"sync"

	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// CW2015 fuel-gauge chip I2C address and register map (ported verbatim
// from ups_lite.py's module-level constants).
const (
	cw2015Address = 0x62
	regVCell      = 0x02
	regSOC        = 0x04

	chargingGPIOPin = 4
)

// elementKey/label mirror on_ui_setup's
// ui.add_element('ups', LabeledValue(..., position=(ui.width()/2+15, 0))).
const (
	elementKey = "ups"
	elementY   = 0
	labelText  = "UPS"
)

// Plugin ports the UPSLite class.
type Plugin struct {
	mu   sync.Mutex
	view pluginmanager.ViewCapability
	dev  pluginmanager.I2CDevice
	gpio pluginmanager.GPIOCapability
	log  pluginmanager.Logger
}

// New ports UPSLite.__init__ (self.ups = None).
func New() *Plugin { return &Plugin{} }

// Name matches the real plugin's config key ("ups_lite"), not this
// package's Go identifier (which drops the underscore — Go convention —
// exactly like internal/wpasec's package `wpasec` reports Name()
// "wpa-sec").
func (p *Plugin) Name() string { return "ups_lite" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "1.3.0",
		Author:      "marbasec (original), Go port by raf181",
		License:     "GPL3",
		Description: "A plugin that will add a voltage indicator for the UPS Lite v1.3",
	}
}

// OnLoad ports on_loaded (UPS() — opens the I2C device once) and
// on_ui_setup (adds the 'ups' LabeledValue element) — see wittypi.go's
// OnLoad doc comment for why a native plugin's OnLoad covers both in one
// step.
func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.view = caps.View
	p.gpio = caps.GPIO
	p.log = caps.Log

	if caps.I2C == nil {
		p.logf("I2C capability unavailable (no bus backend wired yet); battery readings will report 0%%")
	} else {
		dev, err := caps.I2C.Open(1, cw2015Address)
		if err != nil {
			p.logf("opening I2C device: %v", err)
		} else {
			p.dev = dev
		}
	}
	if caps.GPIO == nil {
		p.logf("GPIO capability unavailable (no bus backend wired yet); charging status will report '-'")
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

// HandleEvent ports on_ui_update.
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

// readWordReg ports one `self._bus.read_word_data(addr, reg)` call
// followed by Python's `struct.unpack("<H", struct.pack(">H", read))[0]`
// double byte-swap. The smbus word-read protocol assembles its returned
// int as (byte-at-reg | byte-at-reg+1<<8) — little-endian off the wire —
// but the CW2015's actual register pair is big-endian (MSB at reg, LSB
// at reg+1), which is exactly what that double swap corrects for. This
// reads the same two registers directly in ascending order and
// interprets them big-endian, which is numerically identical to the
// smbus-word-read-then-double-byteswap dance without needing to
// replicate smbus's own wire-order assembly step — verified by working
// the arithmetic through by hand for both paths.
func (p *Plugin) readWordReg(reg uint8) (uint16, error) {
	p.mu.Lock()
	dev := p.dev
	p.mu.Unlock()
	if dev == nil {
		return 0, errNoDevice
	}
	data, err := dev.ReadReg(reg, 2)
	if err != nil {
		return 0, err
	}
	if len(data) < 2 {
		return 0, errShortRead
	}
	return uint16(data[0])<<8 | uint16(data[1]), nil
}

// voltage ports UPS.voltage(): swapped * 1.25 / 1000 / 16.
func (p *Plugin) voltage() float64 {
	word, err := p.readWordReg(regVCell)
	if err != nil {
		p.logf("voltage: %v", err)
		return 0.0
	}
	return float64(word) * 1.25 / 1000 / 16
}

// capacity ports UPS.capacity(): swapped / 256.
func (p *Plugin) capacity() float64 {
	word, err := p.readWordReg(regSOC)
	if err != nil {
		p.logf("capacity: %v", err)
		return 0.0
	}
	return float64(word) / 256
}

// charging ports UPS.charging(): GPIO pin 4 HIGH means charging. Returns
// "-" (never panics/errors out) whenever the GPIO capability is
// unavailable or the line can't be read — matching Python's `if GPIO is
// None: return '-'` / bare `except: return '-'`.
func (p *Plugin) charging() string {
	p.mu.Lock()
	gpio := p.gpio
	p.mu.Unlock()
	if gpio == nil {
		return "-"
	}
	line, err := gpio.Line(chargingGPIOPin)
	if err != nil {
		return "-"
	}
	defer line.Close()
	high, err := line.Read()
	if err != nil {
		return "-"
	}
	if high {
		return "+"
	}
	return "-"
}

// formatReading ports Python's "%2i%s" % (capacity, charging) — see
// wittypi.go's identical helper for the exact space-padding rule.
// capacity here is a float (Python's %i truncates, it does not round).
func formatReading(capacity float64, charging string) string {
	digits := itoa(int(capacity))
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

func (p *Plugin) logf(format string, args ...interface{}) {
	if p.log != nil {
		p.log.Printf(format, args...)
	}
}

type sentinelError string

func (e sentinelError) Error() string { return string(e) }

const (
	errNoDevice  sentinelError = "ups_lite: I2C device not available"
	errShortRead sentinelError = "ups_lite: short I2C read"
)

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.Unloader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
