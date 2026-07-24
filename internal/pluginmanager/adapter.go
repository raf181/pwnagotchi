package pluginmanager

// EmitterPlugin adapts an existing type that already implements the
// EventEmitter shape (`On(event string, args ...interface{})` — the same
// interface satisfied by internal/wpasec.Plugin and, previously, by
// *pyplugin.Bridge itself) into a Manager-managed Plugin, so it gets a
// real serial per-plugin queue, panic isolation, and observable
// handled/dropped/panic counters instead of being called synchronously
// in-line from a hand-rolled fan-out list (main.go's old multiEmitter).
//
// This is the seam existing native plugins (wpa-sec today; more as task
// "port all bundled plugins" lands) are folded into the one plugin
// manager through, without needing to rewrite their internal event
// handling to the lower-level EventHandler(event, []interface{}) shape
// directly.
type EmitterPlugin struct {
	PluginName string
	Meta       Metadata
	Emitter    Emitter
}

// Emitter is the minimal shape being adapted: exactly the EventEmitter
// interface already used throughout internal/agent, internal/mesh,
// internal/ui/view, and internal/cli.
type Emitter interface {
	On(event string, args ...interface{})
}

func (e *EmitterPlugin) Name() string       { return e.PluginName }
func (e *EmitterPlugin) Metadata() Metadata { return e.Meta }

// HandleEvent implements pluginmanager.EventHandler by forwarding to the
// wrapped Emitter's own On method, inside the Manager's per-plugin queue
// goroutine (so a slow or panicking On call is isolated exactly like any
// other native plugin's HandleEvent).
func (e *EmitterPlugin) HandleEvent(event string, args []interface{}) {
	e.Emitter.On(event, args...)
}
