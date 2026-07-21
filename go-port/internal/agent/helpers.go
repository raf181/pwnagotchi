// Package agent ports pwnagotchi/agent.py: the core orchestrator composing
// the bettercap client, the mood automata, and the mesh advertiser into the
// full recon/associate/deauth control loop.
package agent

import (
	"strings"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
)

// AP and Station are the generic bettercap JSON record shapes agent.py
// works with directly as dicts (`ap['mac']`, `ap['clients']`, ...). No
// internal/bettercap package yet defines typed structs for these (its own
// Session()/Run() already return `interface{}` generically), so the Go
// port matches that same genericity here rather than inventing typed
// structs bettercap's real JSON shape would have to be kept in lockstep
// with by hand.
type AP = map[string]interface{}
type Station = map[string]interface{}

func asMap(v interface{}) map[string]interface{} {
	m, _ := v.(map[string]interface{})
	return m
}

func asSlice(v interface{}) []interface{} {
	s, _ := v.([]interface{})
	return s
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getFloat(m map[string]interface{}, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	}
	return 0
}

func getInt(m map[string]interface{}, key string) int {
	return int(getFloat(m, key))
}

func clients(ap AP) []Station {
	raw := asSlice(ap["clients"])
	out := make([]Station, 0, len(raw))
	for _, c := range raw {
		if m := asMap(c); m != nil {
			out = append(out, m)
		}
	}
	return out
}

func containsFold(list []string, s string) bool {
	s = strings.ToLower(s)
	for _, item := range list {
		if strings.ToLower(item) == s {
			return true
		}
	}
	return false
}

func mainMap(cfg config.Map) config.Map {
	m, _ := cfg["main"].(config.Map)
	return m
}

func personalityMap(cfg config.Map) config.Map {
	m, _ := cfg["personality"].(config.Map)
	return m
}

func bettercapMap(cfg config.Map) config.Map {
	m, _ := cfg["bettercap"].(config.Map)
	return m
}

func stringOr(m config.Map, key, def string) string {
	if m == nil {
		return def
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return def
}

func intOr(m config.Map, key string, def int) int {
	if m == nil {
		return def
	}
	switch v := m[key].(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

func boolAt(m config.Map, key string) (bool, bool) {
	if m == nil {
		return false, false
	}
	b, ok := m[key].(bool)
	return b, ok
}

func digSlice(cfg config.Map, keys ...string) ([]interface{}, bool) {
	var cur interface{} = cfg
	for _, k := range keys {
		asMap, ok := cur.(config.Map)
		if !ok {
			return nil, false
		}
		cur, ok = asMap[k]
		if !ok {
			return nil, false
		}
	}
	s, ok := cur.([]interface{})
	return s, ok
}

func digNestedString(cfg config.Map, keys ...string) string {
	var cur interface{} = cfg
	for _, k := range keys {
		asMap, ok := cur.(config.Map)
		if !ok {
			return ""
		}
		cur, ok = asMap[k]
		if !ok {
			return ""
		}
	}
	s, _ := cur.(string)
	return s
}

func digFloat(cfg config.Map, keys ...string) (float64, bool) {
	return config.DigFloat(cfg, keys...)
}
