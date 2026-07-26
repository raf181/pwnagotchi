// Package cache is the native Go port of
// pwnagotchi/plugins/default/cache.py: caches each seen access point's
// last-known JSON representation to disk (keyed by hostname+mac), so
// other plugins (wigle) can look up AP metadata for a handshake file
// after the AP itself is no longer in range/session.
//
// Original Python author: fmatray (see cache.py's own __author__ field,
// left untouched). This Go port is by raf181.
package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

const (
	cleanInterval = 60 * time.Second     // ports on_ui_update's 60s check
	cacheMaxAge   = 5 * 60 * time.Second // ports clean_ap_cache's 5-minute cutoff
	cacheFileExt  = ".apcache"
)

var hostnameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9]`)
var macFilenamePattern = regexp.MustCompile(`^[A-Fa-f0-9]{12}$`)

// clock is overridable for tests, matching this port's established
// injected-clock pattern (real time.Now in production).
type clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Plugin ports the Cache class.
type Plugin struct {
	mu        sync.Mutex
	log       pluginmanager.Logger
	clock     clock
	ready     bool
	cacheDir  string
	lastClean time.Time
}

// New ports Cache.__init__ (self.options = dict(); self.ready = False).
func New() *Plugin { return &Plugin{clock: realClock{}} }

func (p *Plugin) Name() string { return "cache" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "1.0.0",
		Author:      "fmatray (original), Go port by raf181",
		License:     "GPL3",
		Description: "A simple plugin to cache AP informations",
	}
}

func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	p.log = caps.Log
	p.mu.Unlock()
	return nil
}

// OnUnload ports on_unload: a final cache cleanup pass.
func (p *Plugin) OnUnload() error {
	p.cleanAPCache()
	return nil
}

// HandleEvent ports the real event dispatch: config_changed, wifi_update,
// unfiltered_ap_list, association, deauthentication, handshake, ui_update.
func (p *Plugin) HandleEvent(event string, args []interface{}) {
	switch event {
	case "config_changed":
		p.onConfigChanged(args)
	case "wifi_update":
		if len(args) < 2 {
			return
		}
		p.cacheAll(asAPSlice(args[1]))
	case "unfiltered_ap_list":
		if len(args) < 2 {
			return
		}
		p.cacheAll(asAPSlice(args[1]))
	case "association":
		if len(args) < 2 {
			return
		}
		if ap, ok := args[1].(map[string]interface{}); ok {
			p.writeAPCache(ap)
		}
	case "deauthentication":
		if len(args) < 2 {
			return
		}
		if ap, ok := args[1].(map[string]interface{}); ok {
			p.writeAPCache(ap)
		}
	case "handshake":
		// args: (agent, filename, access_point, client_station) — Python's
		// own agent.py emits access_point as a plain BSSID string instead
		// of a dict when the AP wasn't found in the current session (see
		// agent.py's `plugins.on('handshake', self, filename, ap_mac,
		// sta_mac)` branch): real Python's write_ap_cache would itself
		// raise/log a TypeError indexing a string with "mac" in that case.
		// Skipping cleanly here (rather than reproducing a crash-then-log)
		// is a deliberate, harmless difference — not a security-relevant
		// behavior change, and every caller only ever depended on the
		// happy-path (AP found) writing a real cache entry.
		if len(args) < 3 {
			return
		}
		if ap, ok := args[2].(map[string]interface{}); ok {
			p.writeAPCache(ap)
		}
	case "ui_update":
		p.mu.Lock()
		ready := p.ready
		last := p.lastClean
		now := p.clock.Now()
		due := ready && now.Sub(last) > cleanInterval
		if due {
			p.lastClean = now
		}
		p.mu.Unlock()
		if due {
			p.cleanAPCache()
		}
	}
}

