package memtemp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// fakeView is a minimal, fully in-memory pluginmanager.ViewCapability —
// no real display/fonts involved — so these tests stay hermetic and fast
// while still exercising the exact capability contract memtemp uses.
type fakeView struct {
	kind    string
	values  map[string]string
	text    map[string]struct{ x, y int }
	labeled map[string]struct{ label, value string }
	removed []string
}

func newFakeView(kind string) *fakeView {
	return &fakeView{kind: kind, values: map[string]string{}, text: map[string]struct{ x, y int }{}, labeled: map[string]struct{ label, value string }{}}
}

func (f *fakeView) Set(key, value string) { f.values[key] = value }
func (f *fakeView) Update(bool)           {}
func (f *fakeView) Kind() string          { return f.kind }
func (f *fakeView) HasElement(key string) bool {
	_, ok := f.values[key]
	return ok
}
func (f *fakeView) RemoveElement(key string) {
	delete(f.values, key)
	f.removed = append(f.removed, key)
}
func (f *fakeView) AddText(key, value string, x, y int, font pluginmanager.FontStyle, wrap bool, maxLength int) {
	f.values[key] = value
	f.text[key] = struct{ x, y int }{x, y}
}
func (f *fakeView) AddLabeledValue(key, label, value string, x, y int, labelFont, valueFont pluginmanager.FontStyle, labelSpacing int) {
	f.values[key] = value
	f.labeled[key] = struct{ label, value string }{label, value}
}

func (f *fakeView) OnUploading(to string) {}
func (f *fakeView) OnNormal()             {}
func (f *fakeView) Width() int            { return 250 }
func (f *fakeView) Height() int           { return 122 }

var _ pluginmanager.ViewCapability = (*fakeView)(nil)

func writeFakeCPUFreq(t *testing.T, ghz string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "scaling_cur_freq")
	if err := os.WriteFile(path, []byte(ghz), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := cpuFreqPath
	cpuFreqPath = path
	t.Cleanup(func() { cpuFreqPath = orig })
}

func TestOnLoadHorizontalAddsHeaderAndDataElements(t *testing.T) {
	view := newFakeView("dummydisplay")
	p := New()
	err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"orientation": "horizontal", "fields": "mem,cpu,temp"},
		View:   view,
	})
	if err != nil {
		t.Fatalf("OnLoad: %v", err)
	}
	if !view.HasElement("memtemp_header") || !view.HasElement("memtemp_data") {
		t.Fatalf("expected header+data elements, got %v", view.values)
	}
}

func TestOnLoadVerticalAddsOneElementPerField(t *testing.T) {
	view := newFakeView("dummydisplay")
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"orientation": "vertical", "fields": "mem,temp"},
		View:   view,
	}); err != nil {
		t.Fatal(err)
	}
	if !view.HasElement("memtemp_mem") || !view.HasElement("memtemp_temp") {
		t.Fatalf("expected per-field elements, got %v", view.values)
	}
	if view.HasElement("memtemp_cpu") {
		t.Fatal("expected only the configured fields to be added")
	}
}

func TestInvalidFieldsFallBackToDefault(t *testing.T) {
	view := newFakeView("dummydisplay")
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"orientation": "vertical", "fields": "bogus,alsobogus"},
		View:   view,
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range defaultFields {
		if !view.HasElement(elementKey(want)) {
			t.Fatalf("expected default field %q to be present, got %v", want, view.values)
		}
	}
}

func TestOnUnloadRemovesExactlyItsOwnElements(t *testing.T) {
	view := newFakeView("dummydisplay")
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"orientation": "horizontal"},
		View:   view,
	}); err != nil {
		t.Fatal(err)
	}
	if err := p.OnUnload(); err != nil {
		t.Fatal(err)
	}
	if view.HasElement("memtemp_header") || view.HasElement("memtemp_data") {
		t.Fatalf("expected elements removed, got %v", view.values)
	}
}

func TestHandleEventUiUpdateRefreshesMemAndFreq(t *testing.T) {
	writeFakeCPUFreq(t, "1500000")
	view := newFakeView("dummydisplay")
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"orientation": "vertical", "fields": "mem,freq"},
		View:   view,
	}); err != nil {
		t.Fatal(err)
	}
	p.HandleEvent("ui_update", nil)
	if got := view.values["memtemp_freq"]; got != "1.5G" {
		t.Fatalf("memtemp_freq = %q, want 1.5G", got)
	}
	if got := view.values["memtemp_mem"]; got == "" || got == "-" {
		t.Fatalf("memtemp_mem = %q, want a real percentage (real /proc/meminfo read)", got)
	}
}

func TestHandleEventIgnoresOtherEvents(t *testing.T) {
	view := newFakeView("dummydisplay")
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{Config: config.Map{}, View: view}); err != nil {
		t.Fatal(err)
	}
	before := view.values["memtemp_data"]
	p.HandleEvent("epoch", []interface{}{map[string]interface{}{"epoch": 1}})
	if view.values["memtemp_data"] != before {
		t.Fatal("expected non-ui_update events to be ignored")
	}
}

func TestDefaultPositionsVaryByDisplayKind(t *testing.T) {
	cases := map[string]pos{
		"waveshare_2":       {175, 84},
		"waveshare_1":       {170, 80},
		"waveshare144lcd":   {53, 77},
		"inky":              {140, 68},
		"waveshare2in7":     {192, 138},
		"waveshare1in54_v2": {53, 77},
		"something-else":    {155, 76},
	}
	for kind, want := range cases {
		view := newFakeView(kind)
		h, _ := defaultPositions(view)
		if h != want {
			t.Errorf("kind %q: h_pos = %+v, want %+v", kind, h, want)
		}
	}
}

func TestExplicitPositionOverridesDefault(t *testing.T) {
	view := newFakeView("dummydisplay")
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"orientation": "horizontal", "position": "10, 20"},
		View:   view,
	}); err != nil {
		t.Fatal(err)
	}
	pos := view.text["memtemp_header"]
	if pos.x != 10 || pos.y != 20 {
		t.Fatalf("expected explicit position (10,20), got %+v", pos)
	}
}

func TestCPUTempScales(t *testing.T) {
	p := New()
	p.scale = "celsius"
	celsius := p.cpuTemp()
	if celsius == "-" {
		t.Skip("no real /sys/class/thermal/thermal_zone0/temp on this host")
	}
	p.scale = "fahrenheit"
	if got := p.cpuTemp(); got == "-" || got == celsius {
		t.Fatalf("expected a distinct fahrenheit reading, got %q vs celsius %q", got, celsius)
	}
}

func TestPadText(t *testing.T) {
	if got := padText("mem"); got != " mem" {
		t.Fatalf("padText(mem) = %q, want %q", got, " mem")
	}
	if got := padText("toolong5"); got != "toolong5" {
		t.Fatalf("padText should not truncate, got %q", got)
	}
}
