package fs

import (
	"fmt"
	"path/filepath"
	"time"
)

// MountConfig mirrors one entry of config['fs']['memory']['mounts'].
type MountConfig struct {
	Enabled bool
	Mount   string
	Size    string
	Zram    bool
	Rsync   bool
	Sync    int // seconds; 0 disables the periodic daemonize goroutine
}

// SetupMounts mirrors fs.setup_mounts(config): builds and mounts a
// MemoryFS for each enabled entry, starting its periodic sync goroutine
// (Python: threading.Thread(..., name="File Sys", daemon=True)) when
// Sync > 0. Returns the resulting mounts (Python keeps these in a
// module-level `fs.mounts` global; Go returns them for the caller to hold
// and pass to unit.Shutdown/unit.Reboot's `mounts []unit.Mount` parameter).
func SetupMounts(r Runner, enabled bool, mounts map[string]MountConfig, stop <-chan struct{}) ([]*MemoryFS, error) {
	if !enabled {
		return nil, nil
	}

	var result []*MemoryFS
	for _, options := range mounts {
		if !options.Enabled {
			continue
		}
		size, unit, err := ParseSize(options.Size)
		if err != nil {
			return result, fmt.Errorf("fs: mount %s: %w", options.Mount, err)
		}
		target := filepath.Join("/run/pwnagotchi/disk/", filepath.Base(options.Mount))

		isMounted := IsMountpoint(r, target)

		m, err := NewMemoryFS(r, options.Mount, target, options.Size, options.Zram,
			fmt.Sprintf("%d%s", size*2, unit), options.Rsync)
		if err != nil {
			return result, err
		}

		if !isMounted {
			if !m.Mount() {
				continue
			}
			if ok, err := m.Sync(true); err != nil || !ok {
				m.Umount()
				continue
			}
		}

		if options.Sync > 0 {
			go m.Daemonize(time.Duration(options.Sync)*time.Second, stop)
		}
		result = append(result, m)
	}
	return result, nil
}
