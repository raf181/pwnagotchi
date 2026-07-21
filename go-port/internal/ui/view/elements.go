package view

import (
	"image/draw"

	"github.com/jayofelony/pwnagotchi/go-port/internal/ui/components"
)

// sceneElement is what View's internal state.State actually stores: an
// object that is BOTH a state.Element (Value/SetValue, for the generic
// view.Set(key, value) API) and a components.Widget (Draw, for rendering).
// Every concrete wrapper below implements both.
type sceneElement interface {
	Value() interface{}
	SetValue(v interface{})
	Draw(canvas draw.Image) error
}

// textElement wraps components.Text, adding Python's "value can be None,
// meaning don't draw at all" semantics — components.Text.Value is a plain
// (non-nullable) Go string, so the None state is tracked alongside it here
// rather than baked into components.Text itself, keeping that package
// simple for the (more common) always-has-a-value case.
type textElement struct {
	widget   *components.Text
	hasValue bool
}

func newTextElement(w *components.Text, initial *string) *textElement {
	e := &textElement{widget: w}
	if initial != nil {
		w.Value = *initial
		e.hasValue = true
	}
	return e
}

func (e *textElement) Value() interface{} {
	if !e.hasValue {
		return nil
	}
	return e.widget.Value
}

func (e *textElement) SetValue(v interface{}) {
	if v == nil {
		e.hasValue = false
		e.widget.Value = ""
		return
	}
	if s, ok := v.(string); ok {
		e.widget.Value = s
		e.hasValue = true
	}
}

func (e *textElement) Draw(canvas draw.Image) error {
	if !e.hasValue {
		return nil
	}
	return e.widget.Draw(canvas)
}

// labeledValueElement wraps components.LabeledValue similarly (its Value
// is always a string in stock usage — 'channel'/'aps'/'uptime'/'shakes'
// are never set to None anywhere in view.py — so no None-tracking needed).
type labeledValueElement struct {
	widget *components.LabeledValue
}

func (e *labeledValueElement) Value() interface{} { return e.widget.Value }
func (e *labeledValueElement) SetValue(v interface{}) {
	if s, ok := v.(string); ok {
		e.widget.Value = s
	}
}
func (e *labeledValueElement) Draw(canvas draw.Image) error { return e.widget.Draw(canvas) }

// structuralElement wraps a Widget with no meaningful settable Value
// (Line/Rect/FilledRect/Bitmap) — Python's State.set() would raise
// AttributeError if anything ever called view.set() on one of these keys
// (none of view.py's own code does); Go instead silently no-ops on
// SetValue, since there is no real call site to preserve a crash for.
type structuralElement struct {
	widget components.Widget
}

func (e *structuralElement) Value() interface{}           { return nil }
func (e *structuralElement) SetValue(v interface{})       {}
func (e *structuralElement) Draw(canvas draw.Image) error { return e.widget.Draw(canvas) }
