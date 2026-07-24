package fs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Runner executes external commands. The production Runner shells out for
// real via os/exec with explicit argv (never a shell string, unlike the
// Python original's os.system calls — see docs/known-differences.md). Tests
// inject a fake Runner so mount/zram logic can be exercised without root or
// real Linux mount namespaces.
type Runner interface {
	// Run executes name with args and returns whether it exited zero,
	// mirroring `os.system(cmd) == 0` in the Python original.
	Run(name string, args ...string) bool
}

// ExecRunner is the production Runner: os/exec with stdio inherited from the
// parent process (Python's os.system shares the parent's stdio too).
type ExecRunner struct{}

func (ExecRunner) Run(name string, args ...string) bool {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run() == nil
}

var sizeRe = regexp.MustCompile(`^(\d+)([a-zA-Z]+)$`)

// IsMountpoint mirrors fs.is_mountpoint: `mountpoint -q path` exits 0.
func IsMountpoint(r Runner, path string) bool {
	return r.Run("mountpoint", "-q", path)
}

// MemoryFS ports fs.MemoryFS: a zram-backed (or plain tmpfs) mount with an
// optional periodic rsync back to the underlying disk.
type MemoryFS struct {
	Mountpoint   string
	Disk         string
	Size         string
	Zram         bool
	ZramAlg      string
	ZramDiskSize string
	ZramFSType   string
	Rsync        bool

	runner Runner
	zdev   string
}

// NewMemoryFS constructs and immediately runs _setup(), matching Python's
// MemoryFS.__init__ (setup is not a separate opt-in step there either).
func NewMemoryFS(r Runner, mount, disk, size string, zram bool, zramDiskSize string, rsync bool) (*MemoryFS, error) {
	m := &MemoryFS{
		Mountpoint:   mount,
		Disk:         disk,
		Size:         size,
		Zram:         zram,
		ZramAlg:      "lz4",
		ZramDiskSize: zramDiskSize,
		ZramFSType:   "ext4",
		Rsync:        rsync,
		runner:       r,
	}
	if m.Size == "" {
		m.Size = "40M"
	}
	if m.ZramDiskSize == "" {
		m.ZramDiskSize = "100M"
	}
	if err := m.setup(); err != nil {
		return nil, err
	}
	return m, nil
}

func zramInstall(r Runner) bool {
	if _, err := os.Stat("/sys/class/zram-control"); os.IsNotExist(err) {
		return r.Run("modprobe", "zram")
	}
	return true
}

func zramDev() (string, error) {
	data, err := os.ReadFile("/sys/class/zram-control/hot_add")
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\n"), nil
}

func (m *MemoryFS) setup() error {
	if m.Zram && zramInstall(m.runner) {
		dev, err := zramDev()
		if err != nil {
			return fmt.Errorf("fs: zram_dev: %w", err)
		}
		m.zdev = dev
		if err := os.WriteFile(fmt.Sprintf("/sys/block/zram%s/comp_algorithm", m.zdev), []byte(m.ZramAlg), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(fmt.Sprintf("/sys/block/zram%s/disksize", m.zdev), []byte(m.ZramDiskSize), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(fmt.Sprintf("/sys/block/zram%s/mem_limit", m.zdev), []byte(m.Size), 0o644); err != nil {
			return err
		}
		m.runner.Run("mke2fs", "-t", m.ZramFSType, fmt.Sprintf("/dev/zram%s", m.zdev))
	}

	if _, err := os.Stat(m.Disk); os.IsNotExist(err) {
		if err := os.MkdirAll(m.Disk, 0o755); err != nil {
			return err
		}
	}
	if _, err := os.Stat(m.Mountpoint); os.IsNotExist(err) {
		if err := os.MkdirAll(m.Mountpoint, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Mount mirrors MemoryFS.mount().
func (m *MemoryFS) Mount() bool {
	if !m.runner.Run("mount", "--bind", m.Mountpoint, m.Disk) {
		return false
	}
	if !m.runner.Run("mount", "--make-private", m.Disk) {
		return false
	}
	if m.Zram && m.zdev != "" {
		if !m.runner.Run("mount", "-t", m.ZramFSType, "-o", "nosuid,noexec,nodev,user=pwnagotchi",
			fmt.Sprintf("/dev/zram%s", m.zdev), m.Mountpoint+"/") {
			return false
		}
	} else {
		if !m.runner.Run("mount", "-t", "tmpfs", "-o", fmt.Sprintf("nosuid,noexec,nodev,mode=0755,size=%s", m.Size),
			"pwnagotchi", m.Mountpoint+"/") {
			return false
		}
	}
	return true
}

// Umount mirrors MemoryFS.umount().
func (m *MemoryFS) Umount() bool {
	if !m.runner.Run("umount", "-l", m.Mountpoint) {
		return false
	}
	return m.runner.Run("umount", "-l", m.Disk)
}

// Sync mirrors MemoryFS.sync(): rsync (or a recursive copy) between disk and
// mountpoint, gated on available free space at the destination.
func (m *MemoryFS) Sync(toRAM bool) (bool, error) {
	source, dest := m.Mountpoint, m.Disk
	if toRAM {
		source, dest = m.Disk, m.Mountpoint
	}
	needed, err := SizeOf(source)
	if err != nil {
		return false, err
	}
	free, err := diskFree(dest)
	if err != nil {
		return false, err
	}
	if free < needed {
		return false, nil
	}
	if m.Rsync {
		m.runner.Run("rsync", "-aXv", "--inplace", "--no-whole-file", "--delete-after", source+"/", dest+"/")
	} else {
		if err := copyTree(source, dest); err != nil {
			return false, err
		}
	}
	m.runner.Run("sync")
	return true, nil
}

// Daemonize mirrors MemoryFS.daemonize(interval): call the caller in a loop
// forever with time.Sleep(interval) between calls. Callers run this in their
// own goroutine, matching Python's daemon thread.
func (m *MemoryFS) Daemonize(interval time.Duration, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		m.Sync(false)
		select {
		case <-stop:
			return
		case <-time.After(interval):
		}
	}
}

// ParseSize splits a Python re.match(r"(\d+)([a-zA-Z]+)", s) size string like
// "40M" into (40, "M").
func ParseSize(s string) (int, string, error) {
	match := sizeRe.FindStringSubmatch(s)
	if match == nil {
		return 0, "", fmt.Errorf("fs: invalid size %q", s)
	}
	n, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, "", err
	}
	return n, match[2], nil
}

func copyTree(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyTree(s, d); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(s)
		if err != nil {
			return err
		}
		if err := os.WriteFile(d, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