// onConfigChanged ports on_config_changed: derive the cache directory
// from the FULL daemon config's bettercap.handshakes path (not this
// plugin's own options), then run an initial cleanup pass — matching
// real Python's fullCfg-scoped on_config_changed(config) exactly (see
// pluginmanager.Manager.Load's doc comment for why "config_changed"
// carries the whole config, not Capabilities.Config).
func (p *Plugin) onConfigChanged(args []interface{}) {
	if len(args) < 1 {
		return
	}
	fullCfg, ok := args[0].(config.Map)
	if !ok {
		return
	}
	bettercapCfg, _ := fullCfg["bettercap"].(config.Map)
	handshakesDir, _ := bettercapCfg["handshakes"].(string)
	if handshakesDir == "" {
		if p.log != nil {
			p.log.Printf("cannot determine the cache directory: bettercap.handshakes is not set")
		}
		return
	}
	cacheDir := filepath.Join(handshakesDir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		if p.log != nil {
			p.log.Printf("cannot access the cache directory: %v", err)
		}
		return
	}

	p.mu.Lock()
	p.cacheDir = cacheDir
	p.lastClean = p.clock.Now()
	p.ready = true
	p.mu.Unlock()

	p.cleanAPCache()
}

func (p *Plugin) cacheAll(aps []map[string]interface{}) {
	p.mu.Lock()
	ready := p.ready
	p.mu.Unlock()
	if !ready {
		return
	}
	for _, ap := range aps {
		hostname, _ := ap["hostname"].(string)
		if hostname == "" || hostname == "<hidden>" {
			continue
		}
		p.writeAPCache(ap)
	}
}

// writeAPCache ports Cache.write_ap_cache.
func (p *Plugin) writeAPCache(ap map[string]interface{}) {
	p.mu.Lock()
	ready := p.ready
	cacheDir := p.cacheDir
	p.mu.Unlock()
	if !ready {
		return
	}

	mac, _ := ap["mac"].(string)
	hostname, _ := ap["hostname"].(string)
	if mac == "" || hostname == "" {
		return
	}
	mac = strings.ReplaceAll(mac, ":", "")
	if !macFilenamePattern.MatchString(mac) {
		return
	}
	hostname = hostnameSanitizer.ReplaceAllString(hostname, "")

	data, err := json.Marshal(ap)
	if err != nil {
		return
	}
	cacheFile := filepath.Join(cacheDir, hostname+"_"+mac+cacheFileExt)
	if err := os.WriteFile(cacheFile, data, 0o644); err != nil {
		if p.log != nil {
			p.log.Printf("cannot write %s: %v", cacheFile, err)
		}
	}
}

// cleanAPCache ports Cache.clean_ap_cache: delete any *.apcache file
// older than cacheMaxAge.
func (p *Plugin) cleanAPCache() {
	p.mu.Lock()
	ready := p.ready
	cacheDir := p.cacheDir
	now := p.clock.Now()
	p.mu.Unlock()
	if !ready {
		return
	}

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), cacheFileExt) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > cacheMaxAge {
			_ = os.Remove(filepath.Join(cacheDir, entry.Name()))
		}
	}
}

// ReadAPCache ports the module-level read_ap_cache function: derives the
// same cache filename write_ap_cache would have used for a given
// pcap/gps.json/geo.json path and reads it back, for other plugins
// (wigle) that need a previously-cached AP's metadata.
func ReadAPCache(cacheDir, file string) (map[string]interface{}, bool) {
	base := filepath.Base(file)
	base = cacheSuffixPattern.ReplaceAllString(base, cacheFileExt)
	path := filepath.Join(cacheDir, base)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var ap map[string]interface{}
	if err := json.Unmarshal(data, &ap); err != nil {
		return nil, false
	}
	return ap, true
}

var cacheSuffixPattern = regexp.MustCompile(`\.(pcap|gps\.json|geo\.json)$`)

func asAPSlice(v interface{}) []map[string]interface{} {
	switch t := v.(type) {
	case []map[string]interface{}:
		return t
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(t))
		for _, raw := range t {
			if m, ok := raw.(map[string]interface{}); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.Unloader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
