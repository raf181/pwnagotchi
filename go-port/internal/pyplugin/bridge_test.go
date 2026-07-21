package pyplugin

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/go-port/internal/mesh"
)

type fakeAgent struct{ mu int }

func TestToJSONArgPassesThroughPlainData(t *testing.T) {
	ap := map[string]interface{}{"mac": "AA:BB", "channel": 6}
	got := toJSONArg(ap)
	m, ok := got.(map[string]interface{})
	if !ok || m["mac"] != "AA:BB" {
		t.Fatalf("expected plain map to pass through unchanged, got %#v", got)
	}
}

func TestToJSONArgHandlesSliceOfAPs(t *testing.T) {
	aps := []interface{}{
		map[string]interface{}{"mac": "AA"},
		map[string]interface{}{"mac": "BB"},
	}
	got := toJSONArg(aps)
	s, ok := got.([]interface{})
	if !ok || len(s) != 2 {
		t.Fatalf("expected 2-element slice to pass through, got %#v", got)
	}
}

func TestToJSONArgStubsOpaqueGoObjects(t *testing.T) {
	got := toJSONArg(&fakeAgent{})
	m, ok := got.(map[string]interface{})
	if !ok {
		t.Fatalf("expected a goref marker map, got %#v", got)
	}
	if m["__goref__"] != "fakeAgent" {
		t.Fatalf("expected __goref__ = fakeAgent, got %#v", m)
	}

	// Must round-trip through real JSON the way bridge.py will actually
	// receive it, not just as a Go map literal.
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back map[string]interface{}
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back["__goref__"] != "fakeAgent" {
		t.Fatalf("round-trip lost __goref__: %s", data)
	}
}

func TestToJSONArgUnwrapsMeshPeerToRealAdvDict(t *testing.T) {
	p := &mesh.Peer{
		LastSeen: time.Now(),
		Adv:      map[string]interface{}{"name": "unit-friend", "identity": "abc123"},
	}
	got := toJSONArg(p)
	m, ok := got.(map[string]interface{})
	if !ok {
		t.Fatalf("expected the real .Adv dict, got %#v", got)
	}
	if m["name"] != "unit-friend" || m["identity"] != "abc123" {
		t.Fatalf("expected real advertisement fields preserved, got %#v", m)
	}
}

func TestToJSONArgPassesThroughScalars(t *testing.T) {
	for _, v := range []interface{}{"hello", 42, 3.14, true, nil} {
		if got := toJSONArg(v); got != v {
			// nil == nil comparison is fine; others compare by value.
			if v != nil {
				t.Errorf("toJSONArg(%#v) = %#v, want unchanged", v, got)
			}
		}
	}
}
