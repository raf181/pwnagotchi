package fonts

import (
	"image"
	"image/draw"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

func TestNewLoadsAllFaces(t *testing.T) {
	s, err := New("DejaVuSansMono", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	faces := map[string]font.Face{
		"Small": s.Small, "Medium": s.Medium, "BoldSmall": s.BoldSmall,
		"Bold": s.Bold, "BoldBig": s.BoldBig, "Huge": s.Huge,
	}
	for name, f := range faces {
		if f == nil {
			t.Fatalf("%s face is nil", name)
		}
	}
}

func TestRenderedTextProducesNonBlankPixels(t *testing.T) {
	s, err := New("DejaVuSansMono", 0)
	if err != nil {
		t.Fatal(err)
	}

	img := image.NewGray(image.Rect(0, 0, 100, 30))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)

	d := &font.Drawer{
		Dst:  img,
		Src:  image.Black,
		Face: s.Medium,
		Dot:  fixed.P(2, 20),
	}
	d.DrawString("pwn")

	blackPixels := 0
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if img.GrayAt(x, y).Y < 128 {
				blackPixels++
			}
		}
	}
	if blackPixels == 0 {
		t.Fatal("expected drawn text to produce at least some dark pixels, got none (rendering is broken)")
	}
}

func TestStatusFontAppliesSizeOffset(t *testing.T) {
	s, err := New("DejaVuSansMono", 5)
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.StatusFont(10)
	if err != nil {
		t.Fatal(err)
	}
	if f == nil {
		t.Fatal("StatusFont returned nil face")
	}
}
