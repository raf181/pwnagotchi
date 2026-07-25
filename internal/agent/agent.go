package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/automata"
	"github.com/jayofelony/pwnagotchi/internal/bettercap"
	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/grid"
	"github.com/jayofelony/pwnagotchi/internal/identity"
	"github.com/jayofelony/pwnagotchi/internal/mesh"
	"github.com/jayofelony/pwnagotchi/internal/session"
	"github.com/jayofelony/pwnagotchi/internal/unit"
	"github.com/jayofelony/pwnagotchi/internal/version"
)

// RecoveryDataFile mirrors agent.RECOVERY_DATA_FILE. A var (not const), so
// tests can point it elsewhere.
var RecoveryDataFile = "/root/.pwnagotchi-recovery"

// View is the full view interface Agent needs: automata's + mesh's + the
// log-reading callback, plus the extra methods agent.py calls directly
// (set_agent, set, set_closest_peer, on_handshakes, on_assoc, on_deauth,
// on_normal). A concrete internal/ui/view.View will satisfy this once that
// package exists.
type View interface {
	automata.View
	mesh.View
	session.View
	unit.RebootView

	SetAgent(a *Agent)
	// Set mirrors Python's view.set(key, value): value is genuinely
	// polymorphic there (str, None, occasionally int) — e.g. View.
	// SetClosestPeer sets 'friend_face'/'friend_name' to None when there's
	// no closest peer. Every internal/agent call site happens to pass a
	// string today, but the interface itself must accept interface{} to
	// match what a real View (internal/ui/view.View) needs to implement.
	Set(key string, value interface{})
	SetClosestPeer(peer *mesh.Peer, count int)
	OnHandshakes(newShakes int)
	OnAssoc(ap AP)
	OnDeauth(sta Station)
	OnNormal()
	// OnUploading mirrors View.on_uploading — exposed here (not just on
	// the concrete *view.View) so native Go plugin ports like
	// internal/wpasec can show real upload-in-progress status the same
	// way the real wpa-sec.py plugin's on_internet_available calls
	// display.on_uploading(...) between handshake uploads.
	OnUploading(to string)
}

// EventEmitter mirrors pwnagotchi.plugins.on(event, *args). A real
// internal/plugins.Loader will satisfy this once that package exists.
type EventEmitter interface {
	On(event string, args ...interface{})
}

type noopEmitter struct{}

func (noopEmitter) On(string, ...interface{}) {}

// Agent ports agent.Agent (which in Python multiply-inherits from
// bettercap.Client, automata.Automata, and mesh.utils.AsyncAdvertiser; Go
// uses composition via embedded pointers instead — none of the three
// define overlapping method names, so promotion is unambiguous).
type Agent struct {
	*bettercap.Client
	*automata.Automata
	*mesh.AsyncAdvertiser

	config  config.Map
	view    View
	emit    EventEmitter
	keypair *identity.KeyPair
	ctx     context.Context
	cancel  context.CancelFunc

	mu sync.Mutex

	startedAt         time.Time
	currentChannel    int
	totAPs            int
	apsOnChannel      int
	supportedChannels []int

	accessPoints []AP
	lastPwnd     string
	history      map[string]int
	handshakes   map[string]interface{}

	LastSession *session.LastSession
	Mode        string
}

// New ports Agent.__init__.
func New(view View, cfg config.Map, keypair *identity.KeyPair, emit EventEmitter) (*Agent, error) {
	if emit == nil {
		emit = noopEmitter{}
	}

	bcCfg, _ := cfg["bettercap"].(config.Map)
	hostname := stringOr(bcCfg, "hostname", "127.0.0.1")
	scheme := stringOr(bcCfg, "scheme", "http")
	port := intOr(bcCfg, "port", 8081)
	username := stringOr(bcCfg, "username", "pwnagotchi")
	password := stringOr(bcCfg, "password", "pwnagotchi")
	client := bettercap.NewClient(hostname, scheme, port, username, password)

	name, err := unit.Name()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	a := &Agent{
		Client:     client,
		Automata:   automata.New(cfg, view),
		config:     cfg,
		view:       view,
		emit:       emit,
		keypair:    keypair,
		ctx:        ctx,
		cancel:     cancel,
		startedAt:  time.Now(),
		history:    map[string]int{},
		handshakes: map[string]interface{}{},
		Mode:       "auto",
	}
	a.AsyncAdvertiser = mesh.NewAsyncAdvertiser(cfg, view, name, version.Version, keypair.Fingerprint)
	a.AsyncAdvertiser.Grid = grid.NewClient(version.Version)
	a.AsyncAdvertiser.Emit = emit
	a.AsyncAdvertiser.HandshakesCount = func() int {
		a.mu.Lock()
		defer a.mu.Unlock()
		return len(a.handshakes)
	}
	a.Automata.Emit = emit
	a.Automata.Peers = a.AsyncAdvertiser
	a.Automata.Restart = func(mode string) { a.restart(mode) }

	ifaceName := stringOr(mainMap(cfg), "iface", "")
	a.supportedChannels = config.IfaceChannels(config.ExecCommandRunner, ifaceName)

	view.SetAgent(a)

	logPath := digNestedString(cfg, "main", "log", "path")
	lang := stringOr(mainMap(cfg), "lang", "en")
	a.LastSession = session.New(cfg, logPath, lang)

	handshakesDir := stringOr(bcCfg, "handshakes", "")
	if handshakesDir != "" {
		if _, err := os.Stat(handshakesDir); os.IsNotExist(err) {
			if err := os.MkdirAll(handshakesDir, 0o755); err != nil {
				return nil, err
			}
		}
	}

	log.Printf("%s@%s (v%s)", name, keypair.Fingerprint, version.Version)

	return a, nil
}

