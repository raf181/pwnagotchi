// Package wigle is the native Go port of
// pwnagotchi/plugins/default/wigle.py: uploads captured Wi-Fi networks
// (from cached AP metadata or, failing that, directly parsed PCAP files)
// paired with their saved GPS coordinates to wigle.net, and shows a
// cycling account-statistics line on screen.
//
// Original Python authors: Dadav, updated by Jayofelony and fmatray (see
// wigle.py's own __author__ field, left untouched). This Go port is by
// raf181.
//
// KNOWN CROSS-PLUGIN GAP (not fixed here, flagged for the parent
// conversation): real Python's wigle.py expects every *.gps.json file to
// contain "Accuracy" and "Updated" keys in addition to Latitude/
// Longitude/Altitude — these come from bettercap's real GPS session
// object, which apparently reports more fields than just the three
// coordinates. This port's OWN internal/plugins/native/gps plugin
// currently only saves Latitude/Longitude/Altitude to *.gps.json (see
// gps.go's onHandshake), not Accuracy/Updated. Until that's widened,
// this plugin defaults a missing Accuracy to 0 and a missing/unparseable
// Updated to the file's own modification time (a reasonable, disclosed
// fallback) rather than crashing or silently fabricating a "success"
// upload with wrong data — real Python has no such fallback and would
// raise an uncaught KeyError in this situation instead, which this port
// deliberately does not reproduce (not a security-relevant behavior,
// just a crash Python would hit that Go can trivially avoid).
package wigle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/cache"
	"github.com/jayofelony/pwnagotchi/internal/version"
	"github.com/jayofelony/pwnagotchi/internal/wifiparse"
)

const (
	defaultTimeout   = 30 * time.Second
	statsInterval    = 30 * time.Second
	userStatsURL     = "https://api.wigle.net/api/v2/stats/user"
	groupStatsURL    = "https://api.wigle.net/api/v2/stats/group"
	usergroupURLBase = "https://api.wigle.net/api/v2/group/groupForUser/"
	pluginVersionStr = "4.1.0"
)

// uploadURLVar is overridable for tests (real wigle.net endpoint in
// production), matching this port's established package-level-var
// override pattern (e.g. internal/wpasec.DBPath).
var uploadURLVar = "https://api.wigle.net/api/v2/file/upload"

// GPSData mirrors the real *.gps.json / *.geo.json extracted shape.
type GPSData struct {
	Latitude, Longitude, Altitude, Accuracy float64
	Updated                                 time.Time
}

func (g GPSData) valid() bool { return g.Latitude != 0 || g.Longitude != 0 }

// PCAPData mirrors the fields extracted from a handshake, either via the
// cache (internal/plugins/native/cache.ReadAPCache) or, failing that,
// internal/wifiparse.ExtractFromPCAP directly.
type PCAPData struct {
	BSSID, ESSID string
	Encryption   []string
	Channel      int
	Frequency    int
	RSSI         int
}

// clock is overridable for tests.
type clock interface{ Now() time.Time }
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// statistics mirrors WigleStatistics.
type statistics struct {
	ready                                      bool
	username                                   string
	rank, monthRank, discoveredWiFi, groupRank int
	last, groupID, groupName                   string
}

// Plugin ports the Wigle class.
type Plugin struct {
	mu sync.Mutex

	apiKey        string
	donate        bool
	handshakesDir string
	cacheDir      string
	csvDir        string
	whitelist     []string
	timeout       time.Duration
	posX, posY    int
	ready         bool

	skip       map[string]bool
	reportPath string

	stats    statistics
	lastStat time.Time
	uiCount  int

	httpClient *http.Client
	view       pluginmanager.ViewCapability
	log        pluginmanager.Logger
	clock      clock
}

func New() *Plugin {
	return &Plugin{skip: map[string]bool{}, clock: realClock{}, timeout: defaultTimeout, posX: 10, posY: 10}
}

func (p *Plugin) Name() string { return "wigle" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     pluginVersionStr,
		Author:      "Dadav and updated by Jayofelony and fmatray (original), Go port by raf181",
		License:     "GPL3",
		Description: "This plugin automatically uploads collected WiFi to wigle.net",
		HasWebhook:  true,
	}
}

func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	p.view = caps.View
	p.log = caps.Log
	if caps.HTTPClient != nil {
		p.httpClient = caps.HTTPClient
	} else {
		p.httpClient = http.DefaultClient
	}
	if caps.Clock != nil {
		p.clock = caps.Clock
	}
	posX, posY := p.posX, p.posY
	view := p.view
	p.mu.Unlock()

	if view != nil {
		view.AddText("wigle", "-", posX, posY, pluginmanager.FontSmall, false, 0)
	}
	return nil
}

