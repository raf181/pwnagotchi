package unit

import (
	"os"
	"path/filepath"
	"testing"
)

func withOverride(t *testing.T, ptr *string, value string) {
	t.Helper()
	old := *ptr
	*ptr = value
	t.Cleanup(func() { *ptr = old })
}

func TestName(t *testing.T) {
	ResetNameCache()
	t.Cleanup(ResetNameCache)

	dir := t.TempDir()
	path := filepath.Join(dir, "hostname")
	if err := os.WriteFile(path, []byte("pwnagotchi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withOverride(t, &HostnamePath, path)

	got, err := Name()
	if err != nil {
		t.Fatal(err)
	}
	if got != "pwnagotchi" {
		t.Fatalf("Name() = %q, want %q", got, "pwnagotchi")
	}

	// Cached: changing the file afterwards must not change the result,
	// matching Python's module-level _name cache.
	if err := os.WriteFile(path, []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got2, err := Name()
	if err != nil {
		t.Fatal(err)
	}
	if got2 != "pwnagotchi" {
		t.Fatalf("Name() after cache = %q, want cached %q", got2, "pwnagotchi")
	}
}

func TestUptimeTakesIntegerPartBeforeFirstDot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uptime")
	// Second field also contains a '.', matching /proc/uptime's real shape;
	// Python's split('.')[0] only ever looks at the text before the FIRST
	// dot in the whole file, ignoring the second field's decimal point.
	if err := os.WriteFile(path, []byte("12345.67 6789.01\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withOverride(t, &UptimePath, path)

	got, err := Uptime()
	if err != nil {
		t.Fatal(err)
	}
	if got != 12345 {
		t.Fatalf("Uptime() = %d, want 12345", got)
	}
}

func TestMemUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meminfo")
	content := "MemTotal:       10000 kB\n" +
		"MemFree:         2000 kB\n" +
		"Buffers:         1000 kB\n" +
		"Cached:          3000 kB\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	withOverride(t, &MeminfoPath, path)

	got, err := MemUsage()
	if err != nil {
		t.Fatal(err)
	}
	// (10000-2000-3000-1000)/10000 = 0.4
	if got != 0.4 {
		t.Fatalf("MemUsage() = %v, want 0.4", got)
	}
}

func TestCPULoadUsesCachedTagAndSkipsSleep(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stat")
	withOverride(t, &StatPath, path)

	write := func(cpu string) {
		if err := os.WriteFile(path, []byte("cpu  "+cpu+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	stats := NewCPUStats()
	// Prime the cache for tag "x" with an initial sample.
	write("100 0 0 100 0 0 0 0")
	if _, err := stats.Load("x"); err != nil {
		t.Fatal(err)
	}

	// Second call with the same tag reuses the cached first sample instead
	// of resampling (no sleep), and stores this second sample as the new
	// cache entry.
	write("150 0 0 150 0 0 0 0")
	got, err := stats.Load("x")
	if err != nil {
		t.Fatal(err)
	}
	// diffs: user=50 idle=50 -> load 50/100 = 0.5
	if got != 0.5 {
		t.Fatalf("Load(tag) = %v, want 0.5", got)
	}
}

func TestCelsiusTruncatesTowardZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "temp")
	if err := os.WriteFile(path, []byte("45678\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withOverride(t, &ThermalPath, path)

	got, err := Celsius()
	if err != nil {
		t.Fatal(err)
	}
	if got != 45 {
		t.Fatalf("Celsius() = %d, want 45", got)
	}

	f, err := Fahrenheit()
	if err != nil {
		t.Fatal(err)
	}
	want := (45.0 * (9.0 / 5.0)) + 32
	if f != want {
		t.Fatalf("Fahrenheit() = %v, want %v", f, want)
	}
}
