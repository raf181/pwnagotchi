// Package view ports pwnagotchi/ui/view.py: the real widget-state
// orchestrator and frame-render pipeline (canvas + hw.Driver.Render),
// driven by the same mood/state-change contract internal/cli.HeadlessView
// exposes for headless operation. This is the "has a real display" path.
package view

import (
	"fmt"
	"image"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/go-port/internal/agent"
	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
	"github.com/jayofelony/pwnagotchi/go-port/internal/ui/components"
	"github.com/jayofelony/pwnagotchi/go-port/internal/ui/faces"
	"github.com/jayofelony/pwnagotchi/go-port/internal/ui/fonts"
	"github.com/jayofelony/pwnagotchi/go-port/internal/ui/hw"
	"github.com/jayofelony/pwnagotchi/go-port/internal/ui/state"
	"github.com/jayofelony/pwnagotchi/go-port/internal/voice"
)

// Black/White mirror view.py's BLACK/WHITE module-level constants, which
// Python swaps when config['ui']['invert'] is true. Since Go has no
// package-level mutable-global equivalent that's safe to share across View
// instances, these live on the View itself (Black()/White() methods) —
// see the invert field below.
const (
	blackNormal = 0xFF
	whiteNormal = 0x00
	blackInvert = 0x00
	whiteInvert = 0xFF
)

// EventEmitter mirrors pwnagotchi.plugins.on(event, *args).
type EventEmitter interface {
	On(event string, args ...interface{})
}

type noopEmitter struct{}

func (noopEmitter) On(string, ...interface{}) {}

// View ports view.View.
type View struct {
	mu sync.Mutex

	invert bool
	black  uint8
	white  uint8

	agent     *agent.Agent
	renderCbs []func(*image.Gray)
	config    config.Map
	canvas    *image.Gray
	frozen    bool

	voice      *voice.Voice
	faces      *faces.Set
	fonts      *fonts.Set
	fontsSuper *fonts.Set // same faces at components.SupersampleFactor×, see fontsFromLayout

	impl   hw.Driver
	layout *hw.Layout

	width, height int
	rotation      int

	state         *state.State
	widgets       map[string]sceneElement
	ignoreChanges []string
	fps           float64

	emit EventEmitter
	rnd  *rand.Rand

	stopCh chan struct{}
}

// New ports View.__init__(config, impl, state=None).
func New(cfg config.Map, impl hw.Driver, initial map[string]interface{}, emit EventEmitter) (*View, error) {
	if emit == nil {
		emit = noopEmitter{}
	}
	uiCfg, _ := cfg["ui"].(config.Map)

	v := &View{
		black:  blackNormal,
		white:  whiteNormal,
		config: cfg,
		impl:   impl,
		emit:   emit,
		rnd:    rand.New(rand.NewSource(rand.Int63())),
		stopCh: make(chan struct{}),
	}

	if invertVal, ok := uiCfg["invert"].(bool); ok && invertVal {
		log.Printf("INVERT BLACK/WHITES:%v", invertVal)
		v.invert = true
		v.black = blackInvert
		v.white = whiteInvert
	}

	v.faces = faces.Default()
	if facesCfg, ok := uiCfg["faces"].(config.Map); ok {
		strCfg := map[string]string{}
		for k, val := range facesCfg {
			if s, ok := val.(string); ok {
				strCfg[k] = s
			}
		}
		v.faces.LoadFromConfig(strCfg)
	}

	lang, _ := mainField(cfg)["lang"].(string)
	v.voice = voice.New(lang)

	layout, err := impl.Layout()
	if err != nil {
		return nil, err
	}
	v.layout = layout
	v.fonts, v.fontsSuper = fontsFromLayout(layout)

	displayCfg, _ := uiCfg["display"].(config.Map)
	v.rotation = intFieldOr(displayCfg, "rotation", 0)
	if (v.rotation/90)%2 == 0 {
		v.width, v.height = layout.Width, layout.Height
	} else {
		v.width, v.height = layout.Height, layout.Width
	}

	v.buildInitialState(uiCfg)

	if initial != nil {
		for k, val := range initial {
			v.state.Set(k, val)
		}
	}

	v.emit.On("ui_setup", v)

	fps, _ := config.DigFloat(cfg, "ui", "fps")
	v.fps = fps
	if fps > 0.0 {
		go v.refreshHandler()
		v.ignoreChanges = nil
	} else {
		log.Print("ui.fps is 0, the display will only update for major changes")
		v.ignoreChanges = []string{"uptime", "name"}
	}

	return v, nil
}

