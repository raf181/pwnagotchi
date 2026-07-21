package view

import (
	"image"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
	"github.com/jayofelony/pwnagotchi/go-port/internal/mesh"
	"github.com/jayofelony/pwnagotchi/go-port/internal/ui/hw"
)

func testConfig() config.Map {
	return config.Map{
		"main": config.Map{"lang": "en"},
		"ui": config.Map{
			"fps":    float64(0), // avoid the background refresh goroutine in most tests
			"cursor": true,
			"faces":  config.Map{"position_x": int64(0), "position_y": int64(40), "png": false},
			"display": config.Map{
				"type": "dummydisplay",
			},
		},
		"bettercap":   config.Map{"handshakes": "/nonexistent"},
		"personality": config.Map{"bond_encounters_factor": int64(20000)},
	}
}

func newTestView(t *testing.T) *View {
	t.Helper()
	cfg := testConfig()
	driver, err := hw.NewDriver(cfg)
	if err != nil {
		t.Fatal(err)
	}
	v, err := New(cfg, driver, map[string]interface{}{"name": "pwnagotchi>"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if v.fps > 0 {
			v.Stop()
		}
	})
	return v
}

func TestNewViewBuildsInitialState(t *testing.T) {
	v := newTestView(t)
	if v.Get("name") != "pwnagotchi>" {
		t.Fatalf("name = %v, want pwnagotchi> (from the initial override)", v.Get("name"))
	}
	if v.Get("channel") != "00" {
		t.Fatalf("channel default = %v, want 00", v.Get("channel"))
	}
	if v.Get("face") != v.faces.Sleep {
		t.Fatalf("face default = %v, want faces.Sleep", v.Get("face"))
	}
	if v.Width() <= 0 || v.Height() <= 0 {
		t.Fatalf("Width/Height = %d/%d, want positive", v.Width(), v.Height())
	}
}

func TestSetGetRoundTrip(t *testing.T) {
	v := newTestView(t)
	v.Set("channel", "6")
	if v.Get("channel") != "6" {
		t.Fatalf("Get(channel) = %v, want 6", v.Get("channel"))
	}
}

func TestUpdateRendersOnChangeAndSkipsWhenNoChange(t *testing.T) {
	v := newTestView(t)
	renderCount := 0
	v.mu.Lock()
	v.renderCbs = append(v.renderCbs, func(c *image.Gray) { renderCount++ })
	v.mu.Unlock()

	v.Set("channel", "6") // a real change -> should render
	v.Update(false, nil)
	if renderCount != 1 {
		t.Fatalf("renderCount = %d, want 1 after a real change", renderCount)
	}

	v.Update(false, nil) // no changes since last reset -> should NOT render
	if renderCount != 1 {
		t.Fatalf("renderCount = %d, want still 1 when nothing changed", renderCount)
	}

	v.Update(true, nil) // force=true -> always renders
	if renderCount != 2 {
		t.Fatalf("renderCount = %d, want 2 after a forced update", renderCount)
	}
}

func TestIsNormalReplicatesPythonSubstringBug(t *testing.T) {
	v := newTestView(t)

	v.Set("face", v.faces.Awake)
	if !v.IsNormal() {
		t.Fatal("AWAKE should be considered normal")
	}

	v.Set("face", v.faces.Sad)
	if v.IsNormal() {
		t.Fatal("SAD should NOT be considered normal (present in the concatenated string)")
	}
}

func TestOnShutdownFreezesView(t *testing.T) {
	v := newTestView(t)
	v.OnShutdown()
	if !v.Freeze() {
		t.Fatal("expected view to be frozen after OnShutdown")
	}
	// Set() itself still mutates state even when frozen (Python's set()
	// doesn't check self._frozen; only update()/render does).
	v.Set("status", "should still apply")
	if v.Get("status") != "should still apply" {
		t.Fatal("Set() should still mutate the underlying state key even when frozen")
	}
}

func TestSetClosestPeerNilClearsFriendFields(t *testing.T) {
	v := newTestView(t)
	v.SetClosestPeer(nil, 0)
	if v.Get("friend_face") != nil {
		t.Fatalf("friend_face = %v, want nil", v.Get("friend_face"))
	}
	if v.Get("friend_name") != nil {
		t.Fatalf("friend_name = %v, want nil", v.Get("friend_name"))
	}
}

func TestSetClosestPeerWithPeerBuildsSignalBars(t *testing.T) {
	v := newTestView(t)
	peer := mesh.NewPeer(map[string]interface{}{
		"rssi":       float64(-60), // >= -67 -> 4 bars
		"encounters": float64(1),
		"advertisement": map[string]interface{}{
			"name": "unit1", "identity": "abc", "pwnd_run": float64(2), "pwnd_tot": float64(5),
		},
	})
	v.SetClosestPeer(peer, 2)
	name, _ := v.Get("friend_name").(string)
	if name == "" {
		t.Fatal("expected a non-empty friend_name")
	}
	for _, sub := range []string{"▌▌▌▌", "unit1", "2", "5", "of 2"} {
		if !contains(name, sub) {
			t.Fatalf("friend_name = %q, missing expected fragment %q", name, sub)
		}
	}
}

func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestOnNewPeerSleeps3Seconds(t *testing.T) {
	v := newTestView(t)
	peer := mesh.NewPeer(map[string]interface{}{
		"encounters":    float64(1),
		"advertisement": map[string]interface{}{"name": "newbie", "identity": "x"},
	})
	start := time.Now()
	v.OnNewPeer(peer)
	if elapsed := time.Since(start); elapsed < 2900*time.Millisecond {
		t.Fatalf("OnNewPeer returned after %v, want >= ~3s (matches Python's time.sleep(3))", elapsed)
	}
}

func TestOnStartingSetsStatusAndFace(t *testing.T) {
	v := newTestView(t)
	v.OnStarting()
	status, _ := v.Get("status").(string)
	if status == "" {
		t.Fatal("expected non-empty status after OnStarting")
	}
	if !contains(status, "(v") {
		t.Fatalf("status = %q, want it to include the version suffix", status)
	}
}
