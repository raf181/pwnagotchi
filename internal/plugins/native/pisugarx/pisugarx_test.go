package pisugarx

import (
	"sync"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// fakeI2CDevice is an in-memory 256-register file.
type fakeI2CDevice struct {
	mu     sync.Mutex
	regs   [256]byte
	closed bool
	writes []struct {
		reg uint8
		val byte
	}
}

func (d *fakeI2CDevice) ReadReg(reg uint8, n int) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = d.regs[int(reg)+i]
	}
	return out, nil
}

func (d *fakeI2CDevice) WriteReg(reg uint8, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, b := range data {
		d.regs[int(reg)+i] = b
		d.writes = append(d.writes, struct {
			reg uint8
			val byte
		}{reg + uint8(i), b})
	}
	return nil
}

func (d *fakeI2CDevice) Close() error { d.closed = true; return nil }

// fakeI2CBus opens a single fixed device at a configured address; any
// other address reports "not present" (ReadReg error), matching how
// checkDevice/connectAndInit distinguish which board is attached.
type fakeI2CBus struct {
	addr uint8
	dev  *fakeI2CDevice
}

func (b *fakeI2CBus) Open(bus int, addr uint8) (pluginmanager.I2CDevice, error) {
	if addr != b.addr {
		return &missingDevice{}, nil
	}
	return b.dev, nil
}

// missingDevice answers every ReadReg with an error, exactly like a real
// smbus transaction to an address with nothing listening.
type missingDevice struct{}

func (missingDevice) ReadReg(reg uint8, n int) ([]byte, error) { return nil, errNoResponse }
func (missingDevice) WriteReg(reg uint8, data []byte) error    { return errNoResponse }
func (missingDevice) Close() error                             { return nil }

type sentinel string

func (e sentinel) Error() string { return string(e) }

const errNoResponse sentinel = "no device at this address"

type fakeView struct {
	mu     sync.Mutex
	values map[string]string
	added  bool
}

func newFakeView() *fakeView { return &fakeView{values: map[string]string{}} }
func (f *fakeView) Set(key, value string) {
	f.mu.Lock()
	f.values[key] = value
	f.mu.Unlock()
}
func (f *fakeView) Update(bool)  {}
func (f *fakeView) Kind() string { return "dummydisplay" }
func (f *fakeView) HasElement(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.values[key]
	return ok
}
func (f *fakeView) RemoveElement(key string) {
	f.mu.Lock()
	delete(f.values, key)
	f.mu.Unlock()
}
func (f *fakeView) AddText(key, value string, x, y int, font pluginmanager.FontStyle, wrap bool, maxLength int) {
	f.mu.Lock()
	f.values[key] = value
	f.mu.Unlock()
}
func (f *fakeView) AddLabeledValue(key, label, value string, x, y int, labelFont, valueFont pluginmanager.FontStyle, labelSpacing int) {
	f.mu.Lock()
	f.values[key] = value
	f.added = true
	f.mu.Unlock()
}
func (f *fakeView) OnUploading(to string) {}
func (f *fakeView) OnNormal()             {}
func (f *fakeView) Width() int            { return 250 }
func (f *fakeView) Height() int           { return 122 }
func (f *fakeView) get(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.values[key]
}

type fakeSystem struct {
	mu            sync.Mutex
	shutdownCalls int
}

func (f *fakeSystem) Shutdown() error {
	f.mu.Lock()
	f.shutdownCalls++
	f.mu.Unlock()
	return nil
}
func (f *fakeSystem) Reboot(string) error  { return nil }
func (f *fakeSystem) Restart(string) error { return nil }
func (f *fakeSystem) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shutdownCalls
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

var (
	_ pluginmanager.ViewCapability   = (*fakeView)(nil)
	_ pluginmanager.I2CCapability    = (*fakeI2CBus)(nil)
	_ pluginmanager.I2CDevice        = (*fakeI2CDevice)(nil)
	_ pluginmanager.SystemCapability = (*fakeSystem)(nil)
	_ pluginmanager.Clock            = (*fakeClock)(nil)
)

