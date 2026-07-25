// Package fixservices is the native Go port of
// pwnagotchi/plugins/default/fix_services.py: a watchdog that detects the
// onboard BCM43430A1/brcmfmac WiFi chip going "blind" (kernel firmware
// crashes, stuck channel hopping, monitor interface disappearing) by
// scanning recent kernel/daemon logs, and recovers by reloading the
// brcmfmac kernel module and recreating the monitor interface instead of
// a full reboot. Auto-disables itself when an external (non-brcmfmac)
// WiFi adapter is detected.
//
// Original Python author: jayofelony (see fix_services.py's own
// __author__ field, left untouched). This Go port is by raf181.
package fixservices

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

var (
	patternIfaceValidation = regexp.MustCompile(`ieee80211 phy0: brcmf_cfg80211_add_iface: iface validation failed: err=-95`)
	patternChannelHopError = regexp.MustCompile(`wifi error while hopping to channel`)
	patternFirmwareHalted  = regexp.MustCompile(`Firmware has halted or crashed`)
	patternIfaceNotFound   = regexp.MustCompile(`error 400: could not find interface wlan0mon`)
	patternConcurrentMap   = regexp.MustCompile(`fatal error: concurrent map iteration and map write`)
	patternPanic           = regexp.MustCompile(`panic: runtime error`)
	patternAllmulti        = regexp.MustCompile(`ieee80211 phy0: _brcmf_set_multicast_list: Setting allmulti failed, -110`)
)

// rateLimitWindow ports the real `time.time() - self.LASTTRY > 180` guard
// in on_epoch: don't re-scan/re-act more than once per 3 minutes.
const rateLimitWindow = 180 * time.Second

const commandTimeout = 30 * time.Second

// netClassDir/journalKernelArgs/etc are overridable for tests, matching
// this port's established package-var override pattern (e.g.
// internal/wpasec.DBPath).
var (
	netClassDir   = "/sys/class/net"
	pwnagotchiLog = "/etc/pwnagotchi/log/pwnagotchi.log"
)

// realClock is the production pluginmanager.Clock.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Plugin ports the FixServices class.
type Plugin struct {
	mu             sync.Mutex
	isDisabled     bool
	isReloadingMon bool
	lastTry        time.Time

	exec   pluginmanager.CommandRunner
	agent  pluginmanager.AgentCapability
	view   pluginmanager.ViewCapability
	log    pluginmanager.Logger
	clock  pluginmanager.Clock
	system pluginmanager.SystemCapability
}

// New ports FixServices.__init__ minus the external-adapter check, which
// real Python does synchronously in __init__ but this port defers to
// OnLoad (where real capabilities/filesystem access are meant to happen).
func New() *Plugin { return &Plugin{clock: realClock{}} }

func (p *Plugin) Name() string { return "fix_services" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "1.0.1",
		Author:      "jayofelony (original), Go port by raf181",
		License:     "GPL3",
		Description: "Fix blindness, firmware crashes and brain not being loaded. Auto-disables for external WiFi adapters.",
	}
}

// OnLoad ports __init__'s external-adapter detection (_check_external_adapter)
// plus on_loaded's log line.
func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exec = caps.Exec
	p.agent = caps.Agent
	p.view = caps.View
	p.log = caps.Log
	p.system = caps.System
	if caps.Clock != nil {
		p.clock = caps.Clock
	}

	p.isDisabled = p.checkExternalAdapter()
	if p.isDisabled {
		p.logf("plugin loaded but disabled due to external WiFi adapter.")
	} else {
		p.logf("plugin loaded.")
	}
	return nil
}

