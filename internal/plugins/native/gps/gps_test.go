package gps

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

type recordedRun struct{ cmd string }

type fakeAgent struct {
	mu        sync.Mutex
	runs      []recordedRun
	runErr    error
	sessionFn func() (interface{}, error)
}

func (f *fakeAgent) Run(cmd string, verbose bool) (interface{}, error) {
	f.mu.Lock()
	f.runs = append(f.runs, recordedRun{cmd})
	f.mu.Unlock()
	return nil, f.runErr
}
func (f *fakeAgent) Session(string) (interface{}, error) {
	if f.sessionFn != nil {
		return f.sessionFn()
	}
	return nil, errors.New("no session")
}
func (f *fakeAgent) IsModuleRunning(string) bool { return false }
func (f *fakeAgent) StartModule(string)          {}
func (f *fakeAgent) RestartModule(string)        {}
func (f *fakeAgent) SupportedChannels() []int    { return nil }
func (f *fakeAgent) ResetHistory()               {}

func (f *fakeAgent) snapshot() []recordedRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRun(nil), f.runs...)
}

type fakeView struct {
	mu      sync.Mutex
	kind    string
	values  map[string]string
	added   map[string]bool
	removed []string
}

func newFakeView(kind string) *fakeView {
	return &fakeView{kind: kind, values: map[string]string{}, added: map[string]bool{}}
}
func (f *fakeView) Set(key, value string) { f.mu.Lock(); f.values[key] = value; f.mu.Unlock() }
func (f *fakeView) Update(bool)           {}
func (f *fakeView) Kind() string          { return f.kind }
func (f *fakeView) HasElement(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.added[key]
}
func (f *fakeView) RemoveElement(key string) {
	f.mu.Lock()
	delete(f.added, key)
	f.removed = append(f.removed, key)
	f.mu.Unlock()
}
func (f *fakeView) AddText(key, value string, x, y int, font pluginmanager.FontStyle, wrap bool, maxLength int) {
	f.mu.Lock()
	f.added[key] = true
	f.values[key] = value
	f.mu.Unlock()
}
func (f *fakeView) AddLabeledValue(key, label, value string, x, y int, labelFont, valueFont pluginmanager.FontStyle, labelSpacing int) {
	f.mu.Lock()
	f.added[key] = true
	f.values[key] = value
	f.mu.Unlock()
}

func (f *fakeView) OnUploading(to string) {}
func (f *fakeView) OnNormal()             {}
func (f *fakeView) Width() int            { return 250 }
func (f *fakeView) Height() int           { return 122 }

var _ pluginmanager.ViewCapability = (*fakeView)(nil)
var _ pluginmanager.AgentCapability = (*fakeAgent)(nil)

func TestOnLoadAddsThreeElements(t *testing.T) {
	view := newFakeView("dummydisplay")
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{Config: config.Map{"device": "/dev/ttyUSB0"}, View: view}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"latitude", "longitude", "altitude"} {
		if !view.HasElement(k) {
			t.Fatalf("expected element %q to be added", k)
		}
	}
}

func TestOnUnloadRemovesElements(t *testing.T) {
	view := newFakeView("dummydisplay")
	p := New()
	_ = p.OnLoad(pluginmanager.Capabilities{Config: config.Map{"device": "/dev/ttyUSB0"}, View: view})
	if err := p.OnUnload(); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"latitude", "longitude", "altitude"} {
		if view.HasElement(k) {
			t.Fatalf("expected element %q to be removed", k)
		}
	}
}

func TestOnReadyEnablesBettercapGPSWhenDeviceExists(t *testing.T) {
	orig := deviceExists
	deviceExists = func(string) bool { return true }
	defer func() { deviceExists = orig }()

	agent := &fakeAgent{}
	p := New()
	_ = p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"device": "/dev/ttyUSB0", "speed": "9600"},
		Agent:  agent,
	})
	p.HandleEvent("ready", nil)

	runs := agent.snapshot()
	want := []string{"gps off", "set gps.device /dev/ttyUSB0", "set gps.baudrate 9600", "gps on"}
	if len(runs) != len(want) {
		t.Fatalf("expected %d Run calls, got %d: %+v", len(want), len(runs), runs)
	}
	for i, w := range want {
		if runs[i].cmd != w {
			t.Fatalf("run[%d] = %q, want %q", i, runs[i].cmd, w)
		}
	}
	p.mu.Lock()
	running := p.running
	p.mu.Unlock()
	if !running {
		t.Fatal("expected plugin to be marked running")
	}
}

func TestOnReadyNoOpWhenDeviceMissing(t *testing.T) {
	orig := deviceExists
	deviceExists = func(string) bool { return false }
	defer func() { deviceExists = orig }()

	agent := &fakeAgent{}
	p := New()
	_ = p.OnLoad(pluginmanager.Capabilities{Config: config.Map{"device": "/dev/nonexistent"}, Agent: agent})
	p.HandleEvent("ready", nil)
	if len(agent.snapshot()) != 0 {
		t.Fatal("expected no bettercap commands when the device doesn't exist")
	}
}

