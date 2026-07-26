package pluginmanager

import (
	"context"
	"net/http"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
)

// Capabilities is the one, typed object a native plugin's OnLoad receives.
// Every field is a narrow interface (never `interface{}`, never the
// concrete *agent.Agent/*view.View/... types) so a plugin only sees the
// operations it's actually meant to use, production wiring can inject real
// implementations while tests inject fakes, and this package never has to
// import internal/agent, internal/ui/view, internal/bettercap, or
// internal/grid — those packages already import EventEmitter-shaped
// interfaces the same way, so nothing here creates an import cycle.
//
// Production currently wires Agent, View, Exec, HTTPClient, Clock, GPIO,
// I2C, System, Emit, and the manager-provided Log. Other fields remain
// available for host-specific injection; SPI is not wired by the daemon.
type Capabilities struct {
	// Config is this plugin's own config sub-map
	// (config['main']['plugins'][name]), the same shape Python plugins see
	// as self.options in on_loaded.
	Config config.Map

	// Log is a plugin-scoped logger (every line prefixed with the plugin's
	// name), always non-nil once handed to a plugin.
	Log Logger

	Agent      AgentCapability
	View       ViewCapability
	Bettercap  BettercapCapability
	Grid       GridCapability
	State      StateCapability
	Exec       CommandRunner
	HTTPClient *http.Client
	Clock      Clock
	GPIO       GPIOCapability
	I2C        I2CCapability
	SPI        SPICapability
	Web        WebCapability
	System     SystemCapability
	// Emit lets a plugin broadcast a custom event to every OTHER loaded
	// plugin (Python: the module-level `plugins.on(event, *args)` a few
	// bundled plugins call directly — auto-update.py's 'updating',
	// grid.py's 'unread_inbox', bt-tether.py's dynamic event names).
	// Wired to the same Manager.On real plugins receive daemon events
	// through, so a plugin-emitted event is indistinguishable from a
	// daemon-emitted one to its recipients.
	Emit func(event string, args ...interface{})
}

// SystemCapability lets a plugin trigger the same real, dangerous system
// actions the web UI's shutdown/reboot/restart routes use (Python:
// pwnagotchi.shutdown()/reboot(mode)/restart(mode) — switcher.py's
// reboot-after-task-completion behavior is the bundled plugin that needs
// this). Deliberately shaped identically to internal/web.Actions so
// production wiring can share the exact same adapter for both.
type SystemCapability interface {
	Shutdown() error
	Reboot(mode string) error
	Restart(mode string) error
}

// Logger is the plugin-scoped logging capability.
type Logger interface {
	Printf(format string, args ...interface{})
}

// AgentCapability is the narrow subset of *agent.Agent a plugin may call:
// running bettercap commands, reading session state, and the small set of
// derived accessors real bundled plugins use (is_module_running,
// start/restart_module). Deliberately does not expose the full Agent
// type. Method shapes match *agent.Agent (via its embedded
// *bettercap.Client for Run/Session) exactly, so the real *agent.Agent
// satisfies this interface directly with no adapter wrapper needed.
type AgentCapability interface {
	Run(cmd string, verboseErrors bool) (interface{}, error)
	Session(sess string) (interface{}, error)
	IsModuleRunning(module string) bool
	StartModule(module string)
	RestartModule(module string)
	SupportedChannels() []int
	ResetHistory()
}

// FontStyle names the font role a plugin-added text element should
// render with, matching the names pwnagotchi/ui/fonts.py exposes as
// module attributes (Small, Bold, BoldSmall, Medium, Huge, BoldBig)
// case-insensitively — see view.View.resolveFont, which every production
// ViewCapability implementation is expected to delegate to so plugin
// elements share the exact same font-face pipeline as built-in ones.
type FontStyle string

const (
	FontSmall     FontStyle = "small"
	FontBold      FontStyle = "bold"
	FontBoldSmall FontStyle = "boldsmall"
	FontMedium    FontStyle = "medium"
	FontHuge      FontStyle = "huge"
	FontBoldBig   FontStyle = "boldbig"
)

