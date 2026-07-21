package hw

import (
	"errors"
	"fmt"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
)

// ErrNoDriverInPythonEither is returned for "weact2in9" — a REAL bug in
// pwnagotchi/ui/hw/__init__.py's display_for(): utils.load_config's
// display-type normalization accepts both "weact2in9" and "weact29in" as
// aliases for the canonical type "weact2in9" (see
// testdata/display_type_alias_table.json), but display_for()'s if/elif
// chain has NO branch for "weact2in9" at all and no trailing else — so in
// real Python, selecting this display type makes display_for() fall
// through and implicitly `return None`, and the daemon later crashes with
// `AttributeError: 'NoneType' object has no attribute '...'` the first time
// anything calls a method on the display. Verified by reading
// hw/__init__.py's full if/elif chain end-to-end; there is no dead code
// path that would handle it. The Go port surfaces this as an explicit,
// named error instead of silently accepting the config (which Python
// technically does, right up until the crash) — this is MORE honest than
// Python, not a regression, since a real user hitting this needs to know
// their config is unsupported in the upstream project too.
var ErrNoDriverInPythonEither = errors.New("hw: \"weact2in9\" normalizes successfully but has no display_for() branch in the real Python source either — this is a genuine upstream bug, not a Go-port gap; see docs/known-differences.md")

// NewDriver ports hw.display_for(config): resolves config['ui']['display']['type']
// (already normalized by internal/config.NormalizeDisplayType, matching
// Python's utils.load_config comment "config has been normalized already")
// to a concrete Driver.
//
// Every type in registeredTypes (except "weact2in9", see
// ErrNoDriverInPythonEither) is accepted (so an operator's existing config
// never fails to resolve just because the native driver isn't written
// yet); "dummydisplay" is the one driver fully implemented today. An
// unrecognized type string — which utils.load_config's normalization
// should never actually produce, since it falls back to "dummydisplay"
// itself — returns a distinct error rather than silently defaulting to
// anything.
func NewDriver(cfg config.Map) (Driver, error) {
	displayType, _ := displayConfig(cfg)["type"].(string)

	if displayType == "dummydisplay" {
		return NewDummyDisplay(cfg), nil
	}
	if displayType == "weact2in9" {
		return nil, ErrNoDriverInPythonEither
	}

	// unsupportedDriver.Name() must return the config TYPE STRING (e.g.
	// "waveshare_4"), matching what every real driver's own
	// super().__init__(config, '<type>') actually passes as self.name —
	// verified by reading every hw/*.py driver's constructor. This is NOT
	// the Python class name (e.g. "WaveshareV4"); Display.is_waveshare_v4()
	// and friends compare against exactly this type string. pythonClassNames
	// is kept only for diagnostic error text.
	if _, ok := pythonClassNames[displayType]; ok {
		return &unsupportedDriver{name: displayType, cfg: cfg}, nil
	}

	return nil, fmt.Errorf("hw: unrecognized display type %q (normalize config first)", displayType)
}

// RegisteredTypes returns every display type string NewDriver accepts, for
// diagnostics/tests.
func RegisteredTypes() []string {
	out := make([]string, len(registeredTypes))
	copy(out, registeredTypes)
	return out
}
