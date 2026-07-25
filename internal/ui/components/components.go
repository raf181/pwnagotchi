// Package components ports pwnagotchi/ui/components.py: the drawable UI
// widgets (Bitmap, Line, Rect, FilledRect, Text, LabeledValue) rendered
// onto a canvas. Pillow's Image/ImageDraw are replaced with Go's
// image/image.Gray + golang.org/x/image/font (real rasterization, verified
// in internal/ui/fonts — not a stub).
package components

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"strings"

	xfont "golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// Point mirrors a Python (x, y) position tuple.
type Point struct{ X, Y int }

// Rect mirrors a Python xy rect list [x0, y0, x1, y1].
type Rect struct{ X0, Y0, X1, Y1 int }

// Widget ports components.Widget.
type Widget interface {
	Draw(canvas draw.Image) error
}

// gray converts a Pillow-style single-channel color byte (0 = black,
// 255 = white, matching PIL mode '1'/'L' convention) to a color.Gray.
func gray(c uint8) color.Gray { return color.Gray{Y: c} }

// Bitmap ports components.Bitmap.
type Bitmap struct {
	XY    Point
	Color uint8
	Image image.Image
}

// NewBitmap ports Bitmap.__init__: loads path (PNG) eagerly, matching
// Python's Image.open(path) in the constructor.
func NewBitmap(path string, xy Point, color uint8) (*Bitmap, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("components: Bitmap: %w", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("components: Bitmap: decoding %s: %w", path, err)
	}
	return &Bitmap{XY: xy, Color: color, Image: img}, nil
}

// Draw ports Bitmap.draw: color==0xFF inverts the image before pasting
// (matching ImageOps.invert), matching Python exactly including the
// invert-every-call behavior (Python reassigns self.image on every draw
// call when color==0xFF, so repeated draws re-invert from the ORIGINAL
// each time only because ImageOps.invert is idempotent-per-call on the
// stored self.image — replicated by inverting a copy each call, not
// mutating Image in place, to avoid double-inverting on repeated draws).
func (b *Bitmap) Draw(canvas draw.Image) error {
	img := b.Image
	if b.Color == 0xFF {
		img = invertGray(img)
	}
	drawAt(canvas, img, b.XY)
	return nil
}

func invertGray(src image.Image) image.Image {
	bounds := src.Bounds()
	dst := image.NewGray(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			g := color.GrayModel.Convert(src.At(x, y)).(color.Gray)
			dst.SetGray(x, y, color.Gray{Y: 255 - g.Y})
		}
	}
	return dst
}

func drawAt(canvas draw.Image, img image.Image, at Point) {
	bounds := img.Bounds()
	target := image.Rect(at.X, at.Y, at.X+bounds.Dx(), at.Y+bounds.Dy())
	draw.Draw(canvas, target, img, bounds.Min, draw.Over)
}

// Line ports components.Line.
type Line struct {
	XY    Rect
	Color uint8
	Width int
}

// Draw ports Line.draw via drawer.line(self.xy, fill=color, width=width): a
// Bresenham line, thickened by drawing Width parallel rows/columns —
// matching Pillow's own approach for width>1 axis-aligned-ish lines
// closely enough for the UI's actual usage (all stock layouts draw
// horizontal/vertical separator lines).
func (l *Line) Draw(canvas draw.Image) error {
	w := l.Width
	if w < 1 {
		w = 1
	}
	drawLine(canvas, l.XY.X0, l.XY.Y0, l.XY.X1, l.XY.Y1, gray(l.Color), w)
	return nil
}

func drawLine(canvas draw.Image, x0, y0, x1, y1 int, c color.Color, width int) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx, sy := sign(x1-x0), sign(y1-y0)
	err := dx + dy

	x, y := x0, y0
	for {
		plotThick(canvas, x, y, c, width)
		if x == x1 && y == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x += sx
		}
		if e2 <= dx {
			err += dx
			y += sy
		}
	}
}

func plotThick(canvas draw.Image, x, y int, c color.Color, width int) {
	half := width / 2
	for i := -half; i < width-half; i++ {
		canvas.Set(x, y+i, c)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	default:
		return 0
	}
}

// Rectangle ports components.Rect (outline only; named Rectangle in Go to
// avoid colliding with the Rect coordinate type above).
type Rectangle struct {
	XY    Rect
	Color uint8
}

func (r *Rectangle) Draw(canvas draw.Image) error {
	c := gray(r.Color)
	for x := r.XY.X0; x <= r.XY.X1; x++ {
		canvas.Set(x, r.XY.Y0, c)
		canvas.Set(x, r.XY.Y1, c)
	}
	for y := r.XY.Y0; y <= r.XY.Y1; y++ {
		canvas.Set(r.XY.X0, y, c)
		canvas.Set(r.XY.X1, y, c)
	}
	return nil
}

