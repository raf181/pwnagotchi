// Package webgpsmap is the native Go port of
// pwnagotchi/plugins/default/webgpsmap.py: shows an OpenStreetMap page
// with the position of every captured handshake that has a .gps.json or
// .geo.json sidecar file, plus a "download as a single offline HTML
// file" export.
//
// Original Python authors: https://github.com/xenDE and
// https://github.com/dadav (see webgpsmap.py's own __author__ field,
// left untouched). This Go port is by raf181.
//
// The map page itself (webgpsmap.html, embedded verbatim below) needed
// NO templating changes: the real file has no Jinja2 syntax at all
// ({{ }}/{% %}) — it's a static page whose only "templating" is the
// offlinemap route doing a literal string replace of
// `var offlinePositions = null;`, which this port reproduces exactly
// the same way (see handleOfflineMap).
package webgpsmap

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

//go:embed webgpsmap.html
var mapHTML []byte

const notReadyHTML = `<html>
                    <head>
                    <meta charset="utf-8"/>
                    <style>body{font-size:1000%;}</style>
                    </head>
                    <body>Not ready yet</body>
                    </html>`

// Plugin ports the Webgpsmap class.
type Plugin struct {
	mu            sync.Mutex
	ready         bool
	handshakesDir string
}

func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string { return "webgpsmap" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "1.4.0",
		Author:      "https://github.com/xenDE and https://github.com/dadav (original), Go port by raf181",
		License:     "GPL3",
		Description: "a plugin for pwnagotchi that shows a openstreetmap with positions of ap-handshakes in your webbrowser",
		HasWebhook:  true,
	}
}

// OnLoad ports on_loaded (a log line only — real readiness comes from
// on_config_changed, ported in HandleEvent below).
func (p *Plugin) OnLoad(pluginmanager.Capabilities) error { return nil }

// HandleEvent ports on_config_changed: self.config = config; self.ready = True.
func (p *Plugin) HandleEvent(event string, args []interface{}) {
	if event != "config_changed" || len(args) < 1 {
		return
	}
	fullCfg, ok := args[0].(config.Map)
	if !ok {
		return
	}
	bettercapCfg, _ := fullCfg["bettercap"].(config.Map)
	handshakesDir, _ := bettercapCfg["handshakes"].(string)

	p.mu.Lock()
	p.handshakesDir = handshakesDir
	p.ready = handshakesDir != ""
	p.mu.Unlock()
}

// RegisterRoutes ports on_webhook's GET dispatch: "/" (or empty) serves
// the map page, "all" serves the JSON position data, "offlinemap" serves
// a single downloadable HTML file with the data baked in.
func (p *Plugin) RegisterRoutes(web pluginmanager.WebCapability) {
	web.Handle("", p.handleIndex)
	web.Handle("all", p.handleAll)
	web.Handle("offlinemap", p.handleOfflineMap)
}

// OnWebhook ports on_webhook(path, request) directly, dispatching to the
// exact same handleIndex/handleAll/handleOfflineMap logic RegisterRoutes
// wires up.
//
// Why both interfaces exist: pluginmanager.RouteRegistrar/WebCapability
// is the architecturally-intended integration (matches Python's
// multi-route on_webhook shape one-to-one), but as of this port,
// cmd/pwnagotchi/main.go never sets Capabilities.Web to anything
// non-nil, so a RouteRegistrar-only plugin would compile clean and pass
// tests yet be UNREACHABLE from a real running daemon — internal/web's
// pluginWebhookNative only ever calls a plugin's OnWebhook
// (pluginmanager.WebhookHandler), never RouteRegistrar. Implementing
// OnWebhook too (routing internally on subpath, exactly like the real
// Python single-dispatch-point plugin already does) makes this plugin
// actually functional today via the currently-wired path, while
// RegisterRoutes stays ready to go the moment Capabilities.Web is wired
// up for real.
func (p *Plugin) OnWebhook(subpath string, r *http.Request) (pluginmanager.WebhookResponse, error) {
	rr := httptest.NewRecorder()
	switch {
	case subpath == "" || subpath == "/":
		p.handleIndex(rr, r)
	case strings.HasPrefix(subpath, "all"):
		p.handleAll(rr, r)
	case strings.HasPrefix(subpath, "offlinemap"):
		p.handleOfflineMap(rr, r)
	default:
		return pluginmanager.WebhookResponse{Status: http.StatusNotFound, Body: []byte(notFoundHTML)}, nil
	}

	res := rr.Result()
	body := rr.Body.Bytes()
	headers := map[string]string{}
	for k := range res.Header {
		headers[k] = res.Header.Get(k)
	}
	return pluginmanager.WebhookResponse{Status: res.StatusCode, Headers: headers, Body: body}, nil
}

