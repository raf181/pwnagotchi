// Package gpiobuttons is the native Go port of
// pwnagotchi/plugins/default/gpio_buttons.py: runs a configured shell
// command whenever a configured GPIO line goes low (a physical button
// press, active-low with an internal pull-up — matches real Python's
// `GPIO.setup(gpio, GPIO.IN, GPIO.PUD_UP)` + `GPIO.add_event_detect(gpio,
// GPIO.FALLING, ...)`).
//
// Original Python author: ratmandu@gmail.com (see gpio_buttons.py's own
// __author__ field, left untouched). This Go port is by raf181.
//
// The `gpios` config map's values are real shell command strings (e.g.
// "sudo pwnagotchi shutdown"), exactly the "documented configuration
// field that intentionally represents a shell program" the migration
// spec calls out (see pluginmanager.CommandRunner's doc comment) —
// real Python itself runs it via `subprocess.Popen(command, shell=True,
// ..., executable="/bin/bash")`. This port preserves that by invoking
// `/bin/bash -c <command>` through the injected CommandRunner (a real
// argv call to the shell binary, not a shell string built by this
// plugin), rather than trying to split the command into an argv itself
// (which would break real users' existing multi-word/piped commands).
package gpiobuttons

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// debounceInterval mirrors real Python's `bouncetime=600` (milliseconds)
// passed to GPIO.add_event_detect — this port's GPIOLine.WaitEdge
// contract does not expose a hardware bouncetime parameter, so it is
// reproduced here as a software debounce per pin instead.
const debounceInterval = 600 * time.Millisecond

const commandTimeout = 30 * time.Second

// Plugin ports the GPIOButtons class.
type Plugin struct {
	log  pluginmanager.Logger
	exec pluginmanager.CommandRunner
	gpio pluginmanager.GPIOCapability

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu    sync.Mutex
	lines []pluginmanager.GPIOLine
}

// New ports GPIOButtons.__init__.
func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string { return "gpio_buttons" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "1.0.0",
		Author:      "ratmandu@gmail.com",
		License:     "GPL3",
		Description: "GPIO Button support plugin",
	}
}

// OnLoad ports on_loaded: for every configured (pin -> command) pair,
// open the GPIO line and start a background goroutine watching for a
// falling edge. Real Python silently does nothing at all if RPi.GPIO
// isn't importable (non-Raspberry-Pi hardware); this port's equivalent
// is Capabilities.GPIO being nil (no real bus backend wired up yet, or a
// non-Pi host) — logged clearly, never a panic, never fake success.
func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.log = caps.Log
	p.exec = caps.Exec
	p.gpio = caps.GPIO

	buttons := parseGPIOs(caps.Config)
	if len(buttons) == 0 {
		return nil
	}
	if p.gpio == nil {
		p.logf("GPIO capability unavailable (not on real Raspberry Pi hardware, or the GPIO bus isn't wired up yet); not running")
		return nil
	}

	p.ctx, p.cancel = context.WithCancel(context.Background())
	for pin, command := range buttons {
		line, err := p.gpio.Line(pin)
		if err != nil {
			p.logf("cannot open GPIO #%d: %v", pin, err)
			continue
		}
		p.mu.Lock()
		p.lines = append(p.lines, line)
		p.mu.Unlock()

		p.wg.Add(1)
		go p.watch(pin, command, line)
		p.logf("added command: %s to GPIO #%d", command, pin)
	}
	return nil
}

// watch ports the add_event_detect callback loop: block for a falling
// edge, debounce, run the command, repeat until OnUnload cancels the
// context.
func (p *Plugin) watch(pin int, command string, line pluginmanager.GPIOLine) {
	defer p.wg.Done()
	var last time.Time
	for {
		if err := line.WaitEdge(p.ctx); err != nil {
			return // context cancelled (unload) or a real bus error either way, stop watching
		}
		now := time.Now()
		if !last.IsZero() && now.Sub(last) < debounceInterval {
			continue
		}
		last = now
		p.runCommand(pin, command)
	}
}

// runCommand ports Plugin.runcommand.
func (p *Plugin) runCommand(pin int, command string) {
	p.logf("Button Pressed! Running command: %s", command)
	if p.exec == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	if _, err := p.exec.Run(ctx, "/bin/bash", "-c", command); err != nil {
		p.logf("GPIO #%d: command failed: %v", pin, err)
	}
}

// OnUnload stops every watch goroutine and closes its GPIO line —
// real Python has no on_unload for this plugin at all (RPi.GPIO's
// process-global event detection just dies with the process), but a
// long-lived native plugin manager needs a real, leak-free teardown.
func (p *Plugin) OnUnload() error {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, l := range p.lines {
		_ = l.Close()
	}
	p.lines = nil
	return nil
}

func (p *Plugin) logf(format string, args ...interface{}) {
	if p.log != nil {
		p.log.Printf(format, args...)
	}
}

// parseGPIOs ports the `gpios = self.options['gpios']` read: a config
// map keyed by GPIO pin number (as a string, matching real TOML table
// key syntax) to a shell command string.
func parseGPIOs(cfg config.Map) map[int]string {
	raw, _ := cfg["gpios"].(config.Map)
	if len(raw) == 0 {
		return nil
	}
	out := make(map[int]string, len(raw))
	for pinStr, v := range raw {
		command, ok := v.(string)
		if !ok || command == "" {
			continue
		}
		pin, err := strconv.Atoi(pinStr)
		if err != nil {
			continue
		}
		out[pin] = command
	}
	return out
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.Unloader = (*Plugin)(nil)
