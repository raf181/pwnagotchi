package state

import "testing"

type fakeElement struct{ v interface{} }

func (e *fakeElement) Value() interface{}     { return e.v }
func (e *fakeElement) SetValue(v interface{}) { e.v = v }

func TestAddHasRemoveElement(t *testing.T) {
	s := New(nil)
	if s.HasElement("channel") {
		t.Fatal("should not have channel yet")
	}
	s.AddElement("channel", &fakeElement{v: "1"})
	if !s.HasElement("channel") {
		t.Fatal("should have channel now")
	}
	if s.Get("channel") != "1" {
		t.Fatalf("Get(channel) = %v", s.Get("channel"))
	}
	s.RemoveElement("channel")
	if s.HasElement("channel") {
		t.Fatal("channel should be removed")
	}
}

func TestSetOnMissingKeyIsNoop(t *testing.T) {
	s := New(nil)
	s.Set("nonexistent", "value") // must not panic
	if s.HasChanges() {
		t.Fatal("setting a nonexistent key must not register a change")
	}
}

func TestSetOnlyFiresListenerAndChangeWhenValueActuallyChanges(t *testing.T) {
	s := New(nil)
	s.AddElement("aps", &fakeElement{v: "0"})
	s.Reset() // clear the add_element-triggered change

	var gotOld, gotNew interface{}
	calls := 0
	s.AddListener("aps", func(old, new interface{}) {
		calls++
		gotOld, gotNew = old, new
	})

	s.Set("aps", "0") // same value: no change, no listener call
	if s.HasChanges() || calls != 0 {
		t.Fatalf("setting the same value should not register a change or fire the listener, changes=%v calls=%d", s.Changes(), calls)
	}

	s.Set("aps", "5")
	if !s.HasChanges() || calls != 1 {
		t.Fatalf("expected exactly one change/listener call, got changes=%v calls=%d", s.Changes(), calls)
	}
	if gotOld != "0" || gotNew != "5" {
		t.Fatalf("listener args = (%v, %v), want (0, 5)", gotOld, gotNew)
	}
}

func TestChangesIgnoresListedKeys(t *testing.T) {
	s := New(nil)
	s.AddElement("a", &fakeElement{v: "x"})
	s.AddElement("b", &fakeElement{v: "y"})

	changes := s.Changes("a")
	if len(changes) != 1 || changes[0] != "b" {
		t.Fatalf("Changes(ignore a) = %v, want [b]", changes)
	}
}

func TestItemsReturnsSnapshot(t *testing.T) {
	s := New(nil)
	s.AddElement("a", &fakeElement{v: "1"})
	items := s.Items()
	if len(items) != 1 {
		t.Fatalf("Items() = %v", items)
	}
	s.AddElement("b", &fakeElement{v: "2"})
	if len(items) != 1 {
		t.Fatal("Items() snapshot must not reflect later mutations")
	}
}
