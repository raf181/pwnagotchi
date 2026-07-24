package bttether

import (
	"context"
	"fmt"
	"time"
)

// monitorLoop ports _connection_monitor_loop: periodically checks
// whether the current phone (if any) is still connected, and attempts a
// reconnect via the same connectDevice path if not, with a simple
// consecutive-failure cooldown matching the real
// MAX_RECONNECT_FAILURES/reconnect_failure_cooldown behavior.
func (p *Plugin) monitorLoop() {
	defer close(p.done)

	p.mu.Lock()
	interval := p.reconnectInterval
	if interval <= 0 {
		interval = defaultReconnectInterval
	}
	stop := p.stop
	p.mu.Unlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			p.reconnectTick()
		}
	}
}

func (p *Plugin) reconnectTick() {
	p.mu.Lock()
	mac := p.phoneMAC
	inProgress := p.connectionInProgress
	cooldownUntil := p.cooldownUntil
	autoReconnect := p.autoReconnect
	p.mu.Unlock()

	if !autoReconnect || mac == "" || inProgress {
		return
	}
	if !cooldownUntil.IsZero() && p.now().Before(cooldownUntil) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), btCommandTimeout)
	defer cancel()
	status, err := p.currentStatus(ctx, mac)
	if err == nil && status.Connected && status.PANActive {
		p.mu.Lock()
		p.reconnectFailureCount = 0
		p.mu.Unlock()
		return
	}

	p.setStatus(StateReconnecting, fmt.Sprintf("Reconnecting to %s...", mac))
	info, _ := p.deviceInfo(ctx, mac)
	device := DiscoveredDevice{MAC: mac, Name: mac, HasNAP: info.HasNAP, Paired: info.Paired, Trusted: info.Trusted}
	p.connectDevice(context.Background(), device)

	p.mu.Lock()
	if p.status == StateError {
		p.reconnectFailureCount++
		if p.reconnectFailureCount >= maxReconnectFailures {
			cooldown := p.reconnectFailureCooldown
			if cooldown <= 0 {
				cooldown = defaultReconnectFailureCooldown
			}
			p.cooldownUntil = p.now().Add(cooldown)
			p.logfLocked("WARNING", fmt.Sprintf("Reconnect failed %d times in a row, backing off for %s", p.reconnectFailureCount, cooldown))
		}
	} else {
		p.reconnectFailureCount = 0
		p.cooldownUntil = time.Time{}
	}
	p.mu.Unlock()
}

// refreshOnScreenStatus ports on_ui_update: recompute the mini/detailed
// status text from the current connection status and push it through the
// View capability, exactly like memtemp/wittypi's established pattern.
func (p *Plugin) refreshOnScreenStatus() {
	p.mu.Lock()
	view := p.view
	mac := p.phoneMAC
	showMini := p.showMiniStatus
	showDetailed := p.showDetailedStatus
	showOnScreen := p.showOnScreen
	p.mu.Unlock()

	if view == nil || !showOnScreen || mac == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), btCommandTimeout)
	defer cancel()
	status, err := p.fullConnectionStatus(ctx, mac)
	if err != nil {
		return
	}

	if showMini {
		view.Set("bt_tether_mini", screenStatusLetter(status))
	}
	if showDetailed {
		view.Set("bt_tether_detail", formatDetailedStatus(status))
	}
}

// formatDetailedStatus ports _format_detailed_status's real text line.
func formatDetailedStatus(s ConnectionStatus) string {
	switch {
	case s.PANActive && s.IPAddress != "":
		return fmt.Sprintf("BT: %s (%s)", s.Interface, s.IPAddress)
	case s.Connected:
		return "BT: connected, no IP"
	case s.Paired:
		return "BT: paired, not connected"
	default:
		return "BT: not paired"
	}
}
