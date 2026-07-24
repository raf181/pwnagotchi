package pluginmanager

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
)

// recordingPlugin is a minimal Plugin + EventHandler used across tests.
type recordingPlugin struct {
	name string
	meta Metadata

	mu      sync.Mutex
	events  []string
	loaded  bool
	block   chan struct{} // if non-nil, HandleEvent blocks until closed
	panicOn string
}

func (p *recordingPlugin) Name() string       { return p.name }
func (p *recordingPlugin) Metadata() Metadata { return p.meta }

func (p *recordingPlugin) OnLoad(Capabilities) error {
	p.mu.Lock()
	p.loaded = true
	p.mu.Unlock()
	return nil
}

func (p *recordingPlugin) OnUnload() error {
	p.mu.Lock()
	p.loaded = false
	p.mu.Unlock()
	return nil
}

func (p *recordingPlugin) HandleEvent(event string, args []interface{}) {
	if p.block != nil {
		<-p.block
	}
	if p.panicOn != "" && event == p.panicOn {
		panic("boom: " + event)
	}
	p.mu.Lock()
	p.events = append(p.events, event)
	p.mu.Unlock()
}

func (p *recordingPlugin) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.events...)
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", timeout)
	}
}

func TestRegisterDuplicateRejected(t *testing.T) {
	m := New(Options{})
	p := &recordingPlugin{name: "dup"}
	if err := m.Register(p); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := m.Register(p); err == nil {
		t.Fatal("expected duplicate registration to fail")
	}
}

// expectLoadedAndConfigChanged asserts that events starts with the
// automatic "loaded" then "config_changed" pair every successful Load
// delivers (see Manager.Load's doc comment, mirroring plugins.py's
// one(name, 'loaded') / one(name, 'config_changed', config) sequence),
// and returns whatever follows so the rest of a test can assert on its
// own explicitly-emitted events without hardcoding a leading offset.
func expectLoadedAndConfigChanged(t *testing.T, events []string) []string {
	t.Helper()
	if len(events) < 2 || events[0] != "loaded" || events[1] != "config_changed" {
		t.Fatalf("expected leading [loaded config_changed], got %v", events)
	}
	return events[2:]
}

