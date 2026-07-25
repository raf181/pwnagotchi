// Package example is the native Go port of
// pwnagotchi/plugins/default/example.py: a template plugin demonstrating
// every hook a native Go plugin can implement. It is not meant to be
// enabled on a real unit (its on_loaded warns exactly like the original
// Python file did) — copy this package as the starting point for a new
// bundled plugin, or read docs/plugin-development.md for the
// full walkthrough.
//
// Original Python author: evilsocket@gmail.com (see example.py's own
// __author__ field, left untouched). This Go port is by raf181.
//
// Where Python needed one on_<event> method per callback (on_bored,
// on_sad, on_excited, ... — over twenty of them, each a separate method),
// this port needs exactly one HandleEvent(event string, args
// []interface{}) method with a switch — see the switch below for every
// event name example.py demonstrated a no-op for. Adding a case doesn't
// require widening any interface.
package example

import (
	"fmt"
	"net/http"

	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// Plugin ports the Example class.
type Plugin struct {
	log  pluginmanager.Logger
	view pluginmanager.ViewCapability
}

// New ports Example.__init__ (self.options = dict()).
func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string { return "example" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "1.0.0",
		Author:      "evilsocket@gmail.com (original), Go port by raf181",
		License:     "GPL3",
		Description: "An example plugin for pwnagotchi that implements all the available callbacks.",
		HasWebhook:  true,
	}
}

// OnLoad ports on_loaded (the real Python file's own warning that this
// plugin should never be enabled on a real unit) plus on_ui_setup (adding
// a UPS-style LabeledValue element at `ui.width() / 2 - 25`) — see
// memtemp/OnLoad's doc comment for why a native plugin's OnLoad already
// covers both timings without a separate "ui_setup" event.
func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.log = caps.Log
	p.view = caps.View
	if p.log != nil {
		p.log.Printf("WARNING: this plugin should be disabled! options = %v", caps.Config)
	}
	if p.view != nil {
		x := p.view.Width()/2 - 25
		p.view.AddLabeledValue("ups", "UPS", "0%/0V", x, 0, pluginmanager.FontBold, pluginmanager.FontMedium, 5)
	}
	return nil
}

// OnUnload ports on_unload: remove the element this plugin added.
func (p *Plugin) OnUnload() error {
	if p.view != nil {
		p.view.RemoveElement("ups")
	}
	return nil
}

// HandleEvent ports every remaining on_<event> callback in example.py —
// see the package doc comment for why this is one switch instead of
// twenty near-identical no-op methods. Extend a case with real logic the
// same way memtemp/cache/switcher do; leave cases you don't care about
// out of the switch entirely (an unhandled event is just ignored, exactly
// like Python's getattr(plugin, f"on_{event}", None) no-op fallback).
func (p *Plugin) HandleEvent(event string, args []interface{}) {
	switch event {
	case "ui_update":
		// called when the ui is updated
		if p.view != nil {
			someVoltage, someCapacity := 0.1, 100.0
			p.view.Set("ups", fmt.Sprintf("%4.2fV/%2d%%", someVoltage, int(someCapacity)))
		}

	case "internet_available":
		// called when there's internet connectivity

	case "ready":
		// called when everything is ready and the main loop is about to
		// start. Real Python's commented-out example: agent.run('ble.recon
		// on') via Capabilities.Agent.Run, or a mood change via
		// Capabilities.View.
		if p.log != nil {
			p.log.Printf("unit is ready")
		}

	case "free_channel",
		"bored", "sad", "excited", "lonely", "rebooting",
		"wait", "sleep",
		"wifi_update", "unfiltered_ap_list",
		"association", "deauthentication", "channel_hop",
		"handshake", "epoch",
		"peer_detected", "peer_lost":
		// Every other event example.py demonstrated a no-op callback for.
		// args carries whatever that event's real payload is — see
		// docs/plugin-development.md's event table for each
		// event's actual argument shape.
	}
}

// OnWebhook ports on_webhook: called for
// http://<host>:<port>/plugins/example/<subpath>. Must return a real HTTP
// response — a real *http.Request, not a reconstructed stub, so
// r.URL.Query()/r.Header/r.Body all work exactly like a built-in route.
// IMPORTANT (ported from the original Python comment): if a plugin's
// webhook handles POSTs, it must still apply the daemon's own CSRF
// protection for any state-changing request — see internal/web/csrf.go's
// checkCSRF, which Capabilities.Web-registered routes get for free but a
// raw OnWebhook implementation must call explicitly if it renders its own
// form.
func (p *Plugin) OnWebhook(subpath string, r *http.Request) (pluginmanager.WebhookResponse, error) {
	return pluginmanager.WebhookResponse{
		Status: http.StatusOK,
		Body:   []byte("example plugin webhook: " + subpath),
	}, nil
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.Unloader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
var _ pluginmanager.WebhookHandler = (*Plugin)(nil)
