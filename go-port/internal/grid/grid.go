// Package grid ports pwnagotchi/grid.py: the HTTP client for the local
// pwngrid-peer daemon (port 8666) and the public opwngrid uptime check.
package grid

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
)

// APIAddress mirrors grid.API_ADDRESS.
const APIAddress = "http://127.0.0.1:8666/api/v1"

// connectTimeout/readTimeout mirror Python's `timeout=(30.0, 60.0)` tuple
// (connect timeout, read timeout) used on every grid.py request.
const (
	connectTimeout = 30 * time.Second
	readTimeout    = 60 * time.Second
)

// Client ports the module-level functions in grid.py as methods (Python has
// no Client class here — it's bare module functions closing over a global
// API_ADDRESS — but every call needs the daemon version string for the
// is_connected() User-Agent header, so Go threads it through a small
// struct instead of a package-level global).
type Client struct {
	APIAddress string
	Version    string
	HTTPClient *http.Client
}

// NewClient builds a Client whose HTTPClient reproduces requests'
// (connect_timeout, read_timeout) tuple via a dedicated dialer + response
// header timeout — Go's net/http has no single knob for "read timeout on
// the body", so this is a close approximation, not a byte-for-byte
// reproduction of requests' timeout semantics (see docs/known-differences.md).
func NewClient(version string) *Client {
	dialer := &net.Dialer{Timeout: connectTimeout}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		ResponseHeaderTimeout: readTimeout,
	}
	return &Client{
		APIAddress: APIAddress,
		Version:    version,
		HTTPClient: &http.Client{Transport: transport, Timeout: connectTimeout + readTimeout},
	}
}

// IsConnected mirrors grid.is_connected(): GETs the public opwngrid uptime
// endpoint and returns whether isUp is truthy in the JSON response, folding
// ANY error (network, non-JSON body, missing field) into false — matching
// Python's bare `except: pass`.
func (c *Client) IsConnected() bool {
	req, err := http.NewRequest(http.MethodGet, "https://api.opwngrid.xyz/api/v1/uptime", nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", fmt.Sprintf("pwnagotchi/%s", c.Version))

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}
	var v map[string]interface{}
	if err := json.Unmarshal(body, &v); err != nil {
		return false
	}
	up, _ := v["isUp"].(bool)
	return up
}

// Call mirrors grid.call(path, obj=None):
//   - obj == nil: GET
//   - obj is a map (dict): POST as JSON
//   - otherwise (string/[]byte): POST as a raw body, no Content-Type header
//     (matching Python's requests.post(..., data=obj, headers=None))
//
// A non-200 response raises "(status %d) %s" (matching Python's Exception
// message format exactly).
func (c *Client) Call(path string, obj interface{}) (interface{}, error) {
	url := c.APIAddress + path

	var req *http.Request
	var err error
	switch v := obj.(type) {
	case nil:
		req, err = http.NewRequest(http.MethodGet, url, nil)
	case map[string]interface{}:
		body, jerr := json.Marshal(v)
		if jerr != nil {
			return nil, jerr
		}
		req, err = http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	case []byte:
		req, err = http.NewRequest(http.MethodPost, url, bytes.NewReader(v))
	case string:
		req, err = http.NewRequest(http.MethodPost, url, strings.NewReader(v))
	default:
		return nil, fmt.Errorf("grid: unsupported obj type %T", obj)
	}
	if err != nil {
		return nil, err
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("(status %d) %s", resp.StatusCode, respBody)
	}
	var result interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// Advertise mirrors grid.advertise(enabled=True) EXACTLY, including its
// real Python operator-precedence bug: `"/mesh/%s" % 'true' if enabled else
// 'false'` parses as `("/mesh/%s" % 'true') if enabled else 'false'` — the
// %-format binds tighter than the ternary. So enabled=True correctly calls
// "/mesh/true", but enabled=False calls the bare literal path "false" (NOT
// "/mesh/false"), which becomes the URL {API_ADDRESS}false — a
// malformed request. Preserved intentionally; see docs/known-differences.md.
func (c *Client) Advertise(enabled bool) (interface{}, error) {
	if enabled {
		return c.Call("/mesh/true", nil)
	}
	return c.Call("false", nil)
}

// SetAdvertisementData mirrors grid.set_advertisement_data.
func (c *Client) SetAdvertisementData(data map[string]interface{}) (interface{}, error) {
	return c.Call("/mesh/data", data)
}

// GetAdvertisementData mirrors grid.get_advertisement_data.
func (c *Client) GetAdvertisementData() (interface{}, error) {
	return c.Call("/mesh/data", nil)
}

// Memory mirrors grid.memory.
func (c *Client) Memory() (interface{}, error) {
	return c.Call("/mesh/memory", nil)
}

// Peers mirrors grid.peers.
func (c *Client) Peers() ([]interface{}, error) {
	result, err := c.Call("/mesh/peers", nil)
	if err != nil {
		return nil, err
	}
	list, ok := result.([]interface{})
	if !ok {
		return nil, fmt.Errorf("grid: /mesh/peers did not return a JSON array")
	}
	return list, nil
}

// ClosestPeer mirrors grid.closest_peer.
func (c *Client) ClosestPeer() (interface{}, error) {
	all, err := c.Peers()
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, nil
	}
	return all[0], nil
}

