package wittypi

import (
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// fakeI2CDevice is an in-memory register file: byte i is register i.
type fakeI2CDevice struct {
	regs   map[uint8]byte
	closed bool
}

func newFakeDevice(regs map[uint8]byte) *fakeI2CDevice {
	return &fakeI2CDevice{regs: regs}
}

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

// fakeView records Set/AddLabeledValue/RemoveElement calls.
type fakeView struct {
	values         map[string]string
	added          bool
	removed        bool
	addedLabel     string
	addedX, addedY int
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
	f.addedX, f.addedY = x, y
}

func (f *fakeView) OnUploading(to string) {}
func (f *fakeView) OnNormal()             {}
func (f *fakeView) Width() int            { return 250 }
func (f *fakeView) Height() int           { return 122 }

var _ pluginmanager.ViewCapability = (*fakeView)(nil)
var _ pluginmanager.I2CDevice = (*fakeI2CDevice)(nil)
var _ pluginmanager.I2CCapability = (*fakeI2CBus)(nil)

func TestOnLoadAddsUpsElement(t *testing.T) {
	view := newFakeView()
	bus := &fakeI2CBus{dev: newFakeDevice(nil)}
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{View: view, I2C: bus}); err != nil {
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

// TestCapacityAndChargingFromRegisters ports UPS.voltage/capacity/charging:
// register 1 = integer volts, register 2 = hundredths, register 7 = power
// mode (0 = charging).
func TestCapacityAndChargingFromRegisters(t *testing.T) {
	cases := []struct {
		name           string
		volInt, volDec byte
		powerMode      byte
		wantCapacity   int
		wantCharging   string
	}{
		{"full+charging", 4, 20, 0, 100, "+"},
		{"empty+discharging", 3, 10, 1, 0, "-"},
		{"mid+discharging", 3, 65, 5, 50, "-"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			regs := map[uint8]byte{regVoltageI: c.volInt, regVoltageD: c.volDec, regPowerMode: c.powerMode}
			bus := &fakeI2CBus{dev: newFakeDevice(regs)}
			view := newFakeView()
			p := New()
			if err := p.OnLoad(pluginmanager.Capabilities{View: view, I2C: bus}); err != nil {
				t.Fatal(err)
			}
			if got := p.capacity(); got != c.wantCapacity {
				t.Errorf("capacity() = %d, want %d", got, c.wantCapacity)
			}
			if got := p.charging(); got != c.wantCharging {
				t.Errorf("charging() = %q, want %q", got, c.wantCharging)
			}
		})
	}
}

func TestVoltageClampedBeforeCapacityCalculation(t *testing.T) {
	// 5.0V is above the 4.2V clamp ceiling -> must read as 100%, not >100.
	regs := map[uint8]byte{regVoltageI: 5, regVoltageD: 0}
	bus := &fakeI2CBus{dev: newFakeDevice(regs)}
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{View: newFakeView(), I2C: bus}); err != nil {
		t.Fatal(err)
	}
	if got := p.capacity(); got != 100 {
		t.Fatalf("capacity() = %d, want 100 (clamped)", got)
	}
}

func TestHandleEventUiUpdateSetsFormattedValue(t *testing.T) {
	regs := map[uint8]byte{regVoltageI: 4, regVoltageD: 20, regPowerMode: 0}
	bus := &fakeI2CBus{dev: newFakeDevice(regs)}
	view := newFakeView()
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{View: view, I2C: bus}); err != nil {
		t.Fatal(err)
	}
	p.HandleEvent("ui_update", nil)
	if got := view.values[elementKey]; got != "100+" {
		t.Fatalf("ups value = %q, want %q", got, "100+")
	}
}

func TestHandleEventIgnoresOtherEvents(t *testing.T) {
	view := newFakeView()
	bus := &fakeI2CBus{dev: newFakeDevice(map[uint8]byte{regVoltageI: 4})}
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

// TestNilI2CDegradesSafely proves this plugin never panics and reports a
// clear 0%/"-" reading when the I2C capability isn't wired yet (current
// production state until the bus-abstraction task lands).
func TestNilI2CDegradesSafely(t *testing.T) {
	view := newFakeView()
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{View: view, I2C: nil}); err != nil {
		t.Fatal(err)
	}
	p.HandleEvent("ui_update", nil)
	if got := view.values[elementKey]; got != " 0-" {
		t.Fatalf("expected a safe zero/discharging reading with no I2C, got %q", got)
	}
}

func TestOpenErrorDegradesSafely(t *testing.T) {
	view := newFakeView()
	bus := &fakeI2CBus{openErr: errNoDevice}
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{View: view, I2C: bus}); err != nil {
		t.Fatal(err)
	}
	p.HandleEvent("ui_update", nil)
	if got := view.values[elementKey]; got != " 0-" {
		t.Fatalf("expected a safe zero/discharging reading when Open fails, got %q", got)
	}
}
