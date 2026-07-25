package bttether

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// panInterfacePattern finds a bnep*/bt-pan* interface name in `ip link
// show` output, e.g. "5: bnep0: <BROADCAST,MULTICAST,UP,LOWER_UP> ..."
var panInterfacePattern = regexp.MustCompile(`^\d+:\s+(bnep\S*|bt-pan\S*?)[@:]`)

// findPANInterface ports the PAN-interface-detection half of
// _get_current_status: a real `ip link show` invocation via the injected
// CommandRunner, never shelling out directly.
func (p *Plugin) findPANInterface(ctx context.Context) (string, error) {
	p.mu.Lock()
	exec := p.exec
	p.mu.Unlock()
	if exec == nil {
		return "", fmt.Errorf("bt-tether: no CommandRunner capability wired")
	}
	out, err := exec.Run(ctx, "ip", "link", "show")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if m := panInterfacePattern.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			return m[1], nil
		}
	}
	return "", nil
}

// waitForPANInterface polls findPANInterface for a short window after a
// NAP connect, matching PAN_INTERFACE_WAIT's real intent (the kernel
// needs a moment to create the bnep interface after BlueZ establishes
// the profile). Deliberately uses the real wall clock (time.Now/
// time.After), not the injectable Clock capability: this is a mechanical
// sub-5-second hardware settling delay, not simulated business logic —
// Capabilities.Clock exists for things like reconnect-cooldown timing
// that tests need to fast-forward, not for bounding a real poll loop
// that must eventually give up in real time regardless of what a test's
// fake clock does.
func (p *Plugin) waitForPANInterface(ctx context.Context) string {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if iface, err := p.findPANInterface(ctx); err == nil && iface != "" {
			return iface
		}
		if time.Now().After(deadline) {
			return ""
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(300 * time.Millisecond):
		}
	}
}

var inetAddrPattern = regexp.MustCompile(`inet\s+(\d+\.\d+\.\d+\.\d+)/\d+`)

// interfaceIP ports the IP-extraction half of _get_current_status: `ip
// addr show <iface>`, skipping loopback.
func (p *Plugin) interfaceIP(ctx context.Context, iface string) (string, error) {
	p.mu.Lock()
	exec := p.exec
	p.mu.Unlock()
	if exec == nil {
		return "", fmt.Errorf("bt-tether: no CommandRunner capability wired")
	}
	out, err := exec.Run(ctx, "ip", "addr", "show", iface)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "127.0.0.1") {
			continue
		}
		if m := inetAddrPattern.FindStringSubmatch(line); m != nil {
			return m[1], nil
		}
	}
	return "", nil
}

var defaultRoutePattern = regexp.MustCompile(`default\s+via\s+\S+\s+dev\s+(\S+)`)

// defaultRouteInterface ports _get_default_route_interface: `ip route
// show default`.
func (p *Plugin) defaultRouteInterface(ctx context.Context) (string, error) {
	p.mu.Lock()
	exec := p.exec
	p.mu.Unlock()
	if exec == nil {
		return "", fmt.Errorf("bt-tether: no CommandRunner capability wired")
	}
	out, err := exec.Run(ctx, "ip", "route", "show", "default")
	if err != nil {
		return "", err
	}
	if m := defaultRoutePattern.FindStringSubmatch(string(out)); m != nil {
		return m[1], nil
	}
	return "", nil
}

// setupNetworkDHCP ports _setup_network_dhcp/_setup_dhclient: bring the
// PAN interface up, then run a real DHCP client against it — both via
// the injected CommandRunner in argv form.
func (p *Plugin) setupNetworkDHCP(ctx context.Context, iface string) error {
	p.mu.Lock()
	exec := p.exec
	p.mu.Unlock()
	if exec == nil {
		return fmt.Errorf("bt-tether: no CommandRunner capability wired")
	}

	if _, err := exec.Run(ctx, "ip", "link", "set", iface, "up"); err != nil {
		return fmt.Errorf("bringing %s up: %w", iface, err)
	}

	dhcpCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := exec.Run(dhcpCtx, "dhclient", "-1", iface); err != nil {
		return fmt.Errorf("dhclient on %s: %w", iface, err)
	}
	return nil
}

// testInternetConnectivity ports _test_internet_connectivity: a real,
// short-timeout HTTP request via the injected *http.Client (never a real
// call in tests — see bt_tether_test.go's httptest usage).
func (p *Plugin) testInternetConnectivity(ctx context.Context) (bool, string) {
	p.mu.Lock()
	client := p.httpClient
	p.mu.Unlock()
	if client == nil {
		return false, "no HTTP client capability wired"
	}
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := httpGetRequest(reqCtx, internetCheckURL)
	if err != nil {
		return false, err.Error()
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return true, "Internet connectivity confirmed"
	}
	return false, fmt.Sprintf("unexpected status %d", resp.StatusCode)
}

// internetCheckURL is overridable for tests.
var internetCheckURL = "https://www.google.com"
