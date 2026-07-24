package pluginrpc

import (
	"encoding/json"
	"io"
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