const notFoundHTML = `<html>
                    <head>
                    <meta charset="utf-8"/>
                    <style>body{font-size:1000%;}</style>
                    </head>
                    <body>4😋4</body>
                    </html>`

func (p *Plugin) isReady() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ready
}

func (p *Plugin) writeNotReady(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte(notReadyHTML))
}

func (p *Plugin) handleIndex(w http.ResponseWriter, r *http.Request) {
	if !p.isReady() {
		p.writeNotReady(w)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	w.Write(mapHTML)
}

func (p *Plugin) handleAll(w http.ResponseWriter, r *http.Request) {
	if !p.isReady() {
		p.writeNotReady(w)
		return
	}
	p.mu.Lock()
	dir := p.handshakesDir
	p.mu.Unlock()
	data, err := loadGPSFromDir(dir)
	if err != nil {
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(data)
}

func (p *Plugin) handleOfflineMap(w http.ResponseWriter, r *http.Request) {
	if !p.isReady() {
		p.writeNotReady(w)
		return
	}
	p.mu.Lock()
	dir := p.handshakesDir
	p.mu.Unlock()
	data, err := loadGPSFromDir(dir)
	if err != nil {
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	htmlData := strings.Replace(string(mapHTML), "var offlinePositions = null;", "var offlinePositions = "+string(jsonData)+";", 1)
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Content-Disposition", "attachment; filename=webgpsmap.html")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(htmlData))
}

// position ports the per-AP dict load_gps_from_dir builds.
type position struct {
	SSID     string   `json:"ssid"`
	MAC      string   `json:"mac"`
	Type     string   `json:"type"`
	Lng      float64  `json:"lng"`
	Lat      float64  `json:"lat"`
	Accuracy *float64 `json:"acc"`
	TSFirst  int64    `json:"ts_first"`
	TSLast   int64    `json:"ts_last"`
	Password *string  `json:"pass,omitempty"`
}

var (
	macFromFilename  = regexp.MustCompile(`.*_?([a-zA-Z0-9]{12})\.(?:gps|geo)\.json$`)
	ssidFromFilename = regexp.MustCompile(`(.+)_[a-zA-Z0-9]{12}\.(?:gps|geo)\.json$`)
)

// loadGPSFromDir ports load_gps_from_dir: scan handshakeDir for *.pcap
// files, find each one's .gps.json/.geo.json sidecar (preferring
// .geo.json if both exist — matches Python's sequential if-not-elif
// checks, where the .geo.json check runs after and overwrites the
// .gps.json result), parse it, and key the result map by "ssid_mac".
func loadGPSFromDir(handshakeDir string) (map[string]position, error) {
	result := map[string]position{}
	if handshakeDir == "" {
		return result, nil
	}
	entries, err := os.ReadDir(handshakeDir)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pcap") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".pcap")
		var posFile string
		if names[base+".gps.json"] {
			posFile = filepath.Join(handshakeDir, base+".gps.json")
		}
		if names[base+".geo.json"] {
			posFile = filepath.Join(handshakeDir, base+".geo.json")
		}
		if posFile == "" {
			continue
		}

		pos, mac, ssid, ok := parsePositionFile(posFile)
		if !ok {
			continue
		}
		key := ssid + "_" + mac
		crackedName := firstDotSegment(filepath.Base(posFile)) + ".pcap.cracked"
		if names[crackedName] {
			if pw, err := os.ReadFile(filepath.Join(handshakeDir, crackedName)); err == nil {
				s := string(pw)
				pos.Password = &s
			}
		}
		result[key] = pos
	}
	return result, nil
}

// firstDotSegment returns the portion of name before its first '.' —
// used to recover the pcap base name from a "<base>.gps.json"/
// "<base>.geo.json" filename regardless of dots inside <base> itself.
func firstDotSegment(name string) string {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[:i]
	}
	return name
}

// parsePositionFile ports the PositionFile class: parses one
// .gps.json/.geo.json file into a position, returning ok=false if it
// should be skipped (matches load_gps_from_dir's JSONDecodeError/
// ValueError/OSError -> "continue" handling).
func parsePositionFile(path string) (pos position, mac, ssid string, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return position{}, "", "", false
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return position{}, "", "", false
	}

	base := filepath.Base(path)
	isGPS := strings.HasSuffix(path, ".gps.json")
	isGEO := strings.HasSuffix(path, ".geo.json")
	if !isGPS && !isGEO {
		return position{}, "", "", false
	}

	m := macFromFilename.FindStringSubmatch(base)
	if m == nil {
		return position{}, "", "", false // "Mac can't be parsed from filename"
	}
	mac = m[1]
	if sm := ssidFromFilename.FindStringSubmatch(base); sm != nil {
		ssid = sm[1]
	}
	if ssid == "" {
		ssid = "unknown"
	}

	lat, latOK := extractLatLng(raw, "Latitude", "lat")
	lng, lngOK := extractLatLng(raw, "Longitude", "long")
	if !latOK || !lngOK {
		return position{}, "", "", false
	}

	posType := "geo"
	var accuracy *float64
	if isGPS {
		posType = "gps"
		v := 50.0
		accuracy = &v
	} else if acc, ok := raw["accuracy"].(float64); ok {
		accuracy = &acc
	}

	info, err := os.Stat(path)
	var ctime, mtime time.Time
	if err == nil {
		mtime = info.ModTime()
		ctime = mtime // Go's os.FileInfo has no portable creation time; mtime is the closest available and only ever used as a fallback anyway (see timestampLast).
	}

	tsLast := mtime.Unix()
	if ts, ok := raw["ts"]; ok {
		if f, ok := toFloat(ts); ok {
			tsLast = int64(f)
		}
	} else if updated, ok := raw["Updated"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, updated); err == nil {
			tsLast = t.Unix()
		}
	}

	pos = position{
		SSID:     ssid,
		MAC:      mac,
		Type:     posType,
		Lat:      lat,
		Lng:      lng,
		Accuracy: accuracy,
		TSFirst:  ctime.Unix(),
		TSLast:   tsLast,
	}
	return pos, mac, ssid, true
}

