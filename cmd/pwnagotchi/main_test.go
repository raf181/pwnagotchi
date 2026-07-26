package main

import (
	"path/filepath"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/cli"
)

func TestPluginListDoesNotRequireDaemonLogDirectory(t *testing.T) {
	args := &cli.Args{
		Config:        filepath.Join("..", "..", "internal", "config", "defaults.toml"),
		UserConfig:    filepath.Join(t.TempDir(), "missing-user.toml"),
		UsedPluginCmd: true,
		Plugin:        cli.PluginArgs{Cmd: "list"},
	}
	if code := runPluginCmd(args); code != 0 {
		t.Fatalf("runPluginCmd(list) exit code = %d, want 0", code)
	}
}
