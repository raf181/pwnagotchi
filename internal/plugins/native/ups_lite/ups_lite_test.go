package upslite

import (
	"context"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// fakeI2CDevice is an in-memory register file addressed byte-by-byte.
type fakeI2CDevice struct {
	regs   map[uint8]byte
	closed bool
}

func newFakeDevice(regs map[uint8]byte) *fakeI2CDevice { return &fakeI2CDevice{regs: regs} }

func (d *fakeI2CDevice) ReadReg(reg uint8, n int) ([]byte, error) {
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = d.regs[reg+uint8(i)]
	}
	return out, nil
}
func (d *fakeI2CDevice) WriteReg(reg uint8, data []byte) error { return nil }
func (d *fakeI2CDevice) Close() error                          { d.closed = true; return nil }

type fakeI2CBus struct {
	dev     *fakeI2CDevice
	openErr error
}

func (b *fakeI2CBus) Open(bus int, addr uint8) (pluginmanager.I2CDevice, error) {
	if b.openErr != nil {
		return nil, b.openErr
	}
	return b.dev, nil
}

// fakeGPIOLine is a single fixed-value GPIO line.
type fakeGPIOLine struct {
	high    bool
	readErr error
	closed  bool
}

func (l *fakeGPIOLine) Read() (bool, error) { return l.high, l.readErr }
func (l *fakeGPIOLine) Write(bool) error    { return nil }
func (l *fakeGPIOLine) WaitEdge(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
func (l *fakeGPIOLine) Close() error { l.closed = true; return nil }

type fakeGPIOBus struct {
	line    *fakeGPIOLine
	lineErr error
}

func (b *fakeGPIOBus) Line(pin int) (pluginmanager.GPIOLine, error) {
	if b.lineErr != nil {
		return nil, b.lineErr
	}
	return b.line, nil
}

var _ pluginmanager.I2CDevice = (*fakeI2CDevice)(nil)
var _ pluginmanager.I2CCapability = (*fakeI2CBus)(nil)
var _ pluginmanager.GPIOLine = (*fakeGPIOLine)(nil)
var _ pluginmanager.GPIOCapability = (*fakeGPIOBus)(nil)

// fakeView records Set/AddLabeledValue/RemoveElement calls.
type fakeView struct {
	values     map[string]string
	added      bool
	removed    bool
	addedLabel string
}

func newFakeView() *fakeView { return &fakeView{values: map[string]string{}} }

func (f *fakeView) Set(key, value string) { f.values[key] = value }
func (f *fakeView) Update(bool)           {}
func (f *fakeView) Kind() string          { return "dummydisplay" }
func (f *fakeView) HasElement(key string) bool {
	_, ok := f.values[key]
	return ok
}
func (f *fakeView) RemoveElement(key string) { delete(f.values, key); f.removed = true }
func (f *fakeView) AddText(key, value string, x, y int, font pluginmanager.FontStyle, wrap bool, maxLength int) {
	f.values[key] = value
}
func (f *fakeView) AddLabeledValue(key, label, value string, x, y int, labelFont, valueFont pluginmanager.FontStyle, labelSpacing int) {
	f.values[key] = value
	f.added = true
	f.addedLabel = label
}
func (f *fakeView) OnUploading(to string) {}
func (f *fakeView) OnNormal()             {}
func (f *fakeView) Width() int            { return 250 }
func (f *fakeView) Height() int           { return 122 }

var _ pluginmanager.ViewCapability = (*fakeView)(nil)

// bigEndianWordBytes packs the fake register pair such that readWordReg
// (which reads [reg, reg+1] and interprets them big-endian) reproduces
// exactly the value the real CW2015-over-smbus double-byteswap dance
// would have produced for a given raw 16-bit "swapped" reading.
func bigEndianWordBytes(word uint16) (hi, lo byte) {
	return byte(word >> 8), byte(word)
}

func TestOnLoadAddsUpsElement(t *testing.T) {
	view := newFakeView()
	bus := &fakeI2CBus{dev: newFakeDevice(nil)}
	gpio := &fakeGPIOBus{line: &fakeGPIOLine{}}
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{View: view, I2C: bus, GPIO: gpio}); err != nil {
		t.Fatal(err)
	}
	if !view.added || view.addedLabel != "UPS" {
		t.Fatalf("expected 'ups' LabeledValue element added, got %+v", view)
	}
	if view.values[elementKey] != "0%" {
		t.Fatalf("expected initial value 0%%, got %q", view.values[elementKey])
	}
}

func TestOnUnloadRemovesElementAndClosesDevice(t *testing.T) {
	view := newFakeView()
	dev := newFakeDevice(nil)
	bus := &fakeI2CBus{dev: dev}
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{View: view, I2C: bus}); err != nil {
		t.Fatal(err)
	}
	if err := p.OnUnload(); err != nil {
		t.Fatal(err)
	}
	if !view.removed {
		t.Fatal("expected element removed")
	}
	if !dev.closed {
		t.Fatal("expected I2C device closed")
	}
}

