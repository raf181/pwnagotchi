package view

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
	"github.com/jayofelony/pwnagotchi/go-port/internal/mesh"
	"github.com/jayofelony/pwnagotchi/go-port/internal/session"
	"github.com/jayofelony/pwnagotchi/go-port/internal/version"
	"github.com/jayofelony/pwnagotchi/go-port/internal/voice"
)

// OnStarting ports View.on_starting.
func (v *View) OnStarting() {
	v.Set("status", v.voice.OnStarting()+fmt.Sprintf("\n(v%s)", version.Version))
	v.Set("face", v.getRandomFace(v.faces.Awake))
	v.Update(false, nil)
}

// OnKeysGeneration ports View.on_keys_generation.
func (v *View) OnKeysGeneration() {
	v.Set("face", v.getRandomFace(v.faces.Awake))
	v.Set("status", v.voice.OnKeysGeneration())
	v.Update(false, nil)
}

// OnNormal ports View.on_normal.
func (v *View) OnNormal() {
	v.Set("face", v.getRandomFace(v.faces.Awake))
	v.Set("status", v.voice.OnNormal())
	v.Update(false, nil)
}

// OnManualMode ports View.on_manual_mode(last_session).
func (v *View) OnManualMode(s *session.LastSession) {
	v.Set("mode", "MANU")
	if s.Epochs > 3 && s.Handshakes == 0 {
		v.Set("face", v.getRandomFace(v.faces.Sad))
	} else {
		v.Set("face", v.getRandomFace(v.faces.Happy))
	}
	v.Set("status", v.voice.OnLastSessionData(sessionSummary(s)))
	v.Set("epoch", fmt.Sprintf("%04d", s.Epochs))
	v.Set("uptime", s.Duration)
	v.Set("channel", "-")
	v.Set("aps", fmt.Sprintf("%d", s.Associated))

	dir, _ := bettercapMap(v.config)["handshakes"].(string)
	tot, _ := config.TotalUniqueHandshakes(filepath.Glob, dir)
	v.Set("shakes", fmt.Sprintf("%d (%d)", s.Handshakes, tot))

	v.SetClosestPeer(s.LastPeer, s.Peers)
	v.Update(false, nil)
}

func bettercapMap(cfg config.Map) config.Map {
	m, _ := cfg["bettercap"].(config.Map)
	return m
}

func sessionSummary(s *session.LastSession) voice.LastSessionSummary {
	return voice.LastSessionSummary{
		Duration: s.Duration, DurationHuman: s.DurationHuman,
		Deauthed: s.Deauthed, Associated: s.Associated, Handshakes: s.Handshakes, Peers: s.Peers,
	}
}

// SetClosestPeer ports View.set_closest_peer.
func (v *View) SetClosestPeer(peer *mesh.Peer, numTotal int) {
	if peer == nil {
		v.Set("friend_face", nil)
		v.Set("friend_name", nil)
		v.Update(false, nil)
		return
	}

	var numBars int
	switch {
	case peer.RSSI >= -67:
		numBars = 4
	case peer.RSSI >= -70:
		numBars = 3
	case peer.RSSI >= -80:
		numBars = 2
	default:
		numBars = 1
	}

	name := strings.Repeat("▌", numBars) + strings.Repeat("│", 4-numBars)
	name += fmt.Sprintf(" %s %d (%d)", peer.Name(), peer.PwndRun(), peer.PwndTotal())

	if numTotal > 1 {
		if numTotal > 9000 {
			name += " of over 9000"
		} else {
			name += fmt.Sprintf(" of %d", numTotal)
		}
	}

	v.Set("friend_face", peer.Face())
	v.Set("friend_name", name)
	v.Update(false, nil)
}

// OnNewPeer ports View.on_new_peer.
func (v *View) OnNewPeer(peer *mesh.Peer) {
	var face string
	switch {
	case peer.FirstEncounter():
		face = v.getRandomFace(pick(v.rnd.Intn(2), v.faces.Awake, v.faces.Cool))
	case peer.IsGoodFriend(v.config):
		face = v.getRandomFace(pick(v.rnd.Intn(3), v.faces.Motivated, v.faces.Friend, v.faces.Happy))
	default:
		face = v.getRandomFace(pick(v.rnd.Intn(3), v.faces.Excited, v.faces.Happy, v.faces.Smart))
	}
	v.Set("face", face)
	v.Set("status", v.voice.OnNewPeer(peer))
	v.Update(false, nil)
	time.Sleep(3 * time.Second)
}

func pick(i int, options ...string) string { return options[i] }

// OnLostPeer ports View.on_lost_peer.
func (v *View) OnLostPeer(peer *mesh.Peer) {
	v.Set("face", v.getRandomFace(v.faces.Lonely))
	v.Set("status", v.voice.OnLostPeer(peer))
	v.Update(false, nil)
}

// OnFreeChannel ports View.on_free_channel.
func (v *View) OnFreeChannel(channel int) {
	v.Set("face", v.getRandomFace(v.faces.Smart))
	v.Set("status", v.voice.OnFreeChannel(channel))
	v.Update(false, nil)
}

// OnReadingLogs ports View.on_reading_logs.
func (v *View) OnReadingLogs(linesSoFar int) {
	v.Set("face", v.getRandomFace(v.faces.Smart))
	v.Set("status", v.voice.OnReadingLogs(linesSoFar))
	v.Update(false, nil)
}

