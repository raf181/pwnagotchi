package pluginrpc

import (
	"fmt"
	"sync"

	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// RemotePlugin adapts one checksum-verified, separately-compiled Go
// plugin executable into a pluginmanager.Plugin — the same lifecycle
// (Loader/Unloader/EventHandler) every in-process native plugin already
// implements, so cmd/pwnagotchi's Manager.Load/On/Unload treats a
// third-party out-of-process plugin identically to a bundled one.
type RemotePlugin struct {
	name         string
	manifest     *Manifest
	execPath     string
	capabilities []string

	mu     sync.Mutex
	host   *Host
	failed error
}

// NewRemotePlugin describes (but does not yet spawn) one third-party
// plugin: name, its manifest (for metadata + checksum verification), and
// the path to its already-downloaded executable.
func NewRemotePlugin(name string, manifest *Manifest, execPath string) *RemotePlugin {
	return &RemotePlugin{name: name, manifest: manifest, execPath: execPath, capabilities: manifest.Capabilities}
}

func (p *RemotePlugin) Name() string { return p.name }

func (p *RemotePlugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     p.manifest.Version,
		Author:      p.manifest.Author,
		License:     p.manifest.License,
		Description: p.manifest.Description,
	}
}

// OnLoad verifies the executable's checksum (again — defense in depth;
// Spawn also checks) and spawns it, wiring a CapabilityDispatcher scoped
// to exactly this plugin's manifest-declared capabilities.
func (p *RemotePlugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	dispatcher := NewCapabilityDispatcher(caps, p.capabilities)
	host, err := Spawn(p.name, p.execPath, p.manifest, Options{
		Dispatcher: dispatcher,
		Logger:     nil,
		OnFailure: func(err error) {
			p.mu.Lock()
			p.failed = err
			p.mu.Unlock()
		},
	})
	if err != nil {
		return fmt.Errorf("pluginrpc: spawning %q: %w", p.name, err)
	}
	p.host = host
	// Deliberately does NOT send its own "loaded" event here:
	// pluginmanager.Manager.Load already delivers "loaded" then
	// "config_changed" to every EventHandler-implementing plugin
	// immediately after a successful OnLoad (see manager.go's Load doc
	// comment) — RemotePlugin implements EventHandler, so the manager's
	// own HandleEvent("loaded", ...) call reaches this same subprocess
	// via HandleEvent below. Sending it again here would deliver it
	// twice.
	return nil
}

// OnUnload stops the subprocess gracefully.
func (p *RemotePlugin) OnUnload() error {
	p.mu.Lock()
	host := p.host
	p.host = nil
	p.mu.Unlock()
	if host == nil {
		return nil
	}
	return host.Close()
}

// HandleEvent forwards a daemon event to the subprocess. If the process
// has already crashed/hung, this is a clear, logged no-op — never a
// panic, and never propagates the remote failure back into the emitting
// goroutine (matching in-process EventHandler's own panic-isolation
// guarantee, just at the process boundary).
func (p *RemotePlugin) HandleEvent(event string, args []interface{}) {
	p.mu.Lock()
	host := p.host
	p.mu.Unlock()
	if host == nil || !host.Alive() {
		return
	}
	_ = host.SendEvent(event, args)
}

// Failed reports the most recent crash/hang reason, if any, for
// diagnostics (a future `pwnagotchi plugins doctor` command).
func (p *RemotePlugin) Failed() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.failed
}

// Alive reports whether the subprocess is currently running and
// responsive.
func (p *RemotePlugin) Alive() bool {
	p.mu.Lock()
	host := p.host
	p.mu.Unlock()
	return host != nil && host.Alive()
}

var (
	_ pluginmanager.Plugin       = (*RemotePlugin)(nil)
	_ pluginmanager.Loader       = (*RemotePlugin)(nil)
	_ pluginmanager.Unloader     = (*RemotePlugin)(nil)
	_ pluginmanager.EventHandler = (*RemotePlugin)(nil)
)
