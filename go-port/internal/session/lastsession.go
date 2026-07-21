// Package session ports the LastSession part of pwnagotchi/log.py: parsing
// the previous run's log file into aggregate stats (deauths, handshakes,
// epochs, reward stats, peers) for display and grid reporting.
package session

import (
	"crypto/md5"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
	"github.com/jayofelony/pwnagotchi/go-port/internal/mesh"
	"github.com/jayofelony/pwnagotchi/go-port/internal/voice"
)

// LastSessionFile mirrors log.LAST_SESSION_FILE. A var (not const), like
// internal/unit's *Path variables, so tests can point it at a scratch file
// instead of the real system path.
var LastSessionFile = "/root/.pwnagotchi-last-session"

// Tokens mirror LastSession's class-level string constants.
const (
	epochToken     = "[epoch "
	trainingToken  = " training epoch "
	startToken     = "connecting to http"
	deauthToken    = "deauthing "
	assocToken     = "sending association frame to "
	handshakeToken = "!!! captured new handshake "
	peerToken      = "detected unit "
)

var (
	epochParser     = regexp.MustCompile(`^.+\[epoch (\d+)\] (.+)`)
	epochDataParser = regexp.MustCompile(`([a-z_]+)=(\S+)`)
	peerParser      = regexp.MustCompile(`detected unit (.+)@(.+) \(v.+\) on channel \d+ \(([\d\-]+) dBm\) \[sid:(.+) pwnd_tot:(\d+) uptime:(\d+)\]`)
)

// View is the subset of pwnagotchi.ui.view.View LastSession.parse calls into.
type View interface {
	OnReadingLogs(linesSoFar int)
}

// LastSession ports log.LastSession.
type LastSession struct {
	Config config.Map
	Voice  *voice.Voice
	Path   string

	LastSession        []string
	LastSessionID      string
	LastSavedSessionID string

	Duration      string
	DurationHuman string

	Deauthed   int
	Associated int
	Handshakes int
	Peers      int
	LastPeer   *mesh.Peer

	Epochs      int
	TrainEpochs int
	MinReward   float64
	MaxReward   float64
	AvgReward   float64

	Parsed bool
}

// New ports LastSession.__init__.
func New(cfg config.Map, path, lang string) *LastSession {
	return &LastSession{
		Config:    cfg,
		Voice:     voice.New(lang),
		Path:      path,
		MinReward: 1000,
		MaxReward: -1000,
	}
}

