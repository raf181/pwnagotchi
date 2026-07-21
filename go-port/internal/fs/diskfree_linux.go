//go:build linux

package fs

import "syscall"

// diskFree mirrors Python's shutil.disk_usage(dest)[2] (free bytes) via
// statfs(2), matching the real Linux syscall Python's shutil uses under the
// hood.
func diskFree(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
