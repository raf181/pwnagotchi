package pluginhost

import (
	"context"
	"os/exec"
)

// Exec is the production pluginmanager.CommandRunner: real argument-vector
// process execution (never a shell string), matching this port's
// established "avoid unnecessary shell execution" requirement. Plugins
// needing arbitrary shell behavior (switcher.py's configured
// `commands: [...]` list) write those commands into a real script file
// and execute the file itself via this runner, exactly like the plugin's
// own generated systemd unit does — the shell interpretation is confined
// to that one documented, intentionally-shell-program config field, never
// to this runner's own argv.
type Exec struct{}

// Run implements pluginmanager.CommandRunner.
func (Exec) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}
