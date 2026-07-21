package cli

import (
	"log"
	"time"

	"github.com/jayofelony/pwnagotchi/go-port/internal/agent"
	"github.com/jayofelony/pwnagotchi/go-port/internal/grid"
	"github.com/jayofelony/pwnagotchi/go-port/internal/session"
)

// ManualModeView is the extra HeadlessView method do_manual_mode needs
// (ui.display.on_manual_mode, not part of agent.View). Matches the real
// internal/ui/view.View.OnManualMode signature exactly (session.LastSession,
// not interface{}) so *view.View/*display.Display satisfy this too.
type ManualModeView interface {
	OnManualMode(s *session.LastSession)
}

// EventEmitter mirrors pwnagotchi.plugins.on(event, *args).
type EventEmitter interface {
	On(event string, args ...interface{})
}

// RunManualMode ports cli.py's do_manual_mode(agent).
func RunManualMode(a *agent.Agent, view ManualModeView, skipSession bool, gridClient *grid.Client, emit EventEmitter, stop <-chan struct{}) {
	log.Print("entering manual mode ...")
	a.Mode = "manual"
	a.LastSession.Parse(a.View(), skipSession)
	if !skipSession {
		s := a.LastSession
		log.Printf("the last session lasted %s (%d completed epochs, trained for %d), average reward:%v (min:%v max:%v)",
			s.DurationHuman, s.Epochs, s.TrainEpochs, s.AvgReward, s.MinReward, s.MaxReward)
	}

	for {
		select {
		case <-stop:
			return
		default:
		}
		view.OnManualMode(a.LastSession)
		select {
		case <-stop:
			return
		case <-time.After(5 * time.Second):
		}
		if gridClient != nil && gridClient.IsConnected() {
			emit.On("internet_available", a)
		}
	}
}

// RunAutoMode ports cli.py's do_auto_mode(agent).
//
// Python's version wraps the whole loop body in try/except Exception, with
// a special recovery path when the exception message contains "wifi.interface
// not set" (sleep 60s, then next_epoch() to force cleanup code to run) vs.
// the general case (log and continue). internal/agent's Recon/SetChannel/
// Associate/Deauth methods handle bettercap command errors internally
// (logging and returning) rather than propagating them, so that specific
// substring-matched recovery branch has no equivalent error to catch here
// — it is a known, currently-unreachable difference (see
// docs/known-differences.md), not a silently dropped feature. The general
// "don't let one bad iteration kill the process" protection is kept via
// recover(), since a nil-map/type-assertion bug in malformed bettercap JSON
// is the realistic failure mode here, not a raised bettercap command error.
func RunAutoMode(a *agent.Agent, skipSession bool, gridClient *grid.Client, emit EventEmitter, stop <-chan struct{}) {
	log.Print("entering auto mode ...")
	a.Mode = "auto"
	a.LastSession.Parse(a.View(), skipSession)
	a.Start()

	for {
		select {
		case <-stop:
			return
		default:
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("main loop exception (%v)", r)
				}
			}()

			a.Recon()
			channels := a.GetAccessPointsByChannel()
			for _, group := range channels {
				time.Sleep(1 * time.Second)
				a.SetChannel(group.Channel, true)

				if !a.IsStale() && a.AnyActivity() {
					log.Printf("%d access points on channel %d", len(group.APs), group.Channel)
				}

				for _, ap := range group.APs {
					a.Associate(ap, -1)
					for _, sta := range clientsOf(ap) {
						a.Deauth(ap, sta, -1)
						time.Sleep(1 * time.Second)
					}
				}
			}

			a.NextEpoch()

			if gridClient != nil && gridClient.IsConnected() {
				emit.On("internet_available", a)
			}
		}()
	}
}

func clientsOf(ap agent.AP) []agent.Station {
	raw, _ := ap["clients"].([]interface{})
	out := make([]agent.Station, 0, len(raw))
	for _, c := range raw {
		if m, ok := c.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}