func mainField(cfg config.Map) config.Map {
	m, _ := cfg["main"].(config.Map)
	return m
}

func intFieldOr(m config.Map, key string, def int) int {
	if m == nil {
		return def
	}
	switch v := m[key].(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

// fontsFromLayout builds the real font.Set for this specific driver from
// hw.Layout.FontsSetup — the exact (bold, bold_small, medium, huge,
// bold_big, small) arguments that driver's own real Python layout()
// method passes to fonts.setup(...) (AST-extracted per driver into
// testdata/hw_layouts.json — see scripts/gen_hw_layouts.py). A previous
// revision of this function ignored FontsSetup entirely and always built
// the generic fonts.New("", 0) sizes instead, silently rendering every
// non-generic driver's "huge" face glyph and other widgets at the wrong
// size (e.g. waveshare_4 real Python uses huge=35; the generic default is
// 25) — a real parity bug, not just a rendering-quality one, now fixed.
func fontsFromLayout(l *hw.Layout) (*fonts.Set, *fonts.Set) {
	sizes := l.FontsSetup
	if sizes == ([6]int{}) {
		// No driver-specific sizes recorded (e.g. a Layout built by hand
		// without populating FontsSetup) — fall back to Python's own
		// fonts.py module-level default (fonts.init calls setup(10, 8,
		// 10, 25, 25, 9)), matching what a real display with no
		// layout()-time fonts.setup() call at all would still have from
		// whatever ran first.
		sizes = [6]int{10, 8, 10, 25, 25, 9}
	}
	fs, err := fonts.NewSized(sizes[0], sizes[1], sizes[2], sizes[3], sizes[4], sizes[5])
	if err != nil {
		// Only fails if the embedded TTF data itself is corrupt, which
		// would be a build-time defect, not a runtime condition —
		// panicking here matches "this can't happen in a working build"
		// rather than threading an error through every caller for a case
		// that isn't a real operational failure mode.
		panic(fmt.Sprintf("view: embedded fonts failed to load: %v", err))
	}
	// Same faces at components.SupersampleFactor× the point size, used to
	// render text at higher resolution before downsampling — see
	// components.Text.SuperFont's doc comment for why.
	const f = components.SupersampleFactor
	superFs, err := fonts.NewSized(sizes[0]*f, sizes[1]*f, sizes[2]*f, sizes[3]*f, sizes[4]*f, sizes[5]*f)
	if err != nil {
		panic(fmt.Sprintf("view: embedded fonts failed to load (super): %v", err))
	}
	return fs, superFs
}

func (v *View) buildInitialState(uiCfg config.Map) {
	facesCfg, _ := uiCfg["faces"].(config.Map)
	posX := intFieldOr(facesCfg, "position_x", faces.DefaultPositionX)
	posY := intFieldOr(facesCfg, "position_y", faces.DefaultPositionY)
	pngFace, _ := facesCfg["png"].(bool)

	elements := map[string]sceneElement{
		"channel": &labeledValueElement{widget: &components.LabeledValue{
			Label: strPtr("CH"), Value: "00", Position: pt(v.layout.Channel), LabelFont: v.fonts.Bold, TextFont: v.fonts.Medium, Color: v.black, LabelSpacing: components.DefaultLabelSpacing,
			LabelFontSuper: v.fontsSuper.Bold, TextFontSuper: v.fontsSuper.Medium,
		}},
		"aps": &labeledValueElement{widget: &components.LabeledValue{
			Label: strPtr("APS"), Value: "0 (00)", Position: pt(v.layout.APs), LabelFont: v.fonts.Bold, TextFont: v.fonts.Medium, Color: v.black, LabelSpacing: components.DefaultLabelSpacing,
			LabelFontSuper: v.fontsSuper.Bold, TextFontSuper: v.fontsSuper.Medium,
		}},
		"uptime": &labeledValueElement{widget: &components.LabeledValue{
			Label: strPtr("UP"), Value: "00:00:00", Position: pt(v.layout.Uptime), LabelFont: v.fonts.Bold, TextFont: v.fonts.Medium, Color: v.black, LabelSpacing: components.DefaultLabelSpacing,
			LabelFontSuper: v.fontsSuper.Bold, TextFontSuper: v.fontsSuper.Medium,
		}},
		"line1": &structuralElement{widget: &components.Line{XY: rectFrom4(v.layout.Line1), Color: v.black}},
		"line2": &structuralElement{widget: &components.Line{XY: rectFrom4(v.layout.Line2), Color: v.black}},
		"face": newTextElement(&components.Text{
			Position: components.Point{X: posX, Y: posY}, Color: v.black, Font: v.fonts.Huge, SuperFont: v.fontsSuper.Huge, PNG: pngFace,
		}, strPtr(v.faces.Sleep)),
		"friend_name": newTextElement(&components.Text{
			Position: pt(v.layout.FriendFace), Color: v.black, Font: v.fonts.BoldSmall, SuperFont: v.fontsSuper.BoldSmall,
		}, nil),
		"name": newTextElement(&components.Text{
			Position: pt(v.layout.Name), Color: v.black, Font: v.fonts.Bold, SuperFont: v.fontsSuper.Bold,
		}, strPtr("pwnagotchi>")),
		"status": newTextElement(&components.Text{
			Position: pt(v.layout.Status.Pos), Color: v.black, Font: v.layout.Status.Font, Wrap: true, MaxLength: v.layout.Status.Max,
		}, strPtr(v.voice.Default())),
		"shakes": &labeledValueElement{widget: &components.LabeledValue{
			Label: strPtr("PWND "), Value: "0 (00)", Position: pt(v.layout.Shakes), LabelFont: v.fonts.Bold, TextFont: v.fonts.Medium, Color: v.black, LabelSpacing: components.DefaultLabelSpacing,
			LabelFontSuper: v.fontsSuper.Bold, TextFontSuper: v.fontsSuper.Medium,
		}},
		"mode": newTextElement(&components.Text{
			Position: pt(v.layout.Mode), Color: v.black, Font: v.fonts.Bold, SuperFont: v.fontsSuper.Bold,
		}, strPtr("AUTO")),
	}

	stateElems := make(map[string]state.Element, len(elements))
	widgets := make(map[string]sceneElement, len(elements))
	for k, e := range elements {
		stateElems[k] = e
		widgets[k] = e
	}
	v.state = state.New(stateElems)
	v.widgets = widgets
}

func strPtr(s string) *string        { return &s }
func pt(p hw.Point) components.Point { return components.Point{X: p.X, Y: p.Y} }
func rectFrom4(r [4]int) components.Rect {
	return components.Rect{X0: r[0], Y0: r[1], X1: r[2], Y1: r[3]}
}

// SetAgent ports View.set_agent.
func (v *View) SetAgent(a *agent.Agent) {
	v.mu.Lock()
	v.agent = a
	v.mu.Unlock()
}

// HasElement ports View.has_element.
func (v *View) HasElement(key string) bool { return v.state.HasElement(key) }

// RemoveElement ports View.remove_element.
func (v *View) RemoveElement(key string) { v.state.RemoveElement(key) }

// Width/Height port View.width/height.
func (v *View) Width() int  { return v.width }
func (v *View) Height() int { return v.height }

// OnStateChange ports View.on_state_change.
func (v *View) OnStateChange(key string, cb func(old, new interface{})) {
	v.state.AddListener(key, cb)
}

// OnRender ports View.on_render.
func (v *View) OnRender(cb func(*image.Gray)) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.renderCbs = append(v.renderCbs, cb)
}

func (v *View) refreshHandler() {
	delay := time.Duration(float64(time.Second) / v.fps)
	for {
		select {
		case <-v.stopCh:
			return
		default:
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("non fatal error while updating view: %v", r)
				}
			}()
			uiCfg, _ := v.config["ui"].(config.Map)
			cursor := true
			if c, ok := uiCfg["cursor"].(bool); ok {
				cursor = c
			}
			if cursor {
				name, _ := v.state.Get("name").(string)
				if strings.Contains(name, "█") {
					v.Set("name", strings.TrimRight(strings.TrimSuffix(name, "█"), "█ "))
				} else {
					v.Set("name", name+" █")
				}
			}
			v.Update(false, nil)
		}()
		select {
		case <-v.stopCh:
			return
		case <-time.After(delay):
		}
	}
}