// FilledRect ports components.FilledRect.
type FilledRect struct {
	XY    Rect
	Color uint8
}

func (r *FilledRect) Draw(canvas draw.Image) error {
	c := gray(r.Color)
	for y := r.XY.Y0; y <= r.XY.Y1; y++ {
		for x := r.XY.X0; x <= r.XY.X1; x++ {
			canvas.Set(x, y, c)
		}
	}
	return nil
}

// Text ports components.Text.
type Text struct {
	Value     string
	Position  Point
	Font      xfont.Face
	Color     uint8
	Wrap      bool
	MaxLength int
	PNG       bool

	// SuperFont, if set, is Font's exact same face at SupersampleFactor×
	// the point size (see internal/ui/view's fontsSuper). When present,
	// Draw renders through it at higher resolution and downsamples with
	// box-filter averaging before the caller's later global 1-bit
	// threshold — see drawSupersampled's doc comment for why this
	// matters. Left nil, Draw falls back to direct single-resolution
	// rendering (used by tests that don't need pixel-perfect fidelity).
	SuperFont xfont.Face
}

// SupersampleFactor is how much larger SuperFont's point size is than
// Font's. Must match how internal/ui/view builds its "Super" fonts.Set.
const SupersampleFactor = 4

// Draw ports Text.draw. The `value is None` guard in Python (skip drawing
// entirely) is replicated by callers passing a nil *Text / not calling
// Draw — Go's Value is a plain string, so an empty string still draws (an
// empty line), matching Python's behavior for value="" (only value=None,
// never possible for a Python str field either in stock usage, skips).
func (t *Text) Draw(canvas draw.Image) error {
	if t.PNG {
		return t.drawPNG(canvas)
	}
	text := t.Value
	if t.Wrap {
		text = wrapText(t.Value, t.MaxLength)
	}
	if t.SuperFont != nil {
		if grayCanvas, ok := canvas.(*image.Gray); ok {
			return t.drawSupersampled(grayCanvas, text)
		}
	}
	d := &xfont.Drawer{
		Dst:  canvas,
		Src:  image.NewUniform(gray(t.Color)),
		Face: t.Font,
	}
	metrics := t.Font.Metrics()
	spacing := pilLineSpacing(t.Font)
	y := t.Position.Y
	for _, line := range strings.Split(text, "\n") {
		d.Dot = fixed.P(t.Position.X, y+metrics.Ascent.Round())
		d.DrawString(line)
		y += spacing
	}
	return nil
}

// pilLineSpacing reproduces the exact line-to-line pixel pitch Pillow's
// ImageDraw.text/multiline_text uses for wrapped text, NOT a font's design
// line-height metric (Ascent+Descent+LineGap, what golang.org/x/image/
// font's Metrics().Height gives). PIL/ImageText.py's Text._get_lines
// (called by any multi-line drawer.text(..., font=...) — pwnagotchi never
// passes a custom `spacing=`, so the default 4 always applies) computes:
//
//	line_spacing = font.getbbox("A", ...)[3] + stroke_width(0) + spacing(4)
//
// i.e. the actual rendered pixel bounding box of the specific glyph "A"
// (measured from the text draw origin, which PIL anchors at the font's
// ascender line — the same origin Go's Position.Y + Ascent baseline
// convention uses) — NOT a generic ascent+descent+linegap sum. For a font
// with no descender on "A" (true of DejaVu Sans Mono, the only font this
// port ever loads), that bbox-bottom coincides with the hinted/rounded
// Ascent value, but this is computed via the same real glyph-bounds
// mechanism PIL uses rather than assumed, so it stays correct for any
// future font swap too. A prior revision of this function used
// metrics.Height.Round() (no +4 spacing, and using LineGap instead of "A"'s
// real glyph extent), which under-advanced each line — by line 2 of any
// wrapped status message the text visibly overlapped the line above it
// (verified against a real Python-rendered golden in
// tests/visual/golden_test.go; see docs/rendering-investigation.md).
func pilLineSpacing(face xfont.Face) int {
	const pilDefaultSpacing = 4
	metrics := face.Metrics()
	bounds, _, ok := face.GlyphBounds('A')
	bottom := metrics.Ascent
	if ok {
		bottom += bounds.Max.Y
	}
	return bottom.Round() + pilDefaultSpacing
}

