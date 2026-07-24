package mesh

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/grid"
)

type fakeAdvertiserView struct {
	stateChangeEvent string
	stateChangeCB    func(old, new interface{})
	newPeers         []*Peer
	lostPeers        []*Peer
}

func (v *fakeAdvertiserView) OnStateChange(event string, cb func(old, new interface{})) {
	v.stateChangeEvent = event
	v.stateChangeCB = cb
}
func (v *fakeAdvertiserView) OnNewPeer(p *Peer)  { v.newPeers = append(v.newPeers, p) }
func (v *fakeAdvertiserView) OnLostPeer(p *Peer) { v.lostPeers = append(v.lostPeers, p) }

type fakeEmitter struct{ events []string }

func (e *fakeEmitter) On(event string, args ...interface{}) { e.events = append(e.events, event) }

func testAdvertiserConfig(advertise bool) config.Map {
	return config.Map{
		"personality": config.Map{
			"advertise":              advertise,
			"bond_encounters_factor": int64(20000),
		},
		"bettercap": config.Map{
			"handshakes": "/nonexistent/handshakes/dir",
		},
	}
}

func TestNewAsyncAdvertiserBuildsInitialAdvertisement(t *testing.T) {
	a := NewAsyncAdvertiser(testAdvertiserConfig(true), &fakeAdvertiserView{}, "pwnagotchi", "2.9.5.5", "fp123")
	if a.GetFingerprint() != "fp123" {
		t.Fatalf("GetFingerprint() = %q", a.GetFingerprint())
	}
	if a.advertisement["name"] != "pwnagotchi" || a.advertisement["identity"] != "fp123" {
		t.Fatalf("advertisement = %v", a.advertisement)
	}
	if a.advertisement["face"] != DefaultFriendFace {
		t.Fatalf("default face = %v", a.advertisement["face"])
	}
}

func TestStartAdvertisingDisabledLogsAndDoesNothing(t *testing.T) {
	view := &fakeAdvertiserView{}
	a := NewAsyncAdvertiser(testAdvertiserConfig(false), view, "pwnagotchi", "2.9.5.5", "fp123")
	a.StartAdvertising(context.Background())
	if view.stateChangeEvent != "" {
		t.Fatal("disabled advertising must not register the face state-change callback")
	}
}

func TestStartAdvertisingCallsGridAndRegistersFaceCallback(t *testing.T) {
	var gotAdvertise, gotSetData bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/mesh/true" {
			gotAdvertise = true
		}
		if r.URL.Path == "/api/v1/mesh/data" {
			gotSetData = true
		}
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer srv.Close()

	gridClient := grid.NewClient("2.9.5.5")
	gridClient.APIAddress = srv.URL + "/api/v1"

	view := &fakeAdvertiserView{}
	a := NewAsyncAdvertiser(testAdvertiserConfig(true), view, "pwnagotchi", "2.9.5.5", "fp123")
	a.Grid = gridClient

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.StartAdvertising(ctx)

	if !gotAdvertise || !gotSetData {
		t.Fatalf("gotAdvertise=%v gotSetData=%v", gotAdvertise, gotSetData)
	}
	if view.stateChangeEvent != "face" {
		t.Fatalf("stateChangeEvent = %q, want face", view.stateChangeEvent)
	}

	view.stateChangeCB(nil, "(o_o)")
	if a.advertisement["face"] != "(o_o)" {
		t.Fatalf("face not updated: %v", a.advertisement["face"])
	}
}

func TestReconcilePeersDetectsNewAndLostAndUpdated(t *testing.T) {
	view := &fakeAdvertiserView{}
	emitter := &fakeEmitter{}
	a := NewAsyncAdvertiser(testAdvertiserConfig(true), view, "pwnagotchi", "2.9.5.5", "fp123")
	a.View = view
	a.Emit = emitter

	peerA := map[string]interface{}{
		"encounters":    float64(1),
		"advertisement": map[string]interface{}{"identity": "peerA", "name": "alice"},
	}
	peerB := map[string]interface{}{
		"encounters":    float64(1),
		"advertisement": map[string]interface{}{"identity": "peerB", "name": "bob"},
	}

	a.reconcilePeers([]interface{}{peerA, peerB})
	if a.PeerCount() != 2 {
		t.Fatalf("PeerCount() = %d, want 2", a.PeerCount())
	}
	if len(view.newPeers) != 2 {
		t.Fatalf("expected 2 new-peer callbacks, got %d", len(view.newPeers))
	}
	if a.ClosestPeer() == nil || a.ClosestPeer().Identity() != "peerA" {
		t.Fatalf("ClosestPeer() = %v, want peerA (first in the list)", a.ClosestPeer())
	}

	// Second poll: peerA is gone, peerB gets more encounters (update, not new).
	peerBUpdated := map[string]interface{}{
		"encounters":    float64(5),
		"advertisement": map[string]interface{}{"identity": "peerB", "name": "bob"},
	}
	a.reconcilePeers([]interface{}{peerBUpdated})

	if a.PeerCount() != 1 {
		t.Fatalf("PeerCount() = %d, want 1 after peerA drops out", a.PeerCount())
	}
	if len(view.lostPeers) != 1 || view.lostPeers[0].Identity() != "peerA" {
		t.Fatalf("lostPeers = %v, want [peerA]", view.lostPeers)
	}
	if a.TotalEncounters() != 5 {
		t.Fatalf("TotalEncounters() = %v, want 5 (peerB updated in place)", a.TotalEncounters())
	}

	foundDetected, foundLost := false, false
	for _, e := range emitter.events {
		if e == "peer_detected" {
			foundDetected = true
		}
		if e == "peer_lost" {
			foundLost = true
		}
	}
	if !foundDetected || !foundLost {
		t.Fatalf("events = %v, want both peer_detected and peer_lost", emitter.events)
	}
}

func TestAdvPollerRespectsContextCancellation(t *testing.T) {
	a := NewAsyncAdvertiser(testAdvertiserConfig(true), &fakeAdvertiserView{}, "pwnagotchi", "2.9.5.5", "fp123")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: advPoller must return almost immediately

	done := make(chan struct{})
	go func() {
		a.advPoller(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("advPoller did not exit promptly on cancellation")
	}
}
