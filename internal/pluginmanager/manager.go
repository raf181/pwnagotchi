// Package pluginmanager is the native Go replacement for the Python
// subprocess bridge (internal/pyplugin): one production plugin manager that
// every native plugin registers with, in-process, with no interpreter, no
// IPC, and no serialization boundary.
//
// It implements the same EventEmitter shape already used throughout this
// port (agent.EventEmitter, mesh.EventEmitter, view.EventEmitter,
// cli.EventEmitter: `On(event string, args ...interface{})`), so a *Manager
// can be passed anywhere those packages currently accept a *pyplugin.Bridge
// or any other EventEmitter, without changing their interfaces.
//
// Each registered plugin gets its own bounded, serial event queue and
// goroutine: events for one plugin are delivered in order, a panic or long
// handler in one plugin can never block or corrupt another plugin's
// delivery, and a full queue drops the oldest-pending event (logged +
// counted) rather than blocking the emitter (which would otherwise stall
// the whole daemon on one slow plugin, exactly the failure mode a per-event
// Python thread pool would also have to guard against).
package pluginmanager

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
)

// Metadata mirrors the class attributes real Python plugins expose
// (__author__, __version__, __license__, __description__) plus whether the
// plugin implements a webhook, exactly matching pyplugin.PluginMeta's shape
// so internal/web's existing /plugins page rendering needs no format change.
type Metadata struct {
	Version     string
	Author      string
	License     string
	Description string
	HasWebhook  bool
}

// Plugin is the minimum every native plugin must implement. Everything else
// (lifecycle hooks, event handling, webhooks) is expressed as an optional
// interface the Manager type-asserts for, so a plugin with no background
// work or web surface stays a two-method struct.
type Plugin interface {
	Name() string
	Metadata() Metadata
}

// Loader is implemented by plugins that need setup work and/or capabilities
// when the manager starts them. Returning an error marks the plugin failed
// (logged, not fatal to the daemon — exactly like a real Python plugin
// raising inside on_loaded is caught by plugins.load()'s own try/except).
type Loader interface {
	OnLoad(Capabilities) error
}

// Unloader is implemented by plugins with real teardown work (stopping
// background goroutines, closing files/sockets). Called on Unload/Shutdown.
type Unloader interface {
	OnUnload() error
}

// EventHandler is implemented by plugins that react to the on_<event>
// lifecycle: loaded, config_changed, unload, mood-related events, wait,
// sleep, epoch, wifi_update, unfiltered_ap_list, bcap_<tag>, handshake,
// association/deauthentication/channel-hop, peer events, internet
// availability, UI/display setup/update, inbox events, and any future
// event name — matching real Python's dynamic getattr(plugin,
// f"on_{event}") dispatch via a name+args pair instead of one Go method per
// event name, so adding a new emitted event never requires widening this
// interface.
type EventHandler interface {
	HandleEvent(event string, args []interface{})
}

// WebhookResponse is a native plugin's on_webhook return value.
type WebhookResponse struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

// WebhookHandler is implemented by plugins with a synchronous HTTP
// passthrough route (Python: on_webhook(request)), called in-process with
// no serialization boundary — a real net/http.Request, not a reconstructed
// stub.
type WebhookHandler interface {
	OnWebhook(subpath string, r *http.Request) (WebhookResponse, error)
}

// RouteRegistrar is implemented by plugins that need dedicated web routes
// beyond the generic webhook passthrough (Python plugins registering extra
// Flask blueprints/routes, e.g. webgpsmap's map page, ohcapi's API surface).
// Capabilities.Web is handed the same mux the rest of internal/web serves
// from, so routes are indistinguishable from built-in ones.
type RouteRegistrar interface {
	RegisterRoutes(web WebCapability)
}

// FailureReporter is implemented by isolated plugin backends that can fail
// after OnLoad has returned, such as an out-of-process plugin executable.
type FailureReporter interface {
	Failed() error
}

// entry is the manager's bookkeeping for one registered plugin.
type entry struct {
	plugin  Plugin
	enabled bool

	lifecycleMu      sync.RWMutex
	routesRegistered bool

	queue    chan queuedEvent
	cancel   context.CancelFunc
	done     chan struct{}
	queueCap int

	mu       sync.Mutex
	dropped  uint64
	panics   uint64
	handled  uint64
	lastErr  string
	loadErr  error
	loadedAt time.Time
}

type queuedEvent struct {
	name string
	args []interface{}
}

