package agent

import (
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/epoch"
)

// Recon ports Agent.recon.
func (a *Agent) Recon() {
	reconTime, _ := digFloat(a.config, "personality", "recon_time")
	maxInactive, _ := digFloat(a.config, "personality", "max_inactive_scale")
	reconMul, _ := digFloat(a.config, "personality", "recon_inactive_multiplier")
	channels, _ := digSlice(a.config, "personality", "channels")

	if float64(a.Automata.Epoch.InactiveFor) >= maxInactive {
		reconTime *= reconMul
	}

	a.view.Set("channel", "*")

	if len(channels) == 0 {
		a.mu.Lock()
		a.currentChannel = 0
		a.mu.Unlock()
		log.Printf("RECON %.0fs", reconTime)
		a.Run("wifi.recon.channel clear", true)
	} else {
		parts := make([]string, len(channels))
		for i, c := range channels {
			parts[i] = strconv.Itoa(int(toFloat(c)))
		}
		joined := strings.Join(parts, ",")
		log.Printf("RECON %.0fs ON CHANNELS %s", reconTime, joined)
		a.Run("wifi.recon.channel "+joined, true)
	}

	a.WaitFor(reconTime, false)
}

// SetAccessPoints ports Agent.set_access_points.
func (a *Agent) SetAccessPoints(aps []AP) []AP {
	a.mu.Lock()
	a.accessPoints = aps
	a.mu.Unlock()

	a.emit.On("wifi_update", a, aps)

	obs := make([]epoch.AccessPointObservation, len(aps))
	for i, ap := range aps {
		obs[i] = epoch.AccessPointObservation{Channel: getInt(ap, "channel"), NumClients: len(clients(ap))}
	}
	peers := a.AsyncAdvertiser.PeersSnapshot()
	peerObs := make([]epoch.PeerObservation, len(peers))
	for i, p := range peers {
		peerObs[i] = epoch.PeerObservation{Encounters: p.Encounters, LastChannel: p.LastChannel}
	}
	a.Automata.Epoch.Observe(obs, peerObs)

	return aps
}

// GetAccessPoints ports Agent.get_access_points.
func (a *Agent) GetAccessPoints() []AP {
	var whitelist []string
	if raw, ok := digSlice(a.config, "main", "whitelist"); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				whitelist = append(whitelist, s)
			}
		}
	}

	var aps []AP
	s, err := a.Session("")
	if err != nil {
		log.Printf("Error while getting access points (%s)", err)
	} else {
		sm := asMap(s)
		wifi := asMap(sm["wifi"])
		allAPs := asSlice(wifi["aps"])
		a.emit.On("unfiltered_ap_list", a, allAPs)
		for _, raw := range allAPs {
			ap := asMap(raw)
			if ap == nil {
				continue
			}
			enc := getString(ap, "encryption")
			mac := strings.ToLower(getString(ap, "mac"))
			hostname := getString(ap, "hostname")
			if enc == "" || enc == "OPEN" {
				continue
			}
			if containsFold(whitelist, hostname) || (len(mac) >= 13 && containsFold(whitelist, mac[:13])) || containsFold(whitelist, mac) {
				continue
			}
			aps = append(aps, ap)
		}
	}

	sort.SliceStable(aps, func(i, j int) bool { return getInt(aps[i], "channel") < getInt(aps[j], "channel") })
	return a.SetAccessPoints(aps)
}

// GetTotalAPs ports Agent.get_total_aps.
func (a *Agent) GetTotalAPs() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.totAPs
}

// GetAPsOnChannel ports Agent.get_aps_on_channel.
func (a *Agent) GetAPsOnChannel() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.apsOnChannel
}

// GetCurrentChannel ports Agent.get_current_channel.
func (a *Agent) GetCurrentChannel() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.currentChannel
}

// ChannelGroup is one entry of Agent.get_access_points_by_channel's return
// value: a channel and the APs on it.
type ChannelGroup struct {
	Channel int
	APs     []AP
}

// GetAccessPointsByChannel ports Agent.get_access_points_by_channel.
func (a *Agent) GetAccessPointsByChannel() []ChannelGroup {
	aps := a.GetAccessPoints()
	channelsFilter, _ := digSlice(a.config, "personality", "channels")
	var allowed map[int]bool
	if len(channelsFilter) > 0 {
		allowed = map[int]bool{}
		for _, c := range channelsFilter {
			allowed[int(toFloat(c))] = true
		}
	}

	grouped := map[int][]AP{}
	var order []int
	for _, ap := range aps {
		ch := getInt(ap, "channel")
		if allowed != nil && !allowed[ch] {
			continue
		}
		if _, ok := grouped[ch]; !ok {
			order = append(order, ch)
		}
		grouped[ch] = append(grouped[ch], ap)
	}

	groups := make([]ChannelGroup, 0, len(order))
	for _, ch := range order {
		groups = append(groups, ChannelGroup{Channel: ch, APs: grouped[ch]})
	}
	sort.SliceStable(groups, func(i, j int) bool { return len(groups[i].APs) > len(groups[j].APs) })
	return groups
}

