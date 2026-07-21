package config

import (
	"os/exec"
	"strconv"
	"strings"
)

// CommandRunner runs an external command and returns its combined
// stdout+stderr as a string (mirroring subprocess.getoutput's contract:
// output stripped of trailing newline, no error surfaced for a non-zero
// exit — whatever text the command produced is what's parsed).
type CommandRunner func(name string, args ...string) string

// ExecCommandRunner is the production CommandRunner: os/exec with an
// explicit argv, no shell. Python's original
// `subprocess.getoutput("/sbin/iw %s info | grep wiphy | cut -d ' ' -f 2" % ifname)`
// interpolates ifname into a shell pipeline unescaped — a real (if
// low-severity, since ifname comes from the local admin's own config file,
// not untrusted network input) command-injection surface. The Go port
// instead runs `iw` directly and does the grep/cut/sed-equivalent filtering
// in Go (see IfaceChannels), eliminating the shell entirely.
func ExecCommandRunner(name string, args ...string) string {
	out, _ := exec.Command(name, args...).CombinedOutput()
	return strings.TrimRight(string(out), "\n")
}

// IfaceChannels mirrors utils.iface_channels(ifname): resolves the PHY
// index for ifname via `iw <ifname> info`, then lists that PHY's supported
// channel numbers via `iw phy<N> channels`, replicating the
// grep/cut/sed pipeline's exact extraction logic (including its silent
// "ignore anything that doesn't parse as an int" behavior) natively in Go
// instead of a shell pipeline.
func IfaceChannels(run CommandRunner, ifname string) []int {
	info := run("iw", ifname, "info")
	phy := ""
	for _, line := range strings.Split(info, "\n") {
		if strings.Contains(line, "wiphy") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				phy = fields[1]
			}
			break
		}
	}

	channelsOut := run("iw", "phy"+phy, "channels")
	var channels []int
	for _, line := range strings.Split(channelsOut, "\n") {
		if !strings.Contains(line, " MHz") || strings.Contains(line, "disabled") {
			continue
		}
		// sed 's/^.*\[//g' then 's/\].*$//g': keep only the text between
		// the LAST '[' and the following ']'.
		lastOpen := strings.LastIndex(line, "[")
		if lastOpen == -1 {
			continue
		}
		rest := line[lastOpen+1:]
		closeIdx := strings.Index(rest, "]")
		if closeIdx == -1 {
			continue
		}
		numStr := strings.TrimSpace(rest[:closeIdx])
		if n, err := strconv.Atoi(numStr); err == nil {
			channels = append(channels, n)
		}
	}
	return channels
}