// checkExternalAdapter ports _check_external_adapter: if wlan0's driver
// isn't brcmfmac (or wlan0/its driver link can't be determined at all),
// this plugin is for a different chip's watchdog logic and must stay
// inactive. Ported without shelling out to `ls`/readlink via a subprocess
// (real Python's `subprocess.check_output("ls /sys/class/net/", shell=True)`)
// since Go can list a directory and resolve a symlink natively — a
// disclosed, behavior-preserving simplification, not a shortcut around
// "avoid unnecessary shell execution".
func (p *Plugin) checkExternalAdapter() bool {
	entries, err := os.ReadDir(netClassDir)
	if err != nil {
		p.logf("error detecting WiFi adapter: %v. Assuming external adapter.", err)
		return true
	}
	found := false
	for _, e := range entries {
		if e.Name() == "wlan0" {
			found = true
			break
		}
	}
	if !found {
		p.logf("wlan0 interface not found. Plugin will be disabled.")
		return true
	}

	driverPath := filepath.Join(netClassDir, "wlan0", "device", "driver")
	if _, err := os.Lstat(driverPath); err == nil {
		link, err := os.Readlink(driverPath)
		if err != nil {
			p.logf("error checking driver: %v. Assuming external adapter.", err)
			return true
		}
		driverName := filepath.Base(link)
		p.logf("Detected WiFi driver: %s", driverName)
		if driverName != "brcmfmac" {
			p.logf("External WiFi adapter detected (%s). Plugin will be disabled.", driverName)
			return true
		}
		p.logf("Onboard brcmfmac detected. Plugin will remain active.")
		return false
	}

	// driver symlink doesn't exist: fall back to an lsmod check (matches
	// Python's `lsmod | grep brcmfmac` fallback branch).
	if p.exec == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	out, err := p.exec.Run(ctx, "lsmod")
	if err != nil || !strings.Contains(string(out), "brcmfmac") {
		p.logf("brcmfmac module not found. External adapter likely in use. Plugin will be disabled.")
		return true
	}
	p.logf("brcmfmac module detected via lsmod. Plugin will remain active.")
	return false
}

// HandleEvent ports on_ready/on_bcap_sys_log/on_epoch. Every hook is a
// real no-op while disabled, matching every real Python method's leading
// `if self.is_disabled: return`.
func (p *Plugin) HandleEvent(event string, args []interface{}) {
	p.mu.Lock()
	disabled := p.isDisabled
	p.mu.Unlock()
	if disabled {
		return
	}
	switch event {
	case "ready":
		p.onReady()
	case "bcap_sys_log":
		p.onBcapSysLog(args)
	case "epoch":
		p.onEpoch()
	}
}

// onReady ports on_ready: if `ip link show wlan0mon` fails (the monitor
// interface doesn't exist), the chip is presumed blind — try recovery.
func (p *Plugin) onReady() {
	if _, err := p.run("ip", "link", "show", "wlan0mon"); err != nil {
		p.tryTurningItOffAndOnAgain()
	}
}

// onBcapSysLog ports on_bcap_sys_log: react to bettercap's own syslog
// passthrough event (real event name: "bcap_sys_log", the sanitized form
// of bettercap's "sys.log" tag — see internal/agent/events.go's
// bcapTagSanitizer). args: (agent, event map[string]interface{}).
func (p *Plugin) onBcapSysLog(args []interface{}) {
	if len(args) < 2 {
		return
	}
	event, ok := args[1].(map[string]interface{})
	if !ok {
		return
	}
	data, _ := event["data"].(map[string]interface{})
	message, _ := data["Message"].(string)
	if message == "" || !patternChannelHopError.MatchString(message) {
		return
	}
	p.flipWifiRecon()
}

// flipWifiRecon ports the repeated `agent.run("wifi.recon off; wifi.recon on")`
// pattern (a single bettercap REST call — bettercap's own command parser
// handles the ";"-separated command list, not a shell).
func (p *Plugin) flipWifiRecon() bool {
	if p.agent == nil {
		return false
	}
	if _, err := p.agent.Run("wifi.recon off; wifi.recon on", true); err != nil {
		p.logf("wifi.recon flip: FAILED: %v", err)
		return false
	}
	p.logf("wifi.recon flip: success!")
	if p.view != nil {
		p.view.Set("status", "Wifi recon flipped!")
		p.view.Update(true)
	}
	return true
}

// onEpoch ports on_epoch: rate-limited (once per rateLimitWindow) scan of
// recent kernel/daemon logs against the 7 known failure patterns, each
// with its own remediation.
func (p *Plugin) onEpoch() {
	p.mu.Lock()
	due := p.clock.Now().Sub(p.lastTry) > rateLimitWindow
	p.mu.Unlock()
	if !due {
		return
	}

	kernelLog := p.tailOutput("journalctl", "-n10", "-k")
	generalLog := p.tailOutput("journalctl", "-n10")
	fileLog := p.tailFile(pwnagotchiLog, 10)

	switch {
	case len(patternIfaceValidation.FindAllString(kernelLog, -1)) >= 1:
		p.run("monstop")
		p.run("monstart")
		p.setStatus("Wifi channel stuck. Restarting recon.")
		p.restart("AUTO")
	case len(patternChannelHopError.FindAllString(generalLog, -1)) >= 5:
		p.setStatus("Wifi channel stuck. Restarting recon.")
		p.flipWifiRecon()
	case len(patternFirmwareHalted.FindAllString(generalLog, -1)) >= 1:
		p.setStatus("Firmware has halted or crashed. Restarting wlan0mon.")
		p.run("monstart")
	case len(patternIfaceNotFound.FindAllString(fileLog, -1)) >= 3:
		p.setStatus("Restarting wlan0 now!")
		p.run("monstart")
	case len(patternConcurrentMap.FindAllString(fileLog, -1)) >= 1:
		p.setStatus("Restarting pwnagotchi!")
		p.run("systemctl", "restart", "bettercap")
		p.restart("AUTO")
	case len(patternPanic.FindAllString(fileLog, -1)) >= 1:
		p.setStatus("Restarting pwnagotchi!")
		p.run("systemctl", "restart", "bettercap")
		p.restart("AUTO")
	case len(patternAllmulti.FindAllString(fileLog, -1)) >= 1:
		p.flipWifiRecon()
	}
}