// Config ports Agent.config().
func (a *Agent) Config() config.Map { return a.config }

// View ports Agent.view().
func (a *Agent) View() View { return a.view }

// SupportedChannels ports Agent.supported_channels().
func (a *Agent) SupportedChannels() []int { return a.supportedChannels }

// SetupEvents ports Agent.setup_events.
func (a *Agent) SetupEvents() {
	log.Printf("connecting to %s ...", a.Client.URL)
	silence, _ := digSlice(a.config, "bettercap", "silence")
	for _, tag := range silence {
		s, ok := tag.(string)
		if !ok {
			continue
		}
		a.Run(fmt.Sprintf("events.ignore %s", s), false)
	}
}

func (a *Agent) resetWifiSettings() {
	iface := stringOr(mainMap(a.config), "iface", "")
	a.Run(fmt.Sprintf("set wifi.interface %s", iface), true)
	a.Run(fmt.Sprintf("set wifi.ap.ttl %d", intOr(personalityMap(a.config), "ap_ttl", 0)), true)
	a.Run(fmt.Sprintf("set wifi.sta.ttl %d", intOr(personalityMap(a.config), "sta_ttl", 0)), true)
	a.Run(fmt.Sprintf("set wifi.rssi.min %d", intOr(personalityMap(a.config), "min_rssi", 0)), true)
	a.Run(fmt.Sprintf("set wifi.handshakes.file %s", stringOr(bettercapMap(a.config), "handshakes", "")), true)
	a.Run("set wifi.handshakes.aggregate false", true)
}

// StartMonitorMode ports Agent.start_monitor_mode.
func (a *Agent) StartMonitorMode() {
	monIface := stringOr(mainMap(a.config), "iface", "")
	monStartCmd := stringOr(mainMap(a.config), "mon_start_cmd", "")
	noRestart, _ := boolAt(mainMap(a.config), "no_restart")
	restart := !noRestart

	hasMon := false
	for !hasMon {
		s, err := a.Session("")
		if err == nil {
			if sm := asMap(s); sm != nil {
				for _, raw := range asSlice(sm["interfaces"]) {
					iface := asMap(raw)
					if iface != nil && getString(iface, "name") == monIface {
						log.Printf("found monitor interface: %s", monIface)
						hasMon = true
						break
					}
				}
			}
		}
		if !hasMon {
			if monStartCmd != "" {
				log.Print("starting monitor interface ...")
				a.Run("!"+monStartCmd, true)
			} else {
				log.Printf("waiting for monitor interface %s ...", monIface)
				time.Sleep(1 * time.Second)
			}
		}
	}

	log.Printf("supported channels: %v", a.supportedChannels)
	log.Printf("handshakes will be collected inside %s", stringOr(bettercapMap(a.config), "handshakes", ""))

	a.resetWifiSettings()

	wifiRunning := a.IsModuleRunning("wifi")
	if wifiRunning && restart {
		a.RestartModule("wifi.recon")
		a.Run("wifi.clear", true)
	} else if !wifiRunning {
		a.StartModule("wifi.recon")
	}

	a.StartAdvertising(a.ctx)
}

func (a *Agent) waitBettercap() {
	for {
		if _, err := a.Session(""); err == nil {
			return
		}
		log.Print("waiting for bettercap API to be available ...")
		time.Sleep(1 * time.Second)
	}
}

// Start ports Agent.start.
func (a *Agent) Start() {
	a.waitBettercap()
	a.SetupEvents()
	a.SetStarting()
	a.StartMonitorMode()
	a.StartEventPolling()
	a.StartSessionFetcher()
	a.NextEpoch()
	a.SetReady()
}