func TestOnReadyAcceptsHostPortDevice(t *testing.T) {
	orig := deviceExists
	deviceExists = func(d string) bool { return len(d) > 0 && containsColon(d) }
	defer func() { deviceExists = orig }()

	agent := &fakeAgent{}
	p := New()
	_ = p.OnLoad(pluginmanager.Capabilities{Config: config.Map{"device": "127.0.0.1:2947", "speed": "9600"}, Agent: agent})
	p.HandleEvent("ready", nil)
	if len(agent.snapshot()) == 0 {
		t.Fatal("expected gpsd-style host:port device to be accepted")
	}
}

func containsColon(s string) bool {
	for _, c := range s {
		if c == ':' {
			return true
		}
	}
	return false
}

func TestOnHandshakeSavesValidCoordinatesAndUpdatesUI(t *testing.T) {
	orig := deviceExists
	deviceExists = func(string) bool { return true }
	defer func() { deviceExists = orig }()

	dir := t.TempDir()
	pcapPath := filepath.Join(dir, "net_aabbcc.pcap")
	os.WriteFile(pcapPath, []byte("data"), 0o644)

	agent := &fakeAgent{sessionFn: func() (interface{}, error) {
		return map[string]interface{}{
			"gps": map[string]interface{}{"Latitude": 40.7128, "Longitude": -74.0060, "Altitude": 10.5},
		}, nil
	}}
	view := newFakeView("dummydisplay")
	p := New()
	_ = p.OnLoad(pluginmanager.Capabilities{Config: config.Map{"device": "/dev/ttyUSB0"}, Agent: agent, View: view})
	p.HandleEvent("ready", nil)
	p.HandleEvent("handshake", []interface{}{nil, pcapPath, map[string]interface{}{}, map[string]interface{}{}})

	gpsFile := filepath.Join(dir, "net_aabbcc.gps.json")
	data, err := os.ReadFile(gpsFile)
	if err != nil {
		t.Fatalf("expected gps.json written: %v", err)
	}
	var got map[string]float64
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["Latitude"] != 40.7128 || got["Longitude"] != -74.0060 {
		t.Fatalf("unexpected saved coordinates: %v", got)
	}

	p.HandleEvent("ui_update", nil)
	if view.values["latitude"] != "40.7128 " {
		t.Fatalf("latitude = %q, want %q", view.values["latitude"], "40.7128 ")
	}
}

func TestOnHandshakeSkipsZeroCoordinates(t *testing.T) {
	orig := deviceExists
	deviceExists = func(string) bool { return true }
	defer func() { deviceExists = orig }()

	dir := t.TempDir()
	pcapPath := filepath.Join(dir, "net_zero.pcap")
	os.WriteFile(pcapPath, []byte("data"), 0o644)

	agent := &fakeAgent{sessionFn: func() (interface{}, error) {
		return map[string]interface{}{
			"gps": map[string]interface{}{"Latitude": 0.0, "Longitude": 0.0, "Altitude": 0.0},
		}, nil
	}}
	p := New()
	_ = p.OnLoad(pluginmanager.Capabilities{Config: config.Map{"device": "/dev/ttyUSB0"}, Agent: agent})
	p.HandleEvent("ready", nil)
	p.HandleEvent("handshake", []interface{}{nil, pcapPath, map[string]interface{}{}, map[string]interface{}{}})

	if _, err := os.Stat(filepath.Join(dir, "net_zero.gps.json")); !os.IsNotExist(err) {
		t.Fatal("expected no gps.json written for all-zero coordinates")
	}
}

func TestOnHandshakeNoOpWhenNotRunning(t *testing.T) {
	dir := t.TempDir()
	pcapPath := filepath.Join(dir, "net.pcap")
	os.WriteFile(pcapPath, []byte("data"), 0o644)

	agent := &fakeAgent{sessionFn: func() (interface{}, error) {
		t.Fatal("Session should not be called when not running")
		return nil, nil
	}}
	p := New()
	_ = p.OnLoad(pluginmanager.Capabilities{Config: config.Map{"device": "/dev/ttyUSB0"}, Agent: agent})
	// deliberately never fire "ready"
	p.HandleEvent("handshake", []interface{}{nil, pcapPath, map[string]interface{}{}, map[string]interface{}{}})
}

func TestPositionsVaryByDisplayKind(t *testing.T) {
	cases := map[string]pos{
		"waveshare_2":     {127, 74},
		"waveshare_1":     {130, 70},
		"inky":            {127, 60},
		"waveshare144lcd": {67, 73},
		"dfrobot_v2":      {127, 74},
		"waveshare2in7":   {6, 120},
		"unknown":         {127, 51},
	}
	for kind, want := range cases {
		view := newFakeView(kind)
		p := New()
		p.view = view
		lat, _, _ := p.positions(config.Map{})
		if lat != want {
			t.Errorf("kind %q: lat_pos = %+v, want %+v", kind, lat, want)
		}
	}
}

func TestExplicitPositionOverridesDefault(t *testing.T) {
	p := New()
	p.lineSpacing = 10
	lat, lon, alt := p.positions(config.Map{"position": "10, 20"})
	if lat != (pos{15, 20}) || lon != (pos{10, 30}) || alt != (pos{15, 40}) {
		t.Fatalf("unexpected positions: lat=%+v lon=%+v alt=%+v", lat, lon, alt)
	}
}
