// Package bettercap ports pwnagotchi/bettercap.py: the HTTP/WebSocket
// client used to drive the bettercap REST API and consume its event stream.
package bettercap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Timing constants mirror bettercap.py's module-level constants exactly.
const (
	PingTimeout  = 180 * time.Second
	PingInterval = 15 * time.Second
	MaxQueue     = 10000

	MinSleep = 500 * time.Millisecond
	MaxSleep = 5 * time.Second
)

// Restarter lets the caller (Agent) supply pwnagotchi.restart("AUTO") —
// bettercap.py calls the module-level pwnagotchi.restart function directly
// on an OSError from the websocket loop; Go takes it as an injected
// callback instead of importing a "restart the whole process" global.
type Restarter interface {
	Restart(mode string)
}

// Client ports bettercap.Client.
type Client struct {
	Hostname string
	Scheme   string
	Port     int
	Username string
	Password string

	URL       string
	WebSocket string

	HTTPClient *http.Client
	Restart    Restarter

	// randSleep is overridable so tests can make retry/backoff
	// deterministic instead of depending on math/rand.
	randSleep func() float64
}

// NewClient ports Client.__init__'s defaults
// (hostname='localhost', scheme='http', port=8081, username='user', password='pass').
func NewClient(hostname, scheme string, port int, username, password string) *Client {
	if hostname == "" {
		hostname = "localhost"
	}
	if scheme == "" {
		scheme = "http"
	}
	if port == 0 {
		port = 8081
	}
	if username == "" {
		username = "user"
	}
	if password == "" {
		password = "pass"
	}
	return &Client{
		Hostname:   hostname,
		Scheme:     scheme,
		Port:       port,
		Username:   username,
		Password:   password,
		URL:        fmt.Sprintf("%s://%s:%d/api", scheme, hostname, port),
		WebSocket:  fmt.Sprintf("ws://%s:%s@%s:%d/api", username, password, hostname, port),
		HTTPClient: &http.Client{},
		randSleep:  rand.Float64,
	}
}

func (c *Client) sleepDuration() time.Duration {
	return MinSleep + time.Duration(c.randSleep()*float64(MaxSleep))
}

// decode mirrors bettercap.py's module-level decode(r, verbose_errors=True).
// On a JSON-decode failure: if the HTTP status was 200, logs and returns the
// raw body text as the value (NOT an error — matching Python returning
// r.text rather than raising); otherwise builds "error %d: %s" and returns
// it as an error, optionally logging it first.
func decode(resp *http.Response, body []byte, verboseErrors bool) (interface{}, error) {
	var v interface{}
	if err := json.Unmarshal(body, &v); err == nil {
		return v, nil
	} else if resp.StatusCode == 200 {
		log.Printf("error while decoding json: error='%s' resp='%s'", err, body)
		return string(body), nil
	} else {
		msg := fmt.Sprintf("error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		if verboseErrors {
			log.Print(msg)
		}
		return nil, errors.New(msg)
	}
}

func (c *Client) doRequest(req *http.Request) (interface{}, error) {
	req.SetBasicAuth(c.Username, c.Password)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return decode(resp, body, true)
}

// Session mirrors Client.session(sess="session"): GET {url}/{sess}.
func (c *Client) Session(sess string) (interface{}, error) {
	if sess == "" {
		sess = "session"
	}
	req, err := http.NewRequest(http.MethodGet, c.URL+"/"+sess, nil)
	if err != nil {
		return nil, err
	}
	return c.doRequest(req)
}

// Run mirrors Client.run(command, verbose_errors=True): POST {url}/session
// {"cmd": command}, retrying forever (with jittered backoff) only on a
// connection-level failure — matching Python's
// `except requests.exceptions.ConnectionError` (NOT other exceptions like
// timeouts, which Python lets propagate uncaught).
func (c *Client) Run(command string, verboseErrors bool) (interface{}, error) {
	payload, err := json.Marshal(map[string]string{"cmd": command})
	if err != nil {
		return nil, err
	}

	for {
		req, err := http.NewRequest(http.MethodPost, c.URL+"/session", bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.SetBasicAuth(c.Username, c.Password)

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			if !isConnectionError(err) {
				return nil, err
			}
			sleepTime := c.sleepDuration()
			log.Print("[bettercap] can't run my request... connection to the bettercap endpoint failed...")
			log.Printf("[bettercap] retrying run in %v sec", sleepTime.Seconds())
			time.Sleep(sleepTime)
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		return decode(resp, body, verboseErrors)
	}
}

// isConnectionError approximates Python's requests.exceptions.ConnectionError
// (raised for DNS failure, connection refused, connection reset — i.e.
// transport-level dial/connect failures) as opposed to e.g. a timeout.
// Go's net/http wraps these in *url.Error -> *net.OpError; this is a
// best-effort classification, not a byte-for-byte match of requests'
// exception hierarchy (see docs/known-differences.md).
func isConnectionError(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr)
}

