package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// golden loads testdata/python_golden.json, captured directly from the
// running Python reference implementation (see docs/python-baseline.md).
func golden(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "python_golden.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden fixture: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parsing golden fixture: %v", err)
	}
	return m
}

func TestParseVersionGolden(t *testing.T) {
	g := golden(t)
	var cases []struct {
		In  string   `json:"in"`
		Out []string `json:"out"`
	}
	if err := json.Unmarshal(g["parse_version"], &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		got := ParseVersion(c.In)
		if len(got) != len(c.Out) {
			t.Fatalf("ParseVersion(%q) = %v, want %v", c.In, got, c.Out)
		}
		for i := range got {
			if got[i] != c.Out[i] {
				t.Fatalf("ParseVersion(%q) = %v, want %v", c.In, got, c.Out)
			}
		}
	}
}

func TestCompareVersionsLexicalQuirk(t *testing.T) {
	// python-baseline.md: parse_version('2.10.0') > parse_version('2.9.5.5') is False.
	if CompareVersions("2.10.0", "2.9.5.5") > 0 {
		t.Fatal("CompareVersions must replicate the lexical (non-numeric) Python quirk: 2.10.0 must NOT compare greater than 2.9.5.5")
	}
	if CompareVersions("2.9.5.5", "2.9.5.5") != 0 {
		t.Fatal("equal versions must compare equal")
	}
	if CompareVersions("1.0", "1.0.0") >= 0 {
		t.Fatal("a prefix tuple must compare as smaller (shorter tuple)")
	}
}

func TestSecsToHHMMSSGolden(t *testing.T) {
	g := golden(t)
	var cases []struct {
		In  int64  `json:"in"`
		Out string `json:"out"`
	}
	if err := json.Unmarshal(g["secs_to_hhmmss"], &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if got := SecsToHHMMSS(c.In); got != c.Out {
			t.Errorf("SecsToHHMMSS(%d) = %q, want %q", c.In, got, c.Out)
		}
	}
}

func TestSecsToHHMMSSFloatMatchesPython(t *testing.T) {
	// Captured directly from the real Python secs_to_hhmmss with float
	// inputs (epoch.py logs float durations from time.time() deltas).
	cases := []struct {
		in   float64
		want string
	}{
		{0.0, "00:00:00"},
		{59.9, "00:00:59"},
		{61.5, "00:01:01"},
		{3661.999, "01:01:01"},
		{90061.1, "25:01:01"},
	}
	for _, c := range cases {
		if got := SecsToHHMMSSFloat(c.in); got != c.want {
			t.Errorf("SecsToHHMMSSFloat(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRemoveWhitelistedGolden(t *testing.T) {
	g := golden(t)
	var fixture struct {
		In        []string `json:"in"`
		Whitelist []string `json:"whitelist"`
		Out       []string `json:"out"`
	}
	if err := json.Unmarshal(g["remove_whitelisted"], &fixture); err != nil {
		t.Fatal(err)
	}
	got := RemoveWhitelisted(fixture.In, fixture.Whitelist, true)
	if len(got) != len(fixture.Out) {
		t.Fatalf("RemoveWhitelisted = %v, want %v", got, fixture.Out)
	}
	for i := range got {
		if got[i] != fixture.Out[i] {
			t.Fatalf("RemoveWhitelisted = %v, want %v", got, fixture.Out)
		}
	}
}

func TestMergeConfigGolden(t *testing.T) {
	g := golden(t)
	var cases []struct {
		User    map[string]interface{} `json:"user"`
		Default map[string]interface{} `json:"default"`
		Out     map[string]interface{} `json:"out"`
	}
	if err := json.Unmarshal(g["merge_config"], &cases); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		got := MergeConfig(c.User, c.Default)
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(c.Out)
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("case %d: MergeConfig(%v, %v) = %s, want %s", i, c.User, c.Default, gotJSON, wantJSON)
		}
	}
}
