// Package hw ports pwnagotchi/ui/hw/*.py: the display driver interface
// (base.py's DisplayImpl), the type-string dispatch table (__init__.py's
// display_for), and DummyDisplay (the one driver with no real hardware to
// talk to, hence fully implemented like Python's). The ~90 real e-paper/
// LCD/OLED drivers are registered (so config resolution and error messages
// work end-to-end) but return ErrUnsupportedDisplay from Initialize/Render/
// Clear until a real SPI/I2C implementation lands for each — verified only
// possible on real hardware, per docs/feature-matrix.md's "Interface-only"
// rule. No driver here fakes success.
package hw

import (
	"errors"
	"fmt"
	"image"

	"golang.org/x/image/font"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
	"github.com/jayofelony/pwnagotchi/go-port/internal/ui/fonts"
)

// Point mirrors a Python (x, y) layout tuple.
type Point struct{ X, Y int }

// StatusLayout mirrors base.py's _layout['status'] dict.
type StatusLayout struct {
	Pos  Point
	Font font.Face
	Max  int
}

// Layout ports DisplayImpl._layout.
type Layout struct {
	Width, Height                        int
	Face, Name, Channel, APs, Uptime     Point
	FriendFace, FriendName, Shakes, Mode Point
	Line1, Line2                         [4]int
	Status                               StatusLayout

	// FontsSetup is (bold, boldSmall, medium, huge, boldBig, small) — the
	// exact args this specific driver's real layout() method passes to
	// fonts.setup(...), so callers building a font.Set for non-Status
	// widgets (Face/Name/Channel/APs/Uptime/Shakes/Mode) use the real
	// per-driver sizes too, not just Status (which already got this right
	// via StatusFont below — everything else didn't, until this field
	// existed to carry it out of Layout()).
	FontsSetup [6]int
}

// Driver ports hw.base.DisplayImpl (the ABI every concrete display driver
// implements: layout/initialize/render/clear).
type Driver interface {
	// Name is the driver's Python class name (e.g. "DummyDisplay"),
	// mirroring DisplayImpl.name.
	Name() string
	Layout() (*Layout, error)
	Initialize() error
	Render(canvas *image.Gray) error
	Clear() error
}

// ErrUnsupportedDisplay is returned by every not-yet-natively-implemented
// driver's Initialize/Render/Clear — an explicit, typed "unsupported on
// this build" error rather than a silent no-op or fake success.
var ErrUnsupportedDisplay = errors.New("hw: this display driver has a real Go interface but no native hardware I/O implementation yet (needs real hardware to verify against); see docs/feature-matrix.md")

// unsupportedDriver is the concrete type every not-yet-natively-implemented
// display type resolves to. Its Layout() is REAL (extracted from the
// actual Python driver's layout() method — pure widget-position/font-size
// data with no hardware I/O, see layouts_gen.go), matching Python's own
// behavior where View construction succeeds and sizes itself correctly
// for ANY configured display type regardless of whether real hardware is
// attached. Only Initialize/Render/Clear (the actual SPI/I2C/GPIO
// operations) are unsupported, since those need real hardware to verify.
type unsupportedDriver struct {
	name string
	cfg  config.Map // the FULL config, not just config['ui']['display']
}

// Name returns the config TYPE STRING (e.g. "waveshare_4"), matching what
// every real driver's own super().__init__(config, '<type>') passes as
// self.name in Python — verified by reading every hw/*.py driver
// constructor. This is what Display.is_waveshare_v4() and friends compare
// against, NOT the Python class name.
func (d *unsupportedDriver) Name() string { return d.name }

func (d *unsupportedDriver) diagName() string {
	if className, ok := pythonClassNames[d.name]; ok {
		return fmt.Sprintf("%s (Python class %s)", d.name, className)
	}
	return d.name
}

// Layout ports the real Python driver's layout() method: pure data (canvas
// size, widget positions, font sizes), extracted from
// testdata/hw_layouts.json (itself parsed out of every hw/*.py file's
// layout() via Python's ast module — see scripts/gen_hw_layouts.py).
func (d *unsupportedDriver) Layout() (*Layout, error) {
	data, ok := layoutTable[d.name]
	if !ok {
		return nil, fmt.Errorf("hw: %s: no layout data generated (regenerate layouts_gen.go)", d.name)
	}

	width, height := data.Width, data.Height
	if d.name == "i2coled" {
		// i2coled.py's layout() reads width/height from config, defaulting
		// to 128x64 — the ONE driver whose canvas size isn't a fixed
		// literal (verified exhaustively across all 94 drivers).
		dCfg := displayConfig(d.cfg)
		width = intConfigOr(dCfg, "width", 128)
		height = intConfigOr(dCfg, "height", 64)
	}

	fs := data.FontsSetup
	fontSet, err := fonts.NewSized(fs[0], fs[1], fs[2], fs[3], fs[4], fs[5])
	if err != nil {
		return nil, err
	}

	baseSize := float64(fs[2]) // Medium
	if data.StatusFontBase == "Small" {
		baseSize = float64(fs[5])
	}
	sizeOffset := intConfigOr(fontConfig(d.cfg), "size_offset", 0)
	statusFont, err := fontSet.StatusFont(baseSize + float64(sizeOffset))
	if err != nil {
		return nil, err
	}

	return &Layout{
		Width: width, Height: height,
		Face: ptFrom(data.Face), Name: ptFrom(data.Name), Channel: ptFrom(data.Channel),
		APs: ptFrom(data.APs), Uptime: ptFrom(data.Uptime),
		FriendFace: ptFrom(data.FriendFace), FriendName: ptFrom(data.FriendName),
		Shakes: ptFrom(data.Shakes), Mode: ptFrom(data.Mode),
		Line1: data.Line1, Line2: data.Line2,
		Status:     StatusLayout{Pos: ptFrom(data.StatusPos), Font: statusFont, Max: data.StatusMax},
		FontsSetup: fs,
	}, nil
}

func ptFrom(a [2]int) Point { return Point{X: a[0], Y: a[1]} }

func fontConfig(cfg config.Map) config.Map {
	ui, _ := cfg["ui"].(config.Map)
	if ui == nil {
		return nil
	}
	f, _ := ui["font"].(config.Map)
	return f
}

func (d *unsupportedDriver) Initialize() error {
	return fmt.Errorf("%s: %w", d.diagName(), ErrUnsupportedDisplay)
}
func (d *unsupportedDriver) Render(*image.Gray) error {
	return fmt.Errorf("%s: %w", d.diagName(), ErrUnsupportedDisplay)
}
func (d *unsupportedDriver) Clear() error {
	return fmt.Errorf("%s: %w", d.diagName(), ErrUnsupportedDisplay)
}

// displayConfig extracts config['ui']['display'], matching every driver
// constructor's `self.config = config['ui']['display']`.
func displayConfig(cfg config.Map) config.Map {
	ui, _ := cfg["ui"].(config.Map)
	if ui == nil {
		return config.Map{}
	}
	d, _ := ui["display"].(config.Map)
	if d == nil {
		return config.Map{}
	}
	return d
}