// Stop ends the refresh-handler goroutine, if running (an addition for
// clean shutdown — Python's daemon thread just dies with the process).
func (v *View) Stop() { close(v.stopCh) }

// Set ports View.set.
func (v *View) Set(key string, value interface{}) { v.state.Set(key, value) }

// Get ports View.get.
func (v *View) Get(key string) interface{} { return v.state.Get(key) }

func (v *View) getRandomFace(choices ...string) string {
	if len(choices) == 0 {
		return ""
	}
	return choices[v.rnd.Intn(len(choices))]
}

// Update ports View.update(force=False, new_data={}).
func (v *View) Update(force bool, newData map[string]interface{}) {
	for k, val := range newData {
		v.Set(k, val)
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.frozen {
		return
	}

	changes := v.state.Changes(v.ignoreChanges...)
	if !force && len(changes) == 0 {
		return
	}

	canvas := image.NewGray(image.Rect(0, 0, v.width, v.height))
	fillColor := v.white
	for i := range canvas.Pix {
		canvas.Pix[i] = fillColor
	}

	v.emit.On("ui_update", v)

	for _, elem := range v.widgets {
		if err := elem.Draw(canvas); err != nil {
			log.Printf("view: draw error: %v", err)
		}
	}

	// Real Python creates its canvas as PIL mode '1' (Image.new('1', ...),
	// see ui/view.py's View.update): a true 1-bit-per-pixel image, so
	// every draw operation — including font glyph rendering — is
	// necessarily pure black/white with no anti-aliased gray edges at
	// all, matching the actual e-ink/OLED hardware's binary pixels. Go's
	// font rasterizer produces genuine anti-aliased gray edges on our
	// 8-bit image.Gray canvas; thresholding every pixel to whichever of
	// v.black/v.white it's closer to reproduces Python's real
	// stark-pixel rendering instead of leaving soft/blurry edges in
	// what's shown on screen and served to the web UI.
	thresholdToBlackAndWhite(canvas, v.black, v.white)
	v.canvas = canvas

	if v.impl != nil {
		if err := v.impl.Render(canvas); err != nil {
			log.Printf("view: render error: %v", err)
		}
	}

	for _, cb := range v.renderCbs {
		cb(canvas)
	}

	v.state.Reset()
}

// inkCoverageThreshold is the minimum ink-coverage fraction (0=pure
// background, 1=pure ink) a pixel needs to be quantized to black instead
// of white. A plain 50% midpoint cutoff already looks correct for most
// text/shapes (verified side-by-side against Pillow/FreeType rendering
// the same text/font/size directly onto a real mode-'1' image); this is
// nudged slightly below 50% because a handful of thin strokes in small
// digits/letters land just under the midpoint and disappear entirely at
// exactly 0.5, fragmenting the letterform. Tuned empirically by rendering
// the same small labeled-value text (fonts.Bold + fonts.Medium, the exact
// faces/sizes/hinting internal/ui/fonts actually uses) through several
// candidate thresholds and comparing legibility — not derived from a
// specification.
const inkCoverageThreshold = 0.42

// thresholdToBlackAndWhite quantizes every pixel to black or white (no
// dithering, matching Pillow's own default behavior when compositing
// drawn shapes/text onto a mode-'1' image — ui/view.py never requests
// dithering). ink is the fraction of the way from white to black a pixel
// sits (0=white/background, 1=black/ink) — computed this way round
// (not "distance to nearest endpoint") so inkCoverageThreshold's bias
// consistently means "more generous toward ink" regardless of whether
// black is numerically greater than white or vice versa (the invert
// config swaps which raw byte value means "black" — see the black/white
// consts above).
func thresholdToBlackAndWhite(canvas *image.Gray, black, white uint8) {
	b, w := int(black), int(white)
	span := b - w
	for i, p := range canvas.Pix {
		v := int(p)
		var ink float64
		if span != 0 {
			ink = float64(v-w) / float64(span)
		}
		if ink >= inkCoverageThreshold {
			canvas.Pix[i] = black
		} else {
			canvas.Pix[i] = white
		}
	}
}

// IsNormal ports View.is_normal: NOTE this replicates a real Python bug —
// `face not in (faces.INTENSE + faces.COOL + ... + faces.LONELY)` is
// STRING CONCATENATION (missing commas/parens for a tuple), so the check
// is a SUBSTRING test against one big concatenated string, not a proper
// membership test against a set of distinct faces. Verified against the
// real interpreter (see docs/known-differences.md). Preserved exactly.
func (v *View) IsNormal() bool {
	face, _ := v.state.Get("face").(string)
	combined := v.faces.Intense + v.faces.Cool + v.faces.Bored + v.faces.Happy + v.faces.Excited +
		v.faces.Motivated + v.faces.Demotivated + v.faces.Smart + v.faces.Sad + v.faces.Lonely
	return !strings.Contains(combined, face)
}

// Freeze reports whether the view is frozen (post on_shutdown), for tests.
func (v *View) Freeze() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.frozen
}

// Canvas returns the last-rendered frame, for tests/inspection.
func (v *View) Canvas() *image.Gray {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.canvas
}

// Compile-time proof that *View satisfies internal/agent.View — the same
// contract internal/cli.HeadlessView satisfies for headless operation.
var _ agent.View = (*View)(nil)
