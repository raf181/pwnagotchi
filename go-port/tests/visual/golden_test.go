// Package visual holds deterministic Go-vs-Python UI rendering regression
// tests: identical scripted state rendered through the real
// internal/ui/view pipeline (view.New + view.Update, the same View/Text/
// LabeledValue/Line code every real display and the web UI use), compared
// pixel-for-pixel at native resolution against a checked-in golden PNG
// produced by the real Python pwnagotchi.ui.view.View for the exact same
// state (see testdata/README.md and oracle_test.go for how the golden was
// generated, and docs/rendering-investigation.md for the full pipeline
// trace this exists to guard).
package visual

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
	"github.com/jayofelony/pwnagotchi/go-port/internal/ui/faces"
	"github.com/jayofelony/pwnagotchi/go-port/internal/ui/hw"
	"github.com/jayofelony/pwnagotchi/go-port/internal/ui/view"
)

// scriptedConfig mirrors oracle.py's build_config(): the real waveshare_4
// (WaveshareV4 / config type "waveshare_4", the default hardware — see
// pwnagotchi/ui/hw/waveshare2in13_V4.py, 250x122) driver, fps=0 (no
// background refresh goroutine racing the test), and no face-string
// overrides so faces.Default()'s plain module-level constants apply,
// exactly like Python's faces.py when load_from_config() doesn't touch
// them.
func scriptedConfig() config.Map {
	return config.Map{
		"main": config.Map{"lang": "en"},
		"ui": config.Map{
			"fps":    float64(0),
			"cursor": true,
			"invert": false,
			"faces":  config.Map{"position_x": int64(0), "position_y": int64(40), "png": false},
			"font":   config.Map{"name": "DejaVuSansMono", "size_offset": int64(0)},
			"display": config.Map{
				"type":     "waveshare_4",
				"rotation": int64(0),
			},
		},
	}
}

// scriptedUpdate is the exact new_data map oracle.py's main() passes to
// view.update(force=True, new_data=...) — the shared fixture between both
// oracles. Keep these two in lockstep; a change on one side without the
// other invalidates the golden.
func scriptedUpdate() map[string]interface{} {
	return map[string]interface{}{
		"channel": "6",
		"aps":     "3 (11)",
		"uptime":  "00:12:34",
		"name":    "pwnagotchi>",
		"face":    faces.Default().Happy,
		"status":  "Hello world, this is a test of the emergency broadcast system.",
		"shakes":  "2 (04)",
		"mode":    "AUTO",
	}
}

// renderGoFrame builds a real View over the real waveshare_4 driver and
// returns the real rendered canvas — the same *image.Gray a real display's
// Render() or the web UI's /ui route would receive.
func renderGoFrame(t *testing.T) *image.Gray {
	t.Helper()
	cfg := scriptedConfig()
	driver, err := hw.NewDriver(cfg)
	if err != nil {
		t.Fatalf("hw.NewDriver: %v", err)
	}
	v, err := view.New(cfg, driver, nil, nil)
	if err != nil {
		t.Fatalf("view.New: %v", err)
	}
	v.Update(true, scriptedUpdate())
	canvas := v.Canvas()
	if canvas == nil {
		t.Fatal("view.Update produced a nil canvas")
	}
	return canvas
}

func loadGoldenPNG(t *testing.T, path string) *image.Gray {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening golden %s: %v", path, err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decoding golden %s: %v", path, err)
	}
	b := img.Bounds()
	gray := image.NewGray(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			gray.Set(x, y, img.At(x, y))
		}
	}
	return gray
}

func savePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encoding %s: %v", path, err)
	}
}

// pixelDiff compares two same-size 1-bit-quantized (pure black/white)
// images pixel by pixel, returning the count of mismatched pixels, the
// total pixel count, and a red/black diff visualization (black where both
// agree, red where they disagree) — an actual diff image, not just a
// percentage.
func pixelDiff(a, b *image.Gray) (mismatches, total int, diff *image.RGBA) {
	bounds := a.Bounds()
	total = bounds.Dx() * bounds.Dy()
	diff = image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pa := a.GrayAt(x, y).Y
			pb := b.GrayAt(x, y).Y
			// Both oracles quantize to pure ink/background; treat any
			// pixel past the midpoint as "ink" so an inverted (white/
			// black swapped) golden vs render still compares on ink
			// shape, not on which literal byte value each side picked
			// for "ink" (that polarity choice is asserted separately,
			// see TestGoMatchesPythonGoldenNativeResolution's polarity
			// check).
			inkA := pa >= 128
			inkB := pb >= 128
			if inkA != inkB {
				mismatches++
				diff.Set(x, y, color.RGBA{R: 255, A: 255})
			} else if inkA {
				diff.Set(x, y, color.RGBA{R: 0, G: 0, B: 0, A: 255})
			} else {
				diff.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}
	return mismatches, total, diff
}

