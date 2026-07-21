package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatusFileMissingFile(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStatusFile(filepath.Join(dir, "nope"), "json")
	if err != nil {
		t.Fatal(err)
	}
	if s.NewerThenMinutes(1000) {
		t.Fatal("a StatusFile backed by a missing file must never be 'newer than'")
	}
	got, err := s.DataFieldOr("reported", []interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if list, ok := got.([]interface{}); !ok || len(list) != 0 {
		t.Fatalf("DataFieldOr default = %v, want empty list default", got)
	}
}

func TestStatusFileJSONRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")

	s, err := NewStatusFile(path, "json")
	if err != nil {
		t.Fatal(err)
	}
	data := map[string]interface{}{"reported": []interface{}{"a", "b"}}
	if err := s.Update(data); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"reported":["a","b"]}` {
		t.Fatalf("on-disk JSON = %s", raw)
	}

	reloaded, err := NewStatusFile(path, "json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.DataFieldOr("reported", nil)
	if err != nil {
		t.Fatal(err)
	}
	list, ok := got.([]interface{})
	if !ok || len(list) != 2 || list[0] != "a" || list[1] != "b" {
		t.Fatalf("DataFieldOr(reported) = %v", got)
	}

	if !reloaded.NewerThenMinutes(5) {
		t.Fatal("just-written file should be newer than 5 minutes")
	}
}

func TestStatusFileRawUpdateWritesDateTimeWhenNil(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.raw")
	s, err := NewStatusFile(path, "raw")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// "YYYY-MM-DD HH:MM:SS" or with ".ffffff" — never garbage.
	if _, err := time.Parse("2006-01-02 15:04:05", string(raw)); err != nil {
		if _, err2 := time.Parse("2006-01-02 15:04:05.000000", string(raw)); err2 != nil {
			t.Fatalf("Update(nil) wrote %q, not a Python str(datetime.now()) shape", raw)
		}
	}
}

func TestPyDateTimeStrOmitsMicrosecondsWhenZero(t *testing.T) {
	zero := time.Date(2024, 3, 5, 12, 30, 45, 0, time.UTC)
	if got := pyDateTimeStr(zero); got != "2024-03-05 12:30:45" {
		t.Fatalf("pyDateTimeStr(zero micros) = %q", got)
	}
	nonzero := time.Date(2024, 3, 5, 12, 30, 45, 123456000, time.UTC)
	if got := pyDateTimeStr(nonzero); got != "2024-03-05 12:30:45.123456" {
		t.Fatalf("pyDateTimeStr(nonzero micros) = %q", got)
	}
}
