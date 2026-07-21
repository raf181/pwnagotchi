package automata

import (
	"testing"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
	"github.com/jayofelony/pwnagotchi/go-port/internal/epoch"
)

type fakeView struct {
	calls []string
	waits []struct {
		t        float64
		sleeping bool
	}
	missed []string
}

func (v *fakeView) OnStarting()  { v.calls = append(v.calls, "starting") }
func (v *fakeView) OnGrateful()  { v.calls = append(v.calls, "grateful") }
func (v *fakeView) OnLonely()    { v.calls = append(v.calls, "lonely") }
func (v *fakeView) OnBored()     { v.calls = append(v.calls, "bored") }
func (v *fakeView) OnSad()       { v.calls = append(v.calls, "sad") }
func (v *fakeView) OnAngry()     { v.calls = append(v.calls, "angry") }
func (v *fakeView) OnExcited()   { v.calls = append(v.calls, "excited") }
func (v *fakeView) OnRebooting() { v.calls = append(v.calls, "rebooting") }
func (v *fakeView) OnMiss(who string) {
	v.calls = append(v.calls, "miss:"+who)
	v.missed = append(v.missed, who)
}
func (v *fakeView) Wait(t float64, sleeping bool) {
	v.waits = append(v.waits, struct {
		t        float64
		sleeping bool
	}{t, sleeping})
}

type fakeEmitter struct {
	events []string
}

func (e *fakeEmitter) On(event string, args ...interface{}) {
	e.events = append(e.events, event)
}

type fakePeers struct {
	total float64
	count int
}

func (p *fakePeers) TotalEncounters() float64 { return p.total }
func (p *fakePeers) PeerCount() int           { return p.count }

func testConfig() config.Map {
	return config.Map{
		"personality": config.Map{
			"bond_encounters_factor": int64(20000),
			"sad_num_epochs":         int64(25),
			"bored_num_epochs":       int64(15),
			"excited_num_epochs":     int64(10),
			"max_misses_for_recon":   int64(5),
		},
		"main": config.Map{
			"mon_max_blind_epochs": int64(3),
		},
	}
}

func newTestAutomata() (*Automata, *fakeView, *fakeEmitter, *fakePeers) {
	view := &fakeView{}
	a := New(testConfig(), view)
	emitter := &fakeEmitter{}
	peers := &fakePeers{}
	a.Emit = emitter
	a.Peers = peers
	return a, view, emitter, peers
}

func TestInGoodMoodThreshold(t *testing.T) {
	a, _, _, peers := newTestAutomata()
	if a.InGoodMood() {
		t.Fatal("no encounters yet: should not be in a good mood")
	}
	peers.total = 20000 // == bond_encounters_factor -> support_factor == 1.0
	if !a.InGoodMood() {
		t.Fatal("support_factor >= 1.0 should be a good mood")
	}
}

func TestSetLonelyDefersToGratefulWithSupport(t *testing.T) {
	a, view, emitter, peers := newTestAutomata()
	peers.total = 20000 // enough support -> grateful instead of lonely
	a.SetLonely()
	if len(view.calls) != 1 || view.calls[0] != "grateful" {
		t.Fatalf("view calls = %v, want [grateful]", view.calls)
	}
	if len(emitter.events) != 1 || emitter.events[0] != "grateful" {
		t.Fatalf("events = %v, want [grateful]", emitter.events)
	}
}

func TestSetLonelyWithoutSupport(t *testing.T) {
	a, view, emitter, _ := newTestAutomata()
	a.SetLonely()
	if len(view.calls) != 1 || view.calls[0] != "lonely" {
		t.Fatalf("view calls = %v, want [lonely]", view.calls)
	}
	if len(emitter.events) != 1 || emitter.events[0] != "lonely" {
		t.Fatalf("events = %v, want [lonely]", emitter.events)
	}
}

