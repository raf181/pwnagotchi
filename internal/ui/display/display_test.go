package display

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
)

func testConfig() config.Map {
	return config.Map{
		"main": config.Map{"lang": "en"},
		"ui": config.Map{
			"fps":    float64(0),
			"cursor": true,
			"faces":  config.Map{"position_x": int64(0), "position_y": int64(40), "png": false},
			"display": config.Map{
				"type":    "dummydisplay",
				"enabled": true,
			},
			"web": config.Map{"on_frame": ""},
		},
		"bettercap":   config.Map{"handshakes": "/nonexistent"},
		"personality": config.Map{"bond_encounters_factor": int64(20000)},
	}
}

func newTestDisplay(t *testing.T) *Display {
	t.Helper()
	d, err := New(testConfig(), map[string]interface{}{"name": "pwnagotchi>"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(d.Stop)
	return d
}

func TestNewDisplayInitializesDummyDriver(t *testing.T) {
	d := newTestDisplay(t)
	if d.implName() != "DummyDisplay" {
		t.Fatalf("implName() = %q", d.implName())
	}
	if err := d.Clear(); err != nil {
		t.Fatalf("Clear() = %v, want nil (dummy driver is a real no-op)", err)
	}
}

func TestIsDummyDisplayReplicatesRealPythonBug(t *testing.T) {
	d := newTestDisplay(t)
	// The real Python bug: DummyDisplay passes name='DummyDisplay' (capital
	// D) to its base class, but is_dummy_display() compares against the
	// lowercase 'dummydisplay' — so it's ALWAYS false, even for an actual
	// dummy display. Verified against the real interpreter.
	if d.IsDummyDisplay() {
		t.Fatal("IsDummyDisplay() should replicate Python's always-false bug for the real DummyDisplay")
	}
}

func TestIsDfrobotAlwaysFalse(t *testing.T) {
	d := newTestDisplay(t)
	// is_dfrobot_v1/v2 compare against "dfrobot_v1"/"dfrobot_v2", which no
	// real driver ever uses as its name (the actual names are "dfrobot_1"/
	// "dfrobot_2") — always false in Python, replicated here.
	if d.IsDfrobotV1() || d.IsDfrobotV2() {
		t.Fatal("IsDfrobotV1/V2 should always be false (real Python bug, unrelated to the active driver)")
	}
}

func TestIsWaveshareAnyAlwaysTrue(t *testing.T) {
	d := newTestDisplay(t)
	// Real Python bug: `self.is_waveshare_v3` (missing parens) is always
	// truthy, so is_waveshare_any() is always true regardless of driver.
	if !d.IsWaveshareAny() {
		t.Fatal("IsWaveshareAny() should always be true, matching the real Python bug")
	}
}

func TestImageReturnsNilBeforeFirstRender(t *testing.T) {
	d := newTestDisplay(t)
	if d.Image() != nil {
		t.Fatal("Image() should be nil before any frame has rendered")
	}
}

func TestUpdateProducesRenderedImage(t *testing.T) {
	d := newTestDisplay(t)
	d.Set("channel", "6")
	d.Update(false, nil)

	// The render thread consumes canvases asynchronously; give it a moment.
	deadline := time.Now().Add(2 * time.Second)
	for d.Image() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	img := d.Image()
	if img == nil {
		t.Fatal("expected Image() to return a rendered frame after Update")
	}
	if img.Bounds().Dx() != d.Width() || img.Bounds().Dy() != d.Height() {
		t.Fatalf("image size = %v, want %dx%d", img.Bounds(), d.Width(), d.Height())
	}
}

func TestOnFrameHookRunsShellCommand(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "on_frame_ran")

	cfg := testConfig()
	cfg["ui"].(config.Map)["web"] = config.Map{"on_frame": "touch " + marker}

	d, err := New(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Stop)

	d.Set("channel", "1")
	d.Update(false, nil)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expected on_frame command to have run and created the marker file")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDisabledDisplaySkipsInitializeButStillRenders(t *testing.T) {
	cfg := testConfig()
	cfg["ui"].(config.Map)["display"].(config.Map)["enabled"] = false

	d, err := New(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Stop)

	d.Set("channel", "1")
	d.Update(false, nil)
	time.Sleep(50 * time.Millisecond)

	if d.Image() != nil {
		t.Fatal("a disabled display should never populate Image() (Python's _on_view_rendered only sets self._canvas when self._enabled)")
	}
}
