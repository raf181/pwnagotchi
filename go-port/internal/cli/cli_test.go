package cli

import (
	"os"
	"testing"
)

func TestParseArgsDefaults(t *testing.T) {
	a, err := ParseArgs(nil, os.Stdout, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if a.Config != DefaultConfig || a.UserConfig != DefaultUserConfig {
		t.Fatalf("defaults = %q, %q", a.Config, a.UserConfig)
	}
	if a.DoManual || a.SkipSession || a.DoClear || a.Debug || a.Version || a.PrintConfig || a.CheckUpdate || a.Donate || a.UsedPluginCmd {
		t.Fatalf("expected all bools false by default, got %+v", a)
	}
}

func TestParseArgsFlags(t *testing.T) {
	a, err := ParseArgs([]string{
		"-C", "/tmp/default.toml",
		"-U", "/tmp/config.toml",
		"--manual", "--skip-session", "--clear", "--debug",
		"--version", "--print-config", "--check-update", "--donate",
	}, os.Stdout, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if a.Config != "/tmp/default.toml" || a.UserConfig != "/tmp/config.toml" {
		t.Fatalf("config paths = %q, %q", a.Config, a.UserConfig)
	}
	if !a.DoManual || !a.SkipSession || !a.DoClear || !a.Debug || !a.Version || !a.PrintConfig || !a.CheckUpdate || !a.Donate {
		t.Fatalf("expected all bool flags true, got %+v", a)
	}
}

func TestParseArgsLongFormConfig(t *testing.T) {
	a, err := ParseArgs([]string{"--config", "/x.toml", "--user-config", "/y.toml"}, os.Stdout, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if a.Config != "/x.toml" || a.UserConfig != "/y.toml" {
		t.Fatalf("got %q, %q", a.Config, a.UserConfig)
	}
}

func TestParseArgsHelpExits0(t *testing.T) {
	r, w, _ := os.Pipe()
	defer r.Close()
	_, err := ParseArgs([]string{"--help"}, w, os.Stderr)
	w.Close()
	ee, ok := err.(*ExitError)
	if !ok || ee.Code != 0 {
		t.Fatalf("expected ExitError{0}, got %v", err)
	}
}

func TestParseArgsUnknownFlagExits2(t *testing.T) {
	_, err := ParseArgs([]string{"--bogus-flag"}, os.Stdout, os.Stderr)
	ee, ok := err.(*ExitError)
	if !ok || ee.Code != 2 {
		t.Fatalf("expected ExitError{2}, got %v", err)
	}
}

func TestParseArgsMissingValueExits2(t *testing.T) {
	_, err := ParseArgs([]string{"--config"}, os.Stdout, os.Stderr)
	ee, ok := err.(*ExitError)
	if !ok || ee.Code != 2 {
		t.Fatalf("expected ExitError{2} for a flag missing its value, got %v", err)
	}
}

func TestParseArgsPluginsSubcommands(t *testing.T) {
	cases := []struct {
		argv []string
		want PluginArgs
	}{
		{[]string{"plugins", "search", "wpa*"}, PluginArgs{Cmd: "search", Pattern: "wpa*"}},
		{[]string{"plugins", "list"}, PluginArgs{Cmd: "list", Pattern: "*"}},
		{[]string{"plugins", "list", "-i"}, PluginArgs{Cmd: "list", Pattern: "*", Installed: true}},
		{[]string{"plugins", "list", "--installed"}, PluginArgs{Cmd: "list", Pattern: "*", Installed: true}},
		{[]string{"plugins", "update"}, PluginArgs{Cmd: "update", Pattern: "*"}},
		{[]string{"plugins", "upgrade"}, PluginArgs{Cmd: "upgrade", Pattern: "*"}},
		{[]string{"plugins", "upgrade", "grid*"}, PluginArgs{Cmd: "upgrade", Pattern: "grid*"}},
		{[]string{"plugins", "enable", "grid"}, PluginArgs{Cmd: "enable", Pattern: "*", Name: "grid"}},
		{[]string{"plugins", "disable", "grid"}, PluginArgs{Cmd: "disable", Pattern: "*", Name: "grid"}},
		{[]string{"plugins", "install", "grid"}, PluginArgs{Cmd: "install", Pattern: "*", Name: "grid"}},
		{[]string{"plugins", "uninstall", "grid"}, PluginArgs{Cmd: "uninstall", Pattern: "*", Name: "grid"}},
		{[]string{"plugins", "edit", "grid"}, PluginArgs{Cmd: "edit", Pattern: "*", Name: "grid"}},
	}
	for _, c := range cases {
		a, err := ParseArgs(c.argv, os.Stdout, os.Stderr)
		if err != nil {
			t.Fatalf("%v: %v", c.argv, err)
		}
		if !a.UsedPluginCmd {
			t.Fatalf("%v: UsedPluginCmd should be true", c.argv)
		}
		if a.Plugin != c.want {
			t.Fatalf("%v: Plugin = %+v, want %+v", c.argv, a.Plugin, c.want)
		}
	}
}

func TestParseArgsPluginsNoSubcommandStillUsed(t *testing.T) {
	a, err := ParseArgs([]string{"plugins"}, os.Stdout, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !a.UsedPluginCmd {
		t.Fatal("bare `plugins` with no sub-subcommand must still count as used (matches Python's hasattr check)")
	}
	if a.Plugin.Cmd != "" {
		t.Fatalf("Plugin.Cmd = %q, want empty", a.Plugin.Cmd)
	}
}

func TestParseArgsPluginsSearchRequiresPattern(t *testing.T) {
	_, err := ParseArgs([]string{"plugins", "search"}, os.Stdout, os.Stderr)
	ee, ok := err.(*ExitError)
	if !ok || ee.Code != 2 {
		t.Fatalf("expected ExitError{2} for missing search pattern, got %v", err)
	}
}

func TestParseArgsPluginsInvalidSubcommand(t *testing.T) {
	_, err := ParseArgs([]string{"plugins", "bogus"}, os.Stdout, os.Stderr)
	ee, ok := err.(*ExitError)
	if !ok || ee.Code != 2 {
		t.Fatalf("expected ExitError{2} for an invalid plugins subcommand, got %v", err)
	}
}
