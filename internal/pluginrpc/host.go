package pluginrpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// maxLineSize bounds every single message this host will read or
	// write — a plugin (or a corrupted pipe) trying to send an
	// unbounded line is a protocol violation, not something to buffer
	// without limit.
	maxLineSize = 4 << 20 // 4 MiB

	defaultCallTimeout    = 10 * time.Second
	defaultPingInterval   = 5 * time.Second
	defaultMaxMissedPings = 3
)

// Dispatcher executes an incoming "call" message's named capability
// method (e.g. "Agent.Run", "View.Set") against the real, live
// capabilities for one plugin, and returns its result (or error) to be
// sent back to the plugin subprocess. A RemotePlugin builds one of these
// per load from the same pluginmanager.Capabilities every in-process
// plugin already receives — see remoteplugin.go.
type Dispatcher interface {
	Dispatch(method string, args json.RawMessage) (result interface{}, err error)
}

// Host manages exactly one running, checksum-verified plugin subprocess:
// sends it daemon events, answers its capability calls via Dispatcher,
// and detects both a hard crash (process exit) and a soft hang (missed
// pings) as real, isolated failures of that one plugin — never a panic
// or hang propagating into the daemon itself.
type Host struct {
	name string
	cmd  *exec.Cmd

	stdinMu sync.Mutex
	stdinW  *bufio.Writer
	stdinF  io.WriteCloser

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan Message

	dispatcher Dispatcher
	logger     *log.Logger

	callTimeout    time.Duration
	pingInterval   time.Duration
	maxMissedPings int

	closed int32
	dead   int32 // set once the process is known gone (exited or killed)
	exited chan struct{}

	onFailure func(err error) // called at most once, when the plugin is judged dead
	failOnce  sync.Once
}

// Options configures Spawn.
type Options struct {
	Dispatcher     Dispatcher
	Logger         *log.Logger
	CallTimeout    time.Duration // 0 => defaultCallTimeout
	PingInterval   time.Duration // 0 => defaultPingInterval; <0 disables heartbeat pings (tests only)
	MaxMissedPings int           // 0 => defaultMaxMissedPings
	// OnFailure is invoked exactly once, from a background goroutine, the
	// first time this host judges the plugin dead (process exit or
	// unresponsive heartbeat) — the caller (RemotePlugin) uses this to
	// mark itself unloaded/failed without polling.
	OnFailure func(err error)
}

// Spawn checksum-verifies path against manifest, then starts it as a
// real subprocess and begins the RPC loop. Refuses to start (never
// silently proceeds) if the checksum doesn't match.
func Spawn(name, path string, manifest *Manifest, opts Options) (*Host, error) {
	if manifest != nil {
		if err := manifest.VerifyExecutable(path); err != nil {
			return nil, err
		}
	}

	logger := opts.Logger
	if logger == nil {
		logger = log.Default()
	}
	callTimeout := opts.CallTimeout
	if callTimeout <= 0 {
		callTimeout = defaultCallTimeout
	}
	pingInterval := opts.PingInterval
	if pingInterval == 0 {
		pingInterval = defaultPingInterval
	}
	maxMissed := opts.MaxMissedPings
	if maxMissed <= 0 {
		maxMissed = defaultMaxMissedPings
	}

	cmd := exec.Command(path) // argv only — no shell, no user-influenced arguments
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("pluginrpc: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("pluginrpc: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("pluginrpc: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("pluginrpc: starting plugin %q: %w", name, err)
	}

	h := &Host{
		name:           name,
		cmd:            cmd,
		stdinW:         bufio.NewWriter(stdin),
		stdinF:         stdin,
		pending:        map[int64]chan Message{},
		dispatcher:     opts.Dispatcher,
		logger:         logger,
		callTimeout:    callTimeout,
		pingInterval:   pingInterval,
		maxMissedPings: maxMissed,
		exited:         make(chan struct{}),
		onFailure:      opts.OnFailure,
	}

	go h.readLoop(stdout)
	go h.relayStderr(stderr)
	go h.monitorExit()
	if pingInterval > 0 {
		go h.pingLoop()
	}

	return h, nil
}

// monitorExit waits for the real process to exit (for any reason) and
// marks the host dead — this is the "hard crash" half of crash
// isolation.
func (h *Host) monitorExit() {
	err := h.cmd.Wait()
	atomic.StoreInt32(&h.dead, 1)
	close(h.exited)
	if atomic.LoadInt32(&h.closed) == 0 {
		// Not an intentional Close() — a real crash/unexpected exit.
		h.fail(fmt.Errorf("pluginrpc: plugin %q process exited unexpectedly: %v", h.name, err))
	}
	h.mu.Lock()
	for id, ch := range h.pending {
		close(ch)
		delete(h.pending, id)
	}
	h.mu.Unlock()
}

func (h *Host) fail(err error) {
	h.failOnce.Do(func() {
		h.logger.Printf("pluginrpc: %s: %v", h.name, err)
		if h.onFailure != nil {
			h.onFailure(err)
		}
	})
}

// readLoop consumes newline-delimited Messages from the plugin's
// stdout: "call" messages are dispatched against the real capabilities
// and answered; "response"/"pong" messages complete a pending local
// call (e.g. a heartbeat ping).
func (h *Host) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			h.logger.Printf("pluginrpc: %s: unparseable line: %s", h.name, truncate(line, 200))
			continue
		}
		switch msg.Type {
		case TypeCall:
			go h.handleIncomingCall(msg)
		case TypeResponse, TypePong:
			h.mu.Lock()
			ch, ok := h.pending[msg.ID]
			if ok {
				delete(h.pending, msg.ID)
			}
			h.mu.Unlock()
			if ok {
				ch <- msg
			}
		default:
			h.logger.Printf("pluginrpc: %s: unknown message type %q", h.name, msg.Type)
		}
	}
}

