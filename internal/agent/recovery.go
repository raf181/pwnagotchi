package agent

import (
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/unit"
)

func unixFloatToTime(secs float64) time.Time {
	whole := int64(secs)
	frac := secs - float64(whole)
	return time.Unix(whole, int64(frac*1e9))
}

type recoveryData struct {
	StartedAt  float64                `json:"started_at"`
	Epoch      int64                  `json:"epoch"`
	History    map[string]int         `json:"history"`
	Handshakes map[string]interface{} `json:"handshakes"`
	LastPwnd   string                 `json:"last_pwnd"`
}

// reboot ports Agent._reboot: NOTE Python calls pwnagotchi.reboot() with NO
// mode argument (mode=None default) — unlike restart(), reboot() never
// touches the AUTO/MANU marker files. Preserved exactly.
func (a *Agent) reboot() {
	a.SetRebooting()
	a.saveRecoveryData()
	unit.Reboot(nil, unit.DefaultRunner, a.view, nil)
}

// restart ports Agent._restart(mode='AUTO').
func (a *Agent) restart(mode string) {
	if mode == "" {
		mode = "AUTO"
	}
	a.saveRecoveryData()
	unit.Restart(mode, unit.DefaultRunner)
}

// Restart is the exported entry point external callers (cli.py's SIGUSR1
// handler: `agent._restart("MANU" if args.do_manual else "AUTO")`) use —
// Python has no real access-control distinction between "internal" and
// "external" callers, so this just exposes the same method the automata
// Restart callback and cli.py's signal handler both call.
func (a *Agent) Restart(mode string) { a.restart(mode) }

// Reboot is the exported entry point for Agent._reboot (unused directly by
// cli.py today, but kept exported for parity/future callers — e.g. a
// `pwnagotchi plugins`-style reboot command).
func (a *Agent) Reboot() { a.reboot() }

func (a *Agent) saveRecoveryData() {
	log.Printf("writing recovery data to %s ...", RecoveryDataFile)

	a.mu.Lock()
	data := recoveryData{
		StartedAt:  float64(a.startedAt.UnixNano()) / 1e9, // matches Python's time.time() fractional-second precision
		Epoch:      a.Automata.Epoch.Epoch,
		History:    a.history,
		Handshakes: a.handshakes,
		LastPwnd:   a.lastPwnd,
	}
	a.mu.Unlock()

	f, err := os.Create(RecoveryDataFile)
	if err != nil {
		log.Printf("error writing recovery data: %v", err)
		return
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(data); err != nil {
		log.Printf("error writing recovery data: %v", err)
	}
}

// loadRecoveryData ports Agent._load_recovery_data(delete=True, no_exceptions=True).
func (a *Agent) loadRecoveryData(delete, noExceptions bool) error {
	raw, err := os.ReadFile(RecoveryDataFile)
	if err != nil {
		if noExceptions {
			return nil
		}
		return err
	}

	var data recoveryData
	if err := json.Unmarshal(raw, &data); err != nil {
		if noExceptions {
			return nil
		}
		return err
	}

	log.Printf("found recovery data: %+v", data)

	a.mu.Lock()
	a.startedAt = unixFloatToTime(data.StartedAt)
	a.Automata.Epoch.Epoch = data.Epoch
	a.handshakes = data.Handshakes
	a.history = data.History
	a.lastPwnd = data.LastPwnd
	a.mu.Unlock()

	if delete {
		log.Printf("deleting %s", RecoveryDataFile)
		os.Remove(RecoveryDataFile)
	}
	return nil
}
