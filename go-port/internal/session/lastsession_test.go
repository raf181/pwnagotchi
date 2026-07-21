package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
)

const sampleLog = `[2024-01-01 12:00:00,000] [INFO] [MainThread] : connecting to http://127.0.0.1:8081/api ...
[2024-01-01 12:00:01,000] [INFO] [MainThread] : sending association frame to aa:bb:cc:dd:ee:01 (something)
[2024-01-01 12:00:02,000] [INFO] [MainThread] : deauthing aa:bb:cc:dd:ee:01 (something)
[2024-01-01 12:00:03,000] [INFO] [MainThread] : !!! captured new handshake for FooNet !!!
[2024-01-01 12:00:04,000] [INFO] [MainThread] : detected unit pwnA@fingerprint1 (v2.9.5) on channel 6 (-40 dBm) [sid:sess1 pwnd_tot:5 uptime:100]
[2024-01-01 12:00:05,000] [INFO] [MainThread] : [epoch 3] duration=00:00:01 slept_for=00:00:00 blind=0 sad=0 bored=0 inactive=0 active=1 peers=1 tot_bond=0.00 avg_bond=0.00 hops=1 missed=0 deauths=1 assocs=1 handshakes=1 cpu=10% mem=20% temperature=40C reward=5.0
[2024-01-01 12:00:06,000] [INFO] [MainThread] :  training epoch 1 completed
`

type fakeReadingLogsView struct{ calls []int }

func (v *fakeReadingLogsView) OnReadingLogs(linesSoFar int) { v.calls = append(v.calls, linesSoFar) }

func newTestSession(t *testing.T) (*LastSession, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.log")
	if err := os.WriteFile(logPath, []byte(sampleLog), 0o644); err != nil {
		t.Fatal(err)
	}
	origLastSessionFile := LastSessionFile
	LastSessionFile = filepath.Join(dir, "last-session-id")
	t.Cleanup(func() { LastSessionFile = origLastSessionFile })

	cfg := config.Map{"main": config.Map{"lang": "en"}}
	s := New(cfg, logPath, "en")
	return s, dir
}

// TestParseMatchesPythonGolden replays a real Python LastSession.parse()
// run over the identical sample log (see the inline Python transcript this
// was captured from) and checks every aggregate field matches, INCLUDING
// the min/max-reward mutual-exclusivity bug (Python's `if/elif` means a
// single data point can only ever update min_reward OR max_reward, never
// both, even when both starting sentinels — 1000 / -1000 — would otherwise
// need updating).
func TestParseMatchesPythonGolden(t *testing.T) {
	s, _ := newTestSession(t)
	view := &fakeReadingLogsView{}
	if err := s.Parse(view, false); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if s.Duration != "00:00:06" {
		t.Errorf("Duration = %q, want 00:00:06", s.Duration)
	}
	if s.DurationHuman != "6 seconds" {
		t.Errorf("DurationHuman = %q, want %q", s.DurationHuman, "6 seconds")
	}
	if s.Deauthed != 1 || s.Associated != 1 || s.Handshakes != 1 || s.Peers != 1 {
		t.Errorf("counts: deauthed=%d associated=%d handshakes=%d peers=%d", s.Deauthed, s.Associated, s.Handshakes, s.Peers)
	}
	if s.Epochs != 1 || s.TrainEpochs != 1 {
		t.Errorf("epochs=%d train_epochs=%d, want 1,1", s.Epochs, s.TrainEpochs)
	}
	if s.MinReward != 5.0 {
		t.Errorf("MinReward = %v, want 5.0", s.MinReward)
	}
	if s.MaxReward != -1000 {
		t.Errorf("MaxReward = %v, want -1000 (elif branch never runs when if already matched — see Python transcript)", s.MaxReward)
	}
	if s.AvgReward != 5.0 {
		t.Errorf("AvgReward = %v, want 5.0", s.AvgReward)
	}
	if s.LastPeer == nil || s.LastPeer.Name() != "pwnA" {
		t.Errorf("LastPeer = %v, want name pwnA", s.LastPeer)
	}
	if len(s.LastSession) != 7 {
		t.Errorf("num lines = %d, want 7", len(s.LastSession))
	}
}

func TestParseSkipSetsParsedWithoutReadingFile(t *testing.T) {
	s, _ := newTestSession(t)
	if err := s.Parse(nil, true); err != nil {
		t.Fatal(err)
	}
	if !s.Parsed {
		t.Fatal("Parsed should be true even when skipped")
	}
	if s.LastSession != nil {
		t.Fatal("skip=true must not touch LastSession")
	}
}

func TestParseMissingFileUsesInitialSession(t *testing.T) {
	dir := t.TempDir()
	origLastSessionFile := LastSessionFile
	LastSessionFile = filepath.Join(dir, "last-session-id")
	t.Cleanup(func() { LastSessionFile = origLastSessionFile })

	cfg := config.Map{"main": config.Map{"lang": "en"}}
	s := New(cfg, filepath.Join(dir, "does-not-exist.log"), "en")
	if err := s.Parse(nil, false); err != nil {
		t.Fatal(err)
	}
	if len(s.LastSession) != 1 || s.LastSession[0] != "Initial Session" {
		t.Fatalf("LastSession = %v, want [\"Initial Session\"]", s.LastSession)
	}
}

func TestIsNewAndSaveSessionID(t *testing.T) {
	s, _ := newTestSession(t)
	if err := s.Parse(nil, false); err != nil {
		t.Fatal(err)
	}
	if !s.IsNew() {
		t.Fatal("a fresh session with no saved ID should be 'new'")
	}
	if err := s.SaveSessionID(); err != nil {
		t.Fatal(err)
	}
	if s.IsNew() {
		t.Fatal("after SaveSessionID, IsNew() should be false against itself")
	}

	saved, err := os.ReadFile(LastSessionFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != s.LastSessionID {
		t.Fatalf("on-disk session id = %q, want %q", saved, s.LastSessionID)
	}
}

func TestParseDatetimeStripsFractionalAndMsec(t *testing.T) {
	s, _ := newTestSession(t)
	got, err := s.parseDatetime("2024-01-01 12:00:00,999")
	if err != nil {
		t.Fatal(err)
	}
	if got.Hour() != 12 || got.Minute() != 0 || got.Second() != 0 {
		t.Fatalf("parseDatetime = %v", got)
	}
}
