package mesh

import (
	"context"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/grid"
)

// advPollerFirstDelay/advPollerInterval mirror mesh.utils._adv_poller's
// time.sleep(20) startup delay and time.sleep(3) per-iteration delay.
const (
	advPollerFirstDelay = 20 * time.Second
	advPollerInterval   = 3 * time.Second
)

// View is the subset of pwnagotchi.ui.view.View AsyncAdvertiser calls into.
type View interface {
	OnStateChange(event string, cb func(old, new interface{}))
	OnNewPeer(peer *Peer)
	OnLostPeer(peer *Peer)
}

// EventEmitter mirrors pwnagotchi.plugins.on(event, *args).
type EventEmitter interface {
	On(event string, args ...interface{})
}

// EpochSource is the minimal projection of epoch.Epoch AsyncAdvertiser
// needs (the current epoch counter, for the advertisement payload).
type EpochSource interface {
	CurrentEpoch() int64
}

// AsyncAdvertiser ports mesh.utils.AsyncAdvertiser.
type AsyncAdvertiser struct {
	Config      config.Map
	View        View
	Emit        EventEmitter
	Grid        *grid.Client
	Epoch       EpochSource
	Fingerprint string

	// HandshakesCount/HandshakesPath supply what Python pulls from
	// self._handshakes (an Agent field AsyncAdvertiser never defines
	// itself, just like _epoch/_peers — see automata.go's identical
	// pattern) and config['bettercap']['handshakes'].
	HandshakesCount func() int

	mu            sync.RWMutex
	advertisement map[string]interface{}
	peers         map[string]*Peer
	closestPeer   *Peer
}

// NewAsyncAdvertiser ports AsyncAdvertiser.__init__.
func NewAsyncAdvertiser(cfg config.Map, view View, name, version, fingerprint string) *AsyncAdvertiser {
	personality, _ := cfg["personality"].(config.Map)
	return &AsyncAdvertiser{
		Config:      cfg,
		View:        view,
		Fingerprint: fingerprint,
		advertisement: map[string]interface{}{
			"name":     name,
			"version":  version,
			"identity": fingerprint,
			"face":     DefaultFriendFace,
			"pwnd_run": 0,
			"pwnd_tot": 0,
			"uptime":   0,
			"epoch":    0,
			"policy":   personality,
		},
		peers: map[string]*Peer{},
	}
}

// GetFingerprint ports AsyncAdvertiser.fingerprint().
func (a *AsyncAdvertiser) GetFingerprint() string { return a.Fingerprint }

// UpdateAdvertisement ports AsyncAdvertiser._update_advertisement(s) (the
// `s` parameter is unused in Python's body too, so it's dropped here rather
// than ported as a meaningless argument).
func (a *AsyncAdvertiser) UpdateAdvertisement() {
	a.mu.Lock()
	if a.HandshakesCount != nil {
		a.advertisement["pwnd_run"] = a.HandshakesCount()
	}
	if handshakesDir, ok := digPathString(a.Config, "bettercap", "handshakes"); ok {
		if n, err := config.TotalUniqueHandshakes(filepath.Glob, handshakesDir); err == nil {
			a.advertisement["pwnd_tot"] = n
		}
	}
	if a.Epoch != nil {
		a.advertisement["epoch"] = a.Epoch.CurrentEpoch()
	}
	adv := cloneMap(a.advertisement)
	a.mu.Unlock()

	if a.Grid != nil {
		a.Grid.SetAdvertisementData(adv)
	}
}

// StartAdvertising ports AsyncAdvertiser.start_advertising.
func (a *AsyncAdvertiser) StartAdvertising(ctx context.Context) {
	if enabled, _ := digBool(a.Config, "personality", "advertise"); !enabled {
		log.Print("advertising is disabled")
		return
	}
	go a.advPoller(ctx)

	a.mu.RLock()
	adv := cloneMap(a.advertisement)
	a.mu.RUnlock()
	if a.Grid != nil {
		a.Grid.SetAdvertisementData(adv)
		a.Grid.Advertise(true)
	}
	if a.View != nil {
		a.View.OnStateChange("face", a.onFaceChange)
	}
}

func (a *AsyncAdvertiser) onFaceChange(_ interface{}, new interface{}) {
	face, _ := new.(string)
	a.mu.Lock()
	a.advertisement["face"] = face
	adv := cloneMap(a.advertisement)
	a.mu.Unlock()
	if a.Grid != nil {
		a.Grid.SetAdvertisementData(adv)
	}
}