func (p *Plugin) OnUnload() error {
	p.mu.Lock()
	view := p.view
	p.mu.Unlock()
	if view != nil {
		view.RemoveElement("wigle")
	}
	return nil
}

func (p *Plugin) HandleEvent(event string, args []interface{}) {
	switch event {
	case "config_changed":
		if len(args) < 1 {
			return
		}
		if fullCfg, ok := args[0].(config.Map); ok {
			p.onConfigChanged(fullCfg)
		}
	case "internet_available":
		p.onInternetAvailable()
	case "ui_update":
		p.onUIUpdate()
	}
}

func (p *Plugin) onConfigChanged(fullCfg config.Map) {
	mainCfg, _ := fullCfg["main"].(config.Map)
	pluginsCfg, _ := mainCfg["plugins"].(config.Map)
	opts, _ := pluginsCfg["wigle"].(config.Map)
	bettercapCfg, _ := fullCfg["bettercap"].(config.Map)

	apiKey, _ := opts["api_key"].(string)
	if apiKey == "" {
		p.logf("api_key must be set.")
		return
	}
	donate, _ := opts["donate"].(bool)
	handshakesDir, _ := bettercapCfg["handshakes"].(string)
	csvDir, _ := opts["cvs_dir"].(string)
	timeout := defaultTimeout
	if t, ok := numberToInt(opts["timeout"]); ok && t > 0 {
		timeout = time.Duration(t) * time.Second
	}
	posX, posY := 10, 10
	if pos, ok := opts["position"].([]interface{}); ok && len(pos) == 2 {
		if x, ok := numberToInt(pos[0]); ok {
			posX = x
		}
		if y, ok := numberToInt(pos[1]); ok {
			posY = y
		}
	}
	var whitelist []string
	if raw, ok := mainCfg["whitelist"].([]interface{}); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				whitelist = append(whitelist, s)
			}
		}
	}

	p.mu.Lock()
	p.apiKey = apiKey
	p.donate = donate
	p.handshakesDir = handshakesDir
	p.cacheDir = filepath.Join(handshakesDir, "cache")
	p.csvDir = csvDir
	p.whitelist = whitelist
	p.timeout = timeout
	p.posX, p.posY = posX, posY
	p.reportPath = filepath.Join(handshakesDir, ".wigle_uploads")
	p.ready = true
	p.mu.Unlock()

	p.logf("Ready for wardriving!!!")
	p.getStatistics(true)
}

func numberToInt(v interface{}) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	}
	return 0, false
}

func (p *Plugin) logf(format string, args ...interface{}) {
	p.mu.Lock()
	log := p.log
	p.mu.Unlock()
	if log != nil {
		log.Printf(format, args...)
	}
}

// OnWebhook ports on_webhook: a plain redirect to wigle.net.
func (p *Plugin) OnWebhook(_ string, _ *http.Request) (pluginmanager.WebhookResponse, error) {
	return pluginmanager.WebhookResponse{
		Status:  http.StatusFound,
		Headers: map[string]string{"Location": "https://www.wigle.net/"},
	}, nil
}

// loadReported/saveReported mirror StatusFile persistence of {"reported": [...]}.
type reportedFile struct {
	Reported []string `json:"reported"`
}

func (p *Plugin) loadReported() []string {
	p.mu.Lock()
	path := p.reportPath
	p.mu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f reportedFile
	if json.Unmarshal(data, &f) != nil {
		return nil
	}
	return f.Reported
}

func (p *Plugin) saveReported(reported []string) {
	p.mu.Lock()
	path := p.reportPath
	p.mu.Unlock()
	data, err := json.Marshal(reportedFile{Reported: reported})
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, data, 0o644)
}

// getNewGPSFiles ports get_new_gps_files.
func (p *Plugin) getNewGPSFiles(reported []string) []string {
	p.mu.Lock()
	dir := p.handshakesDir
	whitelist := p.whitelist
	p.mu.Unlock()
	if dir == "" {
		return nil
	}

	gpsGlob, _ := filepath.Glob(filepath.Join(dir, "*.gps.json"))
	geoGlob, _ := filepath.Glob(filepath.Join(dir, "*.geo.json"))
	all := append(gpsGlob, geoGlob...)
	all = config.RemoveWhitelisted(all, whitelist, true)

	reportedSet := map[string]bool{}
	for _, r := range reported {
		reportedSet[r] = true
	}
	p.mu.Lock()
	skip := p.skip
	p.mu.Unlock()

	var out []string
	for _, f := range all {
		if reportedSet[f] || skip[f] {
			continue
		}
		out = append(out, f)
	}
	return out
}