func loadTestPlugin(t *testing.T, i2c pluginmanager.I2CCapability, cfg config.Map, view pluginmanager.ViewCapability, sys pluginmanager.SystemCapability) *Plugin {
	t.Helper()
	p := New()
	if cfg == nil {
		cfg = config.Map{}
	}
	if err := p.OnLoad(pluginmanager.Capabilities{Config: cfg, View: view, I2C: i2c, System: sys, Clock: &fakeClock{now: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.OnUnload() })
	return p
}

// waitReady polls p.ready (set by the background connect goroutine)
// without needing a fixed sleep.
func waitReady(t *testing.T, p *Plugin) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		ready := p.ready
		p.mu.Unlock()
		if ready {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("plugin never became ready")
}

func TestOnLoadAddsBatElement(t *testing.T) {
	view := newFakeView()
	dev := &fakeI2CDevice{}
	bus := &fakeI2CBus{addr: addrPiSugar3, dev: dev}
	p := loadTestPlugin(t, bus, nil, view, nil)
	if !view.added {
		t.Fatal("expected 'bat' LabeledValue element to be added")
	}
	waitReady(t, p)
	p.mu.Lock()
	model := p.model
	p.mu.Unlock()
	if model != ModelPiSugar3 {
		t.Fatalf("expected PiSugar3 detected, got %v", model)
	}
}

func TestDetectsPiSugar2VsPiSugar2Plus(t *testing.T) {
	cases := []struct {
		name string
		c2   byte
		want Model
	}{
		{"plain PiSugar2", 0x00, ModelPiSugar2},
		{"PiSugar2Plus", 0x01, ModelPiSugar2Plus},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dev := &fakeI2CDevice{}
			dev.regs[0xC2] = c.c2
			bus := &fakeI2CBus{addr: addrPiSugar2, dev: dev}
			p := loadTestPlugin(t, bus, nil, newFakeView(), nil)
			waitReady(t, p)
			p.mu.Lock()
			got := p.model
			p.mu.Unlock()
			if got != c.want {
				t.Fatalf("model = %v, want %v", got, c.want)
			}
		})
	}
}

