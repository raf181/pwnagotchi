// Package state ports pwnagotchi/ui/state.py: the thread-safe key/value
// store of UI elements (Widgets, in internal/ui/components terms) backing
// the display's current frame.
package state

import "sync"

// Element is the subset of internal/ui/components.Widget's contract State
// needs: a mutable Value box. Any concrete component (Text, LabeledValue,
// ...) implements this by exposing its Value field through these methods.
type Element interface {
	Value() interface{}
	SetValue(v interface{})
}

// Listener mirrors a State change-listener callback: (old, new value).
type Listener func(old, new interface{})

// State ports state.State.
type State struct {
	mu        sync.Mutex
	state     map[string]Element
	listeners map[string]Listener
	changes   map[string]bool
}

// New ports State.__init__(state={}). Callers may pass an initial element
// map (Python allows passing a pre-populated `state` dict); pass nil for an
// empty one.
func New(initial map[string]Element) *State {
	if initial == nil {
		initial = map[string]Element{}
	}
	return &State{
		state:     initial,
		listeners: map[string]Listener{},
		changes:   map[string]bool{},
	}
}

// AddElement ports State.add_element.
func (s *State) AddElement(key string, elem Element) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state[key] = elem
	s.changes[key] = true
}

// HasElement ports State.has_element.
func (s *State) HasElement(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.state[key]
	return ok
}

// RemoveElement ports State.remove_element.
func (s *State) RemoveElement(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.state, key)
	s.changes[key] = true
}

// AddListener ports State.add_listener.
func (s *State) AddListener(key string, cb Listener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners[key] = cb
}

// Items ports State.items(): a point-in-time snapshot of key->Element.
func (s *State) Items() map[string]Element {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Element, len(s.state))
	for k, v := range s.state {
		out[k] = v
	}
	return out
}

// Get ports State.get: the element's current Value, or nil if key doesn't
// exist (matching Python returning None).
func (s *State) Get(key string) interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.state[key]; ok {
		return e.Value()
	}
	return nil
}

// Reset ports State.reset.
func (s *State) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.changes = map[string]bool{}
}

// Changes ports State.changes(ignore=()).
func (s *State) Changes(ignore ...string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ignoreSet := make(map[string]bool, len(ignore))
	for _, k := range ignore {
		ignoreSet[k] = true
	}
	out := make([]string, 0, len(s.changes))
	for k := range s.changes {
		if !ignoreSet[k] {
			out = append(out, k)
		}
	}
	return out
}

// HasChanges ports State.has_changes.
func (s *State) HasChanges() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.changes) > 0
}

// Set ports State.set: updates key's element Value if key exists (a no-op
// otherwise — Python's `if key in self._state` guard, NOT a KeyError),
// recording a change and firing the listener only if the value actually
// changed.
func (s *State) Set(key string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.state[key]
	if !ok {
		return
	}
	prev := e.Value()
	e.SetValue(value)

	if prev != value {
		s.changes[key] = true
		if l, ok := s.listeners[key]; ok && l != nil {
			l(prev, value)
		}
	}
}
