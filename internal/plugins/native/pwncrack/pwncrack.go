// Package pwncrack is the native Go port of
// pwnagotchi/plugins/default/pwncrack.py: converts captured .pcap
// handshakes to .hc22000 (via the real hcxpcapngtool CLI), uploads the
// combined file to pwncrack.org, and downloads back a cracked-passwords
// potfile, throttled to once per timewait window and only while an
// internet connection is available.
//
// Original Python author: Terminatoror (see pwncrack.py's own __author__
// field, left untouched). This Go port is by raf181.
package pwncrack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

const (
	defaultServerURL  = "http://pwncrack.org/upload_handshake"
	defaultPotfileURL = "http://pwncrack.org/download_potfile_script"
	defaultTimewait   = 600 * time.Second
	commandTimeout    = 5 * time.Minute
	uploadFieldName   = "handshake"
	combinedFileName  = "combined.hc22000"
	potfileNameSuffix = "cracked.pwncrack.potfile"
)

// realClock is the production pluginmanager.Clock fallback used when
// Capabilities.Clock is nil (not yet wired in production — see
// pluginmanager.Capabilities' doc comment on hardware-capability fields
// landing incrementally).
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Plugin ports the UploadConvertPlugin class.
type Plugin struct {
	mu sync.Mutex

	log        pluginmanager.Logger
	exec       pluginmanager.CommandRunner
	httpClient *http.Client
	clock      pluginmanager.Clock

	serverURL  string
	potfileURL string
	timewait   time.Duration
	lastRun    time.Time

	key          string
	whitelist    []string
	handshakeDir string
	combinedFile string
	potfilePath  string
}

// New ports UploadConvertPlugin.__init__.
func New() *Plugin {
	return &Plugin{
		serverURL:  defaultServerURL,
		potfileURL: defaultPotfileURL,
		timewait:   defaultTimewait,
	}
}

func (p *Plugin) Name() string { return "pwncrack" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "1.0.0",
		Author:      "Terminatoror (original), Go port by raf181",
		License:     "GPL3",
		Description: "Converts .pcap files to .hc22000 and uploads them to pwncrack.org when internet is available.",
	}
}

// OnLoad ports on_loaded: just wires capabilities, matching Python's
// on_loaded doing nothing but a log line. Config (key/handshake dir/
// whitelist) is derived from "config_changed" instead — see HandleEvent.
func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.log = caps.Log
	p.exec = caps.Exec
	p.clock = caps.Clock
	if p.clock == nil {
		p.clock = realClock{}
	}
	p.httpClient = caps.HTTPClient
	if p.httpClient == nil {
		p.httpClient = http.DefaultClient
	}
	p.key, _ = caps.Config["key"].(string)
	return nil
}

// OnUnload ports on_unload (a log line only).
func (p *Plugin) OnUnload() error {
	p.logf("unloading")
	return nil
}

// HandleEvent ports the real on_config_changed/on_internet_available
// hooks.
func (p *Plugin) HandleEvent(event string, args []interface{}) {
	switch event {
	case "config_changed":
		p.onConfigChanged(args)
	case "internet_available":
		p.onInternetAvailable()
	}
}

// onConfigChanged ports on_config_changed(config): derives paths from the
// FULL daemon config (bettercap.handshakes, main.whitelist) — see
// pluginmanager.Manager.Load's doc comment for why "config_changed"
// carries the whole config, not just this plugin's own options.
func (p *Plugin) onConfigChanged(args []interface{}) {
	if len(args) < 1 {
		return
	}
	fullCfg, ok := args[0].(config.Map)
	if !ok {
		return
	}
	bettercapCfg, _ := fullCfg["bettercap"].(config.Map)
	handshakeDir, _ := bettercapCfg["handshakes"].(string)

	var whitelist []string
	mainCfg, _ := fullCfg["main"].(config.Map)
	if raw, ok := mainCfg["whitelist"].([]interface{}); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				whitelist = append(whitelist, s)
			}
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.handshakeDir = handshakeDir
	p.whitelist = whitelist
	if handshakeDir != "" {
		p.combinedFile = filepath.Join(handshakeDir, combinedFileName)
		p.potfilePath = filepath.Join(handshakeDir, potfileNameSuffix)
	}
}

