package pluginrpc

import "encoding/json"

// Message is the one wire envelope for every line exchanged with a
// plugin subprocess over stdin/stdout, newline-delimited JSON — the same
// wire shape convention internal/pyplugin/bridge.go established for the
// Python bridge, reused here for a real Go subprocess instead.
//
// Two independent directions share this one envelope:
//   - Host -> Plugin, Type "event": fire-and-forget daemon events
//     (loaded, config_changed, wifi_update, handshake, epoch, ui_update,
//     ... — the exact same event vocabulary pluginmanager.EventHandler
//     already uses in-process; see docs/plugin-development.md's verified
//     event table). No response is required, matching the in-process
//     EventHandler.HandleEvent(name, args) fire-and-forget contract.
//   - Plugin -> Host, Type "call": a capability method invocation (e.g.
//     "Agent.Run", "View.Set", "Exec.Run") the plugin wants the daemon to
//     perform on its behalf, since the plugin process has no direct
//     access to the real *agent.Agent/*view.View/etc. Every call gets
//     exactly one Type "response" reply from the host, carrying Result
//     or Error, matched by ID.
type Message struct {
	ID     int64           `json:"id,omitempty"`
	Type   string          `json:"type"`
	Name   string          `json:"name,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

const (
	TypeEvent    = "event"
	TypeCall     = "call"
	TypeResponse = "response"
	TypePing     = "ping"
	TypePong     = "pong"
)