// Options configures a Manager.
type Options struct {
	// QueueSize bounds each plugin's per-event backlog. 0 defaults to 64.
	QueueSize int
	// Logger receives operational lines (plugin load/unload/panic/drop).
	// Defaults to the standard library logger if nil.
	Logger *log.Logger
	// CapabilitiesFor builds the Capabilities passed to a plugin's OnLoad,
	// given its own config sub-map. Production wires this to real agent/
	// view/exec/http/clock/GPIO/I2C/system/event capabilities; tests can
	// hand back a minimal or fake set. If nil, plugins receive a
	// Capabilities value with only Config populated.
	CapabilitiesFor func(pluginName string, cfg config.Map) Capabilities
}

// Manager is the single native plugin manager. It satisfies the
// EventEmitter shape (`On(event string, args ...interface{})`) used across
// internal/agent, internal/mesh, internal/ui/view, and internal/cli.
type Manager struct {
	mu       sync.RWMutex
	entries  map[string]*entry
	order    []string
	queueCap int
	logger   *log.Logger
	capsFor  func(pluginName string, cfg config.Map) Capabilities
}

// New creates an empty Manager. Register plugins with Register, then start
// them with Load.
func New(opts Options) *Manager {
	qc := opts.QueueSize
	if qc <= 0 {
		qc = 64
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &Manager{
		entries:  map[string]*entry{},
		queueCap: qc,
		logger:   logger,
		capsFor:  opts.CapabilitiesFor,
	}
}

// Register adds a plugin under manager control without starting it (it
// becomes visible to ListPlugins as not-yet-loaded). Registering a name
// that already exists is a programmer error (duplicate plugin), returned
// as an error rather than silently overwriting — real Python's
// plugins.load() likewise refuses to shadow an already-loaded module name.
func (m *Manager) Register(p Plugin) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	name := p.Name()
	if _, exists := m.entries[name]; exists {
		return fmt.Errorf("pluginmanager: duplicate plugin registration: %q", name)
	}
	m.entries[name] = &entry{plugin: p, queueCap: m.queueCap}
	m.order = append(m.order, name)
	return nil
}

// Load starts a registered plugin: builds its Capabilities, calls OnLoad if
// implemented, and — if it implements EventHandler — starts its serial
// event-delivery goroutine, then delivers exactly "loaded" followed by
// "config_changed" (with fullCfg as its sole argument) to THIS plugin only
// — mirroring plugins.py's one(name, 'loaded') / one(name, 'config_changed',
// config) sequence in toggle_plugin (a single-plugin, not broadcast,
// dispatch), which real plugins.load() also reduces to at startup since
// every plugin is loading for the first time together. fullCfg is the
// complete merged daemon config (Python's on_config_changed(config)
// receives the whole thing, not just this plugin's own options — e.g.
// cache.py's on_config_changed reads config['bettercap']['handshakes']).
// A load error is recorded and returned but never panics; the caller
// (typically Manager's own LoadAll, or a web-triggered enable) decides
// whether to treat it as fatal.
func (m *Manager) Load(name string, pluginCfg config.Map, fullCfg config.Map) error {
	m.mu.RLock()
	e, ok := m.entries[name]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("pluginmanager: unknown plugin %q", name)
	}

	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()
	return m.loadEntryLocked(name, e, pluginCfg, fullCfg)
}

func (m *Manager) loadEntryLocked(name string, e *entry, pluginCfg config.Map, fullCfg config.Map) error {
	m.mu.RLock()
	if e.enabled {
		m.mu.RUnlock()
		return nil
	}
	m.mu.RUnlock()

	var caps Capabilities
	if m.capsFor != nil {
		caps = m.capsFor(name, pluginCfg)
	} else {
		caps = Capabilities{Config: pluginCfg}
	}
	caps.Log = &pluginLogger{logger: m.logger, name: name}

	if loader, ok := e.plugin.(Loader); ok {
		if err := loader.OnLoad(caps); err != nil {
			e.mu.Lock()
			e.loadErr = err
			e.mu.Unlock()
			m.logger.Printf("pluginmanager: %s: OnLoad failed: %v", name, err)
			return err
		}
	}

	if reg, ok := e.plugin.(RouteRegistrar); ok && caps.Web != nil && !e.routesRegistered {
		reg.RegisterRoutes(caps.Web)
		e.routesRegistered = true
	}

	if handler, ok := e.plugin.(EventHandler); ok {
		ctx, cancel := context.WithCancel(context.Background())
		e.queue = make(chan queuedEvent, e.queueCap)
		e.cancel = cancel
		e.done = make(chan struct{})
		go m.runPlugin(ctx, name, e, handler)

		e.queue <- queuedEvent{name: "loaded"}
		e.queue <- queuedEvent{name: "config_changed", args: []interface{}{fullCfg}}
	}

	m.mu.Lock()
	e.enabled = true
	e.loadedAt = time.Now()
	m.mu.Unlock()
	e.mu.Lock()
	e.loadErr = nil
	e.mu.Unlock()

	m.logger.Printf("pluginmanager: loaded plugin %q", name)
	return nil
}

