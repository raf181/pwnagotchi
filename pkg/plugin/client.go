// Package plugin is the public SDK for separately compiled Pwnagotchi
// plugins. A plugin process communicates with the daemon over stdin/stdout;
// ordinary log output must therefore go to stderr or through Client.Log.
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/pluginrpc"
)

// Client is the plugin-side connection to the Pwnagotchi daemon.
type Client struct {
	rpc *pluginrpc.Client
}

// NewClient creates a client over an arbitrary transport. Plugin binaries
// normally use NewStdioClient; this constructor is useful for tests.
func NewClient(r io.Reader, w io.Writer) *Client {
	return &Client{rpc: pluginrpc.NewClient(r, w)}
}

// NewStdioClient creates the client used by a plugin executable's main.
func NewStdioClient() *Client {
	return &Client{rpc: pluginrpc.NewStdioClient()}
}

// OnEvent registers the serial callback for daemon events.
func (c *Client) OnEvent(handler func(event string, args json.RawMessage)) {
	c.rpc.OnEvent(handler)
}

// Call invokes a capability method without a caller-supplied timeout.
// Prefer CallContext in event handlers.
func (c *Client) Call(method string, args interface{}) (json.RawMessage, error) {
	return c.rpc.Call(method, args)
}

// CallContext invokes a capability method with cancellation or a deadline.
func (c *Client) CallContext(ctx context.Context, method string, args interface{}) (json.RawMessage, error) {
	return c.rpc.CallContext(ctx, method, args)
}

// Run processes events and responses until the daemon closes the connection.
func (c *Client) Run() error {
	return c.rpc.Run()
}

// Log writes one line through the daemon's plugin-scoped logger.
func (c *Client) Log(ctx context.Context, message string) error {
	_, err := c.CallContext(ctx, "Log.Printf", map[string]string{"message": message})
	return err
}

// AgentRun executes one bettercap command through the daemon.
func (c *Client) AgentRun(ctx context.Context, command string, verboseErrors bool) (interface{}, error) {
	raw, err := c.CallContext(ctx, "Agent.Run", map[string]interface{}{
		"cmd":     command,
		"verbose": verboseErrors,
	})
	if err != nil {
		return nil, err
	}
	var result interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("plugin: decoding Agent.Run response: %w", err)
	}
	return result, nil
}

// AgentSession retrieves a bettercap session through the daemon.
func (c *Client) AgentSession(ctx context.Context, session string) (interface{}, error) {
	raw, err := c.CallContext(ctx, "Agent.Session", map[string]string{"session": session})
	if err != nil {
		return nil, err
	}
	var result interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("plugin: decoding Agent.Session response: %w", err)
	}
	return result, nil
}

// AgentSupportedChannels returns the channels available on the unit's
// configured wireless interface.
func (c *Client) AgentSupportedChannels(ctx context.Context) ([]int, error) {
	raw, err := c.CallContext(ctx, "Agent.SupportedChannels", nil)
	if err != nil {
		return nil, err
	}
	var channels []int
	if err := json.Unmarshal(raw, &channels); err != nil {
		return nil, fmt.Errorf("plugin: decoding Agent.SupportedChannels response: %w", err)
	}
	return channels, nil
}

// AgentResetHistory clears the daemon's per-target interaction history.
func (c *Client) AgentResetHistory(ctx context.Context) error {
	_, err := c.CallContext(ctx, "Agent.ResetHistory", nil)
	return err
}

// ViewSet updates one display state value.
func (c *Client) ViewSet(ctx context.Context, key, value string) error {
	_, err := c.CallContext(ctx, "View.Set", map[string]string{"key": key, "value": value})
	return err
}

// ViewUpdate asks the display to render its current state.
func (c *Client) ViewUpdate(ctx context.Context, force bool) error {
	_, err := c.CallContext(ctx, "View.Update", map[string]bool{"force": force})
	return err
}

// Exec runs an argument-vector command through the daemon and returns its
// combined stdout/stderr.
func (c *Client) Exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	raw, err := c.CallContext(ctx, "Exec.Run", map[string]interface{}{
		"name": name,
		"args": args,
	})
	if err != nil {
		return nil, err
	}
	var result struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("plugin: decoding Exec.Run response: %w", err)
	}
	return []byte(result.Output), nil
}

// Now returns the daemon's current clock value.
func (c *Client) Now(ctx context.Context) (time.Time, error) {
	raw, err := c.CallContext(ctx, "Clock.Now", nil)
	if err != nil {
		return time.Time{}, err
	}
	var result struct {
		UnixNano int64 `json:"unix_nano"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return time.Time{}, fmt.Errorf("plugin: decoding Clock.Now response: %w", err)
	}
	return time.Unix(0, result.UnixNano), nil
}

// Stderr is the safe destination for a plugin's own diagnostic output.
// Stdout is reserved for the RPC protocol.
var Stderr io.Writer = os.Stderr
