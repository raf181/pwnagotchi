// Package fonts ports pwnagotchi/ui/fonts.py: DejaVu Sans Mono at the fixed
// sizes the stock UI layout uses. Pillow's ImageFont.truetype resolves
// "DejaVuSansMono"/"DejaVuSansMono-Bold" via fontconfig against whatever
// system font happens to be installed; Go has no fontconfig integration, so
// the exact same DejaVu Sans Mono TTF files are embedded here instead —
// same font, same rendering, no dependency on the host having fontconfig
// or DejaVu installed.
package fonts

import (
	"embed"
	"fmt"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

//go:embed assets/DejaVuSansMono.ttf assets/DejaVuSansMono-Bold.ttf
var assets embed.FS

// FontName mirrors fonts.FONT_NAME ("should not be changed" in Python too).
const FontName = "DejaVuSansMono"

var (
	regularOTF *opentype.Font
	boldOTF    *opentype.Font
	loadOnce   sync.Once
	loadErr    error
)

func load() error {
	loadOnce.Do(func() {
		regData, err := assets.ReadFile("assets/DejaVuSansMono.ttf")
		if err != nil {
			loadErr = err
			return
		}
		regularOTF, loadErr = opentype.Parse(regData)
		if loadErr != nil {
			return
		}
		boldData, err := assets.ReadFile("assets/DejaVuSansMono-Bold.ttf")
		if err != nil {
			loadErr = err
			return
		}
		boldOTF, loadErr = opentype.Parse(boldData)
	})
	return loadErr
}

// Set ports the Bold/BoldSmall/BoldBig/Medium/Small/Huge module-level
// globals fonts.py's setup()/init() populate — grouped into a struct
// instead of package globals so multiple displays/tests can hold
// independent font sets without racing on shared mutable state (Python's
// module globals are inherently single-instance and non-thread-safe here;
// this is a structural improvement necessitated by Go not having Python's
// "just mutate the module" escape hatch, not a behavior change to what
// gets rendered).
type Set struct {
	Bold      font.Face
	BoldSmall font.Face
	BoldBig   font.Face
	Medium    font.Face
	Small     font.Face
	Huge      font.Face

	StatusFontName string
	SizeOffset     int
}

// New ports fonts.init(config) + fonts.setup(10, 8, 10, 25, 25, 9) — the
// fixed sizes (bold, bold_small, medium, huge, bold_big, small) Python's
// init() always calls setup() with.
func New(statusFontName string, sizeOffset int) (*Set, error) {
	s, err := NewSized(10, 8, 10, 25, 25, 9)
	if err != nil {
		return nil, err
	}
	s.StatusFontName = statusFontName
	s.SizeOffset = sizeOffset
	return s, nil
}

// NewSized ports fonts.setup(bold, bold_small, medium, huge, bold_big,
// small): drivers that derive font sizes from their own screen dimensions
// (e.g. DummyDisplay.layout()) call this directly instead of New.
func NewSized(bold, boldSmall, medium, huge, boldBig, small int) (*Set, error) {
	if err := load(); err != nil {
		return nil, fmt.Errorf("fonts: loading embedded DejaVu Sans Mono: %w", err)
	}
	s := &Set{}

	var err error
	if s.Small, err = newFace(regularOTF, float64(small)); err != nil {
		return nil, err
	}
	if s.Medium, err = newFace(regularOTF, float64(medium)); err != nil {
		return nil, err
	}
	if s.BoldSmall, err = newFace(boldOTF, float64(boldSmall)); err != nil {
		return nil, err
	}
	if s.Bold, err = newFace(boldOTF, float64(bold)); err != nil {
		return nil, err
	}
	if s.BoldBig, err = newFace(boldOTF, float64(boldBig)); err != nil {
		return nil, err
	}
	if s.Huge, err = newFace(boldOTF, float64(huge)); err != nil {
		return nil, err
	}
	return s, nil
}

// MeasureString returns the pixel width font.MeasureString(face, s) would
// draw s at — a small wrapper so callers elsewhere in internal/ui don't
// need their own golang.org/x/image/font import just for this.
func MeasureString(face font.Face, s string) int {
	return font.MeasureString(face, s).Round()
}

func newFace(f *opentype.Font, size float64) (font.Face, error) {
	return opentype.NewFace(f, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
}

// StatusFont ports fonts.status_font(old_font): a face at old size +
// SizeOffset. Since StatusFontName is configurable (config['ui']['font']['name'])
// and may not be DejaVu, this only actually changes size — StatusFontName
// is exposed for callers that need to resolve a genuinely different
// installed font file themselves (Go has no fontconfig to resolve an
// arbitrary name string against, unlike Pillow).
func (s *Set) StatusFont(oldSize float64) (font.Face, error) {
	if err := load(); err != nil {
		return nil, err
	}
	return newFace(regularOTF, oldSize+float64(s.SizeOffset))
}