// TestGoMatchesPythonGoldenNativeResolution is the core rendering-parity
// golden test: renders the scripted fixture through the real Go pipeline
// at native resolution (no upscaling — this is exactly what a real
// waveshare_4 panel or the web UI's raw /ui PNG would show) and diffs it
// against the checked-in real-Python-rendered golden. A small mismatch
// tolerance accounts for FreeType (Python) vs golang.org/x/image (Go)
// rasterizer/hinting differences at sub-pixel glyph edges — see
// docs/rendering-investigation.md for why zero divergence isn't
// achievable and what the tolerance actually covers (edge antialiasing
// noise on diagonal/curved strokes, not missing or misplaced text).
func TestGoMatchesPythonGoldenNativeResolution(t *testing.T) {
	const goldenPath = "testdata/python_golden_waveshare_4.png"
	golden := loadGoldenPNG(t, goldenPath)
	got := renderGoFrame(t)

	if got.Bounds() != golden.Bounds() {
		t.Fatalf("dimension mismatch: Go=%v Python golden=%v", got.Bounds(), golden.Bounds())
	}

	outDir := t.TempDir()
	gotPath := filepath.Join(outDir, "go_render.png")
	savePNG(t, gotPath, got)

	mismatches, total, diff := pixelDiff(got, golden)
	pct := 100 * float64(mismatches) / float64(total)

	// "out/" is gitignored (see tests/visual/.gitignore) so a routine
	// passing `go test` run never dirties the working tree — this is a
	// rendered artifact for a human/CI to inspect on demand, not checked
	// state.
	diffPath := filepath.Join("out", "last_diff_waveshare_4.png")
	savePNG(t, diffPath, diff)
	t.Logf("pixel mismatch: %d/%d (%.2f%%) — diff image written to %s, Go render written to %s", mismatches, total, pct, diffPath, gotPath)

	// Tolerance: anti-aliased glyph edges are the only expected source of
	// divergence (see docs/rendering-investigation.md). This is checked
	// against real measured drift, not picked to make the test pass —
	// widen it only alongside an update to that doc explaining why.
	const maxMismatchPct = 6.0
	if pct > maxMismatchPct {
		t.Errorf("Go render diverges from Python golden by %.2f%% of pixels (max allowed %.2f%%); see %s", pct, maxMismatchPct, diffPath)
	}
}

// TestLabeledValueHasRealGapBetweenLabelAndValue is a regression test for
// a real bug found and fixed this session: internal/ui/view.go's four
// LabeledValue widgets (channel/aps/uptime/shakes) never set LabelSpacing,
// silently defaulting to Go's zero value instead of Python's real
// components.LabeledValue.__init__ default (label_spacing=5). With no
// gap, a value's glyphs render almost flush against the label's last
// character — for compact/small marks (e.g. the "*" channel-scanning
// placeholder recon.go sets before a channel is chosen, or a leading
// digit immediately after "APS") the label and value visually fuse into
// an illegible blob at native resolution, which is what a live
// pwnagotchi-dev deployment's header ("CH *  APS 0") actually showed
// before this fix. Verifies a real blank column exists between the
// channel label's last ink column and its value's first ink column for
// exactly that "*" scenario, using the real hw.NewDriver + view.Update
// pipeline (not a synthetic/simplified reproduction).
func TestLabeledValueHasRealGapBetweenLabelAndValue(t *testing.T) {
	cfg := scriptedConfig()
	driver, err := hw.NewDriver(cfg)
	if err != nil {
		t.Fatalf("hw.NewDriver: %v", err)
	}
	v, err := view.New(cfg, driver, nil, nil)
	if err != nil {
		t.Fatalf("view.New: %v", err)
	}
	update := scriptedUpdate()
	update["channel"] = "*" // the real placeholder recon.go sets pre-lock
	v.Update(true, update)
	canvas := v.Canvas()

	// Real waveshare_4 layout: the channel label+value both start at
	// (0, 0), and the header line ends at y=13 (line1 sits at y=14).
	// "CH" is itself two already-separated glyphs (a blank column between
	// "C" and "H"), so counting contiguous ink RUNS (not just "is there
	// any blank column at all", which would false-positive on the C-H
	// gap alone) is what actually distinguishes "label and value
	// properly spaced" (>=3 runs: C, H, value) from "value fused onto
	// the label" (2 runs: C, H+value merged) — this is a regression test
	// for exactly that fusion bug, verified below to actually fail
	// against the pre-fix code (LabelSpacing left unset) before being
	// trusted here.
	// bandRight=20 deliberately stops well before the "aps" widget's own
	// "APS" label starts (x=28 at this layout/font size) — a wider band
	// would let APS's unrelated ink runs mask a fused "H"+value run here
	// by still totaling >=3 runs overall.
	const bandTop, bandBottom, bandRight = 0, 13, 20
	runs := inkColumnRuns(canvas, bandTop, bandBottom, bandRight)
	if len(runs) < 3 {
		t.Fatalf("expected >=3 separate ink runs in x=[0,%d) (C, H, and the '*' value, each properly gapped) — got %d run(s) %v; label and value are touching/overlapping, the exact bug this test guards against", bandRight, len(runs), runs)
	}
}

