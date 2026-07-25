//go:build live

// Package tests' live-hardware suite verifies internal/config and
// internal/bettercap against REAL hardware and a REAL running bettercap
// instance. It is opt-in ONLY (`-tags=live`, never part of `go test ./...`
// or `make compatibility-test`) and must be run deliberately by a human on
// a machine that actually has the described hardware/services — never
// automatically, and never as a side effect of routine development.
//
// This file intentionally does NOT exercise internal/unit.SetName/Restart/
// Reboot or internal/agent's automata-driven restart path: those call real
// `hostname`/`service ...`/`shutdown -r now` commands and have already
// caused unintended host reboots once during manual testing (see
// docs/known-differences.md). Only read-only queries and, in
// TestLiveBettercapRun, a single explicitly-opt-in active command are run
// here.
package tests

import (
	"os"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/agent"
	"github.com/jayofelony/pwnagotchi/internal/bettercap"
	"github.com/jayofelony/pwnagotchi/internal/cli"
	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/identity"
	"github.com/jayofelony/pwnagotchi/internal/voice"
)

// liveIface is the real monitor-capable Wi-Fi interface name to query,
// overridable via LIVE_IFACE (defaults to "wlan0", matching this lab rig).
func liveIface() string {
	if v := os.Getenv("LIVE_IFACE"); v != "" {
		return v
	}
	return "wlan0"
}

// TestLiveIfaceChannels verifies internal/config.IfaceChannels against a
// REAL wireless PHY (no shell, no mocks — internal/config.ExecCommandRunner
// shells out to the real `iw` binary via os/exec).
func TestLiveIfaceChannels(t *testing.T) {
	channels := config.IfaceChannels(config.ExecCommandRunner, liveIface())
	if len(channels) == 0 {
		t.Fatalf("IfaceChannels(%q) returned no channels — is the interface present and is `iw` on PATH?", liveIface())
	}
	t.Logf("IfaceChannels(%q) = %v (real hardware query via `iw`)", liveIface(), channels)

	// Every real 2.4GHz Wi-Fi channel is 1-14; a sane sanity bound that
	// would catch a badly broken parse without hardcoding this rig's exact
	// channel list (which depends on the adapter's regulatory domain).
	for _, c := range channels {
		if c < 1 || c > 233 {
			t.Errorf("channel %d out of any plausible Wi-Fi range", c)
		}
	}
}

// TestLiveBettercapSession verifies internal/bettercap.Client.Session()
// against a REAL running bettercap instance (read-only: GET /api/session).
// Point BETTERCAP_URL/USER/PASS at your instance; defaults match this lab
// rig's pwnagotchi-auto.cap (127.0.0.1:8081, pwnagotchi/pwnagotchi).
func TestLiveBettercapSession(t *testing.T) {
	client := liveBettercapClient(t)

	result, err := client.Session("")
	if err != nil {
		t.Fatalf("Session(): %v (is bettercap's api.rest running?)", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("Session() = %T, want a JSON object", result)
	}
	if _, ok := m["version"]; !ok {
		t.Fatalf("Session() response missing 'version' field: %v", m)
	}
	t.Logf("real bettercap version: %v", m["version"])

	// wifi sub-session, matching Agent.get_access_points' own call shape.
	wifiResult, err := client.Session("session/wifi")
	if err != nil {
		t.Fatalf("Session(session/wifi): %v", err)
	}
	t.Logf("real wifi session keys present: %v", mapKeys(wifiResult))
}

// TestLiveAgentAccessPoints verifies internal/agent's real AP-parsing/
// whitelist-filtering/channel-grouping logic (GetAccessPoints/
// GetAccessPointsByChannel) against the REAL bettercap session data
// produced by this rig's actual running `bettercap.service`
// (pwnagotchi-auto.cap on wlan0mon, scanning the lab's own "casa"/
// "familia" networks — confirmed to carry no real client traffic, so
// read-only recon against them is safe). It builds a real *agent.Agent
// from the real installed config at /etc/pwnagotchi/config.toml (or
// LIVE_CONFIG) and the real keypair already at identity.DefaultPath, using
// config.LoadTOMLFileForEdit (a plain read, no boot-install/drift-rewrite
// side effects — unlike config.LoadConfig, it never touches
// /etc/pwnagotchi/default.toml).
//
// Deliberately read-only: only Session()-backed queries run. It does NOT
// call Agent.Recon/Associate/Deauth/SetChannel/Start/StartEventPolling/
// StartSessionFetcher — those either issue live bettercap commands that
// would perturb the already-running recon session's own state or
// transmit real 802.11 frames, and are out of scope for what this test is
// verifying (the parsing logic, not the RF actions). It also never
// constructs anything that could reach internal/unit.SetName/Restart/
// Reboot — see the incident note in docs/final-port-report.md.
func TestLiveAgentAccessPoints(t *testing.T) {
	cfgPath := envOr("LIVE_CONFIG", "/etc/pwnagotchi/config.toml")
	cfg, err := config.LoadTOMLFileForEdit(cfgPath)
	if err != nil {
		t.Fatalf("LoadTOMLFileForEdit(%q): %v (is this rig's real pwnagotchi config present?)", cfgPath, err)
	}

	view := cli.NewHeadlessView(voice.New("en"))
	kp, err := identity.NewKeyPair(identity.DefaultPath, view)
	if err != nil {
		t.Fatalf("NewKeyPair(%q): %v", identity.DefaultPath, err)
	}

	a, err := agent.New(view, cfg, kp, nil)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	groups := a.GetAccessPointsByChannel()
	t.Logf("real bettercap session: %d channel group(s) with APs", len(groups))
	for _, g := range groups {
		t.Logf("  channel %d: %d AP(s)", g.Channel, len(g.APs))
	}
	t.Logf("total real APs seen (post-whitelist-filter): %d", a.GetTotalAPs())
}

func mapKeys(v interface{}) []string {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func liveBettercapClient(t *testing.T) *bettercap.Client {
	t.Helper()
	host := envOr("BETTERCAP_HOST", "127.0.0.1")
	port := 8081
	user := envOr("BETTERCAP_USER", "pwnagotchi")
	pass := envOr("BETTERCAP_PASS", "pwnagotchi")
	return bettercap.NewClient(host, "http", port, user, pass)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
