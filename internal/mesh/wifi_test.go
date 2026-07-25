package mesh

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func loadGolden(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "python_golden.json"))
	if err != nil {
		t.Fatalf("reading golden fixture: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNumChannelsGolden(t *testing.T) {
	g := loadGolden(t)
	var n int
	if err := json.Unmarshal(g["NumChannels"], &n); err != nil {
		t.Fatal(err)
	}
	if NumChannels != n {
		t.Fatalf("NumChannels = %d, want %d (from python_golden.json)", NumChannels, n)
	}
}

func TestFreqToChannelGolden(t *testing.T) {
	g := loadGolden(t)
	var cases []struct {
		In  int `json:"in"`
		Out int `json:"out"`
	}
	if err := json.Unmarshal(g["freq_to_channel"], &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("empty golden fixture")
	}
	for _, c := range cases {
		got, err := FreqToChannel(c.In)
		if err != nil {
			t.Errorf("FreqToChannel(%d) unexpected error: %v", c.In, err)
			continue
		}
		if got != c.Out {
			t.Errorf("FreqToChannel(%d) = %d, want %d", c.In, got, c.Out)
		}
	}
}

func TestFreqToChannelInvalidGolden(t *testing.T) {
	g := loadGolden(t)
	var cases []struct {
		In    int    `json:"in"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(g["freq_to_channel_invalid"], &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("empty golden fixture")
	}
	for _, c := range cases {
		_, err := FreqToChannel(c.In)
		if err == nil {
			t.Errorf("FreqToChannel(%d) = nil error, want %q", c.In, c.Error)
			continue
		}
		if err.Error() != c.Error {
			t.Errorf("FreqToChannel(%d) error = %q, want %q", c.In, err.Error(), c.Error)
		}
	}
}