// LoadAll loads every registered plugin whose per-plugin config map has
// enabled=true, mirroring plugins.load()'s own config-driven activation.
// Errors are logged per-plugin and collected but never abort the loop —
// one broken plugin must never prevent the rest from starting, matching
// real Python's per-plugin try/except in plugins.load(). fullCfg is
// passed through to each Load call for its "config_changed" dispatch —
// see Load's doc comment.
func (m *Manager) LoadAll(fullCfg config.Map, pluginConfigs map[string]config.Map) []error {
	m.mu.RLock()
	names := append([]string(nil), m.order...)
	m.mu.RUnlock()

	var errs []error
	for _, name := range names {
		cfg := pluginConfigs[name]
		enabled, _ := cfg["enabled"].(bool)
		if !enabled {
			continue
		}
		if err := m.Load(name, cfg, fullCfg); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errs
}

// runPlugin is the per-plugin serial delivery goroutine: exactly one
// HandleEvent call in flight at a time for this plugin, in the order
// events were emitted, with panic recovery isolating this plugin from the
// rest of the daemon.
func (m *Manager) runPlugin(ctx context.Context, name string, e *entry, handler EventHandler) {
	defer close(e.done)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-e.queue:
			if !ok {
				return
			}
			m.dispatch(name, e, handler, ev)
		}
	}
}

func (m *Manager) dispatch(name string, e *entry, handler EventHandler, ev queuedEvent) {
	defer func() {
		if r := recover(); r != nil {
			e.mu.Lock()
			e.panics++
			e.lastErr = fmt.Sprintf("panic in on_%s: %v", ev.name, r)
			e.mu.Unlock()
			m.logger.Printf("pluginmanager: %s: recovered panic handling event %q: %v", name, ev.name, r)
		}
	}()
	handler.HandleEvent(ev.name, ev.args)
	e.mu.Lock()
	e.handled++
	e.mu.Unlock()
}

// On implements the EventEmitter shape used across internal/agent,
// internal/mesh, internal/ui/view, and internal/cli: fan the event out to
// every loaded plugin's own bounded queue, non-blocking. A full queue drops
// the event for that plugin only (oldest-first: the incoming event is
// dropped, not an already-queued one, matching "don't let one slow plugin
// stall newer events indefinitely" over strict Python-thread-per-call
// parity) and is counted/logged, never blocks the caller.
func (m *Manager) On(event string, args ...interface{}) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, name := range m.order {
		e := m.entries[name]
		if !e.enabled || e.queue == nil {
			continue
		}
		select {
		case e.queue <- queuedEvent{name: event, args: args}:
		default:
			e.mu.Lock()
			e.dropped++
			e.mu.Unlock()
			m.logger.Printf("pluginmanager: %s: queue full, dropped event %q", name, event)
		}
	}
}

// Unload stops one plugin: cancels its event goroutine (if any), waits for
// it to drain/exit, and calls OnUnload if implemented. Safe to call on an
// already-unloaded or never-loaded plugin.
func (m *Manager) Unload(name string) error {
	m.mu.RLock()
	e, ok := m.entries[name]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("pluginmanager: unknown plugin %q", name)
	}

	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()
	return m.unloadEntryLocked(name, e)
}

func (m *Manager) unloadEntryLocked(name string, e *entry) error {
	m.mu.Lock()
	if !e.enabled {
		m.mu.Unlock()
		return nil
	}
	e.enabled = false
	cancel := e.cancel
	done := e.done
	m.mu.Unlock()

	if cancel != nil {
		cancel()
		<-done
	}
	e.queue = nil
	e.cancel = nil
	e.done = nil

	var err error
	if unloader, ok := e.plugin.(Unloader); ok {
		err = unloader.OnUnload()
		if err != nil {
			m.logger.Printf("pluginmanager: %s: OnUnload failed: %v", name, err)
		}
	}
	m.logger.Printf("pluginmanager: unloaded plugin %q", name)
	return err
}

