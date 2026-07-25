// Package automata ports pwnagotchi/automata.py: the "mood" state machine
// driving epoch transitions (bored/sad/angry/excited/grateful/lonely) and
// their plugin/UI side effects.
package automata

import (
	"log"
	"strings"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/epoch"
)

// View is the subset of pwnagotchi.ui.view.View that Automata calls into.
// A real internal/ui/view.View will satisfy this once that package exists.
type View interface {
	OnStarting()
	OnGrateful()
	OnLonely()
	OnBored()
	OnSad()
	OnAngry()
	OnExcited()
	OnRebooting()
	OnMiss(who string)
	Wait(t float64, sleeping bool)
}

// EventEmitter is the subset of pwnagotchi.plugins that Automata calls into
// (plugins.on(event_name, *args)). A real internal/plugins.Loader will
// satisfy this once that package exists.
type EventEmitter interface {
	On(event string, args ...interface{})
}

// PeerSource supplies the total encounter count Automata needs for
// _has_support_network_for. In Python this is `self._peers`, populated by
// the AsyncAdvertiser mixin on the concrete Agent class, not by Automata
// itself — Go models that same "supplied by the composing type" shape as an
// interface rather than a field Automata owns.
type PeerSource interface {
	TotalEncounters() float64
	PeerCount() int
}

// Automata ports automata.Automata. Like Python (whose GIL makes single
// attribute reads atomic-ish but does NOT make the compound read-mutate
// sequences here race-free either), Automata reads epoch.Epoch's exported
// counter fields directly rather than through a lock. This is safe as long
// as callers serialize epoch mutation onto a single goroutine — exactly
// pwnagotchi's own real-world usage, where the main auto/manual-mode loop
// (cli.py's do_auto_mode/do_manual_mode) is the sole driver of
// Track/Next/NextEpoch. See docs/known-differences.md.
type Automata struct {
	Config config.Map
	View   View
	Epoch  *epoch.Epoch
	Emit   EventEmitter
	Peers  PeerSource

	// Restart mirrors calling self._restart() with Python's default
	// mode='AUTO' (automata.py's own call site never passes a mode).
	Restart func(mode string)
}

// New ports Automata.__init__ (the Epoch construction only; Config/View/
// Peers/Emit/Restart are wired by the composing Agent, matching how
// Python's multiple inheritance supplies them).
func New(cfg config.Map, view View) *Automata {
	return &Automata{
		Config: cfg,
		View:   view,
		Epoch:  epoch.New(cfg),
	}
}

func (a *Automata) emit(event string, args ...interface{}) {
	if a.Emit != nil {
		a.Emit.On(event, args...)
	}
}

// OnMiss mirrors Automata._on_miss.
func (a *Automata) OnMiss(who string) {
	log.Printf("it looks like %s is not in range anymore :/", who)
	a.Epoch.Track(epoch.TrackOptions{Miss: true})
	a.View.OnMiss(who)
}

// OnError mirrors Automata._on_error: bettercap's "is an unknown BSSID"
// error for an AP that moved out of range is treated as a miss, not a
// logged error.
func (a *Automata) OnError(who string, err error) {
	if err != nil && strings.Contains(err.Error(), "is an unknown BSSID") {
		a.OnMiss(who)
		return
	}
	log.Print(err)
}

// SetStarting mirrors Automata.set_starting.
func (a *Automata) SetStarting() { a.View.OnStarting() }

// SetReady mirrors Automata.set_ready.
func (a *Automata) SetReady() { a.emit("ready", a) }

// InGoodMood mirrors Automata.in_good_mood.
func (a *Automata) InGoodMood() bool { return a.hasSupportNetworkFor(1.0) }

func (a *Automata) hasSupportNetworkFor(factor float64) bool {
	bondFactor, _ := config.DigFloat(a.Config, "personality", "bond_encounters_factor")
	var total float64
	if a.Peers != nil {
		total = a.Peers.TotalEncounters()
	}
	support := total / bondFactor
	return support >= factor
}

// SetGrateful mirrors Automata.set_grateful.
func (a *Automata) SetGrateful() {
	a.View.OnGrateful()
	a.emit("grateful", a)
}

// SetLonely mirrors Automata.set_lonely.
func (a *Automata) SetLonely() {
	if !a.hasSupportNetworkFor(1.0) {
		log.Print("unit is lonely")
		a.View.OnLonely()
		a.emit("lonely", a)
	} else {
		log.Print("unit is grateful instead of lonely")
		a.SetGrateful()
	}
}