func TestOnMissTracksAndNotifiesView(t *testing.T) {
	a, view, _, _ := newTestAutomata()
	a.OnMiss("aa:bb:cc:dd:ee:ff")
	if a.Epoch.NumMissed != 1 {
		t.Fatalf("NumMissed = %d, want 1", a.Epoch.NumMissed)
	}
	if len(view.missed) != 1 || view.missed[0] != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("view.missed = %v", view.missed)
	}
}

func TestOnErrorUnknownBSSIDBecomesMiss(t *testing.T) {
	a, view, _, _ := newTestAutomata()
	err := fmtErrorf("error 400: 50:c7:bf:2e:d3:37 is an unknown BSSID or it is in the association skip list.")
	a.OnError("50:c7:bf:2e:d3:37", err)
	if len(view.missed) != 1 {
		t.Fatalf("expected the unknown-BSSID error to be treated as a miss, view.missed=%v", view.missed)
	}
}

func TestOnErrorOtherErrorIsNotAMiss(t *testing.T) {
	a, view, _, _ := newTestAutomata()
	a.OnError("aa:bb", fmtErrorf("some other bettercap error"))
	if len(view.missed) != 0 {
		t.Fatalf("non-BSSID errors must not be treated as misses, got %v", view.missed)
	}
}

func TestWaitForTracksSleepAndEmitsCorrectEvent(t *testing.T) {
	a, view, emitter, _ := newTestAutomata()
	a.WaitFor(2.5, true)
	if len(view.waits) != 1 || view.waits[0].t != 2.5 || !view.waits[0].sleeping {
		t.Fatalf("view.waits = %v", view.waits)
	}
	if len(emitter.events) != 1 || emitter.events[0] != "sleep" {
		t.Fatalf("events = %v, want [sleep]", emitter.events)
	}
	if a.Epoch.NumSlept != 2.5 {
		t.Fatalf("NumSlept = %v, want 2.5", a.Epoch.NumSlept)
	}

	a.WaitFor(1.0, false)
	if emitter.events[1] != "wait" {
		t.Fatalf("second event = %q, want wait", emitter.events[1])
	}
}

func TestIsStaleUsesStrictGreaterThan(t *testing.T) {
	a, _, _, _ := newTestAutomata()
	for i := 0; i < 5; i++ {
		a.Epoch.Track(epoch.TrackOptions{Miss: true})
	}
	if a.IsStale() {
		t.Fatal("num_missed == max_misses_for_recon (5) should NOT be stale (strict >, not >=)")
	}
	a.Epoch.Track(epoch.TrackOptions{Miss: true})
	if !a.IsStale() {
		t.Fatal("num_missed > max_misses_for_recon (6 > 5) should be stale")
	}
}

func TestNextEpochRestartsAfterBlindThreshold(t *testing.T) {
	a, _, emitter, _ := newTestAutomata()
	restarted := false
	a.Restart = func(mode string) {
		restarted = true
		if mode != "AUTO" {
			t.Fatalf("Restart mode = %q, want AUTO (automata.py's next_epoch calls self._restart() with no args, i.e. the default)", mode)
		}
	}

	// Drive 3 blind epochs (mon_max_blind_epochs=3 in testConfig).
	// BlindFor only increments via Epoch.Observe() with zero APs (mirroring
	// the real recon loop calling epoch.observe([], peers) when nothing is
	// visible) — NextEpoch() itself only checks the threshold.
	for i := 0; i < 3; i++ {
		a.Epoch.Observe(nil, nil)
		a.NextEpoch()
	}
	if !restarted {
		t.Fatal("expected Restart to be called after reaching mon_max_blind_epochs")
	}
	if a.Epoch.BlindFor != 0 {
		t.Fatalf("BlindFor should reset to 0 after restart, got %d", a.Epoch.BlindFor)
	}

	foundEpochEvent := false
	for _, e := range emitter.events {
		if e == "epoch" {
			foundEpochEvent = true
		}
	}
	if !foundEpochEvent {
		t.Fatal("expected an 'epoch' plugin event to be emitted each NextEpoch()")
	}
}

func fmtErrorf(s string) error {
	return &simpleError{s}
}

type simpleError struct{ s string }

func (e *simpleError) Error() string { return e.s }