func TestNoDeviceNeverBecomesReady(t *testing.T) {
	bus := &fakeI2CBus{addr: 0x99, dev: &fakeI2CDevice{}} // nothing answers at either real address
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{Config: config.Map{}, View: newFakeView(), I2C: bus, Clock: &fakeClock{now: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	defer p.OnUnload()
	time.Sleep(50 * time.Millisecond)
	p.mu.Lock()
	ready := p.ready
	p.mu.Unlock()
	if ready {
		t.Fatal("expected plugin to stay not-ready with no device present")
	}
}

func TestNilI2CCapabilityDoesNotPanic(t *testing.T) {
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{Config: config.Map{}, View: newFakeView(), I2C: nil}); err != nil {
		t.Fatal(err)
	}
	defer p.OnUnload()
	p.HandleEvent("ui_update", nil)
}

func TestPiSugar3VoltageTemperatureAndChargingFlags(t *testing.T) {
	dev := &fakeI2CDevice{}
	// voltage = ((high<<8)+low)/1000 = 4000mV -> 4.0V: high=0x0F,low=0xA0 (4000=0x0FA0)
	dev.regs[0x22] = 0x0F
	dev.regs[0x23] = 0xA0
	dev.regs[0x04] = 65         // temp = 65-40 = 25
	dev.regs[0x02] = 0b11000000 // plugged(bit7) + allow_charging(bit6)
	bus := &fakeI2CBus{addr: addrPiSugar3, dev: dev}
	p := loadTestPlugin(t, bus, nil, newFakeView(), nil)
	waitReady(t, p)

	p.pollOnce()
	p.mu.Lock()
	v, temp, plugged, allow := p.batteryVoltage, p.temperature, p.powerPlugged, p.allowCharging
	p.mu.Unlock()
	if v != 4.0 {
		t.Fatalf("voltage = %v, want 4.0", v)
	}
	if temp != 25 {
		t.Fatalf("temperature = %v, want 25", temp)
	}
	if !plugged || !allow {
		t.Fatalf("expected plugged=true allow=true, got plugged=%v allow=%v", plugged, allow)
	}
}

func TestPiSugar2VoltageHighBranch(t *testing.T) {
	dev := &fakeI2CDevice{}
	// high & 0x20 set: voltage = (2600 - (((high|0xC0)<<8)+low)*0.26855)/1000
	dev.regs[0xa3] = 0x20 // high, bit 0x20 set, rest zero
	dev.regs[0xa2] = 0x00 // low
	bus := &fakeI2CBus{addr: addrPiSugar2, dev: dev}
	p := loadTestPlugin(t, bus, nil, newFakeView(), nil)
	waitReady(t, p)
	p.pollOnce()

	// (0x20|0xC0)=0xE0=224; (224<<8)=57344; *0.26855=15400.192; (2600-15400.192)/1000 = -12.800192
	want := (2600.0 - float64(224<<8)*0.26855) / 1000.0
	p.mu.Lock()
	got := p.batteryVoltage
	p.mu.Unlock()
	if diff := got - want; diff > 0.001 || diff < -0.001 {
		t.Fatalf("voltage = %v, want %v", got, want)
	}
}

func TestPiSugar2PlusPowerPlugged(t *testing.T) {
	dev := &fakeI2CDevice{}
	dev.regs[0xC2] = 0x01 // -> PiSugar2Plus
	dev.regs[0xdd] = 0x1f // plugged
	bus := &fakeI2CBus{addr: addrPiSugar2, dev: dev}
	p := loadTestPlugin(t, bus, nil, newFakeView(), nil)
	waitReady(t, p)
	p.pollOnce()
	p.mu.Lock()
	plugged := p.powerPlugged
	p.mu.Unlock()
	if !plugged {
		t.Fatal("expected power_plugged true when reg 0xdd == 0x1f")
	}
}

func TestConvertVoltageToLevelInterpolatesOnCurve5312(t *testing.T) {
	// Between (3.70, 65.0) and (3.80, 77.0): 3.75 -> halfway -> 71.0
	got := convertVoltageToLevel([]float64{3.75}, curve5312)
	if got < 70.9 || got > 71.1 {
		t.Fatalf("level = %v, want ~71.0", got)
	}
}

func TestConvertVoltageToLevelTrimsOutliersAtFivePlusSamples(t *testing.T) {
	// 5 samples: one extreme low, one extreme high, three at 4.10 (100%).
	// Trimmed mean drops the 2 highest and 2 lowest -> average of the
	// single middle sample only when exactly 5 given how sort+slice[2:-2]
	// behaves (5 samples -> 1 remaining).
	history := []float64{2.0, 4.10, 4.10, 4.10, 5.0}
	got := convertVoltageToLevel(history, curve5312)
	if got != 100.0 {
		t.Fatalf("level = %v, want 100.0 (trimmed mean should isolate the 4.10 cluster)", got)
	}
}

func TestLowPowerShutdownTriggersRealSystemShutdown(t *testing.T) {
	dev := &fakeI2CDevice{}
	dev.regs[0x22], dev.regs[0x23] = 0x0C, 0x1C // voltage=3.100V -> curve5312 gives 0%
	bus := &fakeI2CBus{addr: addrPiSugar3, dev: dev}
	sys := &fakeSystem{}
	p := loadTestPlugin(t, bus, config.Map{"lowpower_shutdown": true, "lowpower_shutdown_level": int64(10)}, newFakeView(), sys)
	waitReady(t, p)
	p.pollOnce()
	p.checkLowPowerShutdown()

	if sys.calls() != 1 {
		t.Fatalf("expected exactly 1 real Shutdown call, got %d", sys.calls())
	}
}

func TestLowPowerShutdownDisabledNeverCallsShutdown(t *testing.T) {
	dev := &fakeI2CDevice{}
	dev.regs[0x22], dev.regs[0x23] = 0x0C, 0x1C // low voltage
	bus := &fakeI2CBus{addr: addrPiSugar3, dev: dev}
	sys := &fakeSystem{}
	p := loadTestPlugin(t, bus, config.Map{"lowpower_shutdown": false}, newFakeView(), sys)
	waitReady(t, p)
	p.pollOnce()
	p.checkLowPowerShutdown()

	if sys.calls() != 0 {
		t.Fatal("expected no Shutdown call when lowpower_shutdown is disabled")
	}
}

func TestHighBatteryNeverTriggersShutdown(t *testing.T) {
	dev := &fakeI2CDevice{}
	dev.regs[0x22], dev.regs[0x23] = 0x0F, 0xA0 // 4.0V -> well above any shutdown threshold
	bus := &fakeI2CBus{addr: addrPiSugar3, dev: dev}
	sys := &fakeSystem{}
	p := loadTestPlugin(t, bus, config.Map{"lowpower_shutdown": true, "lowpower_shutdown_level": int64(10)}, newFakeView(), sys)
	waitReady(t, p)
	p.pollOnce()
	p.checkLowPowerShutdown()

	if sys.calls() != 0 {
		t.Fatal("expected no Shutdown call at high battery level")
	}
}

func TestInvalidDefaultDisplayFallsBackToVoltage(t *testing.T) {
	p := loadTestPlugin(t, &fakeI2CBus{addr: 0x99}, config.Map{"default_display": "bogus", "rotation": false}, newFakeView(), nil)
	p.mu.Lock()
	got := p.defaultDisplay
	p.mu.Unlock()
	if got != "voltage" {
		t.Fatalf("defaultDisplay = %q, want voltage fallback", got)
	}
}

func TestUpdateUIRespectsDefaultDisplayWhenRotationDisabled(t *testing.T) {
	view := newFakeView()
	dev := &fakeI2CDevice{}
	dev.regs[0x22], dev.regs[0x23] = 0x0F, 0xA0 // 4.0V
	bus := &fakeI2CBus{addr: addrPiSugar3, dev: dev}
	p := loadTestPlugin(t, bus, config.Map{"rotation": false, "default_display": "voltage"}, view, nil)
	waitReady(t, p)
	p.pollOnce()
	p.HandleEvent("ui_update", nil)
	if got := view.get("bat"); got != "4.00V" {
		t.Fatalf("bat value = %q, want 4.00V", got)
	}
}

func TestUpdateUIShowsChargeIndicatorWhenPlugged(t *testing.T) {
	view := newFakeView()
	dev := &fakeI2CDevice{}
	dev.regs[0x22], dev.regs[0x23] = 0x0F, 0xA0
	dev.regs[0x02] = 0b10000000 // plugged
	bus := &fakeI2CBus{addr: addrPiSugar3, dev: dev}
	p := loadTestPlugin(t, bus, config.Map{"rotation": false, "default_display": "voltage"}, view, nil)
	waitReady(t, p)
	p.pollOnce()
	p.HandleEvent("ui_update", nil)
	if got := view.get("bat"); got != "CHG 4.00V" {
		t.Fatalf("bat value = %q, want a CHG-prefixed value", got)
	}
}

func TestUpdateUIRotatesThroughModesOverTime(t *testing.T) {
	view := newFakeView()
	dev := &fakeI2CDevice{}
	dev.regs[0x22], dev.regs[0x23] = 0x0F, 0xA0
	bus := &fakeI2CBus{addr: addrPiSugar3, dev: dev}
	fc := &fakeClock{now: time.Now()}
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{Config: config.Map{"rotation": true}, View: view, I2C: bus, Clock: fc}); err != nil {
		t.Fatal(err)
	}
	defer p.OnUnload()
	waitReady(t, p)
	p.pollOnce()

	p.HandleEvent("ui_update", nil)
	first := view.get("bat")
	fc.advance(6 * time.Second)
	p.HandleEvent("ui_update", nil)
	second := view.get("bat")
	if first == second {
		t.Fatalf("expected rotation to change the displayed mode after 6s, got %q both times", first)
	}
}