// ViewCapability is the narrow subset of *view.View (which, in real
// Python, is the same `display`/`ui` object bundled plugins' on_ui_setup/
// on_ui_update/on_unload/on_state_change hooks and display.set() calls
// all share — display.Display IS-A View) a plugin may use: mutate
// on-screen state, add/remove its own widgets, and check which physical
// display is resolved (Python: the ~90 generated is_waveshare_v2()-style
// predicates — collapsed here to one Kind() string plugins compare
// against, since a Go interface can't grow one method per display model).
type ViewCapability interface {
	Set(key string, value string)
	Update(force bool)
	Kind() string
	HasElement(key string) bool
	RemoveElement(key string)
	// AddText ports `ui.add_element(key, Text(...))`.
	AddText(key, value string, x, y int, font FontStyle, wrap bool, maxLength int)
	// AddLabeledValue ports `ui.add_element(key, LabeledValue(...))`. An
	// empty label matches Python's label=None (value-only rendering).
	AddLabeledValue(key, label, value string, x, y int, labelFont, valueFont FontStyle, labelSpacing int)
	// OnUploading/OnNormal port display.on_uploading(to)/display.on_normal()
	// — the transient "uploading to X" mood/status several bundled
	// upload-handshakes-to-a-service plugins (wigle, ohcapi, pwncrack) show
	// during a batch upload, then clear.
	OnUploading(to string)
	OnNormal()
	// Width/Height port view.width()/view.height() — real Python plugins
	// (example.py, several others) center elements against the actual
	// screen size rather than hardcoding a display-specific position.
	Width() int
	Height() int
}

// BettercapCapability is a plugin's access to the real bettercap REST API
// client (Python: pwnagotchi.bettercap module functions), independent of
// AgentCapability.Run's fire-and-forget command style.
type BettercapCapability interface {
	Request(method, path string, body interface{}) (map[string]interface{}, error)
}

// GridCapability is a plugin's access to the pwngrid peer/API client
// (Python: pwnagotchi.grid module functions: report, memory, ...).
type GridCapability interface {
	Report(kind string, data map[string]interface{}) error
	MemoryGet(key string) (interface{}, bool)
	MemorySet(key string, value interface{}) error
}

// StateCapability gives a plugin a private, namespaced directory for
// persisted state (Python plugins mostly just hardcode a path under
// /root or /etc/pwnagotchi/; this gives every native plugin an explicit,
// injectable equivalent instead of reaching for os.UserHomeDir/hardcoded
// absolute paths directly, which is what made wpa-sec's legacy-db
// migration a real bug in the first place — see docs/migration-ledger.md).
type StateCapability interface {
	// Dir returns (creating if necessary) this plugin's private state
	// directory, e.g. /etc/pwnagotchi/plugins/<name>/.
	Dir() (string, error)
}

// CommandRunner is the injectable argv-based process execution capability
// (never a shell string) every plugin that shells out (bt-tether, wittypi,
// fix_services, ...) must use instead of calling os/exec directly, so
// production/test/fake implementations are swappable and no plugin can
// reintroduce shell-string command injection.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Clock is the injectable time source every plugin with scheduled/periodic
// behavior must use instead of calling time.Now directly, for deterministic
// tests.
type Clock interface {
	Now() time.Time
}

// GPIOCapability opens a GPIO line for read, write, and edge-triggered
// waits. The Linux host implementation uses the legacy sysfs GPIO ABI.
type GPIOCapability interface {
	Line(pin int) (GPIOLine, error)
}

// GPIOLine is one open GPIO line.
type GPIOLine interface {
	Read() (bool, error)
	Write(high bool) error
	WaitEdge(ctx context.Context) error
	Close() error
}

// I2CCapability opens an I2C device by bus/address (pisugarx, some
// displays' companion sensors).
type I2CCapability interface {
	Open(bus int, addr uint8) (I2CDevice, error)
}

// I2CDevice is one open I2C device.
type I2CDevice interface {
	ReadReg(reg uint8, n int) ([]byte, error)
	WriteReg(reg uint8, data []byte) error
	Close() error
}

// SPICapability opens an SPI device (mostly used by display drivers, but
// exposed here too since at least one bundled plugin family talks to
// SPI-attached peripherals directly).
type SPICapability interface {
	Open(bus, chipSelect int, speedHz int) (SPIDevice, error)
}

// SPIDevice is one open SPI device.
type SPIDevice interface {
	Transfer(tx []byte) (rx []byte, err error)
	Close() error
}

// WebCapability lets a plugin register its own HTTP routes and a webhook
// subpath prefix directly on the real server mux — the "native web UI/
// webhook integration" required instead of routing every plugin through a
// bespoke bridge dispatch path. Route registers under /plugins/<name>/...
// exactly like the existing bridge passthrough did, so URLs plugin authors
// and users already depend on do not change.
type WebCapability interface {
	// Handle registers subpath (relative to /plugins/<name>/) on the real
	// server mux.
	Handle(subpath string, handler http.HandlerFunc)
	// Render renders one of the server's own templates with extra data,
	// for plugins that want the same base layout/nav as built-in pages
	// (Python: flask.render_template_string against the shared Jinja env).
	Render(w http.ResponseWriter, r *http.Request, template string, data map[string]interface{})
}
