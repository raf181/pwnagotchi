package hw

import (
	"image"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
	"github.com/jayofelony/pwnagotchi/go-port/internal/ui/fonts"
)

// DummyDisplay ports hw.dummydisplay.DummyDisplay: no real hardware to
// drive, so — exactly like Python's version — Initialize/Render/Clear are
// real, correct no-ops (there is nothing to initialize/render/clear), while
// Layout() does the real width/height-derived widget-position and
// font-size computation.
type DummyDisplay struct {
	cfg   config.Map
	fonts *fonts.Set
}

// NewDummyDisplay ports DummyDisplay.__init__.
func NewDummyDisplay(cfg config.Map) *DummyDisplay {
	return &DummyDisplay{cfg: displayConfig(cfg)}
}

func (d *DummyDisplay) Name() string             { return "DummyDisplay" }
func (d *DummyDisplay) Initialize() error        { return nil }
func (d *DummyDisplay) Render(*image.Gray) error { return nil }
func (d *DummyDisplay) Clear() error             { return nil }

// Layout ports DummyDisplay.layout().
func (d *DummyDisplay) Layout() (*Layout, error) {
	width := intConfigOr(d.cfg, "width", 480)
	height := intConfigOr(d.cfg, "height", 720)

	fs, err := fonts.NewSized(height/30, height/40, height/30, height/6, height/30, height/35)
	if err != nil {
		return nil, err
	}
	d.fonts = fs

	l := &Layout{
		Width:      width,
		Height:     height,
		Face:       Point{0, width / 12},
		Name:       Point{5, width / 25},
		Channel:    Point{0, 0},
		APs:        Point{width / 8, 0},
		Uptime:     Point{width - width/12, 0},
		Line1:      [4]int{0, height / 32, width, height / 32},
		Line2:      [4]int{0, height - height/25 - 1, width, height - height/25 - 1},
		FriendFace: Point{0, height / 10},
		FriendName: Point{width / 12, height / 10},
		Shakes:     Point{0, height - height/25},
		Mode:       Point{width - width/8, height - height/25},
	}

	// Python: lw, lh = fonts.Small.getsize("W"); status.max = int(width / lw).
	lw := fonts.MeasureString(d.fonts.Small, "W")
	statusFont, err := d.fonts.StatusFont(float64(height / 35)) // fonts.Small's own point size in this sized set
	if err != nil {
		return nil, err
	}
	maxChars := width
	if lw > 0 {
		maxChars = width / lw
	}
	l.Status = StatusLayout{
		Pos:  Point{width / 48, height / 3},
		Font: statusFont,
		Max:  maxChars,
	}
	return l, nil
}

func intConfigOr(cfg config.Map, key string, def int) int {
	switch v := cfg[key].(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}
