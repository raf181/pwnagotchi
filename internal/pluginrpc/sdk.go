package pluginrpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

const maxClientEventQueue = 64

// Client is the plugin-side half of the protocol: what a third-party Go
// plugin executable links against to speak to the daemon over its own
// stdin/stdout. A real plugin's main() constructs one, registers event
// handlers, and calls Run() (which blocks, reading events, until stdin
// closes) — the mirror image of Host on the daemon side.
type Client struct {
	in  *bufio.Reader
	out *bufio.Writer

	mu      sync.Mutex
	outMu   sync.Mutex
	nextID  int64
	pending map[int64]chan Message
	closed  bool

	handlerMu sync.RWMutex
	handler   func(event string, args json.RawMessage)

	eventMu     sync.Mutex
	eventCond   *sync.Cond
	eventQueue  []Message
	eventClosed bool
}

// NewClient wraps the given reader/writer (production: os.Stdin/
// os.Stdout; tests: in-memory pipes) as a plugin-side protocol client.
func NewClient(r io.Reader, w io.Writer) *Client {
	c := &Client{
		in:      bufio.NewReaderSize(r, 64*1024),
		out:     bufio.NewWriter(w),
		pending: map[int64]chan Message{},
	}
	c.eventCond = sync.NewCond(&c.eventMu)
	return c
}

// NewStdioClient is the convenience constructor a real plugin's main()
// uses.
func NewStdioClient() *Client { return NewClient(os.Stdin, os.Stdout) }

// OnEvent registers the single callback invoked for every "event"
// message the daemon sends (loaded, config_changed, wifi_update,
// handshake, epoch, ui_update, ...). Matches pluginmanager.EventHandler's
// own name+raw-args shape.
func (c *Client) OnEvent(handler func(event string, args json.RawMessage)) {
	c.handlerMu.Lock()
	defer c.handlerMu.Unlock()
	c.handler = handler
}

// Call invokes a daemon capability method (e.g. "Agent.Run", "View.Set")
// and blocks for its result.
func (c *Client) Call(method string, args interface{}) (json.RawMessage, error) {
	return c.CallContext(context.Background(), method, args)
}

// CallContext invokes a daemon capability method and lets callers bound
// the wait. Event handlers should prefer this form so a broken daemon-side
// capability cannot leave the plugin blocked forever.
func (c *Client) CallContext(ctx context.Context, method string, args interface{}) (json.RawMessage, error) {
	var raw json.RawMessage
	if args != nil {
		data, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		raw = data
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("pluginrpc: connection closed before calling %q", method)
	}
	id := c.nextID + 1
	c.nextID = id
	ch := make(chan Message, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.write(Message{ID: id, Type: TypeCall, Name: method, Args: raw}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case msg, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("pluginrpc: connection closed while waiting for %q", method)
		}
		if msg.Error != "" {
			return nil, fmt.Errorf("pluginrpc: %s", msg.Error)
		}
		return msg.Result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("pluginrpc: calling %q: %w", method, ctx.Err())
	}
}

func (c *Client) write(msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if len(data) > maxLineSize {
		return fmt.Errorf("pluginrpc: outgoing message for %q exceeds max size (%d > %d)", msg.Name, len(data), maxLineSize)
	}
	c.outMu.Lock()
	defer c.outMu.Unlock()
	if _, err := c.out.Write(append(data, '\n')); err != nil {
		return err
	}
	return c.out.Flush()
}

// Run reads Messages until EOF/error, dispatching "event"s to a dedicated
// serial worker and answering "ping" with "pong" (the heartbeat contract
// Host.pingLoop expects). The worker is separate from the reader because an
// event handler commonly calls back into the daemon; handling the callback
// on this reader goroutine would deadlock while Call waited for a response
// that only this same goroutine could read.
func (c *Client) Run() (runErr error) {
	eventDone := make(chan struct{})
	go func() {
		defer close(eventDone)
		c.runEvents()
	}()
	defer func() {
		c.mu.Lock()
		c.closed = true
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()

		c.eventMu.Lock()
		c.eventClosed = true
		c.eventCond.Broadcast()
		c.eventMu.Unlock()
		<-eventDone
	}()

	scanner := bufio.NewScanner(c.in)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case TypeEvent:
			c.enqueueEvent(msg)
		case TypePing:
			_ = c.write(Message{ID: msg.ID, Type: TypePong})
		case TypeResponse:
			c.mu.Lock()
			ch, ok := c.pending[msg.ID]
			if ok {
				delete(c.pending, msg.ID)
			}
			c.mu.Unlock()
			if ok {
				ch <- msg
			}
		}
	}

	return scanner.Err()
}

func (c *Client) enqueueEvent(msg Message) {
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	if c.eventClosed {
		return
	}
	if len(c.eventQueue) >= maxClientEventQueue {
		return
	}
	c.eventQueue = append(c.eventQueue, msg)
	c.eventCond.Signal()
}

func (c *Client) runEvents() {
	for {
		c.eventMu.Lock()
		for len(c.eventQueue) == 0 && !c.eventClosed {
			c.eventCond.Wait()
		}
		if len(c.eventQueue) == 0 && c.eventClosed {
			c.eventMu.Unlock()
			return
		}
		msg := c.eventQueue[0]
		c.eventQueue[0] = Message{}
		c.eventQueue = c.eventQueue[1:]
		c.eventMu.Unlock()

		c.handlerMu.RLock()
		handler := c.handler
		c.handlerMu.RUnlock()
		if handler != nil {
			handler(msg.Name, msg.Args)
		}
	}
}
