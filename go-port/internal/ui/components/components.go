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
}

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
	d := &xfont.Drawer{
		Dst:  canvas,
		Src:  image.NewUniform(gray(t.Color)),
		Face: t.Font,
	}
	y := t.Position.Y
	for _, line := range strings.Split(text, "\n") {
		metrics := t.Font.Metrics()
		d.Dot = fixed.P(t.Position.X, y+metrics.Ascent.Round())
		d.DrawString(line)
		y += metrics.Height.Round()
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

// LabeledValue ports components.LabeledValue.
type LabeledValue struct {
	Label        *string
	Value        string
	Position     Point
	LabelFont    xfont.Face
	TextFont     xfont.Face
	Color        uint8
	LabelSpacing int
}

// Draw ports LabeledValue.draw.
func (lv *LabeledValue) Draw(canvas draw.Image) error {
	if lv.Label == nil {
		t := &Text{Value: lv.Value, Position: lv.Position, Font: lv.LabelFont, Color: lv.Color}
		return t.Draw(canvas)
	}
	label := &Text{Value: *lv.Label, Position: lv.Position, Font: lv.LabelFont, Color: lv.Color}
	if err := label.Draw(canvas); err != nil {
		return err
	}
	valuePos := Point{
		X: lv.Position.X + lv.LabelSpacing + 5*len(*lv.Label),
		Y: lv.Position.Y,
	}
	value := &Text{Value: lv.Value, Position: valuePos, Font: lv.TextFont, Color: lv.Color}
	return value.Draw(canvas)
}
