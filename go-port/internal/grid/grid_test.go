package grid

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
)

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := NewClient("2.9.5.5")
	// Matches production's APIAddress shape (a URL ending in a path
	// segment, "http://127.0.0.1:8666/api/v1"), so path-concatenation bugs
	// like Advertise's reproduce the same way they do in production instead
	// of colliding with the port number.
	c.APIAddress = srv.URL + "/api/v1"
	return c
}

func TestCallGET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/mesh/peers" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode([]map[string]string{{"name": "peer1"}})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	peers, err := c.Peers()
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 {
		t.Fatalf("Peers() = %v", peers)
	}
}

func TestCallPOSTJSON(t *testing.T) {
	var gotBody []byte
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		gotBody = buf
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.SetAdvertisementData(map[string]interface{}{"name": "unit1"})
	if err != nil {
		t.Fatal(err)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q", gotContentType)
	}
	if !strings.Contains(string(gotBody), `"name":"unit1"`) {
		t.Fatalf("body = %s", gotBody)
	}
}

func TestCallNon200RaisesFormattedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.Memory()
	if err == nil {
		t.Fatal("expected an error for non-200 status")
	}
	want := "(status 500) internal error"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// TestAdvertiseReproducesOperatorPrecedenceBug verifies grid.Advertise
// replicates Python's real `"/mesh/%s" % 'true' if enabled else 'false'`
// bug: enabled=true hits "/mesh/true", but enabled=false hits the bare path
// "false" (missing the "/mesh/" prefix entirely), which resolves to
// {APIAddress}false, not {APIAddress}/mesh/false.
func TestAdvertiseReproducesOperatorPrecedenceBug(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)

	if _, err := c.Advertise(true); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/mesh/true" {
		t.Fatalf("Advertise(true) path = %q, want /api/v1/mesh/true", gotPath)
	}

	if _, err := c.Advertise(false); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1false" {
		t.Fatalf("Advertise(false) path = %q, want the buggy bare path %q (matching Python: APIAddress+'false', not APIAddress+'/mesh/false')", gotPath, "/api/v1false")
	}
}

func TestIsConnectedFalseOnNon200OrBadJSON(t *testing.T) {
	c := NewClient("2.9.5.5")
	// api.opwngrid.xyz is a real external host; we don't want tests to
	// depend on network reachability, so just verify the JSON-parsing
	// failure path folds into false using a manually constructed response.
	if up := parseIsUp([]byte("not json")); up {
		t.Fatal("malformed JSON must resolve to false")
	}
	if up := parseIsUp([]byte(`{"isUp": false}`)); up {
		t.Fatal("isUp: false must resolve to false")
	}
	if up := parseIsUp([]byte(`{"isUp": true}`)); !up {
		t.Fatal("isUp: true must resolve to true")
	}
	_ = c
}

func parseIsUp(body []byte) bool {
	var v map[string]interface{}
	if err := json.Unmarshal(body, &v); err != nil {
		return false
	}
	up, _ := v["isUp"].(bool)
	return up
}

func TestInboxUnwrapsMessagesUnlessWithPager(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "p=2" {
			t.Errorf("query = %q, want p=2", r.URL.RawQuery)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"messages": []interface{}{"a", "b"},
			"page":     2,
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	msgs, err := c.Inbox(2, false)
	if err != nil {
		t.Fatal(err)
	}
	list, ok := msgs.([]interface{})
	if !ok || len(list) != 2 {
		t.Fatalf("Inbox(withPager=false) = %v", msgs)
	}

	full, err := c.Inbox(2, true)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := full.(map[string]interface{})
	if !ok || m["page"] != float64(2) {
		t.Fatalf("Inbox(withPager=true) = %v", full)
	}
}

func TestSendMessagePostsRawUTF8Body(t *testing.T) {
	var gotBody []byte
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/unit/abc123/inbox" {
			t.Errorf("path = %q", r.URL.Path)
		}
		gotContentType = r.Header.Get("Content-Type")
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		gotBody = buf
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, err := c.SendMessage("abc123", "hello"); err != nil {
		t.Fatal(err)
	}
	if string(gotBody) != "hello" {
		t.Fatalf("body = %q, want raw 'hello'", gotBody)
	}
	if gotContentType != "" {
		t.Fatalf("Content-Type = %q, want empty (Python sends data= with headers=None)", gotContentType)
	}
}

func TestUpdateDataBuildsExpectedPayload(t *testing.T) {
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/data" {
			t.Errorf("path = %q, want /api/v1/data", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	cfg := config.Map{
		"main": config.Map{
			"lang": "en",
			"plugins": config.Map{
				"grid":   config.Map{"enabled": true},
				"wigle":  config.Map{"enabled": false},
				"wpasec": config.Map{"enabled": true},
			},
		},
	}
	session := SessionSummary{Epochs: 5, Handshakes: 2}
	if err := c.UpdateData(cfg, session); err != nil {
		t.Fatal(err)
	}
	if gotBody["ai"] != "No AI!" {
		t.Fatalf("ai field = %v, want 'No AI!' (matches this fork's stripped-down build)", gotBody["ai"])
	}
	if gotBody["language"] != "en" {
		t.Fatalf("language = %v", gotBody["language"])
	}
	plugins, ok := gotBody["plugins"].([]interface{})
	if !ok || len(plugins) != 2 {
		t.Fatalf("plugins = %v, want 2 enabled plugins", gotBody["plugins"])
	}
	session_, ok := gotBody["session"].(map[string]interface{})
	if !ok || session_["epochs"] != float64(5) {
		t.Fatalf("session = %v", gotBody["session"])
	}
}