var gpsSuffix = regexp.MustCompile(`\.(geo|gps)\.json$`)

// getPcapFilename ports get_pcap_filename.
func getPcapFilename(gpsFile string) (string, bool) {
	pcapPath := gpsSuffix.ReplaceAllString(gpsFile, ".pcap")
	if _, err := os.Stat(pcapPath); err != nil {
		return "", false
	}
	return pcapPath, true
}

// extractGPSData ports extract_gps_data + get_gps_data (folded together:
// this port has no separate "extraction failed" vs "not enough data"
// distinction beyond the two bool returns real Python's two-function
// split gave).
func extractGPSData(path string) (GPSData, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return GPSData{}, false
	}

	var g GPSData
	if strings.HasSuffix(path, ".geo.json") {
		var geo struct {
			Location struct{ Lat, Lng float64 } `json:"location"`
			Accuracy float64                    `json:"accuracy"`
			TS       int64                      `json:"ts"`
		}
		if json.Unmarshal(data, &geo) != nil {
			return GPSData{}, false
		}
		g = GPSData{Latitude: geo.Location.Lat, Longitude: geo.Location.Lng, Altitude: 10, Accuracy: geo.Accuracy, Updated: time.Unix(geo.TS, 0).UTC()}
	} else {
		var raw map[string]interface{}
		if json.Unmarshal(data, &raw) != nil {
			return GPSData{}, false
		}
		g.Latitude, _ = toFloat(raw["Latitude"])
		g.Longitude, _ = toFloat(raw["Longitude"])
		g.Altitude, _ = toFloat(raw["Altitude"])
		g.Accuracy, _ = toFloat(raw["Accuracy"]) // may be absent — see package doc gap note
		if updated, ok := raw["Updated"].(string); ok {
			g.Updated = parseUpdatedTimestamp(updated)
		} else if info, err := os.Stat(path); err == nil {
			g.Updated = info.ModTime().UTC() // documented fallback, see package doc comment
		}
	}
	if !g.valid() {
		return GPSData{}, false
	}
	return g, true
}

func parseUpdatedTimestamp(s string) time.Time {
	s = strings.SplitN(s, ".", 2)[0]
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func toFloat(v interface{}) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int64:
		return float64(t), true
	case int:
		return float64(t), true
	}
	return 0, false
}

// getPcapData ports get_pcap_data: cache first, then a real PCAP parse.
func (p *Plugin) getPcapData(pcapFilename string) (PCAPData, bool) {
	p.mu.Lock()
	cacheDir := p.cacheDir
	p.mu.Unlock()

	if ap, ok := cache.ReadAPCache(cacheDir, pcapFilename); ok {
		mac, _ := ap["mac"].(string)
		hostname, _ := ap["hostname"].(string)
		if mac != "" {
			channel, _ := numberToInt(ap["channel"])
			freq, _ := numberToInt(ap["frequency"])
			rssi, _ := numberToInt(ap["rssi"])
			var enc []string
			if s, ok := ap["encryption"].(string); ok && s != "" {
				enc = []string{s}
			} else if arr, ok := ap["encryption"].([]interface{}); ok {
				for _, e := range arr {
					if s, ok := e.(string); ok {
						enc = append(enc, s)
					}
				}
			}
			p.logf("Using cache for %s", pcapFilename)
			return PCAPData{BSSID: mac, ESSID: hostname, Encryption: enc, Channel: channel, Frequency: freq, RSSI: rssi}, true
		}
	}

	results, err := wifiparse.ExtractFromPCAP(pcapFilename, []wifiparse.Field{
		wifiparse.FieldBSSID, wifiparse.FieldESSID, wifiparse.FieldEncryption,
		wifiparse.FieldChannel, wifiparse.FieldFrequency, wifiparse.FieldRSSI,
	})
	if err != nil {
		p.logf("Cannot extract all data: %s (skipped)", pcapFilename)
		return PCAPData{}, false
	}
	return PCAPData{
		BSSID:      results[wifiparse.FieldBSSID].String,
		ESSID:      results[wifiparse.FieldESSID].String,
		Encryption: results[wifiparse.FieldEncryption].Crypto,
		Channel:    results[wifiparse.FieldChannel].Int,
		Frequency:  results[wifiparse.FieldFrequency].Int,
		RSSI:       results[wifiparse.FieldRSSI].Int,
	}, true
}

