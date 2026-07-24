package bttether

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// runBT runs a real bluetoothctl argv command via the injected
// CommandRunner (never a shell string) and returns its combined output,
// matching _run_cmd(capture=True)'s real behavior.
func (p *Plugin) runBT(ctx context.Context, args ...string) (string, error) {
	p.mu.Lock()
	exec := p.exec
	p.mu.Unlock()
	if exec == nil {
		return "", fmt.Errorf("bt-tether: no CommandRunner capability wired")
	}
	out, err := exec.Run(ctx, "bluetoothctl", args...)
	return string(out), err
}

// napConnect ports _connect_nap_dbus: a real BlueZ D-Bus ConnectProfile
// call against the NAP UUID. Real Python resolves the device's D-Bus
// object path by querying org.bluez's ObjectManager
// (GetManagedObjects) over a real D-Bus connection. This port instead
// invokes the standard `dbus-send` CLI tool (part of the base dbus
// package on any BlueZ-capable Debian/RPi system, NOT a Python
// dependency) via the injected CommandRunner, targeting the
// device's object path directly:
// /org/bluez/<adapter>/dev_<MAC-with-underscores> — a real, standard,
// deterministic BlueZ object path for a device already known to the
// adapter (paired/trusted), avoiding the need to parse a
// GetManagedObjects reply (a nontrivial nested D-Bus type serialization)
// or add a Go D-Bus client library dependency for a single call. This
// assumes the single onboard adapter is hci0, true for a real
// Pwnagotchi's hardware. If a genuine need for richer D-Bus interaction
// arises elsewhere, adding github.com/godbus/dbus/v5 (pure Go, no cgo,
// the de facto standard Go D-Bus client) would be the natural next step
// — flagged here rather than added speculatively.
func (p *Plugin) napConnect(ctx context.Context, mac string) error {
	p.mu.Lock()
	exec := p.exec
	p.mu.Unlock()
	if exec == nil {
		return fmt.Errorf("bt-tether: no CommandRunner capability wired")
	}
	objectPath := fmt.Sprintf("/org/bluez/%s/dev_%s", bluetoothAdapter, strings.ReplaceAll(mac, ":", "_"))
	out, err := exec.Run(ctx, "dbus-send",
		"--system", "--print-reply", "--type=method_call",
		"--dest=org.bluez", objectPath,
		"org.bluez.Device1.ConnectProfile",
		"string:"+NAPUUID,
	)
	if err != nil {
		return fmt.Errorf("bt-tether: NAP ConnectProfile failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// napDisconnect mirrors the disconnect half of the real plugin's DBus NAP
// handling (org.bluez.Device1.DisconnectProfile).
func (p *Plugin) napDisconnect(ctx context.Context, mac string) error {
	p.mu.Lock()
	exec := p.exec
	p.mu.Unlock()
	if exec == nil {
		return fmt.Errorf("bt-tether: no CommandRunner capability wired")
	}
	objectPath := fmt.Sprintf("/org/bluez/%s/dev_%s", bluetoothAdapter, strings.ReplaceAll(mac, ":", "_"))
	_, err := exec.Run(ctx, "dbus-send",
		"--system", "--print-reply", "--type=method_call",
		"--dest=org.bluez", objectPath,
		"org.bluez.Device1.DisconnectProfile",
		"string:"+NAPUUID,
	)
	return err
}

// deviceInfo ports the "Paired: yes"/"Connected: yes"/"Trusted: yes"
// substring parsing of `bluetoothctl info <mac>` real output.
type deviceInfo struct {
	Paired, Trusted, Connected, HasNAP bool
	Name                               string
}

var nameLinePattern = regexp.MustCompile(`(?m)^\s*Name:\s*(.+)$`)

func parseDeviceInfo(output string) deviceInfo {
	info := deviceInfo{
		Paired:    strings.Contains(output, "Paired: yes"),
		Trusted:   strings.Contains(output, "Trusted: yes"),
		Connected: strings.Contains(output, "Connected: yes"),
		HasNAP:    strings.Contains(output, NAPUUID),
	}
	if m := nameLinePattern.FindStringSubmatch(output); m != nil {
		info.Name = strings.TrimSpace(m[1])
	}
	return info
}

func (p *Plugin) deviceInfo(ctx context.Context, mac string) (deviceInfo, error) {
	out, err := p.runBT(ctx, "info", mac)
	if err != nil {
		return deviceInfo{}, err
	}
	return parseDeviceInfo(out), nil
}

// parsedPairedDevice mirrors one "Device XX:XX:.. Name" line from
// `bluetoothctl devices Paired`.
type parsedPairedDevice struct{ MAC, Name string }

var deviceLinePattern = regexp.MustCompile(`^Device\s+(([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2})\s+(.*)$`)

func parsePairedDevices(output string) []parsedPairedDevice {
	var out []parsedPairedDevice
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if m := deviceLinePattern.FindStringSubmatch(line); m != nil {
			out = append(out, parsedPairedDevice{MAC: strings.ToUpper(m[1]), Name: m[3]})
		}
	}
	return out
}

func (p *Plugin) pairedDevices(ctx context.Context) ([]parsedPairedDevice, error) {
	out, err := p.runBT(ctx, "devices", "Paired")
	if err != nil {
		return nil, err
	}
	return parsePairedDevices(out), nil
}

// scanDevices ports _scan_devices: pre-populate from already-paired
// devices, then run one blocking timed scan and parse `[NEW] Device`
// lines from its combined output — see the package doc comment for why
// this is a batch operation rather than Python's real-time stream.
var newDevicePattern = regexp.MustCompile(`\[NEW\].*?(([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2})\s*(.*)$`)

func (p *Plugin) scanDevices(ctx context.Context) ([]DiscoveredDevice, error) {
	p.setStatus(StateScanning, "Scanning for devices...")
	result := map[string]DiscoveredDevice{}

	if paired, err := p.pairedDevices(ctx); err == nil {
		for _, d := range paired {
			result[d.MAC] = DiscoveredDevice{MAC: d.MAC, Name: d.Name, Type: "PAIRED"}
		}
	}

	if _, err := p.runBT(ctx, "power", "on"); err != nil {
		p.logf("WARNING", fmt.Sprintf("bluetoothctl power on failed: %v", err))
	}

	scanCtx, cancel := context.WithTimeout(ctx, p.scanDurationOrDefault()+btCommandTimeout)
	defer cancel()
	out, err := p.runBT(scanCtx, "--timeout", durationSeconds(p.scanDurationOrDefault()), "scan", "on")
	if err != nil {
		p.logf("WARNING", fmt.Sprintf("scan failed or timed out: %v", err))
	}
	for _, line := range strings.Split(out, "\n") {
		clean := stripANSI(line)
		if !strings.Contains(clean, "[NEW]") || !strings.Contains(clean, "Device") {
			continue
		}
		if m := newDevicePattern.FindStringSubmatch(clean); m != nil {
			mac := strings.ToUpper(m[1])
			name := strings.TrimSpace(m[3])
			if name == "" {
				name = "(unnamed)"
			}
			if _, exists := result[mac]; !exists {
				result[mac] = DiscoveredDevice{MAC: mac, Name: name, Type: "NEW"}
			}
		}
	}

	devices := make([]DiscoveredDevice, 0, len(result))
	for _, d := range result {
		devices = append(devices, d)
	}
	return devices, nil
}

func (p *Plugin) scanDurationOrDefault() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.scanDuration <= 0 {
		return defaultScanDuration
	}
	return p.scanDuration
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m|\x08`)

func stripANSI(s string) string { return ansiPattern.ReplaceAllString(s, "") }

// trustedDevices ports _get_trusted_devices.
func (p *Plugin) trustedDevices(ctx context.Context) ([]DiscoveredDevice, error) {
	paired, err := p.pairedDevices(ctx)
	if err != nil {
		return nil, err
	}
	var out []DiscoveredDevice
	for _, d := range paired {
		info, err := p.deviceInfo(ctx, d.MAC)
		if err != nil || !info.Trusted {
			continue
		}
		out = append(out, DiscoveredDevice{
			MAC: d.MAC, Name: d.Name, Trusted: true,
			Paired: info.Paired, Connected: info.Connected, HasNAP: info.HasNAP,
		})
	}
	if out == nil {
		out = []DiscoveredDevice{}
	}
	return out, nil
}

// findBestDeviceToConnect ports _find_best_device_to_connect: the first
// trusted device with NAP support, or the first trusted device at all.
func (p *Plugin) findBestDeviceToConnect(ctx context.Context) (*DiscoveredDevice, error) {
	devices, err := p.trustedDevices(ctx)
	if err != nil || len(devices) == 0 {
		return nil, err
	}
	for _, d := range devices {
		if d.HasNAP {
			d := d
			return &d, nil
		}
	}
	d := devices[0]
	return &d, nil
}

// unpairDevice ports _unpair_device: bluetoothctl remove <mac>.
func (p *Plugin) unpairDevice(ctx context.Context, mac string) (bool, string) {
	if _, err := p.runBT(ctx, "remove", mac); err != nil {
		return false, fmt.Sprintf("failed to remove %s: %v", mac, err)
	}
	return true, fmt.Sprintf("Removed %s", mac)
}

// pairAndConnect ports the core of _pair_device_interactive +
// start_connection/_connect_thread: pair, trust, then the real NAP
// profile connect via D-Bus, then network bring-up.
func (p *Plugin) connectDevice(ctx context.Context, device DiscoveredDevice) {
	mac := device.MAC
	p.mu.Lock()
	p.connectionInProgress = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.connectionInProgress = false
		p.mu.Unlock()
	}()

	info, _ := p.deviceInfo(ctx, mac)

	if !info.Paired {
		p.setStatus(StatePairing, fmt.Sprintf("Pairing with %s...", mac))
		if _, err := p.runBT(ctx, "pair", mac); err != nil {
			p.setStatus(StateError, fmt.Sprintf("Pairing failed: %v", err))
			p.logf("ERROR", fmt.Sprintf("Pairing with %s failed: %v", mac, err))
			return
		}
	}

	p.setStatus(StateTrusting, fmt.Sprintf("Trusting %s...", mac))
	if _, err := p.runBT(ctx, "trust", mac); err != nil {
		p.setStatus(StateError, fmt.Sprintf("Trust failed: %v", err))
		p.logf("ERROR", fmt.Sprintf("Trusting %s failed: %v", mac, err))
		return
	}

	p.setStatus(StateConnecting, fmt.Sprintf("Connecting to %s...", mac))
	if _, err := p.runBT(ctx, "connect", mac); err != nil {
		p.logf("WARNING", fmt.Sprintf("bluetoothctl connect reported: %v (continuing to NAP profile connect)", err))
	}

	napCtx, cancel := context.WithTimeout(ctx, dbusConnectTimeout)
	defer cancel()
	if err := p.napConnect(napCtx, mac); err != nil {
		p.setStatus(StateError, fmt.Sprintf("NAP connection failed: %v", err))
		p.logf("ERROR", err.Error())
		return
	}

	iface := p.waitForPANInterface(ctx)
	if iface == "" {
		p.setStatus(StateError, "No PAN interface appeared after connecting")
		p.logf("ERROR", "No PAN interface (bnep0/bt-pan) appeared after NAP connect")
		return
	}
	if err := p.setupNetworkDHCP(ctx, iface); err != nil {
		p.setStatus(StateError, fmt.Sprintf("Network setup failed: %v", err))
		p.logf("ERROR", fmt.Sprintf("Network setup for %s failed: %v", iface, err))
		return
	}

	p.setStatus(StateConnected, fmt.Sprintf("Connected via %s", iface))
	p.mu.Lock()
	p.reconnectFailureCount = 0
	p.mu.Unlock()
}

// disconnectDevice ports _disconnect_device: NAP disconnect, then
// bluetoothctl disconnect.
func (p *Plugin) disconnectDevice(ctx context.Context, mac string) {
	p.setStatus(StateDisconnecting, "Disconnecting from device...")
	_ = p.napDisconnect(ctx, mac)
	if _, err := p.runBT(ctx, "disconnect", mac); err != nil {
		p.logf("WARNING", fmt.Sprintf("bluetoothctl disconnect: %v", err))
	}
	p.setStatus(StateDisconnected, "Disconnected")
}