// SessionSummary is the minimal projection of log.Session (not yet ported)
// that update_data serializes.
type SessionSummary struct {
	DurationSecs float64
	Epochs       int
	TrainEpochs  int
	AvgReward    float64
	MinReward    float64
	MaxReward    float64
	Deauthed     int
	Associated   int
	Handshakes   int
	Peers        int
}

// UpdateData mirrors grid.update_data(last_session): builds and POSTs the
// unit's status payload to /data. The brain.json read is preserved exactly
// as Python has it — read into a local variable and then never used again
// (dead code from a removed AI feature, matching this repo's "No AI!"
// literal below) — not because it does anything, but because "preserve...
// files, paths... file operations" applies even to vestigial ones the goal
// says not to silently drop.
func (c *Client) UpdateData(cfg config.Map, session SessionSummary) error {
	_, _ = readBrainJSON("/root/brain.json") // read-and-discard, matches Python exactly

	var enabledPlugins []string
	if main, ok := cfg["main"].(config.Map); ok {
		if plugins, ok := main["plugins"].(config.Map); ok {
			for name, raw := range plugins {
				opts, ok := raw.(config.Map)
				if !ok {
					continue
				}
				if enabled, ok := opts["enabled"].(bool); ok && enabled {
					enabledPlugins = append(enabledPlugins, name)
				}
			}
		}
	}
	var language string
	if main, ok := cfg["main"].(config.Map); ok {
		language, _ = main["lang"].(string)
	}

	data := map[string]interface{}{
		"ai": "No AI!",
		"session": map[string]interface{}{
			"duration":     session.DurationSecs,
			"epochs":       session.Epochs,
			"train_epochs": session.TrainEpochs,
			"avg_reward":   session.AvgReward,
			"min_reward":   session.MinReward,
			"max_reward":   session.MaxReward,
			"deauthed":     session.Deauthed,
			"associated":   session.Associated,
			"handshakes":   session.Handshakes,
			"peers":        session.Peers,
		},
		"uname":     getOutput("uname", "-a"),
		"version":   c.Version,
		"build":     "Pwnagotchi by Jayofelony",
		"plugins":   enabledPlugins,
		"language":  language,
		"bettercap": getOutput("bettercap", "-version"),
		"opwngrid":  getOutput("pwngrid", "-version"),
	}

	_, err := c.Call("/data", data)
	return err
}

// ReportAP mirrors grid.report_ap: returns false (and logs, via the
// returned error, left to the caller) on any failure instead of raising.
func (c *Client) ReportAP(essid, bssid string) bool {
	_, err := c.Call("/report/ap", map[string]interface{}{"essid": essid, "bssid": bssid})
	return err == nil
}

// Inbox mirrors grid.inbox(page=1, with_pager=False).
func (c *Client) Inbox(page int, withPager bool) (interface{}, error) {
	obj, err := c.Call(fmt.Sprintf("/inbox?p=%d", page), nil)
	if err != nil {
		return nil, err
	}
	if withPager {
		return obj, nil
	}
	m, ok := obj.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("grid: /inbox response missing 'messages'")
	}
	return m["messages"], nil
}

// InboxMessage mirrors grid.inbox_message.
func (c *Client) InboxMessage(id int) (interface{}, error) {
	return c.Call(fmt.Sprintf("/inbox/%d", id), nil)
}

// MarkMessage mirrors grid.mark_message.
func (c *Client) MarkMessage(id int, mark string) (interface{}, error) {
	return c.Call(fmt.Sprintf("/inbox/%d/%s", id, mark), nil)
}

// SendMessage mirrors grid.send_message: POSTs the UTF-8 message bytes as
// a raw body (not JSON), matching `call(path, message.encode('utf-8'))`.
func (c *Client) SendMessage(to, message string) (interface{}, error) {
	return c.Call(fmt.Sprintf("/unit/%s/inbox", to), []byte(message))
}

func readBrainJSON(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]interface{}{}, nil // matches Python's bare `except: pass`
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]interface{}{}, nil
	}
	return m, nil
}

// getOutput mirrors subprocess.getoutput(cmd): combined stdout+stderr,
// trailing newline stripped, errors folded into whatever text was produced
// rather than propagated — EXCEPT invoked via os/exec with an explicit
// argv (no shell), since these are fixed literal commands with no
// interpolated/untrusted input, so a shell provides no needed feature here
// and only adds injection surface. subprocess.getoutput itself always goes
// through `/bin/sh -c`, so a command genuinely not found produces
// "/bin/sh: 1: <cmd>: not found" in Python; os/exec's LookPath error text
// differs (see docs/known-differences.md) — still folded into the output
// string rather than surfaced as a Go error, matching Python's return type.
func getOutput(name string, args ...string) string {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		if len(out) == 0 {
			return err.Error()
		}
	}
	return strings.TrimRight(string(out), "\n")
}
