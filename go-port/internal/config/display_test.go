package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestNormalizeDisplayTypeGolden replays testdata/display_type_normalize_golden.json,
// captured by actually executing the if/elif chain from pwnagotchi/utils.py's
// load_config (see scripts that produced it referenced in
// docs/python-baseline.md). This is the authoritative check that Go's table
// dispatch — including the "substring of a bare literal" bug for
// single-alias branches like whisplay/oledhat/lcdhat — matches Python
// branch-for-branch, not just on the "normal" inputs.
func TestNormalizeDisplayTypeGolden(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "display_type_normalize_golden.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden fixture: %v", err)
	}
	var cases []struct {
		In  string `json:"in"`
		Out string `json:"out"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("golden fixture is empty")
	}
	for _, c := range cases {
		if got := NormalizeDisplayType(c.In); got != c.Out {
			t.Errorf("NormalizeDisplayType(%q) = %q, want %q", c.In, got, c.Out)
		}
	}
}

func TestNormalizeDisplayTypeUnknownFallsBackToDummy(t *testing.T) {
	if got := NormalizeDisplayType("totally_unknown_display"); got != DefaultDisplayType {
		t.Fatalf("NormalizeDisplayType(unknown) = %q, want %q", got, DefaultDisplayType)
	}
}
