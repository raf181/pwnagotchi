package pluginrpc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// CapabilityDispatcher implements Dispatcher by executing a plugin's
// "Method.Name" calls against a real pluginmanager.Capabilities value —
// the exact same capabilities an in-process native plugin's OnLoad
// receives, just invoked across the RPC boundary instead of a direct Go
// call. Only capabilities the third-party plugin actually needs (per its
// manifest) are ever wired live; anything else dispatches a clear
// "capability not granted" error rather than silently no-op'ing.
//
// The supported RPC surface is deliberately small and versioned:
// Log.Printf; Agent.Run, Agent.Session, Agent.SupportedChannels, and
// Agent.ResetHistory; View.Set and View.Update; Exec.Run; and Clock.Now.
// Manifest validation rejects capability groups outside this surface.
type CapabilityDispatcher struct {
	Caps    pluginmanager.Capabilities
	Granted map[string]bool // capability names (e.g. "Agent", "View") this plugin's manifest declared
}

// NewCapabilityDispatcher builds a dispatcher scoped to exactly the
// capability groups listed in granted (matching Manifest.Capabilities),
// regardless of what's actually populated in caps — a plugin that didn't
// declare "System" can't call Shutdown even if the host happens to have
// a non-nil SystemCapability wired.
func NewCapabilityDispatcher(caps pluginmanager.Capabilities, granted []string) *CapabilityDispatcher {
	g := make(map[string]bool, len(granted))
	for _, name := range granted {
		g[name] = true
	}
	return &CapabilityDispatcher{Caps: caps, Granted: g}
}

type runArgs struct {
	Cmd     string `json:"cmd"`
	Verbose bool   `json:"verbose"`
}

type sessionArgs struct {
	Session string `json:"session"`
}

type viewSetArgs struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type viewUpdateArgs struct {
	Force bool `json:"force"`
}

type execRunArgs struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
}

type logArgs struct {
	Message string `json:"message"`
}

// Dispatch implements Dispatcher.
func (d *CapabilityDispatcher) Dispatch(method string, args json.RawMessage) (interface{}, error) {
	switch method {
	case "Log.Printf":
		if !d.granted("Log") {
			return nil, fmt.Errorf("pluginrpc: plugin was not granted the Log capability")
		}
		if d.Caps.Log == nil {
			return nil, fmt.Errorf("pluginrpc: Log capability not available")
		}
		var a logArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		d.Caps.Log.Printf("%s", a.Message)
		return nil, nil

	case "Agent.Run":
		if !d.granted("Agent") {
			return nil, fmt.Errorf("pluginrpc: plugin was not granted the Agent capability")
		}
		if d.Caps.Agent == nil {
			return nil, fmt.Errorf("pluginrpc: Agent capability not available")
		}
		var a runArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		result, err := d.Caps.Agent.Run(a.Cmd, a.Verbose)
		if err != nil {
			return nil, err
		}
		return result, nil

	case "Agent.Session":
		if !d.granted("Agent") {
			return nil, fmt.Errorf("pluginrpc: plugin was not granted the Agent capability")
		}
		if d.Caps.Agent == nil {
			return nil, fmt.Errorf("pluginrpc: Agent capability not available")
		}
		var a sessionArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		return d.Caps.Agent.Session(a.Session)

	case "Agent.SupportedChannels":
		if !d.granted("Agent") {
			return nil, fmt.Errorf("pluginrpc: plugin was not granted the Agent capability")
		}
		if d.Caps.Agent == nil {
			return nil, fmt.Errorf("pluginrpc: Agent capability not available")
		}
		return d.Caps.Agent.SupportedChannels(), nil

	case "Agent.ResetHistory":
		if !d.granted("Agent") {
			return nil, fmt.Errorf("pluginrpc: plugin was not granted the Agent capability")
		}
		if d.Caps.Agent == nil {
			return nil, fmt.Errorf("pluginrpc: Agent capability not available")
		}
		d.Caps.Agent.ResetHistory()
		return nil, nil

	case "View.Set":
		if !d.granted("View") {
			return nil, fmt.Errorf("pluginrpc: plugin was not granted the View capability")
		}
		if d.Caps.View == nil {
			return nil, fmt.Errorf("pluginrpc: View capability not available")
		}
		var a viewSetArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		d.Caps.View.Set(a.Key, a.Value)
		return nil, nil

	case "View.Update":
		if !d.granted("View") {
			return nil, fmt.Errorf("pluginrpc: plugin was not granted the View capability")
		}
		if d.Caps.View == nil {
			return nil, fmt.Errorf("pluginrpc: View capability not available")
		}
		var a viewUpdateArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		d.Caps.View.Update(a.Force)
		return nil, nil

	case "Exec.Run":
		if !d.granted("Exec") {
			return nil, fmt.Errorf("pluginrpc: plugin was not granted the Exec capability")
		}
		if d.Caps.Exec == nil {
			return nil, fmt.Errorf("pluginrpc: Exec capability not available")
		}
		var a execRunArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), defaultCallTimeout)
		defer cancel()
		out, err := d.Caps.Exec.Run(ctx, a.Name, a.Args...)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"output": string(out)}, nil

	case "Clock.Now":
		if !d.granted("Clock") {
			return nil, fmt.Errorf("pluginrpc: plugin was not granted the Clock capability")
		}
		if d.Caps.Clock == nil {
			return nil, fmt.Errorf("pluginrpc: Clock capability not available")
		}
		return map[string]interface{}{"unix_nano": d.Caps.Clock.Now().UnixNano()}, nil

	default:
		return nil, fmt.Errorf("pluginrpc: unknown or unimplemented capability method %q", method)
	}
}

func (d *CapabilityDispatcher) granted(capability string) bool {
	return d.Granted[capability]
}
