// Package unit ports pwnagotchi/__init__.py: system telemetry (Name,
// Uptime, MemUsage, CPULoad, Celsius/Fahrenheit) and the process lifecycle
// functions (SetName, Shutdown, Restart, Reboot) in lifecycle.go.
package unit

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Overridable so tests can point at fixtures instead of the real host.
var (
	HostnamePath = "/etc/hostname"
	UptimePath   = "/proc/uptime"
	MeminfoPath  = "/proc/meminfo"
	StatPath     = "/proc/stat"
	ThermalPath  = "/sys/class/thermal/thermal_zone0/temp"
)

var (
	nameMu     sync.Mutex
	cachedName *string
)

// Name mirrors pwnagotchi.name(): read HostnamePath once and cache the
// result for the lifetime of the process, matching the Python module-level
// _name cache.
func Name() (string, error) {
	nameMu.Lock()
	defer nameMu.Unlock()
	if cachedName != nil {
		return *cachedName, nil
	}
	data, err := os.ReadFile(HostnamePath)
	if err != nil {
		return "", err
	}
	n := strings.TrimSpace(string(data))
	cachedName = &n
	return n, nil
}

// ResetNameCache clears the cached name; test-only helper (Python has no
// equivalent because each test run is a fresh interpreter).
func ResetNameCache() {
	nameMu.Lock()
	defer nameMu.Unlock()
	cachedName = nil
}

// Uptime mirrors pwnagotchi.uptime(): int(fp.read().split('.')[0]), i.e.
// the integer text before the *first* '.' anywhere in the file content
// (not just the first field) — replicated exactly, not "fixed".
func Uptime() (int64, error) {
	data, err := os.ReadFile(UptimePath)
	if err != nil {
		return 0, err
	}
	s := string(data)
	intPart := s
	if idx := strings.IndexByte(s, '.'); idx != -1 {
		intPart = s[:idx]
	}
	return strconv.ParseInt(strings.TrimSpace(intPart), 10, 64)
}

// MemUsage mirrors pwnagotchi.mem_usage(): round((total-free-cached-buffers)/total, 1).
func MemUsage() (float64, error) {
	f, err := os.Open(MeminfoPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var total, free, buffers, cached int64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			total, err = parseMeminfoKB(line)
		case strings.HasPrefix(line, "MemFree:"):
			free, err = parseMeminfoKB(line)
		case strings.HasPrefix(line, "Buffers:"):
			buffers, err = parseMeminfoKB(line)
		case strings.HasPrefix(line, "Cached:"):
			cached, err = parseMeminfoKB(line)
		}
		if err != nil {
			return 0, err
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if total == 0 {
		return 0, fmt.Errorf("unit: MemTotal not found in %s", MeminfoPath)
	}
	used := total - free - cached - buffers
	return pyRound1(float64(used) / float64(total)), nil
}

func parseMeminfoKB(line string) (int64, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, fmt.Errorf("unit: malformed meminfo line %q", line)
	}
	return strconv.ParseInt(fields[1], 10, 64)
}

// pyRound1 replicates CPython's round(x, 1): correctly-rounded decimal
// conversion with ties-to-even, which is exactly what strconv.FormatFloat's
// shortest-correctly-rounded algorithm also guarantees for the underlying
// IEEE-754 value.
func pyRound1(x float64) float64 {
	s := strconv.FormatFloat(x, 'f', 1, 64)
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// CPUStats holds the tag-keyed sample cache from pwnagotchi's module-level
// _cpu_stats dict, so callers that pass the same tag skip the 0.1s
// sleep-and-resample, exactly like the Python original.
type CPUStats struct {
	mu    sync.Mutex
	stats map[string][]int64
}

// NewCPUStats returns a fresh, independent stats cache (Python has a single
// implicit module-level instance; Go callers should keep one long-lived
// CPUStats per process to reproduce that behavior).
func NewCPUStats() *CPUStats {
	return &CPUStats{stats: make(map[string][]int64)}
}

func cpuStat() ([]int64, error) {
	data, err := os.ReadFile(StatPath)
	if err != nil {
		return nil, err
	}
	firstLine := data
	if idx := strings.IndexByte(string(data), '\n'); idx != -1 {
		firstLine = data[:idx]
	}
	fields := strings.Fields(string(firstLine))
	if len(fields) < 2 {
		return nil, fmt.Errorf("unit: malformed %s", StatPath)
	}
	fields = fields[1:] // drop the "cpu" label
	out := make([]int64, len(fields))
	for i, f := range fields {
		v, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// Load mirrors pwnagotchi.cpu_load(tag): pass "" for the untagged
// (always-sleep) form.
func (c *CPUStats) Load(tag string) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var parts0 []int64
	if tag != "" {
		if cached, ok := c.stats[tag]; ok {
			parts0 = cached
		}
	}
	if parts0 == nil {
		var err error
		parts0, err = cpuStat()
		if err != nil {
			return 0, err
		}
		time.Sleep(100 * time.Millisecond)
	}

	parts1, err := cpuStat()
	if err != nil {
		return 0, err
	}
	if tag != "" {
		c.stats[tag] = parts1
	}

	n := len(parts0)
	if len(parts1) < n {
		n = len(parts1) // matches Python's zip() truncating to the shorter sequence
	}
	if n < 8 {
		return 0, fmt.Errorf("unit: /proc/stat has too few fields (%d)", n)
	}
	diff := make([]int64, n)
	for i := 0; i < n; i++ {
		diff[i] = parts1[i] - parts0[i]
	}
	user, nice, sys, idle, iowait, irq, softirq, steal := diff[0], diff[1], diff[2], diff[3], diff[4], diff[5], diff[6], diff[7]
	idleSum := idle + iowait
	nonIdleSum := user + nice + sys + irq + softirq + steal
	total := idleSum + nonIdleSum
	if total == 0 {
		return 0, nil
	}
	return float64(nonIdleSum) / float64(total), nil
}

// defaultCPUStats backs the package-level CPULoad convenience function,
// mirroring Python's implicit module-level _cpu_stats dict.
var defaultCPUStats = NewCPUStats()

// CPULoad calls Load on the package-level default CPUStats instance.
func CPULoad(tag string) (float64, error) {
	return defaultCPUStats.Load(tag)
}

// Celsius mirrors the celsius=True branch of pwnagotchi.temperature():
// int(temp/1000), i.e. truncated-toward-zero millidegree-to-degree
// conversion (thermal_zone0 readings are always positive, so integer
// division matches Python's int() truncation exactly).
func Celsius() (int, error) {
	data, err := os.ReadFile(ThermalPath)
	if err != nil {
		return 0, err
	}
	temp, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, err
	}
	return temp / 1000, nil
}

// Fahrenheit mirrors the celsius=False branch of pwnagotchi.temperature():
// (c * (9/5)) + 32 computed from the *already-truncated* Celsius integer.
// This intentionally reproduces the Python original's imprecision (it
// converts the truncated integer, not the raw millidegree value) — see
// docs/known-differences.md.
func Fahrenheit() (float64, error) {
	c, err := Celsius()
	if err != nil {
		return 0, err
	}
	return (float64(c) * (9.0 / 5.0)) + 32, nil
}