func TestOnUnloadClosesDeviceAndRemovesElement(t *testing.T) {
	view := newFakeView()
	dev := &fakeI2CDevice{}
	bus := &fakeI2CBus{addr: addrPiSugar3, dev: dev}
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{Config: config.Map{}, View: view, I2C: bus, Clock: &fakeClock{now: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	waitReady(t, p)
	if err := p.OnUnload(); err != nil {
		t.Fatal(err)
	}
	if !dev.closed {
		t.Fatal("expected I2C device closed on unload")
	}
	if view.HasElement("bat") {
		t.Fatal("expected 'bat' element removed on unload")
	}
}

func TestOnWebhookNotReadyReturnsPlaceholderPage(t *testing.T) {
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{Config: config.Map{}, View: newFakeView(), I2C: nil}); err != nil {
		t.Fatal(err)
	}
	defer p.OnUnload()
	resp, err := p.OnWebhook("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(resp.Body), "not ready") {
		t.Fatalf("expected not-ready page, got: %s", resp.Body)
	}
}

func TestOnWebhookReadyRendersRealValues(t *testing.T) {
	dev := &fakeI2CDevice{}
	dev.regs[0x22], dev.regs[0x23] = 0x0F, 0xA0
	copy(dev.regs[0xe2:0xee], []byte("v1.2.3\x00\x00\x00\x00\x00\x00"))
	bus := &fakeI2CBus{addr: addrPiSugar3, dev: dev}
	p := loadTestPlugin(t, bus, nil, newFakeView(), nil)
	waitReady(t, p)
	p.pollOnce()

	resp, err := p.OnWebhook("", nil)
	if err != nil {
		t.Fatal(err)
	}
	body := string(resp.Body)
	if !contains(body, "PiSugar3") || !contains(body, "4.00V") || !contains(body, "v1.2.3") {
		t.Fatalf("expected model/voltage/version in webhook body, got: %s", body)
	}
	if !contains(body, "Not set") || !contains(body, "N/A") {
		t.Fatalf("expected always-stub fields rendered with their real Python default text, got: %s", body)
	}
}

func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
