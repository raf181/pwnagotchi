// Package pyplugin is the Go side of the Python subprocess/IPC bridge that
// runs real, unmodified pwnagotchi bundled/custom plugins (see bridge.py).
// It exists because bundled plugins depend on real Python-only libraries
// (RPi.GPIO, dbus, requests, flask, ...) that cannot be reimplemented in Go
// without losing compatibility — running the actual plugin code in a real
// Python process is the only way to honor "preserve Python plugin
// compatibility" without faking it.
package pyplugin

import (
	"bufio"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jayofelony/pwnagotchi/go-port/internal/mesh"
)

//go:embed bridge.py
var bridgeScript []byte

// Bridge manages one running bridge.py subprocess: real bundled/custom
// plugins loaded via the real pwnagotchi.plugins.load(config), driven by
// newline-delimited JSON events over stdin/stdout exactly as documented in
// bridge.py's module docstring.
type Bridge struct {
	cmd    *exec.Cmd
	stdin  *bufio.Writer
	stdinF io.WriteCloser

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan ackResult

	Loaded []string // plugin names bridge.py reported as loaded, post plugins.load()

	scriptPath string
	closed     int32
}

type ackResult struct {
	ok     bool
	err    string
	result json.RawMessage
}

// Options configures New. Python is the interpreter to run bridge.py with:
// on a real deployed pwnagotchi unit this is plain "python3" (the real
// system-wide pwnagotchi package is already importable there); in this dev
// repo it must point at ../venv/bin/python3 (or wherever `import
// pwnagotchi` resolves), via the PWNAGOTCHI_PYTHON env var or an explicit
// override — see docs/known-differences.md.
type Options struct {
	Python  string        // python3 interpreter; defaults to "python3" on PATH
	Env     []string      // extra environment (e.g. PYTHONPATH); appended to os.Environ()
	Dir     string        // working directory for the subprocess
	Config  interface{}   // the fully-merged config (config.Map), JSON-marshaled and handed to bridge.py
	Timeout time.Duration // how long to wait for the post-load "ready" line; 0 = 30s default
}

// New starts bridge.py, waits for its startup "ready" line (confirming
// plugins.load(config) ran), and returns a live Bridge ready to dispatch
// events. The config is written to a private temp file (0600, deleted on
// close) rather than piped inline, so it can never race with the first
// event line and never appears in argv (ps listings, process-injection
// surface) or logs.
func New(opts Options) (*Bridge, error) {
	python := opts.Python
	if python == "" {
		python = "python3"
	}
	if _, err := exec.LookPath(python); err != nil {
		return nil, fmt.Errorf("pyplugin: python interpreter %q not found: %w (bundled Python plugins will not run; see docs/known-differences.md)", python, err)
	}

	tmp, err := os.CreateTemp("", "pwnagotchi-pyplugin-config-*.json")
	if err != nil {
		return nil, fmt.Errorf("pyplugin: creating config temp file: %w", err)
	}
	scriptPath := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(scriptPath)
		return nil, err
	}
	enc := json.NewEncoder(tmp)
	if err := enc.Encode(opts.Config); err != nil {
		tmp.Close()
		os.Remove(scriptPath)
		return nil, fmt.Errorf("pyplugin: marshaling config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(scriptPath)
		return nil, err
	}

	bridgePyPath, err := writeBridgeScript()
	if err != nil {
		os.Remove(scriptPath)
		return nil, err
	}

	cmd := exec.Command(python, "-u", bridgePyPath, scriptPath)
	cmd.Dir = opts.Dir
	cmd.Env = append(os.Environ(), opts.Env...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		os.Remove(scriptPath)
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		os.Remove(scriptPath)
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		os.Remove(scriptPath)
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		os.Remove(scriptPath)
		return nil, fmt.Errorf("pyplugin: starting bridge: %w", err)
	}

	b := &Bridge{
		cmd:        cmd,
		stdin:      bufio.NewWriter(stdin),
		stdinF:     stdin,
		pending:    map[int64]chan ackResult{},
		scriptPath: scriptPath,
	}

	go b.relayStderr(stderr)

	readyCh := make(chan error, 1)
	go b.readLoop(stdout, readyCh)

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	select {
	case err := <-readyCh:
		if err != nil {
			b.Close()
			return nil, err
		}
	case <-time.After(timeout):
		b.Close()
		return nil, fmt.Errorf("pyplugin: bridge did not become ready within %s", timeout)
	}

	return b, nil
}