// StartWebsocket mirrors Client.start_websocket(consumer): a reconnect loop
// around bettercap's /events websocket with the same ping/pong keepalive
// and backoff structure as the real (non-commented-out) Python
// implementation. consumer is called with each raw message; ctx cancellation
// stops the loop (Python has no equivalent — it runs until the process
// dies — ctx is an addition for clean shutdown, not a behavior change to
// the reconnect/keepalive logic itself).
func (c *Client) StartWebsocket(ctx context.Context, consumer func(msg []byte) error) error {
	// c.WebSocket carries "ws://user:pass@host:port/api" verbatim, matching
	// Python's self.websocket field, but gorilla/websocket's Dialer
	// explicitly rejects userinfo in the URL (websockets, the Python
	// library, accepts it and sends Basic auth over the wire instead) — so
	// the credentials are stripped here and sent as a real Authorization
	// header, producing the same wire-level auth Python's `websockets`
	// (via the URL) results in.
	dialURL, header, err := websocketDialTarget(c.WebSocket + "/events")
	if err != nil {
		return err
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: 45 * time.Second,
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		log.Print("[bettercap] creating new websocket...")
		conn, _, err := dialer.DialContext(ctx, dialURL, header)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if isConnectionRefused(err) {
				sleepTime := c.sleepDuration()
				log.Print("[bettercap] nobody seems to be listening at the bettercap endpoint...")
				log.Printf("[bettercap] retrying connection in %v sec", sleepTime.Seconds())
				time.Sleep(sleepTime)
				continue
			}
			// Python: `except OSError: ... pwnagotchi.restart("AUTO")` — any
			// other OS-level socket error triggers a full process restart
			// rather than a retry loop.
			log.Print("connection to the bettercap endpoint failed...")
			if c.Restart != nil {
				c.Restart.Restart("AUTO")
			}
			return err
		}

		c.runWebsocketSession(ctx, conn, consumer)
	}
}

func (c *Client) runWebsocketSession(ctx context.Context, conn *websocket.Conn, consumer func(msg []byte) error) {
	defer conn.Close()
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(PingTimeout))
	})
	conn.SetReadDeadline(time.Now().Add(PingTimeout))

	pingDone := make(chan struct{})
	defer close(pingDone)
	go func() {
		ticker := time.NewTicker(PingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-pingDone:
				return
			case <-ticker.C:
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(PingTimeout))
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err) || isTimeoutErr(err) {
				sleepTime := c.sleepDuration()
				log.Printf("[bettercap] ping error - retrying connection in %v sec", sleepTime.Seconds())
				select {
				case <-ctx.Done():
				case <-time.After(sleepTime):
				}
			}
			return
		}
		if err := consumer(msg); err != nil {
			log.Printf("[bettercap] error while parsing event (%s)", err)
		}
	}
}

// websocketDialTarget strips "user:pass@" userinfo out of a ws:// URL
// (gorilla/websocket rejects it outright) and returns the equivalent
// Authorization: Basic header instead, so the resulting wire-level
// handshake carries the same credentials Python's `websockets` library
// sends when given a URL with embedded userinfo.
func websocketDialTarget(rawURL string) (string, http.Header, error) {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return "", nil, err
	}
	header := http.Header{}
	if u.User != nil {
		user := u.User.Username()
		pass, _ := u.User.Password()
		header.Set("Authorization", "Basic "+basicAuth(user, pass))
		u.User = nil
	}
	return u.String(), header, nil
}

func basicAuth(username, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
}

func isConnectionRefused(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return strings.Contains(opErr.Err.Error(), "refused")
	}
	return false
}

func isTimeoutErr(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
