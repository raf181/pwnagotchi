// Command pwnagotchi is the Go port of pwnagotchi/cli.py's
// pwnagotchi_cli() entry point.
package main

import (
	"fmt"
	"image"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/agent"
	"github.com/jayofelony/pwnagotchi/internal/cli"
	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/fs"
	"github.com/jayofelony/pwnagotchi/internal/grid"
	"github.com/jayofelony/pwnagotchi/internal/identity"
	golog "github.com/jayofelony/pwnagotchi/internal/logging"
	"github.com/jayofelony/pwnagotchi/internal/pluginhost"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
	"github.com/jayofelony/pwnagotchi/internal/plugins"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/auto_backup"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/auto_tune"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/auto_update"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/bt_tether"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/cache"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/example"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/fix_services"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/gpio_buttons"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/gps"
	gridplugin "github.com/jayofelony/pwnagotchi/internal/plugins/native/grid"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/memtemp"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/ohcapi"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/pisugarx"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/pwncrack"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/pwnstore_ui"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/session_stats"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/switcher"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/ups_lite"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/webgpsmap"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/wigle"
	"github.com/jayofelony/pwnagotchi/internal/plugins/native/wittypi"
	"github.com/jayofelony/pwnagotchi/internal/ui/display"
	"github.com/jayofelony/pwnagotchi/internal/unit"
	"github.com/jayofelony/pwnagotchi/internal/version"
	"github.com/jayofelony/pwnagotchi/internal/voice"
	"github.com/jayofelony/pwnagotchi/internal/web"
	"github.com/jayofelony/pwnagotchi/internal/wpasec"
)

// agentInfoAdapter satisfies internal/web.AgentInfo from a real
// *agent.Agent (Agent.Mode is a plain field, GetFingerprint is promoted
// from the embedded AsyncAdvertiser — neither is directly assignable to
// an interface value, hence this thin wrapper).
type agentInfoAdapter struct{ a *agent.Agent }

func (w agentInfoAdapter) Mode() string           { return w.a.Mode }
func (w agentInfoAdapter) GetFingerprint() string { return w.a.GetFingerprint() }

// unitActions wires internal/web.Actions to the REAL internal/unit
// Shutdown/Reboot/Restart functions against unit.DefaultRunner — the same
// real, dangerous system calls (halt/reboot/service restart) main.go's
// own SetName call already uses. Never point this at anything but
// unit.DefaultRunner in production; tests must use a fake Runner instead
// of exercising this adapter directly (see docs/final-port-report.md's
// reboot incident note).
type unitActions struct {
	view interface {
		OnShutdown()
		OnRebooting()
	}
	mounts []unit.Mount
}

func (u unitActions) Shutdown() error { return unit.Shutdown(unit.DefaultRunner, u.view, u.mounts) }
func (u unitActions) Reboot(mode string) error {
	var m *string
	if mode != "" {
		m = &mode
	}
	return unit.Reboot(m, unit.DefaultRunner, u.view, u.mounts)
}
func (u unitActions) Restart(mode string) error { return unit.Restart(mode, unit.DefaultRunner) }

// fullView is what main() actually needs from a view: the full agent.View
// contract, identity's key-generation callbacks, and do_manual_mode's
// display hook. Both *cli.HeadlessView (headless fallback) and
// *display.Display (real rendering, when the resolved hw.Driver's
// Initialize() succeeds) satisfy this.
type fullView interface {
	agent.View
	cli.ManualModeView
	OnKeysGeneration()
	OnShutdown() // both *cli.HeadlessView and *view.View (via *display.Display) implement this; needed for web.Actions.Shutdown's real unit.Shutdown call
}

func main() {
	os.Exit(run())
}