// writeBridgeScript materializes the embedded bridge.py to a temp file so
// the real python3 interpreter can execute it (Go's embed.FS has no
// executable filesystem path of its own).
func writeBridgeScript() (string, error) {
	f, err := os.CreateTemp("", "pwnagotchi-pyplugin-bridge-*.py")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(bridgeScript); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

type readyMsg struct {
	Ready  bool     `json:"ready"`
	Loaded []string `json:"loaded"`
}

// respMsg covers both event acks ({"id":N,"ok":bool,"error":...}) and call
// responses ({"id":N,"result":...} or {"id":N,"error":...}).
type respMsg struct {
	ID     *int64          `json:"id"`
	OK     bool            `json:"ok"`
	Error  string          `json:"error"`
	Result json.RawMessage `json:"result"`
}

func (b *Bridge) readLoop(stdout io.Reader, readyCh chan<- error) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	first := true
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if first {
			first = false
			var rm readyMsg
			if err := json.Unmarshal(line, &rm); err == nil && rm.Ready {
				b.Loaded = rm.Loaded
				readyCh <- nil
				continue
			}
			readyCh <- fmt.Errorf("pyplugin: unexpected first line from bridge: %s", line)
			continue
		}

		var resp respMsg
		if err := json.Unmarshal(line, &resp); err != nil {
			log.Printf("pyplugin: unparseable line from bridge: %s", line)
			continue
		}
		if resp.ID == nil {
			if !resp.OK {
				log.Printf("pyplugin: bridge error: %s", resp.Error)
			}
			continue
		}
		b.mu.Lock()
		ch, ok := b.pending[*resp.ID]
		if ok {
			delete(b.pending, *resp.ID)
		}
		b.mu.Unlock()
		if ok {
			ch <- ackResult{ok: resp.OK, err: resp.Error, result: resp.Result}
		}
	}
	if first {
		readyCh <- fmt.Errorf("pyplugin: bridge exited before printing a ready line")
	}
}

func (b *Bridge) relayStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		log.Printf("%s", scanner.Text())
	}
}

// eventMsg mirrors bridge.py's expected stdin shape.
type eventMsg struct {
	ID    int64         `json:"id"`
	Event string        `json:"event"`
	Args  []interface{} `json:"args"`
}

// On mirrors pwnagotchi.plugins.on(event_name, *args): dispatches event to
// every loaded real Python plugin's on_<event> handler. Matches the
// EventEmitter interfaces used throughout internal/agent, internal/mesh,
// internal/ui/view, internal/ui/display, and internal/cli exactly, so a
// *Bridge can be plugged in directly wherever those packages accept an
// EventEmitter. Fire-and-forget, exactly like real plugins.on(): a
// per-plugin handler error is logged (via bridge.py's stderr, relayed
// above) but never returned here, matching Python's own behavior of
// swallowing per-plugin callback exceptions inside process_events.
func (b *Bridge) On(event string, args ...interface{}) {
	if atomic.LoadInt32(&b.closed) != 0 {
		return
	}
	safeArgs := make([]interface{}, len(args))
	for i, a := range args {
		safeArgs[i] = toJSONArg(a)
	}

	id := atomic.AddInt64(&b.nextID, 1)
	msg := eventMsg{ID: id, Event: event, Args: safeArgs}
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("pyplugin: marshaling event %q: %v", event, err)
		return
	}

	b.mu.Lock()
	if b.pending == nil {
		b.mu.Unlock()
		return
	}
	b.pending[id] = make(chan ackResult, 1)
	_, werr := b.stdin.Write(append(data, '\n'))
	if werr == nil {
		werr = b.stdin.Flush()
	}
	b.mu.Unlock()

	if werr != nil {
		log.Printf("pyplugin: writing event %q: %v", event, werr)
	}
}