func TestVoltageAndCapacityFromRegisters(t *testing.T) {
	// Pick a round "swapped" word (2560 -> voltage = 2560*1.25/1000/16 = 0.2;
	// capacity = 2560/256 = 10.0) and verify the big-endian register
	// encoding this plugin expects reproduces it exactly.
	const word = 2560
	vHi, vLo := bigEndianWordBytes(word)
	sHi, sLo := bigEndianWordBytes(word)
	regs := map[uint8]byte{
		regVCell:     vHi,
		regVCell + 1: vLo,
		regSOC:       sHi,
		regSOC + 1:   sLo,
	}
	bus := &fakeI2CBus{dev: newFakeDevice(regs)}
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{View: newFakeView(), I2C: bus}); err != nil {
		t.Fatal(err)
	}
	if got := p.voltage(); got < 0.199 || got > 0.201 {
		t.Fatalf("voltage() = %v, want ~0.2", got)
	}
	if got := p.capacity(); got < 9.99 || got > 10.01 {
		t.Fatalf("capacity() = %v, want ~10.0", got)
	}
}

func TestChargingReflectsGPIOLine(t *testing.T) {
	cases := []struct {
		high bool
		want string
	}{
		{true, "+"},
		{false, "-"},
	}
	for _, c := range cases {
		bus := &fakeI2CBus{dev: newFakeDevice(nil)}
		gpio := &fakeGPIOBus{line: &fakeGPIOLine{high: c.high}}
		p := New()
		if err := p.OnLoad(pluginmanager.Capabilities{View: newFakeView(), I2C: bus, GPIO: gpio}); err != nil {
			t.Fatal(err)
		}
		if got := p.charging(); got != c.want {
			t.Errorf("high=%v: charging() = %q, want %q", c.high, got, c.want)
		}
	}
}

func TestHandleEventUiUpdateSetsFormattedValue(t *testing.T) {
	socHi, socLo := bigEndianWordBytes(256 * 42) // capacity = 42.0
	regs := map[uint8]byte{regSOC: socHi, regSOC + 1: socLo}
	bus := &fakeI2CBus{dev: newFakeDevice(regs)}
	gpio := &fakeGPIOBus{line: &fakeGPIOLine{high: true}}
	view := newFakeView()
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{View: view, I2C: bus, GPIO: gpio}); err != nil {
		t.Fatal(err)
	}
	p.HandleEvent("ui_update", nil)
	if got := view.values[elementKey]; got != "42+" {
		t.Fatalf("ups value = %q, want %q", got, "42+")
	}
}

func TestHandleEventIgnoresOtherEvents(t *testing.T) {
	view := newFakeView()
	bus := &fakeI2CBus{dev: newFakeDevice(nil)}
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{View: view, I2C: bus}); err != nil {
		t.Fatal(err)
	}
	before := view.values[elementKey]
	p.HandleEvent("epoch", nil)
	if view.values[elementKey] != before {
		t.Fatal("expected non-ui_update events to be ignored")
	}
}

// TestNilCapabilitiesDegradeSafely proves this plugin never panics and
// reports a clear 0%/"-" reading when I2C/GPIO capabilities aren't wired
// yet (current production state until the bus-abstraction task lands).
func TestNilCapabilitiesDegradeSafely(t *testing.T) {
	view := newFakeView()
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{View: view, I2C: nil, GPIO: nil}); err != nil {
		t.Fatal(err)
	}
	p.HandleEvent("ui_update", nil)
	if got := view.values[elementKey]; got != " 0-" {
		t.Fatalf("expected a safe zero/discharging reading with no I2C/GPIO, got %q", got)
	}
}

func TestGPIOLineErrorDegradesSafely(t *testing.T) {
	bus := &fakeI2CBus{dev: newFakeDevice(nil)}
	gpio := &fakeGPIOBus{lineErr: errNoDevice}
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{View: newFakeView(), I2C: bus, GPIO: gpio}); err != nil {
		t.Fatal(err)
	}
	if got := p.charging(); got != "-" {
		t.Fatalf("charging() = %q, want '-' when Line() errors", got)
	}
}

func TestI2COpenErrorDegradesSafely(t *testing.T) {
	bus := &fakeI2CBus{openErr: errNoDevice}
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{View: newFakeView(), I2C: bus}); err != nil {
		t.Fatal(err)
	}
	if got := p.voltage(); got != 0.0 {
		t.Fatalf("voltage() = %v, want 0.0 when Open fails", got)
	}
	if got := p.capacity(); got != 0.0 {
		t.Fatalf("capacity() = %v, want 0.0 when Open fails", got)
	}
}
