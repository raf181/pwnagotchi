package mesh

import (
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
)

func TestParseRFC3339ZeroSentinel(t *testing.T) {
	got, err := ParseRFC3339("0001-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(got) > time.Minute {
		t.Fatalf("zero-sentinel should resolve to ~now, got %v", got)
	}
}

// TestParseRFC3339DropsFractionalAndTrailingZ replicates a real, verified
// Python quirk: parse_rfc3339 does dt.split('.')[0] BEFORE parsing with a
// bare (no timezone) layout. A timestamp WITH fractional seconds succeeds
// because the trailing 'Z' is dropped along with the fraction by the
// split; a timestamp WITHOUT fractional seconds FAILS because the 'Z' is
// left in place and the no-timezone layout can't consume it. Both
// behaviors (the accidental success and the accidental failure) must be
// replicated, not "fixed".
func TestParseRFC3339DropsFractionalAndTrailingZ(t *testing.T) {
	got, err := ParseRFC3339("2024-03-05T12:30:45.123456Z")
	if err != nil {
		t.Fatalf("with fractional seconds: unexpected error %v (Python succeeds here)", err)
	}
	want := time.Date(2024, 3, 5, 12, 30, 45, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	_, err = ParseRFC3339("2024-03-05T12:30:45Z")
	if err == nil {
		t.Fatal("without fractional seconds: expected an error (Python's strptime fails on the unconsumed trailing 'Z' here too)")
	}
}

func TestNewPeerDefaults(t *testing.T) {
	p := NewPeer(map[string]interface{}{})
	if p.Name() != "???" || p.Identity() != "???" {
		t.Fatalf("defaults: name=%q identity=%q", p.Name(), p.Identity())
	}
	if p.FullName() != "???@???" {
		t.Fatalf("FullName() = %q", p.FullName())
	}
	if p.Face() != DefaultFriendFace {
		t.Fatalf("Face() = %q, want default", p.Face())
	}
	if p.Version() != "1.0.0a" {
		t.Fatalf("Version() = %q", p.Version())
	}
	if p.LastChannel != 1 {
		t.Fatalf("LastChannel = %d, want 1", p.LastChannel)
	}
	if p.Encounters != 0 {
		t.Fatalf("Encounters = %v, want 0", p.Encounters)
	}
}

func TestNewPeerFromFullObject(t *testing.T) {
	obj := map[string]interface{}{
		"encounters":   float64(42),
		"session_id":   "sess-1",
		"channel":      float64(6),
		"rssi":         float64(-40),
		"met_at":       "2024-01-01T00:00:00.000000Z",
		"detected_at":  "2024-01-02T00:00:00.000000Z",
		"prev_seen_at": "2024-01-03T00:00:00.000000Z",
		"advertisement": map[string]interface{}{
			"name":     "unit1",
			"identity": "abc123",
			"face":     "(-_-)",
			"version":  "2.9.5.5",
			"pwnd_run": float64(3),
			"pwnd_tot": float64(30),
			"uptime":   float64(1000),
			"epoch":    float64(7),
		},
	}
	p := NewPeer(obj)
	if p.Encounters != 42 || p.SessionID != "sess-1" || p.LastChannel != 6 || p.RSSI != -40 {
		t.Fatalf("basic fields wrong: %+v", p)
	}
	if p.Name() != "unit1" || p.Identity() != "abc123" || p.Face() != "(-_-)" {
		t.Fatalf("advertisement fields wrong: name=%q identity=%q face=%q", p.Name(), p.Identity(), p.Face())
	}
	if p.PwndRun() != 3 || p.PwndTotal() != 30 || p.Uptime() != 1000 || p.Epoch() != 7 {
		t.Fatalf("advertisement numeric fields wrong: %+v", p)
	}
	if !p.FirstMet.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("FirstMet = %v", p.FirstMet)
	}
}

func TestPeerFirstEncounterAndGoodFriend(t *testing.T) {
	p := NewPeer(map[string]interface{}{"encounters": float64(1)})
	if !p.FirstEncounter() {
		t.Fatal("encounters==1 should be a first encounter")
	}
	cfg := config.Map{"personality": config.Map{"bond_encounters_factor": int64(20000)}}
	if p.IsGoodFriend(cfg) {
		t.Fatal("1 encounter should not be a good friend yet")
	}
	p.Encounters = 20000
	if !p.IsGoodFriend(cfg) {
		t.Fatal("encounters >= bond_encounters_factor should be a good friend")
	}
}

func TestPeerIsCloser(t *testing.T) {
	a := &Peer{RSSI: -30}
	b := &Peer{RSSI: -50}
	if !a.IsCloser(b) {
		t.Fatal("higher (less negative) RSSI should be closer")
	}
	if b.IsCloser(a) {
		t.Fatal("lower RSSI should not be closer")
	}
}

func TestPeerUpdate(t *testing.T) {
	p1 := NewPeer(map[string]interface{}{
		"encounters": float64(1),
		"session_id": "s1",
		"advertisement": map[string]interface{}{
			"name": "alice", "identity": "id1",
		},
	})
	p2 := NewPeer(map[string]interface{}{
		"encounters": float64(2),
		"session_id": "s2",
		"advertisement": map[string]interface{}{
			"name": "bob", "identity": "id1",
		},
	})
	p1.Update(p2)
	if p1.Encounters != 2 || p1.SessionID != "s2" || p1.Name() != "bob" {
		t.Fatalf("Update did not merge fields: %+v", p1)
	}
}
