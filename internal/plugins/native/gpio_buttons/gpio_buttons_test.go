package gpiobuttons

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// fakeLine is an in-memory GPIOLine: WaitEdge blocks until either the
// test sends an edge on trigger, or ctx is cancelled.
type fakeLine struct {
	trigger chan struct{}
	closed  bool
	mu      sync.Mutex
}

func newFakeLine() *fakeLine { return &fakeLine{trigger: make(chan struct{}, 8)} }

func (l *fakeLine) Read() (bool, error) { return false, nil }
func (l *fakeLine) Write(bool) error    { return nil }
func (l *fakeLine) WaitEdge(ctx context.Context) error {
	select {
	case <-l.trigger:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (l *fakeLine) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	return nil
}
func (l *fakeLine) press() { l.trigger <- struct{}{} }
func (l *fakeLine) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

type fakeGPIO struct {
	mu    sync.Mutex
	lines map[int]*fakeLine
	err   map[int]error
}

func newFakeGPIO() *fakeGPIO { return &fakeGPIO{lines: map[int]*fakeLine{}, err: map[int]error{}} }

func (g *fakeGPIO) Line(pin int) (pluginmanager.GPIOLine, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err, ok := g.err[pin]; ok {
		return nil, err
	}
	l := newFakeLine()
	g.lines[pin] = l
	return l, nil
}

type recordedCmd struct {
	name string
	args []string
}

type fakeRunner struct {
	mu   sync.Mutex
	cmds []recordedCmd
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.cmds = append(f.cmds, recordedCmd{name, args})
	f.mu.Unlock()
	return nil, nil
}

func (f *fakeRunner) snapshot() []recordedCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedCmd(nil), f.cmds...)
}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", timeout)
	}
}

func TestParseGPIOsReadsMultipleButtons(t *testing.T) {
	cfg := config.Map{"gpios": config.Map{"26": "echo one", "13": "echo two"}}
	got := parseGPIOs(cfg)
	if len(got) != 2 || got[26] != "echo one" || got[13] != "echo two" {
		t.Fatalf("unexpected parse result: %v", got)
	}
}

func TestParseGPIOsIgnoresInvalidEntries(t *testing.T) {
	cfg := config.Map{"gpios": config.Map{"notanumber": "echo x", "5": "", "7": 123}}
	got := parseGPIOs(cfg)
	if len(got) != 0 {
		t.Fatalf("expected all invalid entries skipped, got %v", got)
	}
}

func TestButtonPressRunsCommandViaBash(t *testing.T) {
	gpio := newFakeGPIO()
	runner := &fakeRunner{}
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"gpios": config.Map{"26": "sudo pwnagotchi shutdown"}},
		GPIO:   gpio,
		Exec:   runner,
	}); err != nil {
		t.Fatal(err)
	}
	defer p.OnUnload()

	line := gpio.lines[26]
	if line == nil {
		t.Fatal("expected GPIO 26 to be opened")
	}
	line.press()

	waitForCondition(t, time.Second, func() bool { return len(runner.snapshot()) == 1 })
	cmd := runner.snapshot()[0]
	if cmd.name != "/bin/bash" || len(cmd.args) != 2 || cmd.args[0] != "-c" || cmd.args[1] != "sudo pwnagotchi shutdown" {
		t.Fatalf("unexpected command invocation: %+v", cmd)
	}
}

func TestDebounceSuppressesRapidRepeatedPresses(t *testing.T) {
	gpio := newFakeGPIO()
	runner := &fakeRunner{}
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"gpios": config.Map{"26": "echo pressed"}},
		GPIO:   gpio,
		Exec:   runner,
	}); err != nil {
		t.Fatal(err)
	}
	defer p.OnUnload()

	line := gpio.lines[26]
	line.press()
	line.press()
	line.press()

	waitForCondition(t, time.Second, func() bool { return len(runner.snapshot()) >= 1 })
	time.Sleep(50 * time.Millisecond) // let any (incorrect) extra runs land
	if got := len(runner.snapshot()); got != 1 {
		t.Fatalf("expected exactly 1 command run within the debounce window, got %d", got)
	}
}

func TestNilGPIOCapabilityDoesNotPanic(t *testing.T) {
	p := New()
	err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"gpios": config.Map{"26": "echo hi"}},
		GPIO:   nil,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err := p.OnUnload(); err != nil {
		t.Fatalf("OnUnload: %v", err)
	}
}

func TestNoConfiguredButtonsIsANoOp(t *testing.T) {
	gpio := newFakeGPIO()
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{Config: config.Map{}, GPIO: gpio}); err != nil {
		t.Fatal(err)
	}
	if len(gpio.lines) != 0 {
		t.Fatalf("expected no GPIO lines opened with no configured buttons, got %v", gpio.lines)
	}
}

func TestGPIOLineOpenErrorIsSkippedNotFatal(t *testing.T) {
	gpio := newFakeGPIO()
	gpio.err[26] = context.DeadlineExceeded // any real error
	runner := &fakeRunner{}
	p := New()
	err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"gpios": config.Map{"26": "echo hi"}},
		GPIO:   gpio,
		Exec:   runner,
	})
	if err != nil {
		t.Fatalf("expected OnLoad to tolerate a per-pin open failure, got %v", err)
	}
}

func TestOnUnloadStopsWatchersAndClosesLines(t *testing.T) {
	gpio := newFakeGPIO()
	runner := &fakeRunner{}
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"gpios": config.Map{"26": "echo hi"}},
		GPIO:   gpio,
		Exec:   runner,
	}); err != nil {
		t.Fatal(err)
	}
	line := gpio.lines[26]

	if err := p.OnUnload(); err != nil {
		t.Fatalf("OnUnload: %v", err)
	}
	if !line.isClosed() {
		t.Fatal("expected the GPIO line to be closed on unload")
	}

	// A press after unload must not run anything: the watcher goroutine
	// has already exited (OnUnload's p.wg.Wait() only returns after it
	// does), so this would just leak into an unbuffered send if it were
	// still running - use a non-blocking send to prove nobody's listening.
	select {
	case line.trigger <- struct{}{}:
	default:
	}
	time.Sleep(20 * time.Millisecond)
	if len(runner.snapshot()) != 0 {
		t.Fatal("expected no command execution after unload")
	}
}