// extractLatLng ports lat()/lng()'s precedence: a bare top-level field
// ("Latitude"/"lat" or "Longitude"/"long") is checked first, then
// OVERWRITTEN by `location.<lat|lng>` if present — Python's sequential
// (not elif) `if` statements mean later checks win, so precedence here
// is location > the bare field, not "first found wins". A missing or
// exactly-zero value is treated as invalid (matches Python's explicit
// "avoid 0.000... measurements" rejection).
func extractLatLng(raw map[string]interface{}, primaryKey, altKey string) (float64, bool) {
	var v float64
	found := false
	if f, ok := toFloat(raw[primaryKey]); ok {
		v, found = f, true
	}
	if f, ok := toFloat(raw[altKey]); ok {
		v, found = f, true
	}
	if loc, ok := raw["location"].(map[string]interface{}); ok {
		locKey := "lat"
		if altKey == "long" {
			locKey = "lng"
		}
		if f, ok := toFloat(loc[locKey]); ok {
			v, found = f, true
		}
	}
	if !found || v == 0 {
		return 0, false
	}
	return v, true
}

func toFloat(v interface{}) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case json.Number:
		f, err := strconv.ParseFloat(t.String(), 64)
		return f, err == nil
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	}
	return 0, false
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
var _ pluginmanager.RouteRegistrar = (*Plugin)(nil)
var _ pluginmanager.WebhookHandler = (*Plugin)(nil)
