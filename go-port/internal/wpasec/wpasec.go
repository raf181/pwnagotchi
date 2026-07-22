// Package wpasec ports pwnagotchi/plugins/default/wpa-sec.py (real
// original author: 33197631+dadav@users.noreply.github.com, editor
// jayofelony — see that file's own __author__/__editor__ fields, both
// left untouched) as a native Go implementation instead of routing it
// through the internal/pyplugin bridge. This Go port is by raf181.
//
// Why native instead of bridged: real wpa-sec.py's on_handshake calls
// `agent.config()` and on_internet_available calls `agent.view()` — real
// method calls on the `agent` argument. Across the bridge, `agent` can
// only ever arrive as a {"__goref__": "Agent"} marker turned into an
// inert _GoProxyStub (see docs/known-differences.md); any attribute
// access on it raises a real, loud NotImplementedError. Verified live
// against the running bridge (not theoretical): every real
// on_handshake/on_internet_available dispatch crashed on its very first
// line, so handshakes never got queued for upload and uploads never
// happened — not a network or credentials problem, a structural one.
// A native Go plugin wired directly into the same EventEmitter chain
// agent/automata/mesh already use gets the REAL *agent.Agent object —
// Config() and View() are already real, public, non-stubbed methods —
// sidestepping the whole class of problem rather than patching one
// instance of it.
package wpasec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/go-port/internal/agent"
	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
)

// Status mirrors the real plugin's Status enum.
type Status int

const (
	StatusToUpload Status = iota
	StatusInvalid
	StatusSuccessful
)

// DBPath is deliberately a DIFFERENT file from the real plugin's sqlite
// database (/etc/pwnagotchi/.wpa_sec_db): a small (path -> status)
// key-value table doesn't justify a new SQL driver dependency (mattn/
// go-sqlite3 needs cgo; modernc.org/sqlite is a large pure-Go port for
// one table), so this uses a JSON-persisted map instead — a real,
// disclosed structural difference (see docs/known-differences.md). Using
// a different filename also avoids two different processes (this native
// plugin and the real wpa-sec.py, which is still separately loaded
// through the bridge for listing/toggling — see
// docs/plugin-compatibility-matrix.md) ever touching the same file
// concurrently.
//
// Overridable (matching internal/unit.HostnamePath's established pattern
// in this codebase) so tests never touch the real host's state file —
// set it to a t.TempDir() path BEFORE calling New(), which captures the
// current value at construction time.
var DBPath = "/etc/pwnagotchi/.wpa_sec_go_db.json"

const defaultUploadTimeout = 30 * time.Second

// Plugin holds the same runtime state real wpa-sec.py's WpaSec instance
// does: readiness, config options, the pending-upload set, and the
// per-run skip-until-reload set for handshakes that failed with a
// network error (real behavior: don't hammer a down/unreachable server
// every single internet_available tick, matching the Python original's
// self.skip_until_reload).
type Plugin struct {
	mu sync.Mutex

	ready            bool
	apiKey           string
	apiURL           string
	downloadResults  bool
	downloadInterval time.Duration
	showPwd          bool
	singleFiles      bool
	whitelist        []string
	handshakesDir    string

	dbPath          string
	records         map[string]Status
	skipUntilReload map[string]bool

	httpClient *http.Client
}

// New ports WpaSec.__init__ + on_loaded: reads config, validates
// api_key/api_url are set (real Python logs an error and leaves the
// plugin permanently not-ready if either is missing — replicated here,
// not "fixed" into always-ready), and loads any persisted pending-upload
// state from a prior run.
func New(cfg config.Map) *Plugin {
	mainCfg, _ := cfg["main"].(config.Map)
	pluginsCfg, _ := mainCfg["plugins"].(config.Map)
	opts, _ := pluginsCfg["wpa-sec"].(config.Map)
	bettercapCfg, _ := cfg["bettercap"].(config.Map)

	p := &Plugin{
		apiKey:           stringField(opts, "api_key", ""),
		apiURL:           stringField(opts, "api_url", ""),
		downloadResults:  boolField(opts, "download_results"),
		downloadInterval: time.Duration(intField(opts, "download_interval", 3600)) * time.Second,
		showPwd:          boolField(opts, "show_pwd"),
		singleFiles:      boolField(opts, "single_files"),
		whitelist:        stringSliceField(mainCfg, "whitelist"),
		handshakesDir:    stringField(bettercapCfg, "handshakes", "/etc/pwnagotchi/handshakes"),
		dbPath:           DBPath,
		records:          map[string]Status{},
		skipUntilReload:  map[string]bool{},
		httpClient:       &http.Client{Timeout: defaultUploadTimeout},
	}

	if p.apiKey == "" {
		log.Print("WPA_SEC: API-KEY isn't set. Can't upload.")
		return p
	}
	if p.apiURL == "" {
		log.Print("WPA_SEC: API-URL isn't set. Can't upload.")
		return p
	}

	if err := p.loadDB(); err != nil {
		// Real Python's sqlite CREATE TABLE IF NOT EXISTS never fails
		// this way in practice; a corrupt/missing JSON file here is the
		// closest Go equivalent to "first run, nothing persisted yet" —
		// log and start with an empty set rather than refusing to load.
		log.Printf("WPA_SEC: no existing upload-state file (%v), starting fresh", err)
	}

	p.ready = true
	log.Print("WPA_SEC: plugin loaded.")
	return p
}

