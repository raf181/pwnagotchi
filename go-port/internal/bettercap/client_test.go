package bettercap

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(u.Hostname(), "http", port, "user", "pass")
	c.randSleep = func() float64 { return 0 } // deterministic, minimal backoff in tests
	return c
}

func TestClientDefaults(t *testing.T) {
	c := NewClient("", "", 0, "", "")
	if c.Hostname != "localhost" || c.Scheme != "http" || c.Port != 8081 || c.Username != "user" || c.Password != "pass" {
		t.Fatalf("defaults not applied: %+v", c)
	}
	if c.URL != "http://localhost:8081/api" {
		t.Fatalf("URL = %q", c.URL)
	}
	if c.WebSocket != "ws://user:pass@localhost:8081/api" {
		t.Fatalf("WebSocket = %q", c.WebSocket)
	}
}

func TestSessionSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "user" || pass != "pass" {
			t.Errorf("missing/incorrect basic auth")
		}
		json.NewEncoder(w).Encode(map[string]string{"wifi": "on"})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.Session("")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	m, ok := got.(map[string]interface{})
	if !ok || m["wifi"] != "on" {
		t.Fatalf("Session result = %v", got)
	}
}

func TestSessionSubDict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session/wifi" {
			t.Errorf("unexpected path %q, want /api/session/wifi", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]string{"aps": "none"})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, err := c.Session("session/wifi"); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeNonJSONWith200ReturnsRawTextNoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.Session("")
	if err != nil {
		t.Fatalf("expected no error for a 200 with invalid JSON body (Python returns r.text), got %v", err)
	}
	if got != "not json" {
		t.Fatalf("got = %v, want raw text %q", got, "not json")
	}
}

func TestDecodeErrorStatusReturnsFormattedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte("  bad request text  "))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.Session("")
	if err == nil {
		t.Fatal("expected an error for a non-200 status")
	}
	want := "error 400: bad request text"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q (Python: 'error %%d: %%s' %% (status_code, text.strip()))", err.Error(), want)
	}
}

func TestRunPostsCommandAndDecodes(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/session" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		gotBody = buf
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.Run("wifi.recon on", true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(string(gotBody), `"cmd":"wifi.recon on"`) {
		t.Fatalf("request body = %s, missing cmd field", gotBody)
	}
	m, ok := got.(map[string]interface{})
	if !ok || m["ok"] != true {
		t.Fatalf("Run result = %v", got)
	}
}

// flakyThenOKTransport fails the first N round trips with a *net.OpError
// (mirroring a refused/reset connection) before delegating to a real
// transport, so TestRunRetriesOnConnectionErrorThenSucceeds can verify
// Run's retry-on-connection-error loop without real socket/port timing.
type flakyThenOKTransport struct {
	failuresLeft int
	inner        http.RoundTripper
}

func (f *flakyThenOKTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if f.failuresLeft > 0 {
		f.failuresLeft--
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: net.ErrClosed}
	}
	return f.inner.RoundTrip(req)
}

func TestRunRetriesOnConnectionErrorThenSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	c.HTTPClient = &http.Client{Transport: &flakyThenOKTransport{failuresLeft: 2, inner: http.DefaultTransport}}

	got, err := c.Run("wifi.recon on", true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if m, ok := got.(map[string]interface{}); !ok || m["ok"] != true {
		t.Fatalf("Run result = %v", got)
	}
}

func TestRunDoesNotRetryOnNonConnectionError(t *testing.T) {
	c := newTestClient(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))
	c.HTTPClient = &http.Client{Transport: &alwaysErrTransport{err: errTimeoutLike{}}}

	_, err := c.Run("wifi.recon on", true)
	if err == nil {
		t.Fatal("expected Run to propagate a non-connection error rather than retry forever")
	}
}

type alwaysErrTransport struct{ err error }

func (a *alwaysErrTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, a.err }

type errTimeoutLike struct{}

func (errTimeoutLike) Error() string { return "simulated non-connection error" }

func TestStartWebsocketReceivesMessages(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.WriteMessage(websocket.TextMessage, []byte(`{"tag":"wifi.client.new"}`))
		time.Sleep(50 * time.Millisecond)
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	c := NewClient(u.Hostname(), "http", port, "user", "pass")
	c.WebSocket = "ws://user:pass@" + u.Host

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	received := make(chan string, 1)
	err := c.StartWebsocket(ctx, func(msg []byte) error {
		select {
		case received <- string(msg):
		default:
		}
		return nil
	})
	if err != nil && err != context.DeadlineExceeded {
		t.Fatalf("StartWebsocket returned unexpected error: %v", err)
	}

	select {
	case msg := <-received:
		if msg != `{"tag":"wifi.client.new"}` {
			t.Fatalf("received = %q", msg)
		}
	default:
		t.Fatal("expected to receive a message from the websocket")
	}
}
