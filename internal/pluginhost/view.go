// Package pluginhost adapts this daemon's concrete runtime objects (the
// real *view.View/*display.Display, or the headless fallback) into the
// narrow pluginmanager capability interfaces, so cmd/pwnagotchi's main.go
// stays thin wiring and the adapters themselves stay independently
// testable — mirroring the rest of this port's "logic lives in internal/
// packages, main.go just wires them together" convention.
package pluginhost

import "github.com/jayofelony/pwnagotchi/internal/pluginmanager"

// View adapts whatever real view object the daemon constructed (either
// *display.Display, which embeds *view.View and so has Kind/AddTextElement/
// AddLabeledValueElement/HasElement/RemoveElement in addition to Set, or
// *cli.HeadlessView, which only has Set) into pluginmanager.ViewCapability.
// Every display-specific method degrades to a safe no-op/zero-value when
// the underlying object doesn't implement it (headless mode) — a plugin
// calling AddText/Kind/etc. against a headless run gets "no screen, no
// element added" behavior, never a panic, matching a real Pwnagotchi unit
// with `ui.display.enabled: false` where those calls are simply inert.
type View struct {
	// V is the real view/display object: *display.Display or
	// *cli.HeadlessView (or any test fake exposing the same optional
	// method set) — passed as `interface{}` specifically so this package
	// never needs to import internal/ui/view, internal/ui/display, or
	// internal/cli (any of which would make pluginhost depend on the full
	// UI stack just to describe five narrow capability methods).
	V interface{}
}

func (a View) Set(key, value string) {
	if s, ok := a.V.(interface {
		Set(key string, value interface{})
	}); ok {
		s.Set(key, value)
	}
}

func (a View) Update(force bool) {
	if u, ok := a.V.(interface {
		Update(force bool, newData map[string]interface{})
	}); ok {
		u.Update(force, nil)
	}
}

func (a View) Kind() string {
	if k, ok := a.V.(interface{ Kind() string }); ok {
		return k.Kind()
	}
	return ""
}

func (a View) HasElement(key string) bool {
	if h, ok := a.V.(interface{ HasElement(key string) bool }); ok {
		return h.HasElement(key)
	}
	return false
}

func (a View) RemoveElement(key string) {
	if r, ok := a.V.(interface{ RemoveElement(key string) }); ok {
		r.RemoveElement(key)
	}
}

func (a View) AddText(key, value string, x, y int, font pluginmanager.FontStyle, wrap bool, maxLength int) {
	if t, ok := a.V.(interface {
		AddTextElement(key, value string, x, y int, font string, wrap bool, maxLength int)
	}); ok {
		t.AddTextElement(key, value, x, y, string(font), wrap, maxLength)
	}
}

func (a View) AddLabeledValue(key, label, value string, x, y int, labelFont, valueFont pluginmanager.FontStyle, labelSpacing int) {
	if lv, ok := a.V.(interface {
		AddLabeledValueElement(key, label, value string, x, y int, labelFont, valueFont string, labelSpacing int)
	}); ok {
		lv.AddLabeledValueElement(key, label, value, x, y, string(labelFont), string(valueFont), labelSpacing)
	}
}

func (a View) OnUploading(to string) {
	if u, ok := a.V.(interface{ OnUploading(to string) }); ok {
		u.OnUploading(to)
	}
}

func (a View) OnNormal() {
	if n, ok := a.V.(interface{ OnNormal() }); ok {
		n.OnNormal()
	}
}

func (a View) Width() int {
	if w, ok := a.V.(interface{ Width() int }); ok {
		return w.Width()
	}
	return 0
}

func (a View) Height() int {
	if h, ok := a.V.(interface{ Height() int }); ok {
		return h.Height()
	}
	return 0
}

var _ pluginmanager.ViewCapability = View{}