type csvEntry struct {
	gps  GPSData
	pcap PCAPData
}

// generateCSV ports generate_csv: the real WigleWifi-1.6 bulk-upload
// format.
func (p *Plugin) generateCSV(entries []csvEntry) (string, []byte) {
	filename := fmt.Sprintf("%s_%s.csv", unitName(), p.clock.Now().Format("20060102_150405"))

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "WigleWifi-1.6,appRelease=%s,model=pwnagotchi,release=%s,device=%s,display=kismet,board=RaspberryPi,brand=pwnagotchi,star=Sol,body=3,subBody=0\n",
		pluginVersionStr, version.Version, unitName())
	buf.WriteString("MAC,SSID,AuthMode,FirstSeen,Channel,Frequency,RSSI,CurrentLatitude,CurrentLongitude,AltitudeMeters,AccuracyMeters,RCOIs,MfgrId,Type\n")

	for _, e := range entries {
		auth := "[" + strings.Join(e.pcap.Encryption, "][") + "]"
		fmt.Fprintf(&buf, "%s,%s,%s,%s,%d,%d,%d,%v,%v,%v,%v,,,WIFI\n",
			e.pcap.BSSID, csvEscape(e.pcap.ESSID), auth, e.gps.Updated.Format("2006-01-02 15:04:05"),
			e.pcap.Channel, e.pcap.Frequency, e.pcap.RSSI,
			e.gps.Latitude, e.gps.Longitude, e.gps.Altitude, e.gps.Accuracy)
	}
	return filename, buf.Bytes()
}

func csvEscape(s string) string {
	if strings.ContainsAny(s, ",\"") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

func unitName() string {
	// Best-effort: real Python calls pwnagotchi.name(); this port has no
	// direct capability for the unit's configured name at this layer, so
	// it falls back to the hostname (a reasonable, disclosed
	// approximation — the CSV filename/header is metadata, not something
	// wigle.net's ingestion depends on for correctness).
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "pwnagotchi"
}

func (p *Plugin) saveToFile(filename string, content []byte) {
	p.mu.Lock()
	dir := p.csvDir
	p.mu.Unlock()
	if dir == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, filename), content, 0o644); err != nil {
		p.logf("Error while writing CSV file (skipping): %v", err)
	}
}

// postWigle ports post_wigle: a real multipart file upload.
func (p *Plugin) postWigle(filename string, content []byte) error {
	p.mu.Lock()
	apiKey, donate, timeout, client := p.apiKey, p.donate, p.timeout, p.httpClient
	p.mu.Unlock()

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	donateVal := "false"
	if donate {
		donateVal = "on"
	}
	_ = w.WriteField("donate", donateVal)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	if _, err := part.Write(content); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURLVar, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Basic "+apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var result struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &result) != nil || !result.Success {
		return fmt.Errorf("upload failed: %s", result.Message)
	}
	return nil
}

// uploadNewHandshakes ports upload_new_handshakes.
func (p *Plugin) uploadNewHandshakes(reported []string, newGPSFiles []string) {
	var entries []csvEntry
	var okFiles []string
	for _, gpsFile := range newGPSFiles {
		pcapFilename, ok := getPcapFilename(gpsFile)
		if !ok {
			p.markSkip(gpsFile)
			continue
		}
		gpsData, ok := extractGPSData(gpsFile)
		if !ok {
			p.markSkip(gpsFile)
			continue
		}
		pcapData, ok := p.getPcapData(pcapFilename)
		if !ok {
			p.markSkip(gpsFile)
			continue
		}
		entries = append(entries, csvEntry{gps: gpsData, pcap: pcapData})
		okFiles = append(okFiles, gpsFile)
	}
	if len(entries) == 0 {
		return
	}

	filename, content := p.generateCSV(entries)
	p.saveToFile(filename, content)

	p.mu.Lock()
	view := p.view
	p.mu.Unlock()
	if view != nil {
		view.OnUploading("wigle.net")
	}
	if err := p.postWigle(filename, content); err != nil {
		p.logf("Exception while uploading: %v", err)
		p.mu.Lock()
		for _, f := range okFiles {
			p.skip[f] = true
		}
		p.mu.Unlock()
	} else {
		reported = append(reported, okFiles...)
		p.saveReported(reported)
		p.logf("Successfully uploaded %d wifis", len(okFiles))
	}
	if view != nil {
		view.OnNormal()
	}
}

