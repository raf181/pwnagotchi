package pluginrpc

import (
	"sync"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// fakeAgent is a minimal, real pluginmanager.AgentCapability used to
// prove a remote plugin's "call" messages actually reach a live
// capability through the whole Manager -> RemotePlugin -> Host ->
// subprocess -> back round trip, not just within this package's own
// Host-level tests.
type fakeAgent struct {
	mu   sync.Mutex
	runs []string
}

func (a *fakeAgent) Run(cmd string, verbose bool) (interface{}, error) {
	a.mu.Lock()
	a.runs = append(a.runs, cmd)
	a.mu.Unlock()
	return map[string]interface{}{"ok": true}, nil
}
func (a *fakeAgent) Session(string) (interface{}, error) { return nil, nil }
func (a *fakeAgent) IsModuleRunning(string) bool         { return false }
func (a *fakeAgent) StartModule(string)                  {}
func (a *fakeAgent) RestartModule(string)                {}

var _ pluginmanager.AgentCapability = (*fakeAgent)(nil)

// TestRemotePluginFullLifecycleThroughRealManager proves a checksum-
// verified, really-spawned third-party plugin process participates in
// the exact same pluginmanager.Manager lifecycle (Register/Load/On/
// Unload) as an in-process native plugin — no special-casing anywhere in
// the manager — and that a daemon event delivered via Manager.On reaches
// the real subprocess and triggers a real capability call back into a
// live, granted capability.
func TestRemotePluginFullLifecycleThroughRealManager(t *testing.T) {
	path := buildFixture(t, "good", goodFixtureSource)
	manifest := manifestFor(t, path, "good-remote")
	manifest.Capabilities = []string{"Agent"}

	agent := &fakeAgent{}
	mgr := pluginmanager.New(pluginmanager.Options{
		CapabilitiesFor: func(name string, cfg config.Map) pluginmanager.Capabilities {
			return pluginmanager.Capabilities{Agent: agent}
		},
	})

	rp := NewRemotePlugin("good-remote", manifest, path)
	if err := mgr.Register(rp); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := mgr.Load("good-remote", config.Map{"enabled": true}, nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer mgr.Unload("good-remote")

	// Manager.Load already delivers "loaded" automatically — that alone
	// makes the fixture call Log.Printf. Now prove a manager-broadcast
	// event (Manager.On, the same real path internal/agent/internal/mesh
	// events travel through) reaches the subprocess and triggers a real
	// Agent.Run call.
	mgr.On("ping-me-back")

	waitFor(t, func() bool {
		agent.mu.Lock()
		defer agent.mu.Unlock()
		for _, r := range agent.runs {
			if r == "echo hi" {
				return true
			}
		}
		return false
	})

	statuses := mgr.List()
	found := false
	for _, s := range statuses {
		if s.Name == "good-remote" {
			found = true
			if !s.Enabled {
				t.Fatal("expected the remote plugin to show as enabled")
			}
		}
	}
	if !found {
		t.Fatal("expected the remote plugin to appear in Manager.List()")
	}
}

// TestRemotePluginCapabilityCallDeniedWithoutManifestGrant proves the
// manifest's declared capability list is enforced, not just cosmetic:
// a plugin whose manifest doesn't list "Agent" gets a real, structured
// denial if it tries to call an Agent method anyway, even though the
// daemon happens to have a live Agent capability wired.
func TestRemotePluginCapabilityCallDeniedWithoutManifestGrant(t *testing.T) {
	path := buildFixture(t, "good", goodFixtureSource)
	manifest := manifestFor(t, path, "unprivileged-remote")
	manifest.Capabilities = nil // deliberately grants nothing

	agent := &fakeAgent{}
	dispatcher := NewCapabilityDispatcher(pluginmanager.Capabilities{Agent: agent}, manifest.Capabilities)

	_, err := dispatcher.Dispatch("Agent.Run", []byte(`{"cmd":"echo hi","verbose":false}`))
	if err == nil {
		t.Fatal("expected the call to be denied since Agent wasn't granted")
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.runs) != 0 {
		t.Fatal("expected the real Agent.Run to never have been invoked")
	}
}
