package agent

import (
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
	"github.com/jayofelony/pwnagotchi/go-port/internal/epoch"
	"github.com/jayofelony/pwnagotchi/go-port/internal/unit"
)

func (a *Agent) updateUptime() {
	secs, err := unit.Uptime()
	if err != nil {
		return
	}
	a.view.Set("uptime", config.SecsToHHMMSS(secs))
}

func (a *Agent) updateCounters() {
	a.mu.Lock()
	aps := a.accessPoints
	current := a.currentChannel
	a.mu.Unlock()

	totAPs := len(aps)
	totStas := 0
	for _, ap := range aps {
		totStas += len(clients(ap))
	}

	a.mu.Lock()
	a.totAPs = totAPs
	a.mu.Unlock()

	if current == 0 {
		a.view.Set("aps", strconv.Itoa(totAPs))
		a.view.Set("sta", strconv.Itoa(totStas))
		return
	}

	apsOnChannel := 0
	stasOnChannel := 0
	for _, ap := range aps {
		if getInt(ap, "channel") == current {
			apsOnChannel++
			stasOnChannel += len(clients(ap))
		}
	}
	a.mu.Lock()
	a.apsOnChannel = apsOnChannel
	a.mu.Unlock()

	a.view.Set("aps", fmt.Sprintf("%d (%d)", apsOnChannel, totAPs))
	a.view.Set("sta", fmt.Sprintf("%d (%d)", stasOnChannel, totStas))
}

// updateHandshakes ports Agent._update_handshakes(new_shakes=0).
func (a *Agent) updateHandshakes(newShakes int) {
	if newShakes > 0 {
		a.Automata.Epoch.Track(epoch.TrackOptions{Handshake: true, Inc: float64(newShakes)})
	}

	handshakesDir := stringOr(bettercapMap(a.config), "handshakes", "")
	tot, _ := config.TotalUniqueHandshakes(filepath.Glob, handshakesDir)

	a.mu.Lock()
	numHandshakes := len(a.handshakes)
	lastPwnd := a.lastPwnd
	a.mu.Unlock()

	txt := fmt.Sprintf("%d (%d)", numHandshakes, tot)
	if lastPwnd != "" {
		txt += fmt.Sprintf(" [%s]", lastPwnd)
	}
	a.view.Set("shakes", txt)

	if newShakes > 0 {
		a.view.OnHandshakes(newShakes)
	}
}

func (a *Agent) updatePeers() {
	a.view.SetClosestPeer(a.AsyncAdvertiser.ClosestPeer(), a.AsyncAdvertiser.PeerCount())
}

// fetchStats ports Agent._fetch_stats.
func (a *Agent) fetchStats() {
	for {
		select {
		case <-a.ctx.Done():
			return
		default:
		}

		if _, err := a.Session(""); err != nil {
			log.Printf("[agent:_fetch_stats] self.session: %v", err)
		}

		a.updateUptime()
		a.AsyncAdvertiser.UpdateAdvertisement()
		a.updatePeers()
		a.updateCounters()
		a.updateHandshakes(0)

		select {
		case <-a.ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// StartSessionFetcher ports Agent.start_session_fetcher: a background
// goroutine (Python: threading.Thread(..., name="Session Fetcher", daemon=True)).
func (a *Agent) StartSessionFetcher() {
	go a.fetchStats()
}
