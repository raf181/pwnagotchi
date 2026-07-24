// Package epoch ports pwnagotchi/epoch.py: per-epoch activity tracking,
// mood-relevant counters, and the observation histograms fed into the AI.
package epoch

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/mesh"
	"github.com/jayofelony/pwnagotchi/internal/unit"
)

// AccessPointObservation is the minimal projection of a bettercap AP record
// epoch.observe() needs (`ap['channel']`, `len(ap['clients'])`). The full AP
// record type belongs to internal/bettercap once that package lands; this
// narrow shape keeps internal/epoch from depending on it prematurely.
type AccessPointObservation struct {
	Channel    int
	NumClients int
}

// PeerObservation is the minimal projection of a mesh.Peer epoch.observe()
// and Automata need (`peer.encounters`, `peer.last_channel`). The full Peer
// type belongs to internal/mesh/peer.go once that lands.
type PeerObservation struct {
	Encounters  float64
	LastChannel int
}

// Observation mirrors Epoch._observation: per-channel histograms.
type Observation struct {
	APsHistogram   []float64
	STAHistogram   []float64
	PeersHistogram []float64
}

// Data mirrors the dict epoch.py builds in next() and hands out via data()/
// wait_for_epoch_data() — field names match Python's dict keys exactly
// since plugins consume this as a serialization contract.
type Data struct {
	DurationSecs       float64 `json:"duration_secs"`
	SleptForSecs       float64 `json:"slept_for_secs"`
	BlindForEpochs     int     `json:"blind_for_epochs"`
	InactiveForEpochs  int     `json:"inactive_for_epochs"`
	ActiveForEpochs    int     `json:"active_for_epochs"`
	SadForEpochs       int     `json:"sad_for_epochs"`
	BoredForEpochs     int     `json:"bored_for_epochs"`
	MissedInteractions int64   `json:"missed_interactions"`
	NumHops            int64   `json:"num_hops"`
	NumPeers           int     `json:"num_peers"`
	TotBond            float64 `json:"tot_bond"`
	AvgBond            float64 `json:"avg_bond"`
	NumDeauths         int64   `json:"num_deauths"`
	NumAssociations    int64   `json:"num_associations"`
	NumHandshakes      int64   `json:"num_handshakes"`
	CPULoad            float64 `json:"cpu_load"`
	MemUsage           float64 `json:"mem_usage"`
	Temperature        int     `json:"temperature"`
}

// event is a minimal port of threading.Event: level-triggered, thread-safe,
// with Wait(timeout) semantics matching Python's Event.wait(timeout).
type event struct {
	mu sync.Mutex
	ch chan struct{}
}

func newEvent() *event { return &event{ch: make(chan struct{})} }

func (e *event) Set() {
	e.mu.Lock()
	defer e.mu.Unlock()
	select {
	case <-e.ch:
	default:
		close(e.ch)
	}
}

func (e *event) Clear() {
	e.mu.Lock()
	defer e.mu.Unlock()
	select {
	case <-e.ch:
		e.ch = make(chan struct{})
	default:
	}
}