// Call issues a synchronous "call" message (see bridge.py's module
// docstring) and blocks for a real result or error from the bridge, with a
// timeout. Used for the handful of things internal/web genuinely needs a
// real answer from real Python state for: the current loaded/database
// plugin list, toggling a plugin on/off (via the real
// plugins.toggle_plugin), and running a plugin's real on_webhook.
func (b *Bridge) Call(call string, params map[string]interface{}, timeout time.Duration) (json.RawMessage, error) {
	if atomic.LoadInt32(&b.closed) != 0 {
		return nil, fmt.Errorf("pyplugin: bridge is closed")
	}

	msg := map[string]interface{}{"call": call}
	for k, v := range params {
		msg[k] = v
	}
	id := atomic.AddInt64(&b.nextID, 1)
	msg["id"] = id
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("pyplugin: marshaling call %q: %w", call, err)
	}

	ch := make(chan ackResult, 1)
	b.mu.Lock()
	if b.pending == nil {
		b.mu.Unlock()
		return nil, fmt.Errorf("pyplugin: bridge is closed")
	}
	b.pending[id] = ch
	_, werr := b.stdin.Write(append(data, '\n'))
	if werr == nil {
		werr = b.stdin.Flush()
	}
	b.mu.Unlock()
	if werr != nil {
		return nil, fmt.Errorf("pyplugin: writing call %q: %w", call, werr)
	}

	select {
	case res := <-ch:
		if res.err != "" {
			return nil, fmt.Errorf("pyplugin: call %q failed: %s", call, res.err)
		}
		return res.result, nil
	case <-time.After(timeout):
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		return nil, fmt.Errorf("pyplugin: call %q timed out after %s", call, timeout)
	}
}

// PluginMeta mirrors the real Python plugin metadata bridge.py's
// list_plugins call reads off each loaded instance's class attributes.
type PluginMeta struct {
	Version     string `json:"version,omitempty"`
	Author      string `json:"author,omitempty"`
	Description string `json:"description,omitempty"`
	License     string `json:"license,omitempty"`
	HasWebhook  bool   `json:"has_webhook"`
}

// PluginList is the real, current pwnagotchi.plugins module-level state
// (plugins.loaded/plugins.database), for internal/web's /plugins page —
// ported from handler.py's Handler.plugins(name=None) route.
type PluginList struct {
	Loaded         map[string]PluginMeta `json:"loaded"`
	Database       map[string]string     `json:"database"`
	DefaultPlugins []string              `json:"default_plugins"`
}

// ListPlugins ports the real Python plugins module's loaded/database
// globals, read live from the running bridge — not cached, not
// reconstructed in Go.
func (b *Bridge) ListPlugins() (*PluginList, error) {
	raw, err := b.Call("list_plugins", nil, 10*time.Second)
	if err != nil {
		return nil, err
	}
	var pl PluginList
	if err := json.Unmarshal(raw, &pl); err != nil {
		return nil, fmt.Errorf("pyplugin: decoding list_plugins result: %w", err)
	}
	return &pl, nil
}

// TogglePlugin ports plugins.toggle_plugin(name, enable): a real load/
// unload of the plugin module inside the running bridge process (not a
// Go-side simulation of it). Persisting the new enabled state to the
// on-disk config is the caller's responsibility (internal/web calls
// internal/config.SaveConfig itself after a successful toggle) since the
// bridge process has no real pwnagotchi.config global set (see
// docs/known-differences.md) — real Python's own toggle_plugin only
// persists when that global happens to be set by the same process's
// cli.py startup, which this bridge never runs.
func (b *Bridge) TogglePlugin(name string, enable bool) (bool, error) {
	raw, err := b.Call("toggle_plugin", map[string]interface{}{"name": name, "enable": enable}, 10*time.Second)
	if err != nil {
		return false, err
	}
	var changed bool
	if err := json.Unmarshal(raw, &changed); err != nil {
		return false, fmt.Errorf("pyplugin: decoding toggle_plugin result: %w", err)
	}
	return changed, nil
}

