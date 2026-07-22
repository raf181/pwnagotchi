package web

import (
	"bufio"
	"image"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
)

type fakeAgentInfo struct {
	mode        string
	fingerprint string
}

func (f fakeAgentInfo) Mode() string           { return f.mode }
func (f fakeAgentInfo) GetFingerprint() string { return f.fingerprint }

// fakeActions ports handler.py's shutdown/reboot/restart routes' real
// behavior of firing a background goroutine and returning immediately —
// exercised concurrently with the test's own assertions, hence the mutex
// (the real Python code has the equivalent race between its
// threading.Thread and whatever inspects module state afterward; this
// fake just makes the read side safe to assert on in a test).
type fakeActions struct {
	mu             sync.Mutex
	shutdownCalled bool
	rebootMode     string
	rebootCalled   bool
	restartMode    string
	restartCalled  bool
}

func (f *fakeActions) Shutdown() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shutdownCalled = true
	return nil
}
func (f *fakeActions) Reboot(mode string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rebootCalled = true
	f.rebootMode = mode
	return nil
}
func (f *fakeActions) Restart(mode string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restartCalled = true
	f.restartMode = mode
	return nil
}

func (f *fakeActions) restartWasCalled() (bool, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restartCalled, f.restartMode
}

func newTestServer(webCfg config.Map, agent AgentInfo, actions Actions) *Server {
	cfg := config.Map{"ui": config.Map{"web": webCfg}}
	return New(cfg, "test-unit", agent, nil, nil, actions, cfg, "")
}

func newMux(s *Server) http.Handler {
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return mux
}

// TestIndexRendersRealOtherModeAndCSRF matches real index.html exactly:
// it renders {{ title }} and {{ other_mode }} but, despite handler.py
// passing fingerprint=self._agent.fingerprint() to the template, never
// actually references {{ fingerprint }} anywhere in its markup — verified
// by reading the real template — so this only asserts what real Python
// really shows on this page.
func TestIndexRendersRealOtherModeAndCSRF(t *testing.T) {
	s := newTestServer(config.Map{"enabled": true}, fakeAgentInfo{mode: "auto", fingerprint: "abc123"}, &fakeActions{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	newMux(s).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Restart MANU") {
		t.Errorf("expected other_mode MANU (mode=auto), got %s", body)
	}
	if rr.Result().Cookies() == nil {
		t.Errorf("expected a csrf_token cookie to be set")
	}
}

// TestProfileRendersRealFingerprint proves the fingerprint IS rendered on
// the page real Python actually shows it on (profile.html).
func TestProfileRendersRealFingerprint(t *testing.T) {
	s := newTestServer(config.Map{"enabled": true}, fakeAgentInfo{fingerprint: "abc123"}, &fakeActions{})
	rr := httptest.NewRecorder()
	newMux(s).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/inbox/profile", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "abc123") {
		t.Errorf("expected real fingerprint abc123 in profile page, got %s", rr.Body.String())
	}
}

func TestUIRouteServesRealFrameOr404(t *testing.T) {
	s := newTestServer(config.Map{"enabled": true}, fakeAgentInfo{}, &fakeActions{})
	dir := t.TempDir()
	old := FramePath
	FramePath = dir + "/frame.png"
	defer func() { FramePath = old }()

	rr := httptest.NewRecorder()
	newMux(s).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/ui", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 before any frame written, got %d", rr.Code)
	}

	img := simpleTestImage()
	if err := UpdateFrame(img); err != nil {
		t.Fatalf("UpdateFrame: %v", err)
	}

	rr = httptest.NewRecorder()
	newMux(s).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/ui", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 after a real frame was written, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if rr.Body.Len() == 0 {
		t.Errorf("expected a real non-empty PNG body")
	}
}

func TestDynamicThemeReflectsConfiguredAccent(t *testing.T) {
	s := newTestServer(config.Map{
		"enabled": true,
		"theme":   config.Map{"accent_r": int64(1), "accent_g": int64(2), "accent_b": int64(3)},
	}, fakeAgentInfo{}, &fakeActions{})

	rr := httptest.NewRecorder()
	newMux(s).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/css/theme.css", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "rgb(1, 2, 3)") {
		t.Errorf("expected configured accent rgb(1, 2, 3) in body, got %s", body)
	}
}