func run() int {
	args, err := cli.ParseArgs(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		if ee, ok := err.(*cli.ExitError); ok {
			return ee.Code
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if args.UsedPluginCmd {
		return runPluginCmd(args)
	}

	if args.Version {
		fmt.Println(version.Version)
		return 0
	}

	if args.Donate {
		fmt.Println(cli.DonateText)
		return 0
	}

	if args.CheckUpdate {
		if err := cli.CheckUpdate(version.Version, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}

	cfg, err := config.LoadConfig(config.Args{Config: args.Config, UserConfig: args.UserConfig}, func(f string, a ...interface{}) {
		fmt.Printf(f+"\n", a...)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if args.PrintConfig {
		tmp, err := os.CreateTemp("", "pwnagotchi-print-config-*.toml")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer os.Remove(tmp.Name())
		tmp.Close()
		if err := config.SaveConfig(cfg, tmp.Name()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		data, err := os.ReadFile(tmp.Name())
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Print(string(data))
		return 0
	}

	stopMounts := make(chan struct{})
	defer close(stopMounts)
	mounts, err := fs.SetupMounts(fs.ExecRunner{}, boolField(fsMemory(cfg), "enabled"), mountConfigsFrom(cfg), stopMounts, fs.DefaultRunRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	unitMounts := make([]unit.Mount, len(mounts))
	for i, m := range mounts {
		unitMounts[i] = m
	}

	logger, err := golog.SetupLogging(golog.Args{Debug: args.Debug}, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	logger.Info(golog.StartupBanner)

	// The native plugin manager: every plugin (all 23 bundled + example,
	// all native as of this migration) registers here, so every one gets
	// a real serial per-plugin queue, panic isolation, and observable
	// handled/dropped/panic counters. There is no Python subprocess
	// bridge anymore — internal/pyplugin was removed once every bundled
	// plugin had a native Go port; emit IS the manager directly.
	//
	// v and a are declared here (zero-valued) and assigned further down,
	// once the real display/agent exist — capsFor closes over these
	// variables by reference, not by value, so it always sees the fully
	// constructed objects PROVIDED every pluginMgr.Register/Load/LoadAll
	// call happens after both assignments below. Constructing the
	// *Manager itself here (rather than after agent/display exist) is
	// still required: it must already be part of `emit` before
	// display.New/agent.New run, since those are exactly what starts
	// emitting the events plugins react to.
	var v fullView
	var a *agent.Agent
	var pluginMgr *pluginmanager.Manager
	capsFor := func(pluginName string, pluginCfg config.Map) pluginmanager.Capabilities {
		return pluginmanager.Capabilities{
			Config:     pluginCfg,
			Agent:      a,
			View:       pluginhost.View{V: v},
			Exec:       pluginhost.Exec{},
			System:     unitActions{view: v, mounts: unitMounts},
			HTTPClient: &http.Client{Timeout: 30 * time.Second},
			Emit:       pluginMgr.On,
		}
	}
	pluginMgr = pluginmanager.New(pluginmanager.Options{CapabilitiesFor: capsFor})

	var emit interface {
		On(event string, args ...interface{})
	} = pluginMgr

	name := stringField(mainField(cfg), "name", "")
	initialState := map[string]interface{}{"name": fmt.Sprintf("%s>", name)}

	// display = Display(config=config, state={'name': '%s>' % pwnagotchi.name()})
	// — try the real rendering pipeline first (hw.NewDriver + Layout()
	// succeed for EVERY registered display type, since layout() is pure
	// data with no hardware I/O in Python either; only Initialize() can
	// fail, and only when ui.display.enabled=true against unimplemented
	// hardware). Fall back to headless (log-only) operation with a clear
	// message if it doesn't, per "unsupported systems return clear
	// errors" — never silently no-op.
	realDisplay, dispErr := display.New(cfg, initialState, emit)
	if dispErr != nil {
		log.Printf("falling back to headless UI (no real display): %v", dispErr)
		v = cli.NewHeadlessView(voice.New(stringField(mainField(cfg), "lang", "en")))
	} else {
		v = realDisplay
		defer realDisplay.Stop()
		// view.update()'s real web.update_frame(self._canvas) call, ported:
		// every real rendered frame is also saved for the web UI's "/ui"
		// route to serve.
		realDisplay.OnRender(func(img *image.Gray) { web.UpdateFrame(img) })
	}

	// pwnagotchi.set_name(config['main']['name']) — matches Python's exact
	// call timing: BEFORE the view exists (view.ROOT is unset at this
	// point in cli.py too), so a name change reboots with no UI refresh,
	// but mounts (already set up above, matching fs.setup_mounts's
	// earlier position in cli.py) ARE synced first. NOTE: this differs
	// from Python's literal ordering (set_name happens before Display() is
	// constructed there) only in that Go must construct the view first to
	// decide real-vs-headless; set_name's own behavior (view=nil passed to
	// Reboot) is unaffected either way since Python's set_name doesn't
	// reference the display it hasn't built yet either.
	if err := unit.SetName(name, unit.DefaultRunner, nil, unitMounts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if args.DoClear {
		if realDisplay != nil {
			if err := realDisplay.Clear(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
		} else {
			v.(*cli.HeadlessView).Clear()
		}
		return 0
	}

	keysPath := stringField(mainField(cfg), "keys_path", identity.DefaultPath)
	keypair, err := identity.NewKeyPair(keysPath, v)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	a, err = agent.New(v, cfg, keypair, emit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	gridClient := grid.NewClient(version.Version)

	// Only now (real *agent.Agent + real view/display both constructed)
	// is it safe to actually start native plugins — capsFor above closes
	// over `a`/`v`, so any plugin's OnLoad from this point on sees the
	// real, fully-wired capabilities, never a nil/zero placeholder.
	registerNativePlugins(pluginMgr, cfg, gridClient, a)
	if errs := pluginMgr.LoadAll(cfg, nativePluginConfigs(cfg)); len(errs) > 0 {
		for _, e := range errs {
			log.Printf("pluginmanager: %v", e)
		}
	}
	defer pluginMgr.Shutdown()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGUSR1)
	go func() {
		for range sigCh {
			mode := "AUTO"
			if args.DoManual {
				mode = "MANU"
			}
			a.Restart(mode)
		}
	}()

	stop := make(chan struct{})

	// web.Server(agent, config['ui']) — real net/http server, started
	// unconditionally (matching Python's own Server.__init__, which is a
	// no-op internally when ui.web.enabled is false).
	webServer := web.New(cfg, name, agentInfoAdapter{a}, gridClient, pluginMgr,
		unitActions{view: v, mounts: unitMounts}, cfg, "/etc/pwnagotchi/config.toml")
	webServer.Start()

	if args.DoManual {
		cli.RunManualMode(a, v, args.SkipSession, gridClient, emit, stop)
	} else {
		cli.RunAutoMode(a, args.SkipSession, gridClient, emit, stop)
	}
	return 0
}

func runPluginCmd(args *cli.Args) int {
	cfg, err := config.LoadConfig(config.Args{Config: args.Config, UserConfig: args.UserConfig}, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if _, err := golog.SetupLogging(golog.Args{Debug: args.Debug}, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	p := args.Plugin
	switch p.Cmd {
	case "update":
		return plugins.Update(cfg)
	case "search":
		return plugins.ListPlugins(cfg, true, p.Pattern)
	case "install":
		return plugins.Install(cfg, args.UserConfig, p.Name)
	case "uninstall":
		return plugins.Uninstall(cfg, p.Name)
	case "list":
		return plugins.ListPlugins(cfg, p.Installed, "*")
	case "enable":
		return plugins.Enable(cfg, args.UserConfig, p.Name)
	case "disable":
		return plugins.Disable(cfg, args.UserConfig, p.Name)
	case "upgrade":
		return plugins.Upgrade(cfg, p.Pattern)
	case "edit":
		return plugins.Edit(cfg, args.UserConfig, p.Name)
	case "doctor":
		return plugins.Doctor(cfg)
	default:
		// Python: handle_cmd's if/elif chain falls through to `raise
		// NotImplementedError()` when `plugins` is invoked with no
		// sub-subcommand — an uncaught exception, exit code 1.
		fmt.Fprintln(os.Stderr, "NotImplementedError")
		return 1
	}
}

func mainField(cfg config.Map) config.Map {
	m, _ := cfg["main"].(config.Map)
	return m
}

// nativePluginConfigs extracts config['main']['plugins'] as the
// map[string]config.Map pluginmanager.Manager.LoadAll expects, so every
// native plugin's own enabled flag (matching real Python's per-plugin
// config-driven activation, not "always on regardless of config") is
// respected uniformly, instead of each plugin's registration site
// special-casing its own enabled check.
func nativePluginConfigs(cfg config.Map) map[string]config.Map {
	out := map[string]config.Map{}
	pluginsCfg, _ := mainField(cfg)["plugins"].(config.Map)
	for name, raw := range pluginsCfg {
		if m, ok := raw.(config.Map); ok {
			out[name] = m
		}
	}
	return out
}

// registerNativePlugins registers every bundled plugin that has a native
// Go implementation with the plugin manager. Registration alone doesn't
// start a plugin — nativePluginConfigs + LoadAll (called right after
// this) decides which of these actually load, based on each plugin's own
// config `enabled` flag, exactly like real Python's plugins.load().
func registerNativePlugins(mgr *pluginmanager.Manager, cfg config.Map, gridClient *grid.Client, a *agent.Agent) {
	wpaSecPlugin := &pluginmanager.EmitterPlugin{
		PluginName: "wpa-sec",
		Meta: pluginmanager.Metadata{
			Version:     "2.1.2",
			Author:      "33197631+dadav@users.noreply.github.com",
			License:     "GPL3",
			Description: "This plugin automatically uploads handshakes to https://wpa-sec.stanev.org",
		},
		Emitter: wpasec.New(cfg),
	}
	if err := mgr.Register(wpaSecPlugin); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(memtemp.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(cache.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(switcher.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(gpiobuttons.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(wittypi.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(upslite.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(pwncrack.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(gps.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(example.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(autotune.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(autobackup.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(autoupdate.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(ohcapi.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(sessionstats.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(webgpsmap.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(fixservices.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(pisugarx.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(pwnstoreui.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(bttether.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(wigle.New()); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(gridplugin.New(gridClient, func() grid.SessionSummary {
		ls := a.LastSession
		if ls == nil {
			return grid.SessionSummary{}
		}
		return grid.SessionSummary{
			Epochs:      ls.Epochs,
			TrainEpochs: ls.TrainEpochs,
			AvgReward:   ls.AvgReward,
			MinReward:   ls.MinReward,
			MaxReward:   ls.MaxReward,
			Deauthed:    ls.Deauthed,
			Associated:  ls.Associated,
			Handshakes:  ls.Handshakes,
			Peers:       ls.Peers,
		}
	})); err != nil {
		log.Printf("pluginmanager: %v", err)
	}

	// logtail/webcfg are already real, native, bridge-free implementations
	// living in internal/web (predating the plugin manager) — folded in
	// here only for accurate Manager.List()/toggle bookkeeping; their
	// actual HTTP routes stay exactly as internal/web already serves them.
	//
	// REAL BUG FIXED HERE: HasWebhook was never set on either of these
	// Metadata literals, defaulting to false. internal/web/templates/
	// plugins.tmpl only renders a plugin's name as a clickable link to
	// its dashboard when HasWebhook is true (`{{if .HasWebhook}}`) —
	// with it false, the /plugins listing page rendered "logtail" and
	// "webcfg" as plain, unclickable text, even though both dashboards
	// work correctly when reached directly by URL (confirmed via direct
	// HTTP testing: /plugins/webcfg and /plugins/logtail both serve real
	// content). Confirmed on real hardware: this was the actual reason
	// neither was reachable from the plugin listing page.
	if err := mgr.Register(pluginhost.MetadataOnlyPlugin{
		PluginName: "logtail",
		Meta: pluginmanager.Metadata{
			Version:     "0.1.0",
			Author:      "33197631+dadav@users.noreply.github.com (original), Go port by raf181",
			License:     "GPL3",
			Description: "This plugin tails the logfile.",
			HasWebhook:  true,
		},
	}); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
	if err := mgr.Register(pluginhost.MetadataOnlyPlugin{
		PluginName: "webcfg",
		Meta: pluginmanager.Metadata{
			Version:     "1.0.0",
			Author:      "33197631+dadav@users.noreply.github.com modified by wsvdmeer (original), Go port by raf181",
			License:     "GPL3",
			Description: "This plugin allows the user to make runtime changes.",
			HasWebhook:  true,
		},
	}); err != nil {
		log.Printf("pluginmanager: %v", err)
	}
}

func fsMemory(cfg config.Map) config.Map {
	f, _ := cfg["fs"].(config.Map)
	if f == nil {
		return nil
	}
	m, _ := f["memory"].(config.Map)
	return m
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

func mountConfigsFrom(cfg config.Map) map[string]fs.MountConfig {
	out := map[string]fs.MountConfig{}
	mem := fsMemory(cfg)
	if mem == nil {
		return out
	}
	mounts, _ := mem["mounts"].(config.Map)
	for name, raw := range mounts {
		m, ok := raw.(config.Map)
		if !ok {
			continue
		}
		out[name] = fs.MountConfig{
			Enabled: boolField(m, "enabled"),
			Mount:   stringField(m, "mount", ""),
			Size:    stringField(m, "size", "40M"),
			Zram:    boolField(m, "zram"),
			Rsync:   boolField(m, "rsync"),
			Sync:    intField(m, "sync"),
		}
	}
	return out
}

func intField(m config.Map, key string) int {
	switch v := m[key].(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}
