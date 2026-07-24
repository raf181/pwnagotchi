package pluginrpc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

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

	handler func(event string, args json.RawMessage)
}

// NewClient wraps the given reader/writer (production: os.Stdin/
// os.Stdout; tests: in-memory pipes) as a plugin-side protocol client.
func NewClient(r io.Reader, w io.Writer) *Client {
	return &Client{
		in:      bufio.NewReaderSize(r, 64*1024),
		out:     bufio.NewWriter(w),
		pending: map[int64]chan Message{},
	}
}

// NewStdioClient is the convenience constructor a real plugin's main()
// uses.
func NewStdioClient() *Client { return NewClient(os.Stdin, os.Stdout) }

// OnEvent registers the single callback invoked for every "event"
// message the daemon sends (loaded, config_changed, wifi_update,
// handshake, epoch, ui_update, ...). Matches pluginmanager.EventHandler's
// own name+raw-args shape.
func (c *Client) OnEvent(handler func(event string, args json.RawMessage)) {
	c.handler = handler
}

// Call invokes a daemon capability method (e.g. "Agent.Run", "View.Set")
// and blocks for its result.
func (c *Client) Call(method string, args interface{}) (json.RawMessage, error) {
	var raw json.RawMessage
	if args != nil {
		data, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		raw = data
	}

	c.mu.Lock()
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

	msg, ok := <-ch
	if !ok {
		return nil, fmt.Errorf("pluginrpc: connection closed while waiting for %q", method)
	}
	if msg.Error != "" {
		return nil, fmt.Errorf("pluginrpc: %s", msg.Error)
	}
	return msg.Result, nil
}

func (c *Client) write(msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.outMu.Lock()
	defer c.outMu.Unlock()
	if _, err := c.out.Write(append(data, '\n')); err != nil {
		return err
	}
	return c.out.Flush()
}

// Run reads Messages until EOF/error, dispatching "event"s to the
// registered handler and answering "ping" with "pong" (the heartbeat
// contract Host.pingLoop expects). Blocks until the connection closes;
// a real plugin's main() calls this last.
func (c *Client) Run() error {
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
			if c.handler != nil {
				c.handler(msg.Name, msg.Args)
			}
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
	c.mu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	return scanner.Err()
}