// Shutdown unloads every loaded plugin. Safe to call multiple times.
func (m *Manager) Shutdown() {
	m.mu.RLock()
	names := append([]string(nil), m.order...)
	m.mu.RUnlock()
	for _, name := range names {
		_ = m.Unload(name)
	}
}

// Toggle enables or disables a plugin at runtime (the web UI's /plugins
// toggle switch), mirroring plugins.toggle_plugin: enabling loads it (with
// the config the caller supplies, which should reflect any just-saved
// on-disk change), disabling unloads it. fullCfg is passed through to
// Load's "config_changed" dispatch when enabling — see Load's doc
// comment. Returns whether the enabled state actually changed.
func (m *Manager) Toggle(name string, enable bool, pluginCfg config.Map, fullCfg config.Map) (bool, error) {
	m.mu.RLock()
	e, ok := m.entries[name]
	m.mu.RUnlock()
	if !ok {
		return false, fmt.Errorf("pluginmanager: unknown plugin %q", name)
	}

	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()
	m.mu.RLock()
	was := e.enabled
	m.mu.RUnlock()
	if enable == was {
		return false, nil
	}
	if enable {
		if err := m.loadEntryLocked(name, e, pluginCfg, fullCfg); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := m.unloadEntryLocked(name, e); err != nil {
		return true, err
	}
	return true, nil
}

// Status is one plugin's observable health for the web UI and diagnostics.
type Status struct {
	Name        string
	Metadata    Metadata
	Enabled     bool
	Handled     uint64
	Dropped     uint64
	Panics      uint64
	LastError   string
	LoadError   string
	HasWebhook  bool
	HasEvents   bool
	LoadedSince time.Time
}

// List returns every registered plugin's status, sorted by name, for the
// web /plugins page and CLI tooling — replacing pyplugin.Bridge.ListPlugins
// with real in-process state instead of a synchronous IPC round-trip.
func (m *Manager) List() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Status, 0, len(m.entries))
	for name, e := range m.entries {
		e.mu.Lock()
		loadErr := ""
		if e.loadErr != nil {
			loadErr = e.loadErr.Error()
		}
		lastErr := e.lastErr
		if reporter, ok := e.plugin.(FailureReporter); ok {
			if err := reporter.Failed(); err != nil {
				lastErr = err.Error()
			}
		}
		_, hasEvents := e.plugin.(EventHandler)
		out = append(out, Status{
			Name:        name,
			Metadata:    e.plugin.Metadata(),
			Enabled:     e.enabled,
			Handled:     e.handled,
			Dropped:     e.dropped,
			Panics:      e.panics,
			LastError:   lastErr,
			LoadError:   loadErr,
			HasWebhook:  e.plugin.Metadata().HasWebhook,
			HasEvents:   hasEvents,
			LoadedSince: e.loadedAt,
		})
		e.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Webhook dispatches a synchronous webhook call to a loaded plugin
// implementing WebhookHandler, in-process — no serialization boundary, no
// bridge process, a real *http.Request.
func (m *Manager) Webhook(name, subpath string, r *http.Request) (WebhookResponse, error) {
	m.mu.RLock()
	e, ok := m.entries[name]
	m.mu.RUnlock()
	if !ok {
		return WebhookResponse{}, fmt.Errorf("pluginmanager: plugin %q not loaded", name)
	}

	e.lifecycleMu.RLock()
	defer e.lifecycleMu.RUnlock()
	m.mu.RLock()
	enabled := e.enabled
	m.mu.RUnlock()
	if !enabled {
		return WebhookResponse{}, fmt.Errorf("pluginmanager: plugin %q not loaded", name)
	}
	handler, ok := e.plugin.(WebhookHandler)
	if !ok {
		return WebhookResponse{}, fmt.Errorf("pluginmanager: plugin %q has no webhook", name)
	}
	return handler.OnWebhook(subpath, r)
}

// Has reports whether name is registered with this manager at all, loaded or
// not. The web layer uses it to distinguish native plugin routes from unknown
// names.
func (m *Manager) Has(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.entries[name]
	return ok
}

// pluginLogger is the Capabilities.Log implementation: every line is
// prefixed with the owning plugin's name so multi-plugin log output stays
// attributable, matching Python's per-module logger naming.
type pluginLogger struct {
	logger *log.Logger
	name   string
}

func (l *pluginLogger) Printf(format string, args ...interface{}) {
	l.logger.Printf("[%s] %s", l.name, fmt.Sprintf(format, args...))
}