func (p *Plugin) markSkip(f string) {
	p.mu.Lock()
	p.skip[f] = true
	p.mu.Unlock()
}

func (p *Plugin) onInternetAvailable() {
	p.mu.Lock()
	ready := p.ready
	p.mu.Unlock()
	if !ready {
		return
	}
	reported := p.loadReported()
	newFiles := p.getNewGPSFiles(reported)
	if len(newFiles) > 0 {
		p.uploadNewHandshakes(reported, newFiles)
	} else {
		p.getStatistics(false)
	}
}

// getStatistics ports get_statistics's 30s-rate-limited real HTTP calls.
func (p *Plugin) getStatistics(force bool) {
	p.mu.Lock()
	now := p.clock.Now()
	due := force || now.Sub(p.lastStat) > statsInterval
	if due {
		p.lastStat = now
	}
	apiKey, timeout, client := p.apiKey, p.timeout, p.httpClient
	p.mu.Unlock()
	if !due || apiKey == "" || client == nil {
		return
	}

	if json := p.requestJSON(userStatsURL, apiKey, timeout); json != nil {
		if ok, _ := json["success"].(bool); ok {
			p.mu.Lock()
			p.stats.ready = true
			p.stats.username, _ = json["user"].(string)
			r, _ := numberToInt(json["rank"])
			p.stats.rank = r
			mr, _ := numberToInt(json["monthRank"])
			p.stats.monthRank = mr
			if stats, ok := json["statistics"].(map[string]interface{}); ok {
				d, _ := numberToInt(stats["discoveredWiFi"])
				p.stats.discoveredWiFi = d
				if last, ok := stats["last"].(string); ok && len(last) >= 8 {
					p.stats.last = fmt.Sprintf("%s/%s/%s", last[6:8], last[4:6], last[0:4])
				}
			}
			p.mu.Unlock()
		}
	}

	p.mu.Lock()
	username, groupID := p.stats.username, p.stats.groupID
	p.mu.Unlock()
	if username != "" && groupID == "" {
		if json := p.requestJSON(usergroupURLBase+username, apiKey, timeout); json != nil {
			p.mu.Lock()
			p.stats.groupID, _ = json["groupId"].(string)
			p.stats.groupName, _ = json["groupName"].(string)
			p.mu.Unlock()
		}
	}

	p.mu.Lock()
	groupID = p.stats.groupID
	p.mu.Unlock()
	if groupID != "" {
		if json := p.requestJSON(groupStatsURL, apiKey, timeout); json != nil {
			if ok, _ := json["success"].(bool); ok {
				if groups, ok := json["groups"].([]interface{}); ok {
					rank := 1
					for _, g := range groups {
						gm, _ := g.(map[string]interface{})
						if id, _ := gm["groupId"].(string); id == groupID {
							p.mu.Lock()
							p.stats.groupRank = rank
							p.mu.Unlock()
						}
						rank++
					}
				}
			}
		}
	}
}

func (p *Plugin) requestJSON(url, apiKey string, timeout time.Duration) map[string]interface{} {
	p.mu.Lock()
	client := p.httpClient
	p.mu.Unlock()
	if client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Basic "+apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return out
}

// onUIUpdate ports on_ui_update's cycling status message.
func (p *Plugin) onUIUpdate() {
	p.mu.Lock()
	view := p.view
	ready := p.ready
	statsReady := p.stats.ready
	p.mu.Unlock()
	if view == nil {
		return
	}
	if !(ready && statsReady) {
		view.Set("wigle", "We Will Wait Wigle")
		return
	}

	p.mu.Lock()
	p.uiCount = (p.uiCount + 1) % 6
	count := p.uiCount
	s := p.stats
	p.mu.Unlock()

	msg := "-"
	switch count {
	case 0:
		msg = "User:" + s.username
	case 1:
		msg = fmt.Sprintf("Rank:%d Month:%d", s.rank, s.monthRank)
	case 2:
		msg = fmt.Sprintf("%d discovered WiFis", s.discoveredWiFi)
	case 3:
		msg = "Last upl.:" + s.last
	case 4:
		msg = "Grp:" + s.groupName
	case 5:
		msg = "Grp rank:" + strconv.Itoa(s.groupRank)
	}
	view.Set("wigle", msg)
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.Unloader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
var _ pluginmanager.WebhookHandler = (*Plugin)(nil)
