package mesh

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/ui/faces"
)

// DefaultFriendFace mirrors pwnagotchi.ui.faces.FRIEND.
const DefaultFriendFace = faces.Friend

// ParseRFC3339 mirrors mesh.peer.parse_rfc3339: the zero-value Go/Protobuf
// RFC3339 sentinel "0001-01-01T00:00:00Z" maps to "now" (matching Python's
// special case for pwngrid's zero-time sentinel), and any fractional-second
// suffix is dropped before parsing with a bare "%Y-%m-%dT%H:%M:%S" layout —
// NOT full RFC3339 (no timezone offset is parsed; matches Python exactly,
// which also ignores any 'Z'/offset suffix once the '.' split happens to
// strip it along with the fractional seconds in the common case, or leaves
// it in place and fails to parse — see docs/known-differences.md for the
// exact edge case).
func ParseRFC3339(dt string) (time.Time, error) {
	if dt == "0001-01-01T00:00:00Z" {
		return time.Now(), nil
	}
	head := dt
	if idx := strings.IndexByte(dt, '.'); idx != -1 {
		head = dt[:idx]
	}
	return time.Parse("2006-01-02T15:04:05", head)
}

// Peer ports mesh.peer.Peer.
type Peer struct {
	FirstMet    time.Time
	FirstSeen   time.Time
	PrevSeen    time.Time
	LastSeen    time.Time
	Encounters  float64
	SessionID   string
	LastChannel int
	RSSI        float64
	Adv         map[string]interface{}
}

// NewPeer ports Peer.__init__(obj): obj is the generic JSON object grid.py's
// /mesh/peers returns (decoded into map[string]interface{}).
func NewPeer(obj map[string]interface{}) *Peer {
	now := time.Now()
	justMet := now.Format("2006-01-02T15:04:05")

	p := &Peer{LastSeen: now}

	firstMet, err1 := ParseRFC3339(getString(obj, "met_at", justMet))
	firstSeen, err2 := ParseRFC3339(getString(obj, "detected_at", justMet))
	prevSeen, err3 := ParseRFC3339(getString(obj, "prev_seen_at", justMet))
	if err1 != nil || err2 != nil || err3 != nil {
		err := err1
		if err == nil {
			err = err2
		}
		if err == nil {
			err = err3
		}
		log.Printf("error while parsing peer timestamps: %s", err)
		zero, _ := time.Parse("2006-01-02T15:04:05", justMet)
		p.FirstMet, p.FirstSeen, p.PrevSeen = zero, zero, zero
	} else {
		p.FirstMet, p.FirstSeen, p.PrevSeen = firstMet, firstSeen, prevSeen
	}

	p.Encounters = getFloat(obj, "encounters", 0)
	p.SessionID = getString(obj, "session_id", "")
	p.LastChannel = int(getFloat(obj, "channel", 1))
	p.RSSI = getFloat(obj, "rssi", 0)
	if adv, ok := obj["advertisement"].(map[string]interface{}); ok {
		p.Adv = adv
	} else {
		p.Adv = map[string]interface{}{}
	}
	return p
}

// Update ports Peer.update(new).
func (p *Peer) Update(new *Peer) {
	if p.Name() != new.Name() {
		log.Printf("peer %s changed name: %s -> %s", p.FullName(), p.Name(), new.Name())
	}
	if p.SessionID != new.SessionID {
		log.Printf("peer %s changed session id: %s -> %s", p.FullName(), p.SessionID, new.SessionID)
	}
	p.Adv = new.Adv
	p.RSSI = new.RSSI
	p.SessionID = new.SessionID
	p.LastSeen = time.Now()
	p.PrevSeen = new.PrevSeen
	p.FirstMet = new.FirstMet
	p.Encounters = new.Encounters
}

// InactiveFor ports Peer.inactive_for.
func (p *Peer) InactiveFor() float64 { return time.Since(p.LastSeen).Seconds() }

// FirstEncounter ports Peer.first_encounter.
func (p *Peer) FirstEncounter() bool { return p.Encounters == 1 }

// IsGoodFriend ports Peer.is_good_friend.
func (p *Peer) IsGoodFriend(cfg config.Map) bool {
	bondFactor, _ := config.DigFloat(cfg, "personality", "bond_encounters_factor")
	return p.Encounters >= bondFactor
}

func (p *Peer) Face() string     { return getString(p.Adv, "face", DefaultFriendFace) }
func (p *Peer) Name() string     { return getString(p.Adv, "name", "???") }
func (p *Peer) Identity() string { return getString(p.Adv, "identity", "???") }
func (p *Peer) FullName() string { return fmt.Sprintf("%s@%s", p.Name(), p.Identity()) }
func (p *Peer) Version() string  { return getString(p.Adv, "version", "1.0.0a") }
func (p *Peer) PwndRun() int     { return int(getFloat(p.Adv, "pwnd_run", 0)) }
func (p *Peer) PwndTotal() int   { return int(getFloat(p.Adv, "pwnd_tot", 0)) }
func (p *Peer) Uptime() float64  { return getFloat(p.Adv, "uptime", 0) }
func (p *Peer) Epoch() float64   { return getFloat(p.Adv, "epoch", 0) }

// IsCloser ports Peer.is_closer.
func (p *Peer) IsCloser(other *Peer) bool { return p.RSSI > other.RSSI }

func getString(m map[string]interface{}, key, def string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}

func getFloat(m map[string]interface{}, key string, def float64) float64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int64:
			return float64(n)
		case int:
			return float64(n)
		}
	}
	return def
}