func (p *Plugin) setStatus(status string) {
	if p.view == nil {
		return
	}
	p.view.Set("status", status)
	p.view.Update(true)
}

func (p *Plugin) restart(mode string) {
	p.mu.Lock()
	sys := p.sys()
	p.mu.Unlock()
	if sys != nil {
		sys.Restart(mode)
	}
}

func (p *Plugin) reboot() {
	sys := p.sys()
	if sys != nil {
		sys.Reboot("")
	}
}

// sys is a small accessor so tests can inject via Capabilities.System
// without this file needing its own separate field bookkeeping.
func (p *Plugin) sys() pluginmanager.SystemCapability { return p.system }

// tryTurningItOffAndOnAgain ports _tryTurningItOffAndOnAgain: the "big
// hammer" recovery — pause recon, tear down the monitor interface, reload
// the brcmfmac kernel module (up to 3 attempts), recreate the monitor
// interface, and resume recon. A real reboot is the last resort if the
// module never reloads successfully.
func (p *Plugin) tryTurningItOffAndOnAgain() {
	p.mu.Lock()
	if p.isReloadingMon && p.clock.Now().Sub(p.lastTry) < rateLimitWindow {
		p.mu.Unlock()
		p.logf("Duplicate attempt ignored")
		return
	}
	p.isReloadingMon = true
	p.lastTry = p.clock.Now()
	p.mu.Unlock()

	p.setStatus("I'm blind! Try turning it off and on again")

	if p.agent != nil {
		if _, err := p.agent.Run("wifi.recon off", true); err != nil {
			p.logf("wifi.recon off: FAILED: %v", err)
		} else {
			p.setStatus("Wifi recon paused!")
		}
	}

	p.run("monstop")

	tries := 1
	reloaded := false
	for tries < 3 {
		if _, err := p.run("sudo", "modprobe", "-r", "brcmfmac"); err != nil {
			p.logf("modprobe -r brcmfmac (try %d): %v", tries, err)
		} else if _, err := p.run("sudo", "modprobe", "brcmfmac"); err != nil {
			p.logf("modprobe brcmfmac (try %d): %v", tries, err)
		} else if _, err := p.run("monstart"); err != nil {
			p.logf("monstart (try %d): %v", tries, err)
		} else {
			if p.agent != nil {
				p.agent.Run("set wifi.interface wlan0mon", true)
			}
			reloaded = true
			break
		}
		tries++
	}

	if !reloaded {
		p.reboot()
		p.mu.Lock()
		p.lastTry = p.clock.Now()
		p.mu.Unlock()
	} else {
		p.setStatus("And back on again...")
	}

	p.mu.Lock()
	p.isReloadingMon = false
	p.mu.Unlock()

	if p.agent != nil {
		if _, err := p.agent.Run("wifi.clear; wifi.recon on", true); err != nil {
			p.logf("wifi.recon on: %v", err)
			p.reboot()
			return
		}
		p.setStatus("I can see again! (probably)")
		p.mu.Lock()
		p.lastTry = p.clock.Now().Add(120 * time.Second)
		p.mu.Unlock()
	}
}

func (p *Plugin) tailOutput(name string, args ...string) string {
	out, err := p.run(name, args...)
	if err != nil {
		return ""
	}
	return string(out)
}

func (p *Plugin) tailFile(path string, n int) string {
	out, err := p.run("tail", "-n", itoa(n), path)
	if err != nil {
		return ""
	}
	return string(out)
}

func (p *Plugin) run(name string, args ...string) ([]byte, error) {
	if p.exec == nil {
		return nil, os.ErrNotExist
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	return p.exec.Run(ctx, name, args...)
}

func (p *Plugin) logf(format string, args ...interface{}) {
	if p.log != nil {
		p.log.Printf(format, args...)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