// CumulativeEncounters ports AsyncAdvertiser.cumulative_encounters.
func (a *AsyncAdvertiser) CumulativeEncounters() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var total float64
	for _, p := range a.peers {
		total += p.Encounters
	}
	return total
}

// TotalEncounters satisfies automata.PeerSource.
func (a *AsyncAdvertiser) TotalEncounters() float64 { return a.CumulativeEncounters() }

// PeerCount satisfies automata.PeerSource.
func (a *AsyncAdvertiser) PeerCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.peers)
}

// ClosestPeer ports the self._closest_peer field's read side.
func (a *AsyncAdvertiser) ClosestPeer() *Peer {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.closestPeer
}

// PeersSnapshot returns a point-in-time copy of the current peers (mirrors
// reading self._peers.values() in Python, e.g. epoch.observe(aps,
// list(self._peers.values()))).
func (a *AsyncAdvertiser) PeersSnapshot() []*Peer {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*Peer, 0, len(a.peers))
	for _, p := range a.peers {
		out = append(out, p)
	}
	return out
}

func (a *AsyncAdvertiser) onNewPeer(p *Peer) {
	log.Printf("new peer %s detected (%d encounters)", p.FullName(), int64(p.Encounters))
	if a.View != nil {
		a.View.OnNewPeer(p)
	}
	if a.Emit != nil {
		a.Emit.On("peer_detected", a, p)
	}
}

func (a *AsyncAdvertiser) onLostPeer(p *Peer) {
	log.Printf("lost peer %s", p.FullName())
	if a.View != nil {
		a.View.OnLostPeer(p)
	}
	if a.Emit != nil {
		a.Emit.On("peer_lost", a, p)
	}
}

// advPoller ports AsyncAdvertiser._adv_poller. ctx cancellation stops the
// loop cleanly (Python has no equivalent — an addition for clean shutdown,
// not a behavior change to the polling logic itself).
func (a *AsyncAdvertiser) advPoller(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(advPollerFirstDelay):
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if a.Grid != nil {
			gridPeers, err := a.Grid.Peers()
			if err != nil {
				log.Printf("error while polling pwngrid-peer: %s", err)
			} else {
				a.reconcilePeers(gridPeers)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(advPollerInterval):
		}
	}
}

func (a *AsyncAdvertiser) reconcilePeers(gridPeers []interface{}) {
	newPeers := make(map[string]*Peer, len(gridPeers))
	var closest *Peer
	for _, raw := range gridPeers {
		obj, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		p := NewPeer(obj)
		newPeers[p.Identity()] = p
		if closest == nil {
			closest = p
		}
	}

	a.mu.Lock()
	a.closestPeer = closest

	var toDelete []string
	for ident := range a.peers {
		if _, ok := newPeers[ident]; !ok {
			toDelete = append(toDelete, ident)
		}
	}
	lost := make([]*Peer, 0, len(toDelete))
	for _, ident := range toDelete {
		lost = append(lost, a.peers[ident])
		delete(a.peers, ident)
	}

	var newlyDetected []*Peer
	for ident, p := range newPeers {
		if existing, ok := a.peers[ident]; !ok {
			a.peers[ident] = p
			newlyDetected = append(newlyDetected, p)
		} else {
			existing.Update(p)
		}
	}
	a.mu.Unlock()

	for _, p := range lost {
		a.onLostPeer(p)
	}
	for _, p := range newlyDetected {
		a.onNewPeer(p)
	}
}

func cloneMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func digBool(m config.Map, keys ...string) (bool, bool) {
	var cur interface{} = m
	for _, k := range keys {
		asMap, ok := cur.(config.Map)
		if !ok {
			return false, false
		}
		cur, ok = asMap[k]
		if !ok {
			return false, false
		}
	}
	b, ok := cur.(bool)
	return b, ok
}

func digPathString(m config.Map, keys ...string) (string, bool) {
	var cur interface{} = m
	for _, k := range keys {
		asMap, ok := cur.(config.Map)
		if !ok {
			return "", false
		}
		cur, ok = asMap[k]
		if !ok {
			return "", false
		}
	}
	s, ok := cur.(string)
	return s, ok
}