// SetBored mirrors Automata.set_bored.
func (a *Automata) SetBored() {
	boredNumEpochs, _ := config.DigFloat(a.Config, "personality", "bored_num_epochs")
	factor := float64(a.Epoch.InactiveFor) / boredNumEpochs
	if !a.hasSupportNetworkFor(factor) {
		log.Printf("%d epochs with no activity -> bored", a.Epoch.InactiveFor)
		a.View.OnBored()
		a.emit("bored", a)
	} else {
		log.Print("unit is grateful instead of bored")
		a.SetGrateful()
	}
}

// SetSad mirrors Automata.set_sad.
func (a *Automata) SetSad() {
	sadNumEpochs, _ := config.DigFloat(a.Config, "personality", "sad_num_epochs")
	factor := float64(a.Epoch.InactiveFor) / sadNumEpochs
	if !a.hasSupportNetworkFor(factor) {
		log.Printf("%d epochs with no activity -> sad", a.Epoch.InactiveFor)
		a.View.OnSad()
		a.emit("sad", a)
	} else {
		log.Print("unit is grateful instead of sad")
		a.SetGrateful()
	}
}

// SetAngry mirrors Automata.set_angry.
func (a *Automata) SetAngry(factor float64) {
	if !a.hasSupportNetworkFor(factor) {
		log.Printf("%d epochs with no activity -> angry", a.Epoch.InactiveFor)
		a.View.OnAngry()
		a.emit("angry", a)
	} else {
		log.Print("unit is grateful instead of angry")
		a.SetGrateful()
	}
}

// SetExcited mirrors Automata.set_excited.
func (a *Automata) SetExcited() {
	log.Printf("%d epochs with activity -> excited", a.Epoch.ActiveFor)
	a.View.OnExcited()
	a.emit("excited", a)
}

// SetRebooting mirrors Automata.set_rebooting.
func (a *Automata) SetRebooting() {
	a.View.OnRebooting()
	a.emit("rebooting", a)
}

// WaitFor mirrors Automata.wait_for(t, sleeping=True).
func (a *Automata) WaitFor(t float64, sleeping bool) {
	event := "wait"
	if sleeping {
		event = "sleep"
	}
	a.emit(event, a, t)
	a.View.Wait(t, sleeping)
	a.Epoch.Track(epoch.TrackOptions{Sleep: true, Inc: t})
}

// IsStale mirrors Automata.is_stale.
func (a *Automata) IsStale() bool {
	maxMisses, _ := config.DigFloat(a.Config, "personality", "max_misses_for_recon")
	return float64(a.Epoch.NumMissed) > maxMisses
}

// AnyActivity mirrors Automata.any_activity.
func (a *Automata) AnyActivity() bool { return a.Epoch.AnyActivity }

// NextEpoch mirrors Automata.next_epoch.
func (a *Automata) NextEpoch() {
	log.Print("agent.next_epoch()")

	wasStale := a.IsStale()
	didMiss := a.Epoch.NumMissed

	a.Epoch.Next()

	maxMisses, _ := config.DigFloat(a.Config, "personality", "max_misses_for_recon")
	sadNumEpochs, _ := config.DigFloat(a.Config, "personality", "sad_num_epochs")
	excitedNumEpochs, _ := config.DigFloat(a.Config, "personality", "excited_num_epochs")

	switch {
	case wasStale:
		factor := float64(didMiss) / maxMisses
		if factor >= 2.0 {
			a.SetAngry(factor)
		} else {
			log.Printf("agent missed %d interactions -> lonely", didMiss)
			a.SetLonely()
		}
	case a.Epoch.SadFor > 0:
		factor := float64(a.Epoch.InactiveFor) / sadNumEpochs
		if factor >= 2.0 {
			a.SetAngry(factor)
		} else {
			a.SetSad()
		}
	case a.Epoch.BoredFor > 0:
		a.SetBored()
	case float64(a.Epoch.ActiveFor) >= excitedNumEpochs:
		a.SetExcited()
	case a.Epoch.ActiveFor >= 5 && a.hasSupportNetworkFor(5.0):
		a.SetGrateful()
	}

	a.emit("epoch", a, a.Epoch.Epoch-1, a.Epoch.Data())

	monMaxBlindEpochs, _ := config.DigFloat(a.Config, "main", "mon_max_blind_epochs")
	if float64(a.Epoch.BlindFor) >= monMaxBlindEpochs {
		log.Printf("%d epochs without visible access points -> restarting ...", a.Epoch.BlindFor)
		if a.Restart != nil {
			a.Restart("AUTO")
		}
		a.Epoch.BlindFor = 0
	}
}
