// Package display ports pwnagotchi/ui/display.py: Display(View), which
// adds hardware init/render-thread/on_frame-hook behavior on top of
// internal/ui/view.View, plus the ~93 is_X() display-type-name predicates.
package display

import (
	"fmt"
	"image"
	"log"
	"os/exec"
	"sync"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/ui/hw"
	"github.com/jayofelony/pwnagotchi/internal/ui/view"
)

// EventEmitter mirrors pwnagotchi.plugins.on(event, *args).
type EventEmitter interface {
	On(event string, args ...interface{})
}

type noopEmitter struct{}

func (noopEmitter) On(string, ...interface{}) {}

// Display ports display.Display.
type Display struct {
	*view.View

	impl     hw.Driver
	enabled  bool
	rotation int
	onFrame  string
	emit     EventEmitter

	mu         sync.Mutex
	canvas     *image.Gray
	canvasNext chan *image.Gray
	stop       chan struct{}
}

// New ports Display.__init__(config, state={}).
func New(cfg config.Map, initial map[string]interface{}, emit EventEmitter) (*Display, error) {
	if emit == nil {
		emit = noopEmitter{}
	}

	impl, err := hw.NewDriver(cfg)
	if err != nil {
		return nil, err
	}

	v, err := view.New(cfg, impl, initial, emit)
	if err != nil {
		return nil, err
	}

	displayCfg := displayConfig(cfg)
	enabled, _ := displayCfg["enabled"].(bool)
	rotation := intFieldOr(displayCfg, "rotation", 0)
	onFrame, _ := onFrameConfig(cfg)

	d := &Display{
		View:       v,
		impl:       impl,
		enabled:    enabled,
		rotation:   rotation,
		onFrame:    onFrame,
		emit:       emit,
		canvasNext: make(chan *image.Gray, 1),
		stop:       make(chan struct{}),
	}

	if err := d.initDisplay(); err != nil {
		return nil, err
	}

	go d.renderThread()

	return d, nil
}

func displayConfig(cfg config.Map) config.Map {
	ui, _ := cfg["ui"].(config.Map)
	if ui == nil {
		return config.Map{}
	}
	dCfg, _ := ui["display"].(config.Map)
	if dCfg == nil {
		return config.Map{}
	}
	return dCfg
}

func onFrameConfig(cfg config.Map) (string, bool) {
	ui, _ := cfg["ui"].(config.Map)
	if ui == nil {
		return "", false
	}
	web, _ := ui["web"].(config.Map)
	if web == nil {
		return "", false
	}
	s, ok := web["on_frame"].(string)
	return s, ok
}

func intFieldOr(m config.Map, key string, def int) int {
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

func (d *Display) implName() string { return d.impl.Name() }

// initDisplay ports Display.init_display.
func (d *Display) initDisplay() error {
	if d.enabled {
		if err := d.impl.Initialize(); err != nil {
			return err
		}
		d.emit.On("display_setup", d.impl)
	} else {
		log.Print("display module is disabled")
	}
	d.OnRender(d.onViewRendered)
	return nil
}

// Clear ports Display.clear.
func (d *Display) Clear() error { return d.impl.Clear() }

// Image ports Display.image(): the last-rendered canvas, rotated if
// configured. NOTE: Go's image package has no built-in arbitrary-angle
// rotate-with-expand the way Pillow's Image.rotate does; only axis-aligned
// (0/90/180/270) rotation is implemented natively here, since pwnagotchi's
// own config only ever uses multiples of 90 (see view.py's own
// `(self._rotation/90)%2` check, which would itself misbehave for a
// non-multiple-of-90 rotation) — see docs/known-differences.md.
func (d *Display) Image() *image.Gray {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.canvas == nil {
		return nil
	}
	if d.rotation == 0 {
		return d.canvas
	}
	return rotate90Multiple(d.canvas, d.rotation)
}

func rotate90Multiple(src *image.Gray, degrees int) *image.Gray {
	steps := ((degrees/90)%4 + 4) % 4
	img := src
	for i := 0; i < steps; i++ {
		img = rotate90(img)
	}
	return img
}

func rotate90(src *image.Gray) *image.Gray {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewGray(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.SetGray(h-1-y, x, src.GrayAt(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// renderThread ports Display._render_thread: consumes canvases pushed by
// onViewRendered and calls the real driver's Render, off the goroutine that
// produced the frame (matching Python's dedicated "Renderer" thread +
// threading.Event handoff).
func (d *Display) renderThread() {
	for {
		select {
		case <-d.stop:
			return
		case canvas := <-d.canvasNext:
			if err := d.impl.Render(canvas); err != nil {
				log.Printf("display: render error: %v", err)
			}
		}
	}
}

// Stop ends the render-thread goroutine (an addition for clean shutdown;
// Python's daemon thread just dies with the process).
func (d *Display) Stop() { close(d.stop) }

// onViewRendered ports Display._on_view_rendered(img).
func (d *Display) onViewRendered(img *image.Gray) {
	if d.onFrame != "" {
		if err := runOnFrame(d.onFrame); err != nil {
			log.Print(err)
		}
	}

	if !d.enabled {
		return
	}

	rotated := img
	if d.rotation != 0 {
		rotated = rotate90Multiple(img, -d.rotation)
	}

	d.mu.Lock()
	d.canvas = rotated
	d.mu.Unlock()

	select {
	case d.canvasNext <- rotated:
	default:
		// Drain a stale pending frame and push the latest — matches
		// Python's threading.Event semantics (only the MOST RECENT canvas
		// before the renderer thread wakes up matters; Event.set() on an
		// already-set event is a no-op, so an unconsumed earlier canvas is
		// silently superseded there too).
		select {
		case <-d.canvasNext:
		default:
		}
		d.canvasNext <- rotated
	}
}

// runOnFrame mirrors `os.system(self._config['ui']['web']['on_frame'])`:
// this is the one place in the UI subsystem where an arbitrary,
// operator-configured SHELL command is genuinely intended (the config
// value is meant to be a shell command line, potentially with pipes/
// redirects) — so, unlike every other external-command call site in this
// port, a real shell IS used here deliberately, via `sh -c`, matching
// os.system's own semantics exactly rather than trying to parse the
// command into an argv (which would silently break any operator's
// on_frame command using shell features).
func runOnFrame(cmd string) error {
	c := exec.Command("sh", "-c", cmd)
	if err := c.Run(); err != nil {
		return fmt.Errorf("display: on_frame command failed: %w", err)
	}
	return nil
}

// IsWaveshareAny ports Display.is_waveshare_any() EXACTLY, including a
// real Python bug: `self.is_waveshare_v3` (missing "()") references the
// BOUND METHOD OBJECT, not its call result — which is always truthy in a
// boolean context. So the real `or` chain is
// `v1() or v2() or v4() or <always-truthy method object>`, meaning this
// method is confirmed to ALWAYS return a truthy value in Python regardless
// of the actual configured display. Verified against the real interpreter.
// Preserved as always-true here, not "fixed" to also check v3.
func (d *Display) IsWaveshareAny() bool { return true }