// WebhookResponse is a real plugin's on_webhook return value, normalized
// via Flask's own make_response inside the bridge (see bridge.py's
// _call_webhook) — a genuine HTTP response, not synthesized in Go.
type WebhookResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"-"`
	BodyB64 string            `json:"body_b64"`
}

// Webhook ports handler.py's plugin webhook passthrough
// (`plugins.loaded[name].on_webhook(subpath, request)`): calls the real
// plugin method inside the bridge with a genuine flask.Request built via
// Flask's own test_request_context from the real incoming HTTP request
// internal/web received, so on_webhook implementations that call
// request.args/form/data/headers get the real thing, not a stub.
func (b *Bridge) Webhook(name, subpath, method, path, query string, headers map[string][]string, body []byte) (*WebhookResponse, error) {
	hdrs := make(map[string]interface{}, len(headers))
	for k, v := range headers {
		vs := make([]interface{}, len(v))
		for i, s := range v {
			vs[i] = s
		}
		hdrs[k] = vs
	}
	raw, err := b.Call("webhook", map[string]interface{}{
		"name":     name,
		"subpath":  subpath,
		"method":   method,
		"path":     path,
		"query":    query,
		"headers":  hdrs,
		"body_b64": base64.StdEncoding.EncodeToString(body),
	}, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var wr WebhookResponse
	if err := json.Unmarshal(raw, &wr); err != nil {
		return nil, fmt.Errorf("pyplugin: decoding webhook result: %w", err)
	}
	wr.Body, err = base64.StdEncoding.DecodeString(wr.BodyB64)
	if err != nil {
		return nil, fmt.Errorf("pyplugin: decoding webhook body: %w", err)
	}
	return &wr, nil
}

// Close ends the bridge subprocess: closes stdin (bridge.py's `for line in
// sys.stdin` loop then exits cleanly on EOF), waits briefly, and kills the
// process if it hasn't exited — matching the graceful-then-forceful
// shutdown shape used elsewhere in this port (internal/fs's mount teardown,
// internal/unit's runners).
func (b *Bridge) Close() error {
	if !atomic.CompareAndSwapInt32(&b.closed, 0, 1) {
		return nil
	}
	defer os.Remove(b.scriptPath)

	_ = b.stdinF.Close()

	done := make(chan error, 1)
	go func() { done <- b.cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		if b.cmd.Process != nil {
			_ = b.cmd.Process.Kill()
		}
		<-done
		return fmt.Errorf("pyplugin: bridge did not exit within 5s, killed")
	}
}

// toJSONArg converts a Go event argument into a JSON-safe representation
// for bridge.py. Plain data (maps, slices, strings, numbers — which is
// what agent.AP/agent.Station and most event payloads already are) passes
// through as real data. mesh.Peer is unwrapped to its real .Adv
// advertisement dict (the same JSON shape Python's own Peer wraps).
// Anything else (the *agent.Agent, view/display objects passed to almost
// every callback) becomes a {"__goref__": "<TypeName>"} marker that
// bridge.py turns into a _GoProxyStub — a real, loud NotImplementedError
// if a plugin actually calls a method on it, never a silent no-op.
func toJSONArg(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case string, bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, float32, float64:
		return t
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			out[k] = toJSONArg(val)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, val := range t {
			out[i] = toJSONArg(val)
		}
		return out
	case *mesh.Peer:
		return toJSONArg(map[string]interface{}(t.Adv))
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map:
		if rv.Type().Key().Kind() == reflect.String {
			out := make(map[string]interface{}, rv.Len())
			for _, k := range rv.MapKeys() {
				out[k.String()] = toJSONArg(rv.MapIndex(k).Interface())
			}
			return out
		}
	case reflect.Slice, reflect.Array:
		out := make([]interface{}, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = toJSONArg(rv.Index(i).Interface())
		}
		return out
	}

	return map[string]interface{}{"__goref__": goRefName(v)}
}

func goRefName(v interface{}) string {
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Name() != "" {
		return t.Name()
	}
	return t.String()
}
