//go:build !linux

package fs

import "fmt"

// diskFree has no portable equivalent outside Linux; the memory-fs subsystem
// is Linux-only in Python too (it shells out to mount/zram), so we return a
// clear error instead of a fake value on unsupported platforms.
func diskFree(path string) (int64, error) {
	return 0, fmt.Errorf("fs: disk free space query is only supported on Linux")
}
