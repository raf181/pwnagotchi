package components

import (
	"image"
	"image/color"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/ui/fonts"
)

func newWhiteCanvas(w, h int) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	return img
}

func countBlack(img *image.Gray) int {
	n := 0
	for _, p := range img.Pix {
		if p < 128 {
			n++
		}
	}
	return n
}

func TestLineDrawsBlackPixels(t *testing.T) {
	canvas := newWhiteCanvas(50, 50)
	l := &Line{XY: Rect{0, 10, 40, 10}, Color: 0, Width: 1}
	if err := l.Draw(canvas); err != nil {
		t.Fatal(err)
	}
	if countBlack(canvas) == 0 {
		t.Fatal("expected the line to draw black pixels")
	}
	// Horizontal line: every pixel on row 10 from x=0..40 should be black.
	for x := 0; x <= 40; x++ {
		if canvas.GrayAt(x, 10).Y != 0 {
			t.Fatalf("pixel (%d,10) = %v, want black", x, canvas.GrayAt(x, 10))
		}
	}
}

func TestRectangleDrawsOutlineOnly(t *testing.T) {
	canvas := newWhiteCanvas(20, 20)
	r := &Rectangle{XY: Rect{2, 2, 10, 10}, Color: 0}
	if err := r.Draw(canvas); err != nil {
		t.Fatal(err)
	}
	// Corner and edge pixels black.
	if canvas.GrayAt(2, 2).Y != 0 || canvas.GrayAt(10, 10).Y != 0 {
		t.Fatal("expected outline corners to be black")
	}
	// Interior pixel must remain white (outline only, not filled).
	if canvas.GrayAt(5, 5).Y != 255 {
		t.Fatal("expected rectangle interior to remain white (outline, not filled)")
	}
}

func TestFilledRectFillsInterior(t *testing.T) {
	canvas := newWhiteCanvas(20, 20)
	r := &FilledRect{XY: Rect{2, 2, 10, 10}, Color: 0}
	if err := r.Draw(canvas); err != nil {
		t.Fatal(err)
	}
	if canvas.GrayAt(5, 5).Y != 0 {
		t.Fatal("expected filled rectangle interior to be black")
	}
}

func TestTextDrawsVisiblePixels(t *testing.T) {
	fs, err := fonts.New("DejaVuSansMono", 0)
	if err != nil {
		t.Fatal(err)
	}
	canvas := newWhiteCanvas(100, 30)
	txt := &Text{Value: "pwn", Position: Point{2, 5}, Font: fs.Medium, Color: 0}
	if err := txt.Draw(canvas); err != nil {
		t.Fatal(err)
	}
	if countBlack(canvas) == 0 {
		t.Fatal("expected drawn text to produce black pixels")
	}
}

func TestTextWrapBreaksAtMaxLength(t *testing.T) {
	got := wrapText("the quick brown fox jumps", 10)
	want := "the quick\nbrown fox\njumps"
	if got != want {
		t.Fatalf("wrapText = %q, want %q", got, want)
	}
}

func TestTextWrapPreservesExistingNewlines(t *testing.T) {
	got := wrapText("line one\nline two", 100)
	want := "line one\nline two"
	if got != want {
		t.Fatalf("wrapText = %q, want %q", got, want)
	}
}

func TestLabeledValueDrawsLabelAndValue(t *testing.T) {
	fs, err := fonts.New("DejaVuSansMono", 0)
	if err != nil {
		t.Fatal(err)
	}
	canvas := newWhiteCanvas(150, 30)
	label := "CH"
	lv := &LabeledValue{
		Label: &label, Value: "6", Position: Point{2, 5},
		LabelFont: fs.Bold, TextFont: fs.Medium, Color: 0, LabelSpacing: 5,
	}
	if err := lv.Draw(canvas); err != nil {
		t.Fatal(err)
	}
	if countBlack(canvas) == 0 {
		t.Fatal("expected label+value to draw black pixels")
	}
}

func TestInvertGray(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 2, 2))
	src.Set(0, 0, color.Gray{Y: 0})
	src.Set(1, 1, color.Gray{Y: 255})
	inverted := invertGray(src)
	if inverted.At(0, 0).(color.Gray).Y != 255 {
		t.Fatal("expected black to invert to white")
	}
	if inverted.At(1, 1).(color.Gray).Y != 0 {
		t.Fatal("expected white to invert to black")
	}
}