// On implements the agent/automata/mesh EventEmitter interface
// (On(event string, args ...interface{})) directly, so cmd/pwnagotchi's
// main.go can chain this alongside the real Python plugin bridge without
// either one needing to know about the other.
func (p *Plugin) On(event string, args ...interface{}) {
	switch event {
	case "handshake":
		if len(args) < 2 {
			return
		}
		a, _ := args[0].(*agent.Agent)
		filename, _ := args[1].(string)
		if a == nil || filename == "" {
			return
		}
		p.onHandshake(a, filename)
	case "internet_available":
		if len(args) < 1 {
			return
		}
		a, _ := args[0].(*agent.Agent)
		if a == nil {
			return
		}
		p.onInternetAvailable(a)
	}
}

// onHandshake ports on_handshake: whitelist-filter the captured file,
// then mark it TOUPLOAD unless it was already recorded INVALID (real
// Python's upsert: `WHERE handshakes.status = INVALID` in the ON
// CONFLICT clause — an invalid file stays invalid, never gets requeued).
func (p *Plugin) onHandshake(a *agent.Agent, filename string) {
	cfg := a.Config()
	mainCfg, _ := cfg["main"].(config.Map)
	whitelist := stringSliceField(mainCfg, "whitelist")

	if len(config.RemoveWhitelisted([]string{filename}, whitelist, true)) == 0 {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.records[filename]; ok && existing == StatusInvalid {
		return
	}
	p.records[filename] = StatusToUpload
	if err := p.saveDBLocked(); err != nil {
		log.Printf("WPA_SEC: saving upload-state: %v", err)
	}
}

// onInternetAvailable ports on_internet_available: upload every
// TOUPLOAD-status handshake not in skip_until_reload, then (if
// configured) download cracked results.
func (p *Plugin) onInternetAvailable(a *agent.Agent) {
	if !p.ready {
		return
	}

	p.mu.Lock()
	var toUpload []string
	for path, status := range p.records {
		if status == StatusToUpload && !p.skipUntilReload[path] {
			toUpload = append(toUpload, path)
		}
	}
	p.mu.Unlock()

	if len(toUpload) > 0 {
		view := a.View()
		log.Print("WPA_SEC: Internet connectivity detected. Uploading new handshakes...")
		for idx, handshake := range toUpload {
			if view != nil {
				view.OnUploading(fmt.Sprintf("WPA-SEC (%d/%d)", idx+1, len(toUpload)))
			}
			log.Printf("WPA_SEC: Uploading %s...", handshake)

			resp, err := p.uploadToWpaSec(handshake)
			p.mu.Lock()
			switch {
			case err == nil && strings.HasPrefix(resp, "hcxpcapngtool"):
				log.Printf("WPA_SEC: %s successfully uploaded.", handshake)
				p.records[handshake] = StatusSuccessful
				_ = p.saveDBLocked()
			case err == nil:
				log.Printf("WPA_SEC: %s uploaded, but it was invalid.", handshake)
				p.records[handshake] = StatusInvalid
				_ = p.saveDBLocked()
			case isNetworkError(err):
				log.Printf("WPA_SEC: network error uploading %s, skipping until reload: %v", handshake, err)
				p.skipUntilReload[handshake] = true
			default:
				log.Printf("WPA_SEC: OSError-equivalent uploading %s, deleting from db: %v", handshake, err)
				delete(p.records, handshake)
				_ = p.saveDBLocked()
			}
			p.mu.Unlock()
		}
		if view != nil {
			view.OnNormal()
		}
	}

	if p.downloadResults {
		p.downloadResultsIfDue()
	}
}

// uploadToWpaSec ports _upload_to_wpasec: a real multipart POST with the
// api_key as a cookie, the pcap as the "file" field, and the same
// browser-spoofing User-Agent real Python sends.
func (p *Plugin) uploadToWpaSec(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, p.apiURL, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("HTTP_USER_AGENT", "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:15.0) Gecko/20100101 Firefox/15.0.1")
	req.AddCookie(&http.Cookie{Name: "key", Value: p.apiKey})

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", &httpStatusError{status: resp.StatusCode}
	}
	first, _, _ := strings.Cut(string(respBody), "\n")
	return first, nil
}