// onInternetAvailable ports on_internet_available(agent): rate-limited
// (timewait) convert+upload, then potfile download. Real Python's own
// try/except around the whole body means one failure never crashes the
// plugin/daemon — matched here by logging and returning, never panicking.
func (p *Plugin) onInternetAvailable() {
	p.mu.Lock()
	now := p.clock.Now()
	remaining := p.timewait - now.Sub(p.lastRun)
	if remaining > 0 && !p.lastRun.IsZero() {
		p.mu.Unlock()
		p.logf("waiting %s more before next run", remaining.Round(time.Second))
		return
	}
	p.lastRun = now
	handshakeDir := p.handshakeDir
	combinedFile := p.combinedFile
	potfilePath := p.potfilePath
	key := p.key
	whitelist := append([]string(nil), p.whitelist...)
	p.mu.Unlock()

	if handshakeDir == "" {
		return
	}

	p.logf("running upload process, waiting %s between runs", p.timewait)
	if err := p.convertAndUpload(handshakeDir, combinedFile, key, whitelist); err != nil {
		p.logf("error during upload process: %v", err)
		return
	}
	if err := p.downloadPotfile(potfilePath, key); err != nil {
		p.logf("error during potfile download: %v", err)
	}
}

// convertAndUpload ports _convert_and_upload: hcxpcapngtool-convert every
// non-whitelisted .pcap in handshakeDir into combinedFile, then upload it
// as multipart/form-data.
func (p *Plugin) convertAndUpload(handshakeDir, combinedFile, key string, whitelist []string) error {
	entries, err := os.ReadDir(handshakeDir)
	if err != nil {
		return fmt.Errorf("reading handshake dir: %w", err)
	}

	var pcapFiles []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pcap") {
			continue
		}
		whitelisted := false
		for _, w := range whitelist {
			if w != "" && strings.Contains(e.Name(), w) {
				whitelisted = true
				break
			}
		}
		if !whitelisted {
			pcapFiles = append(pcapFiles, e.Name())
		}
	}
	if len(pcapFiles) == 0 {
		p.logf("no .pcap files found to convert (or all files are whitelisted)")
		return nil
	}

	if p.exec == nil {
		return fmt.Errorf("no command runner available")
	}
	for _, name := range pcapFiles {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		_, err := p.exec.Run(ctx, "hcxpcapngtool", "-o", combinedFile, filepath.Join(handshakeDir, name))
		cancel()
		if err != nil {
			p.logf("hcxpcapngtool on %s: %v", name, err)
		}
	}

	if _, err := os.Stat(combinedFile); err != nil {
		if err := os.WriteFile(combinedFile, nil, 0o644); err != nil {
			return fmt.Errorf("creating empty combined file: %w", err)
		}
	}
	defer os.Remove(combinedFile)

	return p.uploadCombinedFile(combinedFile, key)
}

func (p *Plugin) uploadCombinedFile(combinedFile, key string) error {
	data, err := os.ReadFile(combinedFile)
	if err != nil {
		return fmt.Errorf("reading combined file: %w", err)
	}

	body, contentType, err := buildMultipartUpload(uploadFieldName, filepath.Base(combinedFile), data, map[string]string{"key": key})
	if err != nil {
		return fmt.Errorf("building upload request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, p.serverURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("uploading handshake: %w", err)
	}
	defer resp.Body.Close()

	var parsed interface{}
	respBody, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(respBody, &parsed)
	p.logf("upload response: %v", parsed)
	return nil
}

// downloadPotfile ports _download_potfile.
func (p *Plugin) downloadPotfile(potfilePath, key string) error {
	u, err := url.Parse(p.potfileURL)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("key", key)
	u.RawQuery = q.Encode()

	resp, err := p.httpClient.Get(u.String())
	if err != nil {
		return fmt.Errorf("downloading potfile: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		p.logf("failed to download potfile: %d", resp.StatusCode)
		p.logf("%s", string(body))
		return fmt.Errorf("potfile download: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if potfilePath == "" {
		return nil
	}
	if err := os.WriteFile(potfilePath, data, 0o644); err != nil {
		return fmt.Errorf("writing potfile: %w", err)
	}
	p.logf("potfile downloaded to %s", potfilePath)
	return nil
}

func (p *Plugin) logf(format string, args ...interface{}) {
	if p.log != nil {
		p.log.Printf(format, args...)
	}
}

// buildMultipartUpload ports requests.post(..., files={...}, data={...}):
// a real multipart/form-data body with one file field and the given
// plain string fields.
func buildMultipartUpload(fieldName, fileName string, fileData []byte, fields map[string]string) (io.Reader, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return nil, "", err
		}
	}
	part, err := w.CreateFormFile(fieldName, fileName)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(fileData); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.Unloader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