// Wait ports View.wait(secs, sleeping=True).
func (v *View) Wait(secs float64, sleeping bool) {
	wasNormal := v.IsNormal()
	part := secs / 10.0

	for step := 0; step < 10; step++ {
		if wasNormal || step > 5 {
			if sleeping {
				v.Set("face", v.getRandomFace(v.faces.Sleep))
				if secs > 1 {
					v.Set("status", v.voice.OnNapping(int(secs)))
				} else {
					v.Set("status", v.voice.OnAwakening())
				}
			} else {
				v.Set("status", v.voice.OnWaiting(int(secs)))

				goodMood := v.agent != nil && v.agent.InGoodMood()
				if step%2 == 0 {
					if goodMood {
						v.Set("face", v.getRandomFace(v.faces.LookRHappy))
					} else {
						v.Set("face", v.getRandomFace(v.faces.LookR))
					}
				} else {
					if goodMood {
						v.Set("face", v.getRandomFace(v.faces.LookLHappy))
					} else {
						v.Set("face", v.getRandomFace(v.faces.LookL))
					}
				}
			}
		}
		time.Sleep(time.Duration(part * float64(time.Second)))
		secs -= part
	}

	v.OnNormal()
}

// OnShutdown ports View.on_shutdown.
func (v *View) OnShutdown() {
	v.Set("face", v.getRandomFace(v.faces.Sleep))
	v.Set("status", v.voice.OnShutdown())
	v.Update(true, nil)
	v.mu.Lock()
	v.frozen = true
	v.mu.Unlock()
}

// OnBored ports View.on_bored.
func (v *View) OnBored() {
	v.Set("face", v.getRandomFace(v.faces.Bored))
	v.Set("status", v.voice.OnBored())
	v.Update(false, nil)
}

// OnSad ports View.on_sad.
func (v *View) OnSad() {
	v.Set("face", v.getRandomFace(v.faces.Sad))
	v.Set("status", v.voice.OnSad())
	v.Update(false, nil)
}

// OnAngry ports View.on_angry.
func (v *View) OnAngry() {
	v.Set("face", v.getRandomFace(v.faces.Angry))
	v.Set("status", v.voice.OnAngry())
	v.Update(false, nil)
}

// OnMotivated ports View.on_motivated.
func (v *View) OnMotivated(reward float64) {
	v.Set("face", v.getRandomFace(v.faces.Motivated))
	v.Set("status", v.voice.OnMotivated(reward))
	v.Update(false, nil)
}

// OnDemotivated ports View.on_demotivated.
func (v *View) OnDemotivated(reward float64) {
	v.Set("face", v.getRandomFace(v.faces.Demotivated))
	v.Set("status", v.voice.OnDemotivated(reward))
	v.Update(false, nil)
}

// OnExcited ports View.on_excited.
func (v *View) OnExcited() {
	v.Set("face", v.getRandomFace(v.faces.Excited))
	v.Set("status", v.voice.OnExcited())
	v.Update(false, nil)
}

// OnAssoc ports View.on_assoc(ap): ap is the generic bettercap AP record
// (map[string]interface{} — matches internal/agent.AP, the same genericity
// bettercap.Client's own JSON responses already use).
func (v *View) OnAssoc(ap map[string]interface{}) {
	ssid, _ := ap["hostname"].(string)
	bssid, _ := ap["mac"].(string)
	v.Set("face", v.getRandomFace(v.faces.Intense))
	v.Set("status", v.voice.OnAssoc(ssid, bssid))
	v.Update(false, nil)
}

// OnDeauth ports View.on_deauth(sta).
func (v *View) OnDeauth(sta map[string]interface{}) {
	mac, _ := sta["mac"].(string)
	v.Set("face", v.getRandomFace(v.faces.Cool))
	v.Set("status", v.voice.OnDeauth(mac))
	v.Update(false, nil)
}

// OnMiss ports View.on_miss.
func (v *View) OnMiss(who string) {
	v.Set("face", v.getRandomFace(v.faces.Sad))
	v.Set("status", v.voice.OnMiss(who))
	v.Update(false, nil)
}

// OnGrateful ports View.on_grateful.
func (v *View) OnGrateful() {
	v.Set("face", v.getRandomFace(v.faces.Grateful))
	v.Set("status", v.voice.OnGrateful())
	v.Update(false, nil)
}

// OnLonely ports View.on_lonely.
func (v *View) OnLonely() {
	v.Set("face", v.getRandomFace(v.faces.Lonely))
	v.Set("status", v.voice.OnLonely())
	v.Update(false, nil)
}

// OnHandshakes ports View.on_handshakes.
func (v *View) OnHandshakes(newShakes int) {
	v.Set("face", v.getRandomFace(v.faces.Happy))
	v.Set("status", v.voice.OnHandshakes(newShakes))
	v.Update(false, nil)
}

// OnUnreadMessages ports View.on_unread_messages.
func (v *View) OnUnreadMessages(count, total int) {
	v.Set("face", v.getRandomFace(v.faces.Excited))
	v.Set("status", v.voice.OnUnreadMessages(count, total))
	v.Update(false, nil)
	time.Sleep(5 * time.Second)
}

// OnUploading ports View.on_uploading.
func (v *View) OnUploading(to string) {
	v.Set("face", v.getRandomFace(v.faces.Upload))
	v.Set("status", v.voice.OnUploading(to))
	v.Update(true, nil)
}

// OnRebooting ports View.on_rebooting.
func (v *View) OnRebooting() {
	v.Set("face", v.getRandomFace(v.faces.Broken))
	v.Set("status", v.voice.OnRebooting())
	v.Update(false, nil)
}

// OnCustom ports View.on_custom.
func (v *View) OnCustom(text string) {
	v.Set("face", v.getRandomFace(v.faces.Debug))
	v.Set("status", v.voice.Custom(text))
	v.Update(false, nil)
}
