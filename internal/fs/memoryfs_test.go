package fs

import (
	"os"
	"path/filepath"
	"testing"
)

type fakeRunner struct {
	calls   [][]string
	failing map[string]bool
}

func (f *fakeRunner) Run(name string, args ...string) bool {
	f.calls = append(f.calls, append([]string{name}, args...))
	return !f.failing[name]
}

func TestMemoryFSMountNoZram(t *testing.T) {
	dir := t.TempDir()
	mnt := filepath.Join(dir, "mnt")
	disk := filepath.Join(dir, "disk")

	r := &fakeRunner{}
	m, err := NewMemoryFS(r, mnt, disk, "40M", false, "80M", true)
	if err != nil {
		t.Fatalf("NewMemoryFS: %v", err)
	}

	if _, err := os.Stat(mnt); err != nil {
		t.Fatalf("mountpoint not created: %v", err)
	}
	if _, err := os.Stat(disk); err != nil {
		t.Fatalf("disk dir not created: %v", err)
	}

	if !m.Mount() {
		t.Fatal("Mount() = false, want true")
	}
	if len(r.calls) != 3 {
		t.Fatalf("expected 3 mount calls (bind, private, tmpfs), got %d: %v", len(r.calls), r.calls)
	}
	if r.calls[2][0] != "mount" || r.calls[2][2] != "tmpfs" {
		t.Fatalf("expected tmpfs mount as third call, got %v", r.calls[2])
	}
}

func TestMemoryFSMountFailsFast(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{failing: map[string]bool{"mount": true}}
	m, err := NewMemoryFS(r, filepath.Join(dir, "mnt"), filepath.Join(dir, "disk"), "", false, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if m.Mount() {
		t.Fatal("Mount() = true, want false when the first mount command fails")
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected short-circuit after first failing call, got %d calls", len(r.calls))
	}
}

func TestParseSize(t *testing.T) {
	cases := []struct {
		in    string
		n     int
		unit  string
		isErr bool
	}{
		{"40M", 40, "M", false},
		{"100K", 100, "K", false},
		{"bogus", 0, "", true},
	}
	for _, c := range cases {
		n, unit, err := ParseSize(c.in)
		if c.isErr {
			if err == nil {
				t.Errorf("ParseSize(%q): expected error", c.in)
			}
			continue
		}
		if err != nil || n != c.n || unit != c.unit {
			t.Errorf("ParseSize(%q) = (%d, %q, %v), want (%d, %q, nil)", c.in, n, unit, err, c.n, c.unit)
		}
	}
}
