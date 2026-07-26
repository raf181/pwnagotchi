package pluginrpc

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// pipePair gives a Host-side (server) and Client-side (plugin) pair of
// connected in-memory pipes, for testing the SDK's Client type without a
// real subprocess (real subprocess coverage lives in host_test.go).
func pipePair() (hostIn io.Reader, hostOut io.Writer, clientIn io.Reader, clientOut io.Writer) {
	r1, w1 := io.Pipe() // host -> client
	r2, w2 := io.Pipe() // client -> host
	return r2, w1, r1, w2
}

func TestClientRepliesPongToPing(t *testing.T) {
	hostIn, hostOut, clientIn, clientOut := pipePair()
	client := NewClient(clientIn, clientOut)
	done := make(chan struct{})
	go func() { client.Run(); close(done) }()

	enc := json.NewEncoder(hostOut)
	_ = enc.Encode(Message{ID: 1, Type: TypePing})

	dec := json.NewDecoder(hostIn)
	var resp Message
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Type != TypePong || resp.ID != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestClientDispatchesEventToHandler(t *testing.T) {
	hostIn, hostOut, clientIn, clientOut := pipePair()
	_ = hostIn
	client := NewClient(clientIn, clientOut)

	received := make(chan string, 1)
	client.OnEvent(func(event string, args json.RawMessage) {
		received <- event
	})
	go client.Run()

	enc := json.NewEncoder(hostOut)
	_ = enc.Encode(Message{ID: 1, Type: TypeEvent, Name: "wifi_update"})

	select {
	case ev := <-received:
		if ev != "wifi_update" {
			t.Fatalf("got event %q", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event dispatch")
	}
}

func TestClientEventHandlerCanCallCapability(t *testing.T) {
	hostIn, hostOut, clientIn, clientOut := pipePair()
	client := NewClient(clientIn, clientOut)

	callDone := make(chan error, 1)
	client.OnEvent(func(event string, args json.RawMessage) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := client.CallContext(ctx, "Agent.Run", map[string]interface{}{
			"cmd":     "echo hi",
			"verbose": false,
		})
		callDone <- err
	})
	go client.Run()

	hostEncoder := json.NewEncoder(hostOut)
	hostDecoder := json.NewDecoder(hostIn)
	if err := hostEncoder.Encode(Message{Type: TypeEvent, Name: "ready"}); err != nil {
		t.Fatal(err)
	}

	var call Message
	if err := hostDecoder.Decode(&call); err != nil {
		t.Fatalf("decode capability call: %v", err)
	}
	if call.Type != TypeCall || call.Name != "Agent.Run" {
		t.Fatalf("unexpected capability call: %+v", call)
	}
	if err := hostEncoder.Encode(Message{
		ID:     call.ID,
		Type:   TypeResponse,
		Result: json.RawMessage(`{"ok":true}`),
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-callDone:
		if err != nil {
			t.Fatalf("event capability call failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event handler deadlocked waiting for its capability response")
	}
}

func TestClientEventQueueIsBounded(t *testing.T) {
	client := NewClient(strings.NewReader(""), io.Discard)
	for i := 0; i < maxClientEventQueue*4; i++ {
		client.enqueueEvent(Message{Type: TypeEvent, Name: "event"})
	}
	client.eventMu.Lock()
	defer client.eventMu.Unlock()
	if got := len(client.eventQueue); got != maxClientEventQueue {
		t.Fatalf("event queue length = %d, want %d", got, maxClientEventQueue)
	}
}

func TestClientRejectsOversizedOutgoingMessage(t *testing.T) {
	client := NewClient(strings.NewReader(""), io.Discard)
	err := client.write(Message{Type: TypeEvent, Name: strings.Repeat("x", maxLineSize)})
	if err == nil || !strings.Contains(err.Error(), "exceeds max size") {
		t.Fatalf("write error = %v, want size rejection", err)
	}
}