// drawSupersampled renders text at SupersampleFactor× resolution into a
// private buffer, then downsamples it back with box-filter (area-average)
// downsampling before writing into the real canvas — real anti-aliased
// coverage information from many finer sub-pixel samples, not a guess.
//
// Why this exists: golang.org/x/image/font's anti-aliased single-resolution
// coverage values for DejaVu Sans Mono at the small sizes this UI actually
// uses (8-10pt) don't survive a later 1-bit threshold as cleanly as
// Pillow/FreeType's rendering of the exact same font/text/size does — verified
// by rendering identical text through both and comparing. No single
// threshold cutoff resolves this: a lenient one (favoring thin diagonal
// strokes in letters like "5"/"2") fills in small digits' counters (the
// hole in "0"), while a strict one (preserving those counters) fragments
// thin strokes elsewhere. Supersampling first (this function) resolves
// both, the same way any font rasterizer gets clean small text: more
// samples per final pixel before the binary decision, not a smarter
// binary decision on the same too-few samples.
func (t *Text) drawSupersampled(canvas *image.Gray, text string) error {
	lineHeight := pilLineSpacing(t.Font)
	lines := strings.Split(text, "\n")

	maxWidth := 0
	for _, line := range lines {
		if w := xfont.MeasureString(t.Font, line).Round(); w > maxWidth {
			maxWidth = w
		}
	}
	totalHeight := lineHeight * len(lines)
	if maxWidth <= 0 || totalHeight <= 0 {
		return nil
	}

	bounds := canvas.Bounds()
	x0, y0 := t.Position.X, t.Position.Y
	x1, y1 := x0+maxWidth, y0+totalHeight
	if x0 < bounds.Min.X {
		x0 = bounds.Min.X
	}
	if y0 < bounds.Min.Y {
		y0 = bounds.Min.Y
	}
	if x1 > bounds.Max.X {
		x1 = bounds.Max.X
	}
	if y1 > bounds.Max.Y {
		y1 = bounds.Max.Y
	}
	if x1 <= x0 || y1 <= y0 {
		return nil
	}
	w, h := x1-x0, y1-y0

	const s = SupersampleFactor
	temp := image.NewGray(image.Rect(0, 0, w*s, h*s))
	for ty := 0; ty < h*s; ty++ {
		srcY := y0 + ty/s
		for tx := 0; tx < w*s; tx++ {
			temp.SetGray(tx, ty, canvas.GrayAt(x0+tx/s, srcY))
		}
	}

	d := &xfont.Drawer{Dst: temp, Src: image.NewUniform(gray(t.Color)), Face: t.SuperFont}
	superAscent := t.SuperFont.Metrics().Ascent.Round()
	// Line pitch in the supersampled buffer must land on the same real
	// pixel boundaries as pilLineSpacing(t.Font)*s once downsampled — using
	// pilLineSpacing(t.SuperFont) directly (the super-sized face's own "A"
	// glyph bounds + 4) rather than lineHeight*s keeps the per-line
	// rounding consistent with how it was actually rasterized at 4x, the
	// same reasoning fontsFromLayout documents for building real
	// super-sized font faces instead of scaling metrics arithmetically.
	superSpacing := pilLineSpacing(t.SuperFont)
	yy := (t.Position.Y - y0) * s // 0 unless the text was clipped at the top
	for _, line := range lines {
		d.Dot = fixed.P((t.Position.X-x0)*s, yy+superAscent)
		d.DrawString(line)
		yy += superSpacing
	}

	for ty := 0; ty < h; ty++ {
		for tx := 0; tx < w; tx++ {
			sum := 0
			for sy := 0; sy < s; sy++ {
				for sx := 0; sx < s; sx++ {
					sum += int(temp.GrayAt(tx*s+sx, ty*s+sy).Y)
				}
			}
			canvas.SetGray(x0+tx, y0+ty, color.Gray{Y: uint8(sum / (s * s))})
		}
	}
	return nil
}

// wrapText approximates Python's textwrap.TextWrapper(width=maxLength,
// replace_whitespace=False).wrap(value): break on spaces without exceeding
// maxLength characters per line, WITHOUT collapsing existing internal
// whitespace/newlines the way default textwrap would (replace_whitespace=False
// is exactly why Python disables that collapsing here). This is a
// reasonable-effort approximation, not a byte-for-byte port of textwrap's
// full algorithm (hyphenation, long-word splitting heuristics, locale-aware
// breaking are not replicated) — see docs/known-differences.md.
func wrapText(value string, maxLength int) string {
	if maxLength <= 0 {
		return value
	}
	var out []string
	for _, paragraph := range strings.Split(value, "\n") {
		out = append(out, wrapParagraph(paragraph, maxLength)...)
	}
	return strings.Join(out, "\n")
}