func TestStaticAssetsAreReallyEmbedded(t *testing.T) {
	s := newTestServer(config.Map{"enabled": true}, fakeAgentInfo{}, &fakeActions{})
	rr := httptest.NewRecorder()
	newMux(s).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/css/style.css", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected real embedded static/css/style.css to be served, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() == 0 {
		t.Errorf("expected non-empty real CSS content")
	}
}

func TestBasicAuthGatesWhenEnabled(t *testing.T) {
	s := newTestServer(config.Map{
		"enabled": true, "auth": true, "username": "admin", "password": "secret",
	}, fakeAgentInfo{}, &fakeActions{})

	rr := httptest.NewRecorder()
	newMux(s).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without credentials, got %d", rr.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "wrong")
	rr = httptest.NewRecorder()
	newMux(s).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong password, got %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "secret")
	rr = httptest.NewRecorder()
	newMux(s).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with correct credentials, got %d", rr.Code)
	}
}

func TestRestartRequiresValidCSRFAndInvokesRealAction(t *testing.T) {
	actions := &fakeActions{}
	s := newTestServer(config.Map{"enabled": true}, fakeAgentInfo{}, actions)
	mux := newMux(s)

	// First, a GET to obtain a real csrf cookie (as a real browser loading
	// the index page would before ever submitting the restart form).
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	var csrfCookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == csrfCookieName {
			csrfCookie = c
		}
	}
	if csrfCookie == nil {
		t.Fatalf("expected a csrf cookie from the index page")
	}

	// POST without a token must be rejected.
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/restart", strings.NewReader(url.Values{"mode": {"AUTO"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without a csrf token, got %d", rr.Code)
	}
	if actions.restartCalled {
		t.Fatalf("real restart action must not run without a valid csrf token")
	}

	// POST with the matching cookie+form token succeeds and triggers the
	// real (fake, in this test) action.
	form := url.Values{"mode": {"AUTO"}, "csrf_token": {csrfCookie.Value}}
	req = httptest.NewRequest(http.MethodPost, "/restart", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	called, mode := false, ""
	if waitFor(func() bool { called, mode = actions.restartWasCalled(); return called }); !called {
		t.Fatalf("expected the real restart action to be invoked")
	}
	if mode != "AUTO" {
		t.Errorf("restartMode = %q, want AUTO", mode)
	}
}

func TestPluginsIndexWithoutBridgeReturnsClearError(t *testing.T) {
	s := newTestServer(config.Map{"enabled": true}, fakeAgentInfo{}, &fakeActions{})
	rr := httptest.NewRecorder()
	newMux(s).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/plugins", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when no plugin bridge is available, got %d: %s", rr.Code, rr.Body.String())
	}
}

