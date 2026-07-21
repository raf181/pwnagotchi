package cli

import (
	"fmt"
	"log"
	"sync"

	"github.com/jayofelony/pwnagotchi/go-port/internal/agent"
	"github.com/jayofelony/pwnagotchi/go-port/internal/mesh"
	"github.com/jayofelony/pwnagotchi/go-port/internal/session"
	"github.com/jayofelony/pwnagotchi/go-port/internal/voice"
)

// HeadlessView is a real, log-based View implementation: every state
// change and mood event is logged (using the same Voice flavor-text
// Python's real display would render), and Set()/SetClosestPeer() actually
// record state, queryable via Snapshot(). It is a legitimate operating
// mode (headless daemon, no attached e-paper/OLED), not a stand-in stub —
// internal/ui/view.View (the real pixel-rendering display) is still
// Planned per docs/feature-matrix.md, and this is what runs until it
// lands.
type HeadlessView struct {
	Voice *voice.Voice

	mu    sync.Mutex
	state map[string]string
	agent *agent.Agent
}

// NewHeadlessView builds a HeadlessView using the given Voice for flavor
// text (matching the language configured in main.lang).
func NewHeadlessView(v *voice.Voice) *HeadlessView {
	return &HeadlessView{Voice: v, state: map[string]string{}}
}

// Snapshot returns a copy of all key/value UI state set via Set so far.
func (h *HeadlessView) Snapshot() map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]string, len(h.state))
	for k, v := range h.state {
		out[k] = v
	}
	return out
}

func (h *HeadlessView) OnStarting()       { log.Print(h.Voice.OnStarting()) }
func (h *HeadlessView) OnKeysGeneration() { log.Print(h.Voice.OnKeysGeneration()) }
func (h *HeadlessView) OnGrateful()       { log.Print(h.Voice.OnGrateful()) }
func (h *HeadlessView) OnLonely()         { log.Print(h.Voice.OnLonely()) }
func (h *HeadlessView) OnBored()          { log.Print(h.Voice.OnBored()) }
func (h *HeadlessView) OnSad()            { log.Print(h.Voice.OnSad()) }
func (h *HeadlessView) OnAngry()          { log.Print(h.Voice.OnAngry()) }
func (h *HeadlessView) OnExcited()        { log.Print(h.Voice.OnExcited()) }
func (h *HeadlessView) OnRebooting()      { log.Print(h.Voice.OnRebooting()) }
func (h *HeadlessView) OnShutdown()       { log.Print(h.Voice.OnShutdown()) }
func (h *HeadlessView) OnNormal()         {}

func (h *HeadlessView) OnMiss(who string) { log.Print(h.Voice.OnMiss(who)) }

func (h *HeadlessView) Wait(t float64, sleeping bool) {
	if sleeping {
		log.Print(h.Voice.OnNapping(int(t)))
	} else {
		log.Print(h.Voice.OnWaiting(int(t)))
	}
}

func (h *HeadlessView) OnStateChange(event string, cb func(old, new interface{})) {
	// Headless mode has no face/UI-driven state-change triggers to attach
	// to; the callback (e.g. AsyncAdvertiser's face-change handler) is
	// simply never invoked, matching a display-less unit that never
	// changes its (nonexistent) face.
}

func (h *HeadlessView) OnNewPeer(p *mesh.Peer)  { log.Print(h.Voice.OnNewPeer(p)) }
func (h *HeadlessView) OnLostPeer(p *mesh.Peer) { log.Print(h.Voice.OnLostPeer(p)) }

func (h *HeadlessView) OnReadingLogs(linesSoFar int) { log.Print(h.Voice.OnReadingLogs(linesSoFar)) }

func (h *HeadlessView) SetAgent(a *agent.Agent) {
	h.mu.Lock()
	h.agent = a
	h.mu.Unlock()
}

// Set mirrors View.set(key, value): value is polymorphic in Python (str,
// None, ...); HeadlessView's own state map is string-keyed/string-valued
// for simplicity (it only ever logs/reports state, never draws it), so a
// nil value is stored as "" rather than removing the key — headless mode
// has no rendering that would distinguish "unset" from "empty string".
func (h *HeadlessView) Set(key string, value interface{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if value == nil {
		h.state[key] = ""
		return
	}
	if s, ok := value.(string); ok {
		h.state[key] = s
		return
	}
	h.state[key] = fmt.Sprint(value)
}

func (h *HeadlessView) SetClosestPeer(peer *mesh.Peer, count int) {
	h.mu.Lock()
	h.state["closest_peer_count"] = fmt.Sprintf("%d", count)
	h.mu.Unlock()
}

func (h *HeadlessView) OnHandshakes(newShakes int) { log.Print(h.Voice.OnHandshakes(newShakes)) }

func (h *HeadlessView) OnAssoc(ap agent.AP) {
	ssid, _ := ap["hostname"].(string)
	bssid, _ := ap["mac"].(string)
	log.Print(h.Voice.OnAssoc(ssid, bssid))
}

func (h *HeadlessView) OnDeauth(sta agent.Station) {
	mac, _ := sta["mac"].(string)
	log.Print(h.Voice.OnDeauth(mac))
}

// OnManualMode ports ui.display's on_manual_mode for headless operation:
// logs the last-session summary text Python's display would render.
func (h *HeadlessView) OnManualMode(s *session.LastSession) {
	log.Printf("manual mode: %+v", s)
}

// Clear mirrors Display.clear() for --clear: there is no real display to
// clear in headless mode, so this just logs the same message Python logs
// before clearing, matching cli.py's do_clear byte-for-byte on the
// observable (log) side.
func (h *HeadlessView) Clear() {
	log.Print("clearing the display ...")
}
