package pluginhost

import "github.com/jayofelony/pwnagotchi/internal/pluginmanager"

// MetadataOnlyPlugin folds an already-native, already-routed feature
// (logtail, webcfg — real Go implementations with their own bespoke
// internal/web routes, no bridge dependency, predating the plugin
// manager) into pluginmanager.Manager's bookkeeping, so it shows up
// correctly in Manager.List()/the web /plugins page and respects its own
// config `enabled` flag via the ordinary Register+LoadAll path, without
// changing how its actual HTTP routes are served (internal/web's
// handler_plugins.go already special-cases these two names before
// falling through to the manager/bridge, and continues to do so — this
// type exists purely for accurate listing/toggling, not routing).
type MetadataOnlyPlugin struct {
	PluginName string
	Meta       pluginmanager.Metadata
}

func (p MetadataOnlyPlugin) Name() string                     { return p.PluginName }
func (p MetadataOnlyPlugin) Metadata() pluginmanager.Metadata { return p.Meta }

var _ pluginmanager.Plugin = MetadataOnlyPlugin{}