func (a *Agent) findAPStaIn(stationMac, apMac string, sess map[string]interface{}) (AP, Station, bool) {
	if sess == nil {
		return nil, nil, false
	}
	wifi := asMap(sess["wifi"])
	for _, raw := range asSlice(wifi["aps"]) {
		ap := asMap(raw)
		if ap == nil || getString(ap, "mac") != apMac {
			continue
		}
		for _, staRaw := range clients(ap) {
			if getString(staRaw, "mac") == stationMac {
				return ap, staRaw, true
			}
		}
		return ap, Station{"mac": stationMac, "vendor": ""}, true
	}
	return nil, nil, false
}

// Associate ports Agent.associate.
func (a *Agent) Associate(ap AP, throttle float64) {
	if a.IsStale() {
		log.Printf("recon is stale, skipping assoc(%s)", getString(ap, "mac"))
		return
	}
	if throttle == -1 {
		if t, ok := digFloat(a.config, "personality", "throttle_a"); ok {
			throttle = t
		}
	}

	associateEnabled, _ := boolAt(personalityMap(a.config), "associate")
	if associateEnabled && a.shouldInteract(getString(ap, "mac")) {
		a.view.OnAssoc(ap)

		log.Printf("sending association frame to %s (%s %s) on channel %d [%d clients], %d dBm...",
			getString(ap, "hostname"), getString(ap, "mac"), getString(ap, "vendor"), getInt(ap, "channel"), len(clients(ap)), getInt(ap, "rssi"))
		if _, err := a.Run("wifi.assoc "+getString(ap, "mac"), true); err != nil {
			a.OnError(getString(ap, "mac"), err)
		} else {
			a.Automata.Epoch.Track(epoch.TrackOptions{Assoc: true})
		}

		a.emit.On("association", a, ap)
		if throttle > 0 {
			time.Sleep(time.Duration(throttle * float64(time.Second)))
		}
		a.view.OnNormal()
	}
}

// Deauth ports Agent.deauth.
func (a *Agent) Deauth(ap AP, sta Station, throttle float64) {
	if a.IsStale() {
		log.Printf("recon is stale, skipping deauth(%s)", getString(sta, "mac"))
		return
	}
	if throttle == -1 {
		if t, ok := digFloat(a.config, "personality", "throttle_d"); ok {
			throttle = t
		}
	}

	deauthEnabled, _ := boolAt(personalityMap(a.config), "deauth")
	if deauthEnabled && a.shouldInteract(getString(sta, "mac")) {
		a.view.OnDeauth(sta)

		log.Printf("deauthing %s (%s) from %s (%s %s) on channel %d, %d dBm ...",
			getString(sta, "mac"), getString(sta, "vendor"), getString(ap, "hostname"), getString(ap, "mac"), getString(ap, "vendor"),
			getInt(ap, "channel"), getInt(ap, "rssi"))
		if _, err := a.Run("wifi.deauth "+getString(sta, "mac"), true); err != nil {
			a.OnError(getString(sta, "mac"), err)
		} else {
			a.Automata.Epoch.Track(epoch.TrackOptions{Deauth: true})
		}

		a.emit.On("deauthentication", a, ap, sta)
		if throttle > 0 {
			time.Sleep(time.Duration(throttle * float64(time.Second)))
		}
		a.view.OnNormal()
	}
}

// SetChannel ports Agent.set_channel.
func (a *Agent) SetChannel(channel int, verbose bool) {
	if a.IsStale() {
		log.Printf("recon is stale, skipping set_channel(%d)", channel)
		return
	}

	var wait float64
	if a.Automata.Epoch.DidDeauth {
		wait, _ = digFloat(a.config, "personality", "hop_recon_time")
	} else if a.Automata.Epoch.DidAssociate {
		wait, _ = digFloat(a.config, "personality", "min_recon_time")
	}

	current := a.GetCurrentChannel()
	if channel == current {
		return
	}

	if current != 0 && wait > 0 {
		if verbose {
			log.Printf("waiting for %.0fs on channel %d ...", wait, current)
		}
		a.WaitFor(wait, true)
	}
	if verbose && a.Automata.Epoch.AnyActivity {
		log.Printf("CHANNEL %d", channel)
	}

	if _, err := a.Run("wifi.recon.channel "+strconv.Itoa(channel), true); err != nil {
		log.Printf("Error while setting channel (%s)", err)
		return
	}
	a.mu.Lock()
	a.currentChannel = channel
	a.mu.Unlock()
	a.Automata.Epoch.Track(epoch.TrackOptions{Hop: true})
	a.view.Set("channel", strconv.Itoa(channel))
	a.emit.On("channel_hop", a, channel)
}

func toFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	}
	return 0
}