func TestLoadDeliversLoadedThenConfigChangedToThatPluginOnly(t *testing.T) {
	m := New(Options{})
	p := &recordingPlugin{name: "loaded-test"}
	other := &recordingPlugin{name: "other"}
	_ = m.Register(p)
	_ = m.Register(other) // never loaded: must receive nothing

	fullCfg := config.Map{"main": config.Map{"name": "unit-test"}}
	if err := m.Load(p.name, config.Map{"enabled": true}, fullCfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	waitFor(t, time.Second, func() bool { return len(p.snapshot()) == 2 })
	expectLoadedAndConfigChanged(t, p.snapshot())

	if got := other.snapshot(); len(got) != 0 {
		t.Fatalf("expected the not-yet-loaded plugin to receive nothing, got %v", got)
	}
}

func TestLoadCallsOnLoadAndStartsEventDelivery(t *testing.T) {
	m := New(Options{})
	p := &recordingPlugin{name: "loaded-test"}
	if err := m.Register(p); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(p.name, config.Map{"enabled": true}, nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	p.mu.Lock()
	loaded := p.loaded
	p.mu.Unlock()
	if !loaded {
		t.Fatal("expected OnLoad to have been called")
	}

	m.On("epoch", map[string]interface{}{"epoch": 1})

	waitFor(t, time.Second, func() bool { return len(p.snapshot()) == 3 })
	got := expectLoadedAndConfigChanged(t, p.snapshot())
	if len(got) != 1 || got[0] != "epoch" {
		t.Fatalf("expected [epoch] after the load sequence, got %v", got)
	}
}

func TestEventsDeliveredInOrderPerPlugin(t *testing.T) {
	m := New(Options{})
	p := &recordingPlugin{name: "order-test"}
	_ = m.Register(p)
	_ = m.Load(p.name, nil, nil)

	for i := 0; i < 50; i++ {
		m.On(fmt.Sprintf("ev%d", i))
	}
	waitFor(t, time.Second, func() bool { return len(p.snapshot()) == 52 })
	got := expectLoadedAndConfigChanged(t, p.snapshot())
	for i, ev := range got {
		want := fmt.Sprintf("ev%d", i)
		if ev != want {
			t.Fatalf("out of order at %d: want %s got %s", i, want, ev)
		}
	}
}

func TestPanicInOneHandlerIsolatedFromOthers(t *testing.T) {
	m := New(Options{})
	bad := &recordingPlugin{name: "bad", panicOn: "trigger"}
	good := &recordingPlugin{name: "good"}
	_ = m.Register(bad)
	_ = m.Register(good)
	_ = m.Load(bad.name, nil, nil)
	_ = m.Load(good.name, nil, nil)

	m.On("trigger")
	m.On("after")

	waitFor(t, time.Second, func() bool { return len(good.snapshot()) == 4 })

	statuses := m.List()
	var badStatus Status
	for _, s := range statuses {
		if s.Name == "bad" {
			badStatus = s
		}
	}
	if badStatus.Panics != 1 {
		t.Fatalf("expected 1 recorded panic, got %d", badStatus.Panics)
	}
	// The panicking plugin's goroutine must still be alive and process the
	// next event normally (isolation, not crash-and-stop). Checked inline
	// (not via expectLoadedAndConfigChanged, which hard-fails) since this
	// runs inside a polling predicate that may observe a partial snapshot.
	waitFor(t, time.Second, func() bool {
		snap := bad.snapshot()
		return len(snap) == 3 && snap[0] == "loaded" && snap[1] == "config_changed" && snap[2] == "after"
	})
}

func TestFullQueueDropsWithoutBlockingEmitter(t *testing.T) {
	m := New(Options{QueueSize: 2})
	p := &recordingPlugin{name: "slow", block: make(chan struct{})}
	_ = m.Register(p)
	_ = m.Load(p.name, nil, nil)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			m.On("ev")
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("On() blocked despite a full/slow plugin queue")
	}

	close(p.block) // let the one in-flight event (if any) proceed
	waitFor(t, time.Second, func() bool {
		for _, s := range m.List() {
			if s.Name == p.name {
				return s.Dropped > 0
			}
		}
		return false
	})
}

func TestUnloadStopsDeliveryAndCallsOnUnload(t *testing.T) {
	m := New(Options{})
	p := &recordingPlugin{name: "unload-test"}
	_ = m.Register(p)
	_ = m.Load(p.name, nil, nil)
	m.On("before")
	waitFor(t, time.Second, func() bool { return len(p.snapshot()) == 3 })

	if err := m.Unload(p.name); err != nil {
		t.Fatalf("Unload: %v", err)
	}
	p.mu.Lock()
	loaded := p.loaded
	p.mu.Unlock()
	if loaded {
		t.Fatal("expected OnUnload to have run")
	}

	m.On("after-unload")
	time.Sleep(20 * time.Millisecond)
	got := expectLoadedAndConfigChanged(t, p.snapshot())
	if len(got) != 1 || got[0] != "before" {
		t.Fatalf("expected no events delivered after unload, got %v", got)
	}
}

func TestToggleEnablesAndDisables(t *testing.T) {
	m := New(Options{})
	p := &recordingPlugin{name: "toggle-test"}
	_ = m.Register(p)

	changed, err := m.Toggle(p.name, true, nil, nil)
	if err != nil || !changed {
		t.Fatalf("enable: changed=%v err=%v", changed, err)
	}
	changed, err = m.Toggle(p.name, true, nil, nil)
	if err != nil || changed {
		t.Fatalf("re-enable should be a no-op: changed=%v err=%v", changed, err)
	}
	changed, err = m.Toggle(p.name, false, nil, nil)
	if err != nil || !changed {
		t.Fatalf("disable: changed=%v err=%v", changed, err)
	}
}

func TestLoadAllRespectsPerPluginEnabledConfig(t *testing.T) {
	m := New(Options{})
	a := &recordingPlugin{name: "a"}
	b := &recordingPlugin{name: "b"}
	_ = m.Register(a)
	_ = m.Register(b)

	errs := m.LoadAll(nil, map[string]config.Map{
		"a": {"enabled": true},
		"b": {"enabled": false},
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	statuses := map[string]bool{}
	for _, s := range m.List() {
		statuses[s.Name] = s.Enabled
	}
	if !statuses["a"] || statuses["b"] {
		t.Fatalf("expected a=enabled b=disabled, got %v", statuses)
	}
}

type loadFailPlugin struct{ recordingPlugin }

func (p *loadFailPlugin) OnLoad(Capabilities) error { return fmt.Errorf("boom") }

func TestOneFailingPluginLoadDoesNotAbortOthers(t *testing.T) {
	m := New(Options{})
	bad := &loadFailPlugin{recordingPlugin{name: "bad-load"}}
	good := &recordingPlugin{name: "good-load"}
	_ = m.Register(bad)
	_ = m.Register(good)

	errs := m.LoadAll(nil, map[string]config.Map{
		"bad-load":  {"enabled": true},
		"good-load": {"enabled": true},
	})
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 error, got %v", errs)
	}
	good.mu.Lock()
	loaded := good.loaded
	good.mu.Unlock()
	if !loaded {
		t.Fatal("expected the good plugin to still load despite the other's failure")
	}
}

type webhookPlugin struct{ recordingPlugin }

func (p *webhookPlugin) OnWebhook(subpath string, r *http.Request) (WebhookResponse, error) {
	return WebhookResponse{Status: 200, Body: []byte("hello:" + subpath)}, nil
}

func TestWebhookDispatchesToLoadedPlugin(t *testing.T) {
	m := New(Options{})
	p := &webhookPlugin{recordingPlugin{name: "hook", meta: Metadata{HasWebhook: true}}}
	_ = m.Register(p)
	_ = m.Load(p.name, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/plugins/hook/sub", nil)
	resp, err := m.Webhook(p.name, "sub", req)
	if err != nil {
		t.Fatalf("Webhook: %v", err)
	}
	if resp.Status != 200 || string(resp.Body) != "hello:sub" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestWebhookFailsForPluginWithoutHandler(t *testing.T) {
	m := New(Options{})
	p := &recordingPlugin{name: "no-hook"}
	_ = m.Register(p)
	_ = m.Load(p.name, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/plugins/no-hook/x", nil)
	if _, err := m.Webhook(p.name, "x", req); err == nil {
		t.Fatal("expected error for plugin without a webhook handler")
	}
}

func TestShutdownDrainsAllPlugins(t *testing.T) {
	m := New(Options{})
	names := []string{"s1", "s2", "s3"}
	plugins := map[string]*recordingPlugin{}
	for _, n := range names {
		p := &recordingPlugin{name: n}
		plugins[n] = p
		_ = m.Register(p)
		_ = m.Load(n, nil, nil)
	}
	m.Shutdown()
	for _, n := range names {
		if plugins[n].loaded {
			t.Fatalf("expected %s to be unloaded after Shutdown", n)
		}
	}
	for _, s := range m.List() {
		if s.Enabled {
			t.Fatalf("expected no plugin enabled after Shutdown, got %+v", s)
		}
	}
}

func TestCapabilitiesForReceivesPluginConfig(t *testing.T) {
	var gotName string
	var gotCfg config.Map
	m := New(Options{
		CapabilitiesFor: func(name string, cfg config.Map) Capabilities {
			gotName = name
			gotCfg = cfg
			return Capabilities{Config: cfg}
		},
	})
	p := &recordingPlugin{name: "caps-test"}
	_ = m.Register(p)
	cfg := config.Map{"foo": "bar"}
	if err := m.Load(p.name, cfg, nil); err != nil {
		t.Fatal(err)
	}
	if gotName != "caps-test" {
		t.Fatalf("expected CapabilitiesFor called with plugin name, got %q", gotName)
	}
	if gotCfg["foo"] != "bar" {
		t.Fatalf("expected plugin config passed through, got %v", gotCfg)
	}
}

func TestHasReportsRegisteredPlugins(t *testing.T) {
	m := New(Options{})
	if m.Has("nope") {
		t.Fatal("expected Has to be false for unregistered plugin")
	}
	_ = m.Register(&recordingPlugin{name: "present"})
	if !m.Has("present") {
		t.Fatal("expected Has to be true after Register")
	}
}

// Ensure the exported context import is actually used (GPIOLine.WaitEdge,
// CommandRunner.Run reference context.Context in capabilities.go); this
// test just exercises a fake implementation to keep that contract honest.
type fakeRunner struct{}

func (fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return []byte(name), nil
}

func TestCommandRunnerCapabilityShape(t *testing.T) {
	var r CommandRunner = fakeRunner{}
	out, err := r.Run(context.Background(), "echo", "hi")
	if err != nil || string(out) != "echo" {
		t.Fatalf("unexpected: %s %v", out, err)
	}
}
