package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
)

// IsModuleRunning ports Agent.is_module_running.
func (a *Agent) IsModuleRunning(module string) bool {
	s, err := a.Session("")
	if err != nil {
		return false
	}
	sm := asMap(s)
	for _, raw := range asSlice(sm["modules"]) {
		m := asMap(raw)
		if m != nil && getString(m, "name") == module {
			running, _ := m["running"].(bool)
			return running
		}
	}
	return false
}

// StartModule ports Agent.start_module.
func (a *Agent) StartModule(module string) {
	a.Run(fmt.Sprintf("%s on", module), true)
}

// RestartModule ports Agent.restart_module.
func (a *Agent) RestartModule(module string) {
	a.Run(fmt.Sprintf("%s off; %s on", module, module), true)
}

func (a *Agent) hasHandshake(bssid string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	bssid = strings.ToLower(bssid)
	for key := range a.handshakes {
		if strings.Contains(strings.ToLower(key), bssid) {
			return true
		}
	}
	return false
}

// shouldInteract ports Agent._should_interact.
func (a *Agent) shouldInteract(who string) bool {
	if a.hasHandshake(who) {
		return false
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.history[who]; !ok {
		a.history[who] = 1
		return true
	}
	a.history[who]++

	maxInteractions, _ := digFloat(a.config, "personality", "max_interactions")
	return float64(a.history[who]) < maxInteractions
}

var bcapTagSanitizer = regexp.MustCompile(`[^a-z0-9_]+`)

// onEvent ports Agent._on_event(msg): dispatches a bettercap websocket
// event to plugins (`bcap_<sanitized tag>`) and, for
// wifi.client.handshake events, records the new handshake.
func (a *Agent) onEvent(msg []byte) error {
	var jmsg map[string]interface{}
	if err := json.Unmarshal(msg, &jmsg); err != nil {
		return err
	}

	tag := getString(jmsg, "tag")
	sanitized := bcapTagSanitizer.ReplaceAllString(strings.ToLower(tag), "_")
	a.emit.On("bcap_"+sanitized, a, jmsg)

	if tag != "wifi.client.handshake" {
		return nil
	}

	data := asMap(jmsg["data"])
	filename := getString(data, "file")
	staMac := getString(data, "station")
	apMac := getString(data, "ap")
	key := fmt.Sprintf("%s -> %s", staMac, apMac)

	a.mu.Lock()
	_, already := a.handshakes[key]
	if !already {
		a.handshakes[key] = jmsg
	}
	a.mu.Unlock()

	foundHandshake := false
	if !already {
		s, _ := a.Session("")
		ap, sta, found := a.findAPStaIn(staMac, apMac, asMap(s))
		if !found {
			log.Printf("!!! captured new handshake: %s !!!", key)
			a.mu.Lock()
			a.lastPwnd = apMac
			a.mu.Unlock()
			a.emit.On("handshake", a, filename, apMac, staMac)
		} else {
			hostname := getString(ap, "hostname")
			last := hostname
			if hostname == "" || hostname == "<hidden>" {
				last = apMac
			}
			a.mu.Lock()
			a.lastPwnd = last
			a.mu.Unlock()
			log.Printf("!!! captured new handshake on channel %d, %d dBm: %s (%s) -> %s [%s (%s)] !!!",
				getInt(ap, "channel"), getInt(ap, "rssi"), getString(sta, "mac"), getString(sta, "vendor"),
				getString(ap, "hostname"), getString(ap, "mac"), getString(ap, "vendor"))
			a.emit.On("handshake", a, filename, ap, sta)
		}
		foundHandshake = true
	}

	newShakes := 0
	if foundHandshake {
		newShakes = 1
	}
	a.updateHandshakes(newShakes)
	return nil
}

// eventPoller ports Agent._event_poller.
func (a *Agent) eventPoller() {
	a.loadRecoveryData(true, true)
	a.Run("events.clear", true)

	for {
		log.Print("[agent:_event_poller] polling events ...")
		err := a.StartWebsocket(a.ctx, a.onEvent)
		if err != nil {
			log.Printf("[agent:_event_poller] Error while polling via websocket (%s)", err)
		}
		select {
		case <-a.ctx.Done():
			return
		default:
		}
	}
}

// StartEventPolling ports Agent.start_event_polling: a background goroutine
// (Python: threading.Thread(..., name="Event Polling", daemon=True)).
func (a *Agent) StartEventPolling() {
	go a.eventPoller()
}