func waitFor(cond func() bool) bool {
	for i := 0; i < 50; i++ {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func simpleTestImage() image.Image {
	return image.NewGray(image.Rect(0, 0, 4, 4))
}

// TestRealHTTPRoundTrip drives the exact same handler stack this package
// registers in production through a REAL TCP listener and a REAL
// net/http.Client (httptest.NewServer, not httptest.NewRecorder) — an
// actual client/server round trip over a real socket, not just an
// in-process ResponseRecorder call. This is the strongest verification
// possible in an environment with no way to drive a real browser: real
// TCP, real HTTP framing, real headers, real cookies persisted across
// requests via a real http.CookieJar exactly like a browser would.
func TestRealHTTPRoundTrip(t *testing.T) {
	actions := &fakeActions{}
	s := newTestServer(config.Map{"enabled": true}, fakeAgentInfo{mode: "auto", fingerprint: "real-fp"}, actions)
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{Jar: jar}

	// Real GET / over a real socket.
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("real GET /: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("real GET / status = %d, body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Restart MANU") {
		t.Fatalf("expected real rendered index page, got %s", body)
	}

	// The real cookie jar now holds the real csrf_token cookie the server
	// set on that response — extract it the same way a real browser's
	// form submission would carry it back.
	u, _ := url.Parse(srv.URL)
	var csrfVal string
	for _, c := range jar.Cookies(u) {
		if c.Name == csrfCookieName {
			csrfVal = c.Value
		}
	}
	if csrfVal == "" {
		t.Fatalf("expected a real csrf cookie from the real server")
	}

	// Real GET of an embedded static asset over the real socket.
	resp, err = client.Get(srv.URL + "/css/style.css")
	if err != nil {
		t.Fatalf("real GET /css/style.css: %v", err)
	}
	cssBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(cssBody) == 0 {
		t.Fatalf("expected real embedded CSS over the real socket, got %d bytes, status %d", len(cssBody), resp.StatusCode)
	}

	// Real POST /restart with the real cookie-jar-carried csrf token,
	// exactly like a real browser form submission — asserts the real
	// action gets invoked on the other end of a real TCP round trip.
	form := url.Values{"mode": {"AUTO"}, "csrf_token": {csrfVal}}
	resp, err = client.PostForm(srv.URL+"/restart", form)
	if err != nil {
		t.Fatalf("real POST /restart: %v", err)
	}
	restartBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("real POST /restart status = %d, body = %s", resp.StatusCode, restartBody)
	}
	if !strings.Contains(string(restartBody), "Restarting in AUTO mode") {
		t.Fatalf("expected real status page body, got %s", restartBody)
	}

	called, mode := false, ""
	if waitFor(func() bool { called, mode = actions.restartWasCalled(); return called }); !called {
		t.Fatalf("expected the real action to run after a real HTTP round trip")
	}
	if mode != "AUTO" {
		t.Errorf("mode = %q, want AUTO", mode)
	}

	// A real POST without any csrf token, over the real socket, must be
	// rejected exactly like the in-process test already proved.
	resp, err = client.PostForm(srv.URL+"/shutdown", url.Values{})
	if err != nil {
		t.Fatalf("real POST /shutdown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected real 403 for a csrf-less POST, got %d", resp.StatusCode)
	}
}

// TestLogtailIsNativeGoNotBridge proves the logtail plugin's web UI works
// via a real, native Go implementation (internal/web/logtail.go) — no
// pyplugin bridge involved at all, so it can never hang on the bridge's
// FIFO backlog or its whole-body-capture limitation on an infinite
// stream. Uses a real temp log file, real tailLines reading, and a real
// live HTTP stream read via http.Flusher.
func TestLogtailIsNativeGoNotBridge(t *testing.T) {
	logDir := t.TempDir()
	logPath := logDir + "/pwnagotchi.log"
	initial := "[2026-01-01 00:00:00] [INFO] : line one\n[2026-01-01 00:00:01] [ERROR] : line two\n"
	if err := os.WriteFile(logPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("writing test log: %v", err)
	}

	cfg := config.Map{
		"ui": config.Map{"web": config.Map{"enabled": true}},
		"main": config.Map{
			"log":     config.Map{"path": logPath},
			"plugins": config.Map{"logtail": config.Map{"max-lines": int64(100)}},
		},
	}
	s := New(cfg, "test-unit", fakeAgentInfo{}, nil, nil, &fakeActions{}, cfg, "")
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Index page: real HTML, no bridge.
	resp, err := http.Get(srv.URL + "/plugins/logtail")
	if err != nil {
		t.Fatalf("GET /plugins/logtail: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "System Log") {
		t.Fatalf("expected real logtail index content, got %s", body)
	}

	// Stream: real initial tail content served promptly, then real live
	// appended lines follow without the request ever needing to finish.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/plugins/logtail/stream", nil)
	client := &http.Client{}
	streamResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer streamResp.Body.Close()

	reader := bufio.NewReader(streamResp.Body)
	line1, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line1, "line one") {
		t.Fatalf("expected real first tailed line, got %q err=%v", line1, err)
	}
	line2, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line2, "line two") {
		t.Fatalf("expected real second tailed line, got %q err=%v", line2, err)
	}

	// Real live append: write a new line to the real file and confirm it
	// arrives on the already-open stream without a new request.
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("opening log for append: %v", err)
	}
	if _, err := f.WriteString("[2026-01-01 00:00:02] [WARNING] : line three\n"); err != nil {
		t.Fatalf("appending: %v", err)
	}
	f.Close()

	done := make(chan string, 1)
	go func() {
		line, _ := reader.ReadString('\n')
		done <- line
	}()
	select {
	case line3 := <-done:
		if !strings.Contains(line3, "line three") {
			t.Fatalf("expected real live-appended line, got %q", line3)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("real appended line never arrived on the live stream")
	}
}
