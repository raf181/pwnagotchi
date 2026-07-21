// Package fs ports pwnagotchi/fs/__init__.py: atomic file writes and the
// zram/tmpfs "memory filesystem" mount manager.
package fs

import (
	"os"
	"path/filepath"
)

// EnsureWrite mirrors the ensure_write context manager: it creates a temp
// file in the same directory as filename, hands it to fn, fsyncs it, and
// atomically renames it onto filename via os.Rename (matching Python's
// os.replace). The temp file is on the same filesystem as the target so the
// final rename is atomic, exactly like the Python original.
func EnsureWrite(filename string, fn func(f *os.File) error) (err error) {
	dir := filepath.Dir(filename)
	tmp, err := os.CreateTemp(dir, ".ensure_write-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			os.Remove(tmpName)
		}
	}()

	if err = fn(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filename)
}

// SizeOf mirrors fs.size_of: the total size in bytes of every regular file
// under path (recursive).
func SizeOf(path string) (int64, error) {
	var total int64
	err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}
