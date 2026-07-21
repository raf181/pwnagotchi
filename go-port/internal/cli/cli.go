// Package cli ports pwnagotchi/cli.py: argument parsing and the top-level
// pwnagotchi_cli() dispatch (--version/--donate/--check-update/
// --print-config/--clear/manual-vs-auto mode, plus the `plugins`
// subcommand).
package cli

import (
	"fmt"
	"os"
	"strings"
)

// HelpText mirrors argparse's --help output byte-for-byte, captured from
// the real Python interpreter (docs/python-baseline.md) at its default
// ~80-column wrap. Column-for-column argparse HelpFormatter behavior isn't
// reimplemented generically — this is the one fixed string it produces for
// pwnagotchi's actual flag set, which is all "preserve --help output"
// requires for a fixed CLI surface like this one.
const HelpText = `usage: pwnagotchi [-h] [-C CONFIG] [-U USER_CONFIG] [--manual]
                  [--skip-session] [--clear] [--debug] [--version]
                  [--print-config] [--check-update] [--donate]
                  {plugins} ...

positional arguments:
  {plugins}

options:
  -h, --help            show this help message and exit
  -C CONFIG, --config CONFIG
                        Main configuration file.
  -U USER_CONFIG, --user-config USER_CONFIG
                        If this file exists, configuration will be merged and
                        this will override default values.
  --manual              Manual mode.
  --skip-session        Skip last session parsing in manual mode.
  --clear               Clear the ePaper display and exit.
  --debug               Enable debug logs.
  --version             Print the version.
  --print-config        Print the configuration.
  --check-update        Check for updates on Pwnagotchi. And tells current
                        version.
  --donate              How to donate to this project.
`

// DonateText mirrors cli.py's --donate output exactly.
const DonateText = "Donations can be made @ \n " +
	"https://github.com/sponsors/jayofelony \n\n" +
	"But only if you really want to!"

// PluginArgs mirrors the `plugins` subcommand's own argparse namespace
// fields (plugincmd, pattern, installed, name).
type PluginArgs struct {
	Cmd       string // "" if the plugins subcommand wasn't used at all
	Pattern   string
	Installed bool
	Name      string
}

// Args mirrors cli.py's top-level argparse Namespace.
type Args struct {
	Config      string
	UserConfig  string
	DoManual    bool
	SkipSession bool
	DoClear     bool
	Debug       bool
	Version     bool
	PrintConfig bool
	CheckUpdate bool
	Donate      bool

	// UsedPluginCmd mirrors used_plugin_cmd(args): true whenever the
	// `plugins` subcommand was invoked at all (hasattr(args, 'plugincmd')
	// in Python), even with no further sub-subcommand — which Python
	// dispatches to handle_cmd's `raise NotImplementedError()`.
	UsedPluginCmd bool
	Plugin        PluginArgs
}

// Default flag values, matching cli.py's add_argument(..., default=...) calls.
const (
	DefaultConfig     = "/etc/pwnagotchi/default.toml"
	DefaultUserConfig = "/etc/pwnagotchi/config.toml"
)

// ExitError signals that ParseArgs already printed the appropriate output
// (help/usage/error text) and the caller should os.Exit(Code) without
// printing anything further — matching argparse's own exit-on-parse-error
// and exit-on---help behavior.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit %d", e.Code) }

// ParseArgs ports cli.py's argparse.ArgumentParser setup and parse_args()
// call. argv should NOT include the program name (matching os.Args[1:]).
func ParseArgs(argv []string, stdout, stderr *os.File) (*Args, error) {
	a := &Args{Config: DefaultConfig, UserConfig: DefaultUserConfig}

	i := 0
	next := func(flag string) (string, error) {
		i++
		if i >= len(argv) {
			fmt.Fprintf(stderr, "pwnagotchi: error: argument %s: expected one argument\n", flag)
			return "", &ExitError{Code: 2}
		}
		return argv[i], nil
	}

	for ; i < len(argv); i++ {
		arg := argv[i]
		switch arg {
		case "-h", "--help":
			fmt.Fprint(stdout, HelpText)
			return nil, &ExitError{Code: 0}
		case "-C", "--config":
			v, err := next(arg)
			if err != nil {
				return nil, err
			}
			a.Config = v
		case "-U", "--user-config":
			v, err := next(arg)
			if err != nil {
				return nil, err
			}
			a.UserConfig = v
		case "--manual":
			a.DoManual = true
		case "--skip-session":
			a.SkipSession = true
		case "--clear":
			a.DoClear = true
		case "--debug":
			a.Debug = true
		case "--version":
			a.Version = true
		case "--print-config":
			a.PrintConfig = true
		case "--check-update":
			a.CheckUpdate = true
		case "--donate":
			a.Donate = true
		case "plugins":
			pa, err := parsePluginArgs(argv[i+1:], stdout, stderr)
			if err != nil {
				return nil, err
			}
			a.Plugin = *pa
			a.UsedPluginCmd = true
			return a, nil
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(stderr, "pwnagotchi: error: unrecognized arguments: %s\n", arg)
				return nil, &ExitError{Code: 2}
			}
			fmt.Fprintf(stderr, "pwnagotchi: error: unrecognized arguments: %s\n", arg)
			return nil, &ExitError{Code: 2}
		}
	}

	return a, nil
}

var pluginSubcommands = map[string]bool{
	"search": true, "list": true, "update": true, "upgrade": true,
	"enable": true, "disable": true, "install": true, "uninstall": true, "edit": true,
}

func parsePluginArgs(argv []string, stdout, stderr *os.File) (*PluginArgs, error) {
	pa := &PluginArgs{}
	if len(argv) == 0 {
		// Matches Python: `plugincmd` remains None (subparsers dest with no
		// required=True), used_plugin_cmd(args) is still True (hasattr),
		// but handle_cmd's if/elif chain falls through to `raise
		// NotImplementedError()`.
		return pa, nil
	}
	if !pluginSubcommands[argv[0]] {
		fmt.Fprintf(stderr, "pwnagotchi: error: argument {plugins}: invalid choice: %q\n", argv[0])
		return nil, &ExitError{Code: 2}
	}
	pa.Cmd = argv[0]
	pa.Pattern = "*" // upgrade's own default; overwritten below for search which requires it

	rest := argv[1:]
	switch pa.Cmd {
	case "search":
		if len(rest) == 0 {
			fmt.Fprintln(stderr, "pwnagotchi: error: the following arguments are required: pattern")
			return nil, &ExitError{Code: 2}
		}
		pa.Pattern = rest[0]
	case "upgrade":
		if len(rest) > 0 {
			pa.Pattern = rest[0]
		}
	case "list":
		for _, arg := range rest {
			if arg == "-i" || arg == "--installed" {
				pa.Installed = true
			}
		}
	case "enable", "disable", "install", "uninstall", "edit":
		if len(rest) == 0 {
			fmt.Fprintln(stderr, "pwnagotchi: error: the following arguments are required: name")
			return nil, &ExitError{Code: 2}
		}
		pa.Name = rest[0]
	case "update":
		// no extra arguments
	}
	return pa, nil
}
