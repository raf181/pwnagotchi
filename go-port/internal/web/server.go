package web

import (
	"context"
	"log"
	"net"
	"net/http"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
	"github.com/jayofelony/pwnagotchi/go-port/internal/grid"
	"github.com/jayofelony/pwnagotchi/go-port/internal/pyplugin"
)

// AgentInfo is the small subset of internal/agent.Agent the web UI reads
// (Handler.index's other_mode/fingerprint, mirrored exactly).
type AgentInfo interface {
	Mode() string
	GetFingerprint() string
}

// Actions are the real, dangerous system operations handler.py's
// /shutdown, /reboot, /restart routes trigger (pwnagotchi.shutdown/
// reboot/restart in __init__.py, already ported to internal/unit).
// Injected rather than called directly so callers control exactly which
// Runner/View/mounts they're bound to (production: the real
// unit.DefaultRunner; tests: a fake Runner — never the reverse, given
// this session's real-reboot incident, see docs/final-port-report.md).
type Actions interface {
	Shutdown() error
	Reboot(mode string) error
	Restart(mode string) error
}

// Server ports pwnagotchi/ui/web/server.py's Server + handler.py's
// Handler: a real net/http server, real routes, real auth/CSRF.
type Server struct {
	enabled                   bool
	address                   string
	port                      int
	origin                    string
	authEnabled               bool
	username                  string
	password                  string
	accentR, accentG, accentB int
	onFrame                   string

	name    string
	agent   AgentInfo
	grid    *grid.Client
	bridge  *pyplugin.Bridge // nil if the plugin bridge isn't running; plugin routes degrade to a clear error, not a crash
	actions Actions
	cfg     config.Map
	cfgPath string

	httpServer *http.Server
}

// New ports Server.__init__(agent, config['ui']). Does not start listening
// until Start() is called (Python's own Server starts a background thread
// unconditionally in __init__ when enabled; Go callers get an explicit,
// separately-cancellable Start instead — see Server.Start).
func New(cfg config.Map, name string, agentInfo AgentInfo, gridClient *grid.Client, bridge *pyplugin.Bridge, actions Actions, fullCfg config.Map, cfgPath string) *Server {
	uiCfg, _ := cfg["ui"].(config.Map)
	webCfg, _ := uiCfg["web"].(config.Map)

	s := &Server{
		enabled:     boolField(webCfg, "enabled"),
		address:     stringField(webCfg, "address", "::"),
		port:        intField(webCfg, "port", 8080),
		origin:      stringField(webCfg, "origin", ""),
		authEnabled: boolField(webCfg, "auth"),
		username:    stringField(webCfg, "username", "changeme"),
		password:    stringField(webCfg, "password", "changeme"),
		onFrame:     stringField(webCfg, "on_frame", ""),
		name:        name,
		agent:       agentInfo,
		grid:        gridClient,
		bridge:      bridge,
		actions:     actions,
		cfg:         fullCfg,
		cfgPath:     cfgPath,
	}
	themeCfg, _ := webCfg["theme"].(config.Map)
	s.accentR = intField(themeCfg, "accent_r", 76)
	s.accentG = intField(themeCfg, "accent_g", 175)
	s.accentB = intField(themeCfg, "accent_b", 80)
	return s
}

// Start ports Server._http_serve: binds and serves in a background
// goroutine (Python's daemon Thread), logging the same "web ui available
// at http://host:port/" line. A no-op (matching Python's own `if
// self._enabled:` gate in __init__) if disabled.
func (s *Server) Start() {
	if !s.enabled {
		return
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	var handler http.Handler = mux
	if s.origin != "" {
		handler = s.withCORS(mux)
	}

	s.httpServer = &http.Server{
		Addr:    net.JoinHostPort(s.address, itoa(s.port)),
		Handler: handler,
	}

	displayAddr := s.address
	if displayAddr == "::" {
		displayAddr = "[::]"
	}
	log.Printf("web ui available at http://%s:%d/", displayAddr, s.port)

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("web: server error: %v", err)
		}
	}()
}

// Stop gracefully shuts the HTTP server down — an addition for clean
// process shutdown (Python's daemon thread just dies with the process).
func (s *Server) Stop(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

// withCORS ports server.py's `CORS(app, resources={r"*": {"origins":
// self._origin}})` (flask-cors): reflects the configured origin (or "*")
// on every response and answers preflight OPTIONS requests directly.
func (s *Server) withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func boolField(m config.Map, key string) bool {
	if m == nil {
		return false
	}
	b, _ := m[key].(bool)
	return b
}

func stringField(m config.Map, key, def string) string {
	if m == nil {
		return def
	}
	if s, ok := m[key].(string); ok && s != "" {
		return s
	}
	return def
}

func intField(m config.Map, key string, def int) int {
	if m == nil {
		return def
	}
	switch v := m[key].(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}