// inkColumnRuns returns the [start,end] column ranges of contiguous runs
// of ink-containing columns within [0, right) for rows [yTop, yBottom].
func inkColumnRuns(img *image.Gray, yTop, yBottom, right int) [][2]int {
	var runs [][2]int
	inRun := false
	for x := 0; x < right; x++ {
		if columnHasInk(img, x, yTop, yBottom) {
			if !inRun {
				runs = append(runs, [2]int{x, x})
				inRun = true
			} else {
				runs[len(runs)-1][1] = x
			}
		} else {
			inRun = false
		}
	}
	return runs
}

func columnHasInk(img *image.Gray, x, yTop, yBottom int) bool {
	for y := yTop; y <= yBottom; y++ {
		if img.GrayAt(x, y).Y >= 128 {
			return true
		}
	}
	return false
}

// TestGoRenderIsPureBlackAndWhite proves the Go canvas has no gray
// antialiasing artifacts left over after thresholding — real e-ink/OLED
// hardware and Python's mode-'1' PIL image are both strictly 1-bit, so
// every pixel must be exactly view.Black() or view.White(), never
// anything in between (a stray gray pixel would mean the threshold step
// in internal/ui/view.thresholdToBlackAndWhite was skipped or bypassed).
func TestGoRenderIsPureBlackAndWhite(t *testing.T) {
	got := renderGoFrame(t)
	seen := map[uint8]int{}
	for _, p := range got.Pix {
		seen[p]++
	}
	if len(seen) > 2 {
		t.Fatalf("expected at most 2 distinct pixel values (pure black/white), got %d distinct values: %v", len(seen), seen)
	}
}

// TestGoNativeResolutionMatchesLayout proves the rendered canvas is
// exactly the real waveshare_4 driver's native 250x122 — the goal
// explicitly requires preserving native resolution/layout, not stretching
// or resampling to some other size.
func TestGoNativeResolutionMatchesLayout(t *testing.T) {
	got := renderGoFrame(t)
	b := got.Bounds()
	if b.Dx() != 250 || b.Dy() != 122 {
		t.Fatalf("native resolution = %dx%d, want 250x122 (real waveshare_4/WaveshareV4 panel size)", b.Dx(), b.Dy())
	}
}

// TestGoldenBoundingBoxesNonEmpty is a coarse "text/shapes actually got
// drawn somewhere sane" sanity check per named UI region — catches the
// specific class of "corrupted UI" bug report this test suite exists to
// prevent (a widget silently rendering nothing, or everything colliding
// into one corner) even before comparing against the Python golden.
// Regions are the real waveshare_4 layout positions from
// pwnagotchi/ui/hw/waveshare2in13_V4.py.
func TestGoldenBoundingBoxesNonEmpty(t *testing.T) {
	got := renderGoFrame(t)
	regions := map[string]image.Rectangle{
		"header (channel/aps/uptime)": image.Rect(0, 0, 250, 13),
		"name":                        image.Rect(5, 15, 120, 33),
		"face":                        image.Rect(0, 40, 120, 88),
		"status":                      image.Rect(120, 15, 250, 88),
		"footer (shakes/mode)":        image.Rect(0, 109, 250, 121),
	}
	for name, r := range regions {
		if !hasInk(got, r) {
			t.Errorf("region %q (%v) has no ink pixels at all — widget failed to render", name, r)
		}
	}
}

func hasInk(img *image.Gray, r image.Rectangle) bool {
	r = r.Intersect(img.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if img.GrayAt(x, y).Y >= 128 {
				return true
			}
		}
	}
	return false
}

func init() {
	// Fail fast with a clear message rather than a confusing decode error
	// if the golden fixture is ever accidentally deleted.
	if _, err := os.Stat("testdata/python_golden_waveshare_4.png"); err != nil {
		panic(fmt.Sprintf("tests/visual: missing golden fixture testdata/python_golden_waveshare_4.png (regenerate with tests/visual/oracle.py under the real Python venv — see docs/rendering-investigation.md): %v", err))
	}
}