// Wait blocks until Set is called or timeout elapses (timeout<=0 means
// block forever, matching Python's Event.wait(None)). Returns whether the
// event was set.
func (e *event) Wait(timeout time.Duration) bool {
	e.mu.Lock()
	ch := e.ch
	e.mu.Unlock()
	if timeout <= 0 {
		<-ch
		return true
	}
	select {
	case <-ch:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Epoch ports epoch.Epoch. All counters are guarded by mu so the port is
// safe to call from multiple goroutines (the real daemon's bettercap event
// handlers, channel hopper, and main loop all touch it) — Python's version
// relies on the GIL for per-statement atomicity, which Go doesn't have, so
// this mutex is a necessary (not optional) structural addition, not a
// behavior change.
type Epoch struct {
	mu  sync.Mutex
	cfg config.Map
	cpu *unit.CPUStats

	Epoch         int64
	InactiveFor   int64
	ActiveFor     int64
	BlindFor      int64
	SadFor        int64
	BoredFor      int64
	DidDeauth     bool
	NumDeauths    int64
	DidAssociate  bool
	NumAssocs     int64
	NumMissed     int64
	DidHandshakes bool
	NumShakes     int64
	NumHops       int64
	NumSlept      float64
	NumPeers      int
	TotBondFactor float64
	AvgBondFactor float64
	AnyActivity   bool

	EpochStarted  time.Time
	EpochDuration float64

	NonOverlappingChannels map[int]int

	observation      Observation
	observationReady *event
	epochData        Data
	epochDataReady   *event
}

// New ports Epoch.__init__.
func New(cfg config.Map) *Epoch {
	e := &Epoch{
		cfg:                    cfg,
		cpu:                    unit.NewCPUStats(),
		EpochStarted:           time.Now(),
		NonOverlappingChannels: map[int]int{1: 0, 6: 0, 11: 0},
		observationReady:       newEvent(),
		epochDataReady:         newEvent(),
		observation: Observation{
			APsHistogram:   make([]float64, mesh.NumChannels),
			STAHistogram:   make([]float64, mesh.NumChannels),
			PeersHistogram: make([]float64, mesh.NumChannels),
		},
	}
	return e
}

// WaitForEpochData mirrors Epoch.wait_for_epoch_data. timeout<=0 blocks
// forever, matching Python's wait_for_epoch_data(timeout=None).
func (e *Epoch) WaitForEpochData(withObservation bool, timeout time.Duration) Data {
	e.epochDataReady.Wait(timeout)
	e.epochDataReady.Clear()
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.epochData
}

// Data mirrors Epoch.data().
func (e *Epoch) Data() Data {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.epochData
}

// Observe mirrors Epoch.observe(aps, peers).
func (e *Epoch) Observe(aps []AccessPointObservation, peers []PeerObservation) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(aps) == 0 {
		e.BlindFor++
	} else {
		e.BlindFor = 0
	}

	bondUnitScale, _ := config.DigFloat(e.cfg, "personality", "bond_encounters_factor")

	e.NumPeers = len(peers)
	numPeers := float64(e.NumPeers) + 1e-10

	var totalEncounters float64
	for _, p := range peers {
		totalEncounters += p.Encounters
	}
	e.TotBondFactor = totalEncounters / bondUnitScale
	e.AvgBondFactor = e.TotBondFactor / numPeers

	numAPs := float64(len(aps)) + 1e-10
	var numSTA float64
	for _, ap := range aps {
		numSTA += float64(ap.NumClients)
	}
	numSTA += 1e-10

	apsPerChan := make([]float64, mesh.NumChannels)
	staPerChan := make([]float64, mesh.NumChannels)
	peersPerChan := make([]float64, mesh.NumChannels)

	for _, ap := range aps {
		idx := ap.Channel - 1
		if idx >= 0 && idx < mesh.NumChannels {
			apsPerChan[idx]++
			staPerChan[idx] += float64(ap.NumClients)
		} else {
			log.Printf("got data on channel %d, we can store %d channels", ap.Channel, mesh.NumChannels)
		}
	}
	for _, p := range peers {
		idx := p.LastChannel - 1
		if idx >= 0 && idx < mesh.NumChannels {
			peersPerChan[idx]++
		} else {
			log.Printf("got peer data on channel %d, we can store %d channels", p.LastChannel, mesh.NumChannels)
		}
	}

	for i := range apsPerChan {
		apsPerChan[i] /= numAPs
		staPerChan[i] /= numSTA
		peersPerChan[i] /= numPeers
	}

	e.observation = Observation{
		APsHistogram:   apsPerChan,
		STAHistogram:   staPerChan,
		PeersHistogram: peersPerChan,
	}
	e.observationReady.Set()
}

// TrackOptions mirrors epoch.py's track() keyword arguments. Inc is a
// float64 because Python's dynamic typing lets any call site pass a float
// (in practice only wait_for's `sleep=True, inc=t` does, with t a
// fractional number of seconds); integer-counter fields (deauths, assocs,
// misses, hops, handshakes) truncate Inc via int64(), matching every real
// call site, which always passes whole-number increments there.
type TrackOptions struct {
	Deauth    bool
	Assoc     bool
	Handshake bool
	Hop       bool
	Sleep     bool
	Miss      bool
	Inc       float64
}

// Track mirrors Epoch.track(...). Callers should set Inc; a zero Inc is
// treated as 1, matching Python's default inc=1 (NOT matching a caller who
// explicitly wants a zero increment — Python has the exact same ambiguity
// since inc=0 passed explicitly is indistinguishable from the default in
// its effect here, both are no-ops on any counter, so this is not a new gap).
func (e *Epoch) Track(opts TrackOptions) {
	inc := opts.Inc
	if inc == 0 {
		inc = 1
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	if opts.Deauth {
		e.NumDeauths += int64(inc)
		e.DidDeauth = true
		e.AnyActivity = true
	}
	if opts.Assoc {
		e.NumAssocs += int64(inc)
		e.DidAssociate = true
		e.AnyActivity = true
	}
	if opts.Miss {
		e.NumMissed += int64(inc)
	}
	if opts.Hop {
		e.NumHops += int64(inc)
		e.DidDeauth = false
		e.DidAssociate = false
	}
	if opts.Handshake {
		e.NumShakes += int64(inc)
		e.DidHandshakes = true
	}
	if opts.Sleep {
		e.NumSlept += inc
	}
}

// Next mirrors Epoch.next(): advances mood counters, snapshots epochData,
// logs the exact `[epoch N] ...` line (a serialization/log contract other
// tooling may parse), and resets per-epoch counters.
func (e *Epoch) Next() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.AnyActivity && !e.DidHandshakes {
		e.InactiveFor++
		e.ActiveFor = 0
	} else {
		e.ActiveFor++
		e.InactiveFor = 0
		e.SadFor = 0
		e.BoredFor = 0
	}

	sadNumEpochs, _ := config.DigFloat(e.cfg, "personality", "sad_num_epochs")
	boredNumEpochs, _ := config.DigFloat(e.cfg, "personality", "bored_num_epochs")

	switch {
	case float64(e.InactiveFor) >= sadNumEpochs:
		e.BoredFor = 0
		e.SadFor++
	case float64(e.InactiveFor) >= boredNumEpochs:
		e.SadFor = 0
		e.BoredFor++
	default:
		e.SadFor = 0
		e.BoredFor = 0
	}

	now := time.Now()
	cpu, cpuErr := e.cpu.Load("epoch")
	if cpuErr != nil {
		cpu = 0
	}
	mem, memErr := unit.MemUsage()
	if memErr != nil {
		mem = 0
	}
	temp, tempErr := unit.Celsius()
	if tempErr != nil {
		temp = 0
	}

	e.EpochDuration = now.Sub(e.EpochStarted).Seconds()

	e.epochData = Data{
		DurationSecs:       e.EpochDuration,
		SleptForSecs:       e.NumSlept,
		BlindForEpochs:     int(e.BlindFor),
		InactiveForEpochs:  int(e.InactiveFor),
		ActiveForEpochs:    int(e.ActiveFor),
		SadForEpochs:       int(e.SadFor),
		BoredForEpochs:     int(e.BoredFor),
		MissedInteractions: e.NumMissed,
		NumHops:            e.NumHops,
		NumPeers:           e.NumPeers,
		TotBond:            e.TotBondFactor,
		AvgBond:            e.AvgBondFactor,
		NumDeauths:         e.NumDeauths,
		NumAssociations:    e.NumAssocs,
		NumHandshakes:      e.NumShakes,
		CPULoad:            cpu,
		MemUsage:           mem,
		Temperature:        temp,
	}
	e.epochDataReady.Set()

	log.Print(formatEpochLogLine(e.Epoch, e.EpochDuration, e.NumSlept, e.BlindFor, e.SadFor, e.BoredFor,
		e.InactiveFor, e.ActiveFor, e.NumPeers, e.TotBondFactor, e.AvgBondFactor, e.NumHops, e.NumMissed,
		e.NumDeauths, e.NumAssocs, e.NumShakes, cpu, mem, temp))

	e.Epoch++
	e.DidDeauth = false
	e.NumDeauths = 0
	e.NumPeers = 0
	e.TotBondFactor = 0.0
	e.AvgBondFactor = 0.0
	e.DidAssociate = false
	e.NumAssocs = 0
	e.NumMissed = 0
	e.DidHandshakes = false
	e.NumShakes = 0
	e.NumHops = 0
	e.NumSlept = 0
	e.AnyActivity = false
}

// formatEpochLogLine mirrors epoch.py's next() logging.info(...) call
// byte-for-byte (field order, formatting verbs, and the literal "C" suffix
// with no space before it on temperature). This is a log-format contract:
// external tooling greps pwnlog for these fields, so field names/order/
// formatting must not drift from Python.
func formatEpochLogLine(epoch int64, duration float64, slept float64, blind, sad, bored, inactive, active int64,
	numPeers int, totBond, avgBond float64, hops, missed, deauths, assocs, shakes int64, cpu, mem float64, temp int) string {
	return fmt.Sprintf(
		"[epoch %d] duration=%s slept_for=%s blind=%d sad=%d bored=%d inactive=%d active=%d peers=%d tot_bond=%.2f "+
			"avg_bond=%.2f hops=%d missed=%d deauths=%d assocs=%d handshakes=%d cpu=%d%% mem=%d%% temperature=%dC",
		epoch,
		config.SecsToHHMMSSFloat(duration),
		config.SecsToHHMMSSFloat(slept),
		blind, sad, bored, inactive, active,
		numPeers, totBond, avgBond,
		hops, missed, deauths, assocs, shakes,
		int64(cpu*100), int64(mem*100), temp,
	)
}
