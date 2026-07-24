package fs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureWriteCreatesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "status.txt")

	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := EnsureWrite(target, func(f *os.File) error {
		_, werr := f.WriteString("new-data")
		return werr
	})
	if err != nil {
		t.Fatalf("EnsureWrite: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-data" {
		t.Fatalf("content = %q, want %q", got, "new-data")
	}

	// Matches Python: os.replace(tmp, filename) means the FINAL file carries
	// the temp file's permissions (0600 from tempfile.mkstemp), not the
	// original target's (0644) — this is real observed Python behavior, not
	// a Go simplification. See docs/known-differences.md.
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %v, want 0600 (temp-file perm survives the atomic rename, matching Python)", perm)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries after rename, want 1 (no leftover temp file)", len(entries))
	}
}

func TestEnsureWriteLeavesNoTempOnError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "status.txt")

	err := EnsureWrite(target, func(f *os.File) error {
		return os.ErrInvalid
	})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("target should not have been created on error")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("dir has %d entries after failed write, want 0 (temp file cleaned up)", len(entries))
	}
}

func TestSizeOf(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b"), []byte("1234567"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SizeOf(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != 12 {
		t.Fatalf("SizeOf = %d, want 12", got)
	}
}