func wrapParagraph(s string, maxLength int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	cur := ""
	for _, w := range words {
		if cur == "" {
			cur = w
			continue
		}
		if len(cur)+1+len(w) > maxLength {
			lines = append(lines, cur)
			cur = w
		} else {
			cur += " " + w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func (t *Text) drawPNG(canvas draw.Image) error {
	f, err := os.Open(t.Value)
	if err != nil {
		return fmt.Errorf("components: Text PNG: %w", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return fmt.Errorf("components: Text PNG: %w", err)
	}

	bounds := img.Bounds()
	processed := image.NewGray(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if a>>8 < 255 {
				// alpha < 255 -> treated as opaque white, matching Python's
				// `if pixels[x,y][3] < 255: pixels[x,y] = (255,255,255,255)`.
				processed.SetGray(x, y, color.Gray{Y: 255})
				continue
			}
			// Convert to grayscale ('L') the same way Pillow's convert('L') does.
			l := (299*r + 587*g + 114*b) / 1000 >> 8
			processed.SetGray(x, y, color.Gray{Y: uint8(l)})
		}
	}

	final := processed
	if t.Color == 255 {
		final = colorizeBlackWhite(processed)
	}
	drawAt(canvas, thresholdTo1Bit(final), t.Position)
	return nil
}

// colorizeBlackWhite ports ImageOps.colorize(image.convert('L'), black="white", white="black"):
// swaps the black/white mapping (an inversion for a grayscale image).
func colorizeBlackWhite(src *image.Gray) *image.Gray {
	bounds := src.Bounds()
	dst := image.NewGray(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dst.SetGray(x, y, color.Gray{Y: 255 - src.GrayAt(x, y).Y})
		}
	}
	return dst
}

// thresholdTo1Bit mirrors .convert('1'): Pillow's default 1-bit conversion
// dithers, but pwnagotchi's monochrome UI content (icons/faces) is
// effectively already bi-level, so a simple 128 threshold (no dithering) is
// used — documented as an approximation, see docs/known-differences.md.
func thresholdTo1Bit(src *image.Gray) *image.Gray {
	bounds := src.Bounds()
	dst := image.NewGray(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if src.GrayAt(x, y).Y < 128 {
				dst.SetGray(x, y, color.Gray{Y: 0})
			} else {
				dst.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	return dst
}

// DefaultLabelSpacing mirrors components.LabeledValue.__init__'s
// label_spacing=5 default. Every real construction of a LabeledValue in
// pwnagotchi/ui/view.py (channel/aps/uptime/shakes) relies on this default
// and never overrides it — Go has no per-field default value for a struct
// literal, so any LabeledValue built without explicitly setting
// LabelSpacing silently gets Go's zero value (0) instead, packing the
// value flush against the label with no gap at all. Callers must set
// `LabelSpacing: components.DefaultLabelSpacing` explicitly (verified via
// a real-Python-rendered golden image in tests/visual/ that a missing
// gap here is not a hairline difference — small marks like the "*"
// channel-scanning placeholder visually fuse with the preceding label
// into what reads as corrupted/garbled text at native resolution).
const DefaultLabelSpacing = 5

// LabeledValue ports components.LabeledValue.
type LabeledValue struct {
	Label        *string
	Value        string
	Position     Point
	LabelFont    xfont.Face
	TextFont     xfont.Face
	Color        uint8
	LabelSpacing int

	// LabelFontSuper/TextFontSuper mirror Text.SuperFont — see there.
	LabelFontSuper xfont.Face
	TextFontSuper  xfont.Face
}

// Draw ports LabeledValue.draw.
func (lv *LabeledValue) Draw(canvas draw.Image) error {
	if lv.Label == nil {
		t := &Text{Value: lv.Value, Position: lv.Position, Font: lv.LabelFont, SuperFont: lv.LabelFontSuper, Color: lv.Color}
		return t.Draw(canvas)
	}
	label := &Text{Value: *lv.Label, Position: lv.Position, Font: lv.LabelFont, SuperFont: lv.LabelFontSuper, Color: lv.Color}
	if err := label.Draw(canvas); err != nil {
		return err
	}
	valuePos := Point{
		X: lv.Position.X + lv.LabelSpacing + 5*len(*lv.Label),
		Y: lv.Position.Y,
	}
	value := &Text{Value: lv.Value, Position: valuePos, Font: lv.TextFont, SuperFont: lv.TextFontSuper, Color: lv.Color}
	return value.Draw(canvas)
}