// httpStatusError mirrors requests.Response.raise_for_status() raising
// requests.exceptions.HTTPError — a SUBCLASS of RequestException in
// Python, so a 4xx/5xx response hits the same "skip until reload" branch
// a connection failure does, not the "delete from db" OSError branch.
// isNetworkError treats this the same way.
type httpStatusError struct{ status int }

func (e *httpStatusError) Error() string { return fmt.Sprintf("wpa-sec: HTTP %d", e.status) }

// downloadResultsIfDue ports the on_internet_available download branch:
// only re-download if the potfile is missing or older than
// download_interval, matching real Python's mtime check.
func (p *Plugin) downloadResultsIfDue() {
	crackedPath := filepath.Join(p.handshakesDir, "wpa-sec.cracked.potfile")
	if info, err := os.Stat(crackedPath); err == nil {
		if time.Since(info.ModTime()) < p.downloadInterval {
			return
		}
	}
	if err := p.downloadFromWpaSec(crackedPath); err != nil {
		log.Printf("WPA_SEC: Exception downloading results: %v", err)
		return
	}
	if p.singleFiles {
		p.writeCrackedSingleFiles(crackedPath)
	}
}

func (p *Plugin) downloadFromWpaSec(output string) error {
	apiURL := p.apiURL
	if !strings.HasSuffix(apiURL, "/") {
		apiURL += "/"
	}
	apiURL += "?api&dl=1"

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("HTTP_USER_AGENT", "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:15.0) Gecko/20100101 Firefox/15.0.1")
	req.AddCookie(&http.Cookie{Name: "key", Value: p.apiKey})

	log.Print("WPA_SEC: Downloading cracked passwords...")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("wpa-sec download: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err := os.WriteFile(output, data, 0o644); err != nil {
		return err
	}
	log.Print("WPA_SEC: Downloaded cracked passwords.")
	return nil
}

var nonAlnumRe = regexp.MustCompile(`[^a-zA-Z0-9]`)

// writeCrackedSingleFiles ports _write_cracked_single_files exactly,
// including its bssid:station_mac:ssid:password line format.
func (p *Plugin) writeCrackedSingleFiles(crackedFilePath string) {
	log.Print("WPA_SEC: Writing cracked single files...")
	data, err := os.ReadFile(crackedFilePath)
	if err != nil {
		log.Printf("WPA_SEC: Exception writing cracked single files: %v", err)
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(line, ":")
		if len(parts) != 4 {
			continue
		}
		bssid, _, ssid, password := parts[0], parts[1], parts[2], parts[3]
		if password == "" {
			continue
		}
		name := nonAlnumRe.ReplaceAllString(ssid, "") + "_" + bssid
		pcapPath := filepath.Join(p.handshakesDir, name+".pcap")
		crackedPath := filepath.Join(p.handshakesDir, name+".pcap.cracked")
		if _, err := os.Stat(pcapPath); err != nil {
			continue
		}
		if _, err := os.Stat(crackedPath); err == nil {
			continue
		}
		if err := os.WriteFile(crackedPath, []byte(password), 0o644); err != nil {
			log.Printf("WPA_SEC: writing %s: %v", crackedPath, err)
		}
	}
	log.Print("WPA_SEC: Wrote cracked single files.")
}

func (p *Plugin) loadDB() error {
	data, err := os.ReadFile(p.dbPath)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return json.Unmarshal(data, &p.records)
}

func (p *Plugin) saveDBLocked() error {
	if err := os.MkdirAll(filepath.Dir(p.dbPath), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(p.records)
	if err != nil {
		return err
	}
	return os.WriteFile(p.dbPath, data, 0o600)
}

// isNetworkError distinguishes a real connectivity failure (real
// Python's `except requests.exceptions.RequestException` branch — skip
// this handshake until the next reload rather than deleting it) from a
// local filesystem failure (real Python's `except OSError` branch —
// delete the record, matching a real "the pcap itself is gone/unreadable"
// condition, not a transient one worth retrying).
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	var statusErr *httpStatusError
	return errors.As(err, &statusErr)
}

func stringField(m config.Map, key, def string) string {
	if m == nil {
		return def
	}
	if s, ok := m[key].(string); ok && s != "" {
		return s
	}
	return def
}

func boolField(m config.Map, key string) bool {
	if m == nil {
		return false
	}
	b, _ := m[key].(bool)
	return b
}

func intField(m config.Map, key string, def int) int {
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

func stringSliceField(m config.Map, key string) []string {
	if m == nil {
		return nil
	}
	raw, ok := m[key].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
