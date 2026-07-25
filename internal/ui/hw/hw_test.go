package hw

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/config"
)

func cfgWithDisplayType(displayType string) config.Map {
	return config.Map{"ui": config.Map{"display": config.Map{"type": displayType}}}
}

func TestNewDriverDummyDisplay(t *testing.T) {
	d, err := NewDriver(cfgWithDisplayType("dummydisplay"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Name() != "DummyDisplay" {
		t.Fatalf("Name() = %q", d.Name())
	}
	if err := d.Initialize(); err != nil {
		t.Fatalf("Initialize() = %v, want nil (real no-op)", err)
	}
	if err := d.Render(nil); err != nil {
		t.Fatalf("Render() = %v, want nil", err)
	}
	if err := d.Clear(); err != nil {
		t.Fatalf("Clear() = %v, want nil", err)
	}
	layout, err := d.Layout()
	if err != nil {
		t.Fatalf("Layout(): %v", err)
	}
	if layout.Width != 480 || layout.Height != 720 {
		t.Fatalf("Layout() defaults = %+v, want 480x720", layout)
	}
}

func TestNewDriverDummyDisplayCustomSize(t *testing.T) {
	cfg := config.Map{"ui": config.Map{"display": config.Map{"type": "dummydisplay", "width": int64(250), "height": int64(122)}}}
	d, err := NewDriver(cfg)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := d.Layout()
	if err != nil {
		t.Fatal(err)
	}
	if layout.Width != 250 || layout.Height != 122 {
		t.Fatalf("Layout() = %+v, want 250x122", layout)
	}
	if layout.Status.Font == nil {
		t.Fatal("expected a real status font face")
	}
	if layout.Status.Max <= 0 {
		t.Fatalf("Status.Max = %d, want > 0", layout.Status.Max)
	}
}

func TestNewDriverUnsupportedReturnsExplicitError(t *testing.T) {
	d, err := NewDriver(cfgWithDisplayType("waveshare_4"))
	if err != nil {
		t.Fatal(err)
	}
	// Name() must be the config TYPE STRING ("waveshare_4"), matching what
	// the real Python driver passes as self.name in
	// super().__init__(config, 'waveshare_4') — NOT the Python class name
	// (WaveshareV4). This is what Display.is_waveshare_v4() compares
	// against; verified by reading every hw/*.py driver's constructor.
	if d.Name() != "waveshare_4" {
		t.Fatalf("Name() = %q, want the config type string \"waveshare_4\"", d.Name())
	}
	if err := d.Initialize(); !errors.Is(err, ErrUnsupportedDisplay) {
		t.Fatalf("Initialize() = %v, want ErrUnsupportedDisplay", err)
	}
	// Layout() is REAL even for a hardware-unsupported driver: it's pure
	// data (widget positions/canvas size/font sizes) extracted from the
	// actual Python driver's layout() method, which has no hardware I/O in
	// Python either. Only Initialize/Render/Clear (actual SPI/I2C/GPIO
	// operations) are unsupported.
	layout, err := d.Layout()
	if err != nil {
		t.Fatalf("Layout() = %v, want a real layout (waveshare_4's layout() has no hardware I/O)", err)
	}
	if layout.Width != 250 || layout.Height != 122 {
		t.Fatalf("Layout() = %dx%d, want 250x122 (waveshare_4's real Python layout() dimensions)", layout.Width, layout.Height)
	}
	if err := d.Render(nil); !errors.Is(err, ErrUnsupportedDisplay) {
		t.Fatalf("Render() = %v, want ErrUnsupportedDisplay", err)
	}
	if err := d.Clear(); !errors.Is(err, ErrUnsupportedDisplay) {
		t.Fatalf("Clear() = %v, want ErrUnsupportedDisplay", err)
	}
}

func TestNewDriverWeact2in9MatchesRealPythonBug(t *testing.T) {
	_, err := NewDriver(cfgWithDisplayType("weact2in9"))
	if !errors.Is(err, ErrNoDriverInPythonEither) {
		t.Fatalf("expected ErrNoDriverInPythonEither, got %v", err)
	}
}

func TestNewDriverUnrecognizedTypeErrors(t *testing.T) {
	_, err := NewDriver(cfgWithDisplayType("not_a_real_type"))
	if err == nil {
		t.Fatal("expected an error for a type string that isn't in the registry at all")
	}
}

// TestEveryNormalizedDisplayTypeResolves cross-checks
// testdata/display_type_alias_table.json's normalized_type outputs against
// the driver registry: every possible NormalizeDisplayType() result must
// resolve to SOME outcome (dummydisplay, an unsupportedDriver, or the known
// weact2in9 upstream-bug case) — nothing should hit the "unrecognized type"
// fallback, since that would mean config normalization can produce a type
// string the driver layer has never heard of.
func TestEveryNormalizedDisplayTypeResolves(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "display_type_alias_table.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		NormalizedType string `json:"normalized_type"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("empty alias table fixture")
	}

	for _, e := range entries {
		_, err := NewDriver(cfgWithDisplayType(e.NormalizedType))
		if err != nil && !errors.Is(err, ErrUnsupportedDisplay) && !errors.Is(err, ErrNoDriverInPythonEither) {
			t.Errorf("NormalizeDisplayType output %q: NewDriver returned an unexpected error: %v", e.NormalizedType, err)
		}
	}
}

// TestEveryDriverHasARealLayout exhaustively checks that every registered
// display type (except "weact2in9", which has no Python driver at all —
// see ErrNoDriverInPythonEither) resolves to a Driver whose Layout()
// succeeds with a positive width/height, matching Python's real behavior:
// View construction (which unconditionally calls impl.layout()) succeeds
// for every real display type regardless of whether hardware is attached
// or config['ui']['display']['enabled'] is true.
func TestEveryDriverHasARealLayout(t *testing.T) {
	for _, typ := range RegisteredTypes() {
		if typ == "weact2in9" {
			continue
		}
		d, err := NewDriver(cfgWithDisplayType(typ))
		if err != nil {
			t.Errorf("%s: NewDriver: %v", typ, err)
			continue
		}
		layout, err := d.Layout()
		if err != nil {
			t.Errorf("%s: Layout(): %v", typ, err)
			continue
		}
		if layout.Width <= 0 || layout.Height <= 0 {
			t.Errorf("%s: Layout() = %dx%d, want positive dimensions", typ, layout.Width, layout.Height)
		}
		if layout.Status.Font == nil {
			t.Errorf("%s: Layout().Status.Font is nil", typ)
		}
	}
}