func (s *LastSession) getLastSavedSessionID() string {
	data, err := os.ReadFile(LastSessionFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// SaveSessionID ports LastSession.save_session_id: a plain (non-atomic)
// overwrite, matching Python's `open(..., 'w+t')` exactly.
func (s *LastSession) SaveSessionID() error {
	if err := os.WriteFile(LastSessionFile, []byte(s.LastSessionID), 0o644); err != nil {
		return err
	}
	s.LastSavedSessionID = s.LastSessionID
	return nil
}

// parseDatetime ports LastSession._parse_datetime: strips a fractional-
// second suffix (by '.'), then a msec suffix (by ','), matching a log line
// timestamp like "2024-01-01 12:00:00,123" down to "2024-01-01 12:00:00",
// then parses it as LOCAL time (matching Python's time.mktime(dt.timetuple()),
// which interprets a naive datetime in the system's local timezone).
func (s *LastSession) parseDatetime(dt string) (time.Time, error) {
	if idx := strings.IndexByte(dt, '.'); idx != -1 {
		dt = dt[:idx]
	}
	if idx := strings.IndexByte(dt, ','); idx != -1 {
		dt = dt[:idx]
	}
	return time.ParseInLocation("2006-01-02 15:04:05", dt, time.Local)
}

// parseStats ports LastSession._parse_stats.
func (s *LastSession) parseStats() {
	s.Duration = ""
	s.DurationHuman = ""
	s.Deauthed = 0
	s.Associated = 0
	s.Handshakes = 0
	s.Epochs = 0
	s.TrainEpochs = 0
	s.Peers = 0
	s.LastPeer = nil
	s.MinReward = 1000
	s.MaxReward = -1000
	s.AvgReward = 0

	var startedAt, stoppedAt time.Time
	started := false
	cache := map[string]bool{}
	peerCache := map[string]*mesh.Peer{}

	for _, line := range s.LastSession {
		parts := strings.SplitN(line, "]", 2)
		if len(parts) < 2 {
			continue
		}

		lineTimestamp := strings.TrimPrefix(parts[0], "[")
		body := parts[1]

		ts, err := s.parseDatetime(lineTimestamp)
		if err != nil {
			log.Printf("error parsing line '%s': %s", body, err)
			continue
		}
		stoppedAt = ts
		if !started {
			startedAt = stoppedAt
			started = true
		}

		switch {
		case strings.Contains(body, deauthToken) && !cache[body]:
			s.Deauthed++
			cache[body] = true

		case strings.Contains(body, assocToken) && !cache[body]:
			s.Associated++
			cache[body] = true

		case strings.Contains(body, handshakeToken) && !cache[body]:
			s.Handshakes++
			cache[body] = true

		case strings.Contains(body, trainingToken):
			s.TrainEpochs++

		case strings.Contains(body, epochToken):
			s.Epochs++
			m := epochParser.FindStringSubmatch(body)
			if m != nil {
				epochData := m[2]
				for _, kv := range epochDataParser.FindAllStringSubmatch(epochData, -1) {
					key, value := kv[1], kv[2]
					if key == "reward" {
						reward, err := strconv.ParseFloat(value, 64)
						if err != nil {
							continue
						}
						s.AvgReward += reward
						if reward < s.MinReward {
							s.MinReward = reward
						} else if reward > s.MaxReward {
							s.MaxReward = reward
						}
					}
				}
			}

		case strings.Contains(body, peerToken):
			m := peerParser.FindStringSubmatch(body)
			if m != nil {
				name, pubkey, rssi, sid, pwndTot, _ := m[1], m[2], m[3], m[4], m[5], m[6]
				if existing, ok := peerCache[pubkey]; !ok {
					rssiInt, _ := strconv.Atoi(rssi)
					pwndTotInt, _ := strconv.Atoi(pwndTot)
					peer := mesh.NewPeer(map[string]interface{}{
						"session_id": sid,
						"channel":    float64(1),
						"rssi":       float64(rssiInt),
						"identity":   pubkey,
						"advertisement": map[string]interface{}{
							"name":     name,
							"pwnd_tot": float64(pwndTotInt),
						},
					})
					s.LastPeer = peer
					s.Peers++
					peerCache[pubkey] = peer
				} else {
					existing.Adv["pwnd_tot"] = pwndTot
				}
			}
		}
	}

	var hours, mins, secs int64
	if started {
		duration := stoppedAt.Sub(startedAt)
		total := int64(duration.Seconds())
		hours = total / 3600
		mins = (total % 3600) / 60
		secs = total % 60
	}

	s.Duration = fmt.Sprintf("%02d:%02d:%02d", hours, mins, secs)

	var parts []string
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", hours, s.Voice.Hhmmss(hours, "h")))
	}
	if mins > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", mins, s.Voice.Hhmmss(mins, "m")))
	}
	if secs > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", secs, s.Voice.Hhmmss(secs, "s")))
	}
	s.DurationHuman = strings.Join(parts, ", ")

	if s.Epochs > 0 {
		s.AvgReward /= float64(s.Epochs)
	}
	// Python: self.avg_reward /= (self.epochs if self.epochs else 1) — for
	// epochs==0 this divides by 1, a no-op on the starting 0.0, so the
	// branch above (skipping the divide entirely) is equivalent.
}

// Parse ports LastSession.parse(ui, skip=False).
func (s *LastSession) Parse(ui View, skip bool) error {
	if skip {
		s.Parsed = true
		return nil
	}

	if ui != nil {
		ui.OnReadingLogs(0)
	}

	var lines []string
	if _, err := os.Stat(s.Path); err == nil {
		reversed, err := readLinesBackwards(s.Path, func(linesSoFar int) {
			if ui != nil && linesSoFar%100 == 0 {
				ui.OnReadingLogs(linesSoFar)
			}
		})
		if err != nil {
			return err
		}
		lines = reversed
	}

	if len(lines) == 0 {
		lines = append(lines, "Initial Session")
	}

	if ui != nil {
		ui.OnReadingLogs(0)
	}

	s.LastSession = lines
	sum := md5.Sum([]byte(lines[0]))
	s.LastSessionID = fmt.Sprintf("%x", sum)
	s.LastSavedSessionID = s.getLastSavedSessionID()

	s.parseStats()
	s.Parsed = true
	return nil
}

// IsNew ports LastSession.is_new.
func (s *LastSession) IsNew() bool { return s.LastSessionID != s.LastSavedSessionID }
