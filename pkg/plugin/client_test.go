package plugin_test

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/pkg/plugin"
)

type wireMessage struct {
	ID     int64           `json:"id,omitempty"`
	Type   string          `json:"type"`
	Name   string          `json:"name,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func TestTypedClientMethodsUseDocumentedWireShape(t *testing.T) {
	pluginConn, hostConn := net.Pipe()
	client := plugin.NewClient(pluginConn, pluginConn)
	runDone := make(chan error, 1)
	go func() { runDone <- client.Run() }()

	expectedCalls := []string{
		"Log.Printf",
		"Agent.Run",
		"Agent.Session",
		"Agent.SupportedChannels",
		"Agent.ResetHistory",
		"View.Set",
		"View.Update",
		"Exec.Run",
		"Clock.Now",
	}
	results := map[string]json.RawMessage{
		"Agent.Run":               json.RawMessage(`{"ok":true}`),
		"Agent.Session":           json.RawMessage(`{"wifi":true}`),
		"Agent.SupportedChannels": json.RawMessage(`[1,6,11]`),
		"Exec.Run":                json.RawMessage(`{"output":"done"}`),
		"Clock.Now":               json.RawMessage(`{"unix_nano":123456789}`),
	}
	serverDone := make(chan error, 1)
	go func() {
		decoder := json.NewDecoder(hostConn)
		encoder := json.NewEncoder(hostConn)
		for _, expected := range expectedCalls {
			var call wireMessage
			if err := decoder.Decode(&call); err != nil {
				serverDone <- err
				return
			}
			if call.Type != "call" || call.Name != expected {
				serverDone <- &unexpectedCallError{got: call.Name, want: expected}
				return
			}
			if err := encoder.Encode(wireMessage{
				ID:     call.ID,
				Type:   "response",
				Result: results[expected],
			}); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Log(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.AgentRun(ctx, "wifi.recon on", false); err != nil {
		t.Fatal(err)
	}
	if _, err := client.AgentSession(ctx, "wifi"); err != nil {
		t.Fatal(err)
	}
	channels, err := client.AgentSupportedChannels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 3 || channels[1] != 6 {
		t.Fatalf("channels = %v", channels)
	}
	if err := client.AgentResetHistory(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.ViewSet(ctx, "status", "ready"); err != nil {
		t.Fatal(err)
	}
	if err := client.ViewUpdate(ctx, true); err != nil {
		t.Fatal(err)
	}
	output, err := client.Exec(ctx, "printf", "done")
	if err != nil || string(output) != "done" {
		t.Fatalf("Exec output=%q err=%v", output, err)
	}
	now, err := client.Now(ctx)
	if err != nil || now.UnixNano() != 123456789 {
		t.Fatalf("Now=%v err=%v", now, err)
	}

	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	_ = hostConn.Close()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
}

type unexpectedCallError struct {
	got  string
	want string
}

func (e *unexpectedCallError) Error() string {
	return "unexpected RPC call: got " + e.got + ", want " + e.want
}
