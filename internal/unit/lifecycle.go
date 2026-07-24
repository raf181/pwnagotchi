package unit

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// HostsPath is overridable, like unit.go's HostnamePath and friends, so
// tests don't touch the real system files.
var HostsPath = "/etc/hosts"

// autoMarkerPath/manualMarkerPath mirror the hardcoded
// "/root/.pwnagotchi-auto"/"/root/.pwnagotchi-manual" marker file paths in
// pwnagotchi.restart/reboot. Overridable for tests.
var (
	autoMarkerPath   = "/root/.pwnagotchi-auto"
	manualMarkerPath = "/root/.pwnagotchi-manual"
)

// UIGracePeriod mirrors the hardcoded time.sleep(10) Python gives the
// display to refresh after on_shutdown()/on_rebooting() before proceeding.
// Overridable so tests don't take 10 real seconds each.
var UIGracePeriod = 10 * time.Second

// ServiceRestartDelay mirrors Restart's hardcoded time.sleep(1) between
// restarting bettercap and pwnagotchi. Overridable for tests.
var ServiceRestartDelay = 1 * time.Second

// Runner executes external commands (systemctl-adjacent `service` calls,
// `hostname`, `sync`, `halt`, `shutdown -r now`) via os/exec with an
// explicit argv — never a shell string, unlike Python's
// `os.system("hostname '%s'" % new_name)` and friends. Overridable for
// tests; production wires ExecRunner.
type Runner interface {
	Run(name string, args ...string) error
}

// ExecRunner is the production Runner.
type ExecRunner struct{}

func (ExecRunner) Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// DefaultRunner is used by SetName/Restart/Reboot/Shutdown unless a test
// overrides it.
var DefaultRunner Runner = ExecRunner{}

var validNameRe = regexp.MustCompile(`^[a-zA-Z0-9\-]{2,25}$`)

// ShutdownView and RebootView are the (disjoint — Python's shutdown() only
// ever calls on_shutdown, reboot() only ever calls on_rebooting; no
// concrete view type needs to implement both to use one of these
// functions) subsets of pwnagotchi.ui.view.View the lifecycle functions
// call into. Python reaches a single global (view.ROOT); Go takes it as an
// explicit parameter instead.
type ShutdownView interface {
	OnShutdown()
}

type RebootView interface {
	OnRebooting()
}

// Mount is the subset of fs.MemoryFS the lifecycle functions call into
// (iterating fs.mounts and calling m.sync()).
type Mount interface {
	Sync(toRAM bool) (bool, error)
}

// SetName ports pwnagotchi.set_name: validates the new name, rewrites
// /etc/hostname and /etc/hosts (replacing ALL occurrences of the current
// name — matching Python's str.replace(current, new_name, -1)), runs
// `hostname <new>`, and reboots. A blank/invalid name is a silent no-op
// (after logging a warning), matching Python exactly — it does not return
// an error for that case.
func SetName(newName string, runner Runner, view RebootView, mounts []Mount) error {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return nil
	}
	if !validNameRe.MatchString(newName) {
		log.Printf("name '%s' is invalid: min length is 2, max length 25, only a-zA-Z0-9- allowed", newName)
		return nil
	}

	current, err := Name()
	if err != nil {
		return err
	}
	if newName == current {
		return nil
	}

	log.Printf("setting unit hostname '%s' -> '%s'", current, newName)
	if err := os.WriteFile(HostnamePath, []byte(newName), 0o644); err != nil {
		return err
	}
	// Python's set_name never invalidates the module-level _name cache
	// after writing the new hostname — Name()/name() would keep returning
	// the OLD name for the remainder of this process's life, which is
	// harmless in practice only because Reboot() below ends the process.
	// Preserved exactly (NOT calling ResetNameCache here) rather than
	// "fixed", per docs/known-differences.md.

	hostsData, err := os.ReadFile(HostsPath)
	if err != nil {
		return err
	}
	patched := strings.ReplaceAll(string(hostsData), current, newName)
	if err := os.WriteFile(HostsPath, []byte(patched), 0o644); err != nil {
		return err
	}

	if runner == nil {
		runner = DefaultRunner
	}
	if err := runner.Run("hostname", newName); err != nil {
		return err
	}

	return Reboot(nil, runner, view, mounts)
}

// Shutdown ports pwnagotchi.shutdown.
func Shutdown(runner Runner, view ShutdownView, mounts []Mount) error {
	log.Print("shutting down ...")

	if view != nil {
		view.OnShutdown()
		time.Sleep(UIGracePeriod)
	}

	log.Print("syncing...")
	for _, m := range mounts {
		m.Sync(false)
	}

	if runner == nil {
		runner = DefaultRunner
	}
	if err := runner.Run("sync"); err != nil {
		return err
	}
	return runner.Run("halt")
}

// Restart ports pwnagotchi.restart(mode).
func Restart(mode string, runner Runner) error {
	log.Printf("restarting in %s mode ...", mode)
	mode = strings.ToUpper(mode)

	if runner == nil {
		runner = DefaultRunner
	}

	marker := manualMarkerPath
	if mode == "AUTO" {
		marker = autoMarkerPath
	}
	if err := touch(marker); err != nil {
		return err
	}

	if err := runner.Run("service", "bettercap", "restart"); err != nil {
		return err
	}
	time.Sleep(ServiceRestartDelay)
	return runner.Run("service", "pwnagotchi", "restart")
}

// Reboot ports pwnagotchi.reboot(mode=None). mode is nil for the
// no-argument call shape, or a pointer to "AUTO"/"MANU" (any other value
// mirrors Python's silent no-op: neither marker file is touched).
func Reboot(mode *string, runner Runner, view RebootView, mounts []Mount) error {
	if mode != nil {
		upper := strings.ToUpper(*mode)
		mode = &upper
		log.Printf("rebooting in %s mode ...", *mode)
	} else {
		log.Print("rebooting ...")
	}

	if view != nil {
		view.OnRebooting()
		time.Sleep(UIGracePeriod)
	}

	if mode != nil {
		switch *mode {
		case "AUTO":
			if err := touch(autoMarkerPath); err != nil {
				return err
			}
		case "MANU":
			if err := touch(manualMarkerPath); err != nil {
				return err
			}
		}
	}

	log.Print("syncing...")
	for _, m := range mounts {
		m.Sync(false)
	}

	if runner == nil {
		runner = DefaultRunner
	}
	if err := runner.Run("sync"); err != nil {
		return err
	}
	return runner.Run("shutdown", "-r", "now")
}

// touch mirrors `os.system("touch %s" % path)`: create the file if it
// doesn't exist, and update its mtime either way — the real semantics of
// the `touch` command, done directly rather than shelling out to it.
func touch(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("unit: touch %s: %w", path, err)
	}
	f.Close()
	now := time.Now()
	return os.Chtimes(path, now, now)
}