func (h *Host) handleIncomingCall(msg Message) {
	if h.dispatcher == nil {
		h.writeMessage(Message{ID: msg.ID, Type: TypeResponse, Error: "pluginrpc: host has no dispatcher configured"})
		return
	}
	result, err := func() (result interface{}, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic dispatching %q: %v", msg.Name, r)
			}
		}()
		return h.dispatcher.Dispatch(msg.Name, msg.Args)
	}()
	resp := Message{ID: msg.ID, Type: TypeResponse}
	if err != nil {
		resp.Error = err.Error()
	} else if result != nil {
		data, merr := json.Marshal(result)
		if merr != nil {
			resp.Error = fmt.Sprintf("pluginrpc: marshaling result of %q: %v", msg.Name, merr)
		} else {
			resp.Result = data
		}
	}
	h.writeMessage(resp)
}

func (h *Host) relayStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	for scanner.Scan() {
		h.logger.Printf("pluginrpc: %s: %s", h.name, scanner.Text())
	}
}

// pingLoop is the "soft hang" half of crash isolation: a plugin process
// that's still alive but stuck (deadlocked, infinite loop, blocked on
// something) never crashes on its own — only a heartbeat can catch it.
func (h *Host) pingLoop() {
	ticker := time.NewTicker(h.pingInterval)
	defer ticker.Stop()
	missed := 0
	for {
		select {
		case <-h.exited:
			return
		case <-ticker.C:
			if atomic.LoadInt32(&h.closed) != 0 {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), h.callTimeout)
			_, err := h.doCall(ctx, TypePing, "", nil)
			cancel()
			if err != nil {
				missed++
				if missed >= h.maxMissedPings {
					h.fail(fmt.Errorf("plugin %q missed %d consecutive heartbeats — treating as hung", h.name, missed))
					h.Kill()
					return
				}
			} else {
				missed = 0
			}
		}
	}
}

// Call issues a "call"-type request (used internally for pings; a real
// third-party plugin's own outbound daemon-event delivery uses SendEvent
// instead) and blocks for a response or the configured timeout.
func (h *Host) doCall(ctx context.Context, msgType, name string, args interface{}) (json.RawMessage, error) {
	if atomic.LoadInt32(&h.dead) != 0 {
		return nil, fmt.Errorf("pluginrpc: %s: plugin process is not running", h.name)
	}
	var raw json.RawMessage
	if args != nil {
		data, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		raw = data
	}

	h.mu.Lock()
	id := h.nextID + 1
	h.nextID = id
	ch := make(chan Message, 1)
	h.pending[id] = ch
	h.mu.Unlock()

	if err := h.writeMessage(Message{ID: id, Type: msgType, Name: name, Args: raw}); err != nil {
		h.mu.Lock()
		delete(h.pending, id)
		h.mu.Unlock()
		return nil, err
	}

	select {
	case msg, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("pluginrpc: %s: plugin process exited while waiting for a response", h.name)
		}
		if msg.Error != "" {
			return nil, fmt.Errorf("pluginrpc: %s: %s", h.name, msg.Error)
		}
		return msg.Result, nil
	case <-ctx.Done():
		h.mu.Lock()
		delete(h.pending, id)
		h.mu.Unlock()
		return nil, fmt.Errorf("pluginrpc: %s: timed out waiting for a response to %q", h.name, name)
	}
}

// SendEvent delivers a daemon event to the plugin, fire-and-forget —
// the same semantics as pluginmanager.EventHandler.HandleEvent, just
// across a process boundary instead of a goroutine boundary.
func (h *Host) SendEvent(name string, args interface{}) error {
	if atomic.LoadInt32(&h.dead) != 0 {
		return fmt.Errorf("pluginrpc: %s: plugin process is not running", h.name)
	}
	var raw json.RawMessage
	if args != nil {
		data, err := json.Marshal(args)
		if err != nil {
			return err
		}
		raw = data
	}
	h.mu.Lock()
	id := h.nextID + 1
	h.nextID = id
	h.mu.Unlock()
	return h.writeMessage(Message{ID: id, Type: TypeEvent, Name: name, Args: raw})
}

func (h *Host) writeMessage(msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if len(data) > maxLineSize {
		return fmt.Errorf("pluginrpc: %s: outgoing message for %q exceeds max size (%d > %d)", h.name, msg.Name, len(data), maxLineSize)
	}
	h.stdinMu.Lock()
	defer h.stdinMu.Unlock()
	if _, err := h.stdinW.Write(append(data, '\n')); err != nil {
		return err
	}
	return h.stdinW.Flush()
}

// Alive reports whether the subprocess is (as far as this host knows)
// still running and responsive.
func (h *Host) Alive() bool {
	return atomic.LoadInt32(&h.dead) == 0 && atomic.LoadInt32(&h.closed) == 0
}

// Close gracefully stops the subprocess (close stdin, wait briefly, kill
// if needed) — an intentional shutdown, not treated as a failure.
func (h *Host) Close() error {
	if !atomic.CompareAndSwapInt32(&h.closed, 0, 1) {
		return nil
	}
	_ = h.stdinF.Close()
	select {
	case <-h.exited:
		return nil
	case <-time.After(5 * time.Second):
		return h.Kill()
	}
}

// Kill forcefully terminates the subprocess.
func (h *Host) Kill() error {
	if h.cmd.Process == nil {
		return nil
	}
	err := h.cmd.Process.Kill()
	select {
	case <-h.exited:
	case <-time.After(2 * time.Second):
	}
	return err
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
