package epoch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
)

func testConfig() config.Map {
	return config.Map{
		"personality": config.Map{
			"bond_encounters_factor": int64(20000),
			"sad_num_epochs":         int64(25),
			"bored_num_epochs":       int64(15),
		},
	}
}

// TestFormatEpochLogLineGolden replays testdata/epoch_log_line_golden.json,
// captured by running epoch.py's exact "%"-format expression (including its
// secs_to_hhmmss helper) against representative synthetic values —
// specifically covering the float truncation quirks in "cpu=%d%%"/"mem=%d%%"
// (Python's %d on a float truncates toward zero, it does not round) and the
// binary-float rounding of "%.2f" on values like 1.005.
func TestFormatEpochLogLineGolden(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "epoch_log_line_golden.json"))
	if err != nil {
		t.Fatalf("reading golden fixture: %v", err)
	}
	var cases []struct {
		In struct {
			Epoch    int64   `json:"epoch"`
			Duration float64 `json:"duration"`
			Slept    float64 `json:"slept"`
			Blind    int64   `json:"blind"`
			Sad      int64   `json:"sad"`
			Bored    int64   `json:"bored"`
			Inactive int64   `json:"inactive"`
			Active   int64   `json:"active"`
			Peers    int     `json:"peers"`
			TotBond  float64 `json:"tot_bond"`
			AvgBond  float64 `json:"avg_bond"`
			Hops     int64   `json:"hops"`
			Missed   int64   `json:"missed"`
			Deauths  int64   `json:"deauths"`
			Assocs   int64   `json:"assocs"`
			Shakes   int64   `json:"shakes"`
			CPU      float64 `json:"cpu"`
			Mem      float64 `json:"mem"`
			Temp     int     `json:"temp"`
		} `json:"in"`
		Out string `json:"out"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("empty golden fixture")
	}
	for _, c := range cases {
		in := c.In
		got := formatEpochLogLine(in.Epoch, in.Duration, in.Slept, in.Blind, in.Sad, in.Bored, in.Inactive, in.Active,
			in.Peers, in.TotBond, in.AvgBond, in.Hops, in.Missed, in.Deauths, in.Assocs, in.Shakes, in.CPU, in.Mem, in.Temp)
		if got != c.Out {
			t.Errorf("formatEpochLogLine(%+v) =\n  %q\nwant\n  %q", in, got, c.Out)
		}
	}
}

func TestEpochTrackAndNext(t *testing.T) {
	e := New(testConfig())
	e.Track(TrackOptions{Deauth: true})
	e.Track(TrackOptions{Assoc: true})
	e.Track(TrackOptions{Handshake: true, Inc: 2})
	e.Track(TrackOptions{Sleep: true, Inc: 3.5})
	e.Track(TrackOptions{Miss: true})

	if e.NumDeauths != 1 || e.NumAssocs != 1 || e.NumShakes != 2 || e.NumMissed != 1 {
		t.Fatalf("unexpected counters after Track: %+v", e)
	}
	if e.NumSlept != 3.5 {
		t.Fatalf("NumSlept = %v, want 3.5", e.NumSlept)
	}
	if !e.AnyActivity {
		t.Fatal("AnyActivity should be true after deauth/assoc")
	}

	e.Next()

	if e.Epoch != 1 {
		t.Fatalf("Epoch = %d, want 1 after Next()", e.Epoch)
	}
	if e.ActiveFor != 1 {
		t.Fatalf("ActiveFor = %d, want 1 (activity occurred)", e.ActiveFor)
	}
	if e.NumDeauths != 0 || e.NumAssocs != 0 || e.NumShakes != 0 || e.NumSlept != 0 {
		t.Fatal("per-epoch counters must reset after Next()")
	}

	data := e.Data()
	if data.MissedInteractions != 1 {
		t.Fatalf("Data().MissedInteractions = %d, want 1 (snapshotted before reset)", data.MissedInteractions)
	}
}

func TestEpochNextTransitionsToSadAfterThreshold(t *testing.T) {
	cfg := config.Map{
		"personality": config.Map{
			"bond_encounters_factor": int64(20000),
			"sad_num_epochs":         int64(2),
			"bored_num_epochs":       int64(1),
		},
	}
	e := New(cfg)
	// No activity across several epochs -> inactive_for climbs.
	e.Next() // inactive_for=1 -> bored (>=1, <2)
	if e.BoredFor != 1 || e.SadFor != 0 {
		t.Fatalf("after 1 inactive epoch: bored=%d sad=%d, want bored=1 sad=0", e.BoredFor, e.SadFor)
	}
	e.Next() // inactive_for=2 -> sad (>=2)
	if e.SadFor != 1 || e.BoredFor != 0 {
		t.Fatalf("after 2 inactive epochs: sad=%d bored=%d, want sad=1 bored=0", e.SadFor, e.BoredFor)
	}
}

func TestEpochObserveHistograms(t *testing.T) {
	e := New(testConfig())
	aps := []AccessPointObservation{{Channel: 1, NumClients: 2}, {Channel: 6, NumClients: 0}}
	peers := []PeerObservation{{Encounters: 100, LastChannel: 1}}
	e.Observe(aps, peers)

	if e.NumPeers != 1 {
		t.Fatalf("NumPeers = %d, want 1", e.NumPeers)
	}
	wantTotBond := 100.0 / 20000.0
	if diff := e.TotBondFactor - wantTotBond; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("TotBondFactor = %v, want %v", e.TotBondFactor, wantTotBond)
	}
	if e.observation.APsHistogram[0] == 0 {
		t.Fatal("expected channel 1 to have nonzero AP histogram weight")
	}
}

func TestEpochWaitForEpochDataUnblocksOnNext(t *testing.T) {
	e := New(testConfig())
	done := make(chan Data, 1)
	go func() {
		done <- e.WaitForEpochData(true, 2*time.Second)
	}()
	// Give the goroutine a moment to start waiting, then trigger Next().
	time.Sleep(20 * time.Millisecond)
	e.Next()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForEpochData did not unblock after Next()")
	}
}
