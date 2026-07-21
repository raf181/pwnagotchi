package plugins

import (
	"log"
	"os"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
	"github.com/jayofelony/pwnagotchi/go-port/internal/pyplugin"
)

// PythonInterpreter resolves which python3 to run the real bundled/custom
// plugins under, via the PWNAGOTCHI_PYTHON env var (already the
// established convention for pointing at a Python interpreter with the
// real pwnagotchi package importable — see Makefile's compatibility-test
// target and docs/python-baseline.md). On a real deployed pwnagotchi unit
// this is unset and plain "python3" already has the real package installed
// system-wide; in this dev repo it must point at ../venv/bin/python3.
func PythonInterpreter() string {
	if p := os.Getenv("PWNAGOTCHI_PYTHON"); p != "" {
		return p
	}
	return "python3"
}

// Load ports the daemon-startup call to plugins.load(config): starts the
// real Python subprocess bridge (internal/pyplugin) and runs the genuine,
// unmodified pwnagotchi.plugins.load(config) inside it — real bundled and
// custom plugins, not a reimplementation.
//
// Mirrors Python's own load()'s top-level try/except Exception: any
// failure to start the bridge (no python3 on PATH, pwnagotchi package not
// importable, ...) is logged clearly here and returns an error so the
// caller falls back to a no-op EventEmitter, exactly like a real
// pwnagotchi unit keeps running with zero loaded plugins if plugins.load()
// itself raises — this is never treated as fatal to the daemon.
func Load(cfg config.Map) (*pyplugin.Bridge, error) {
	b, err := pyplugin.New(pyplugin.Options{
		Python: PythonInterpreter(),
		Config: cfg,
	})
	if err != nil {
		log.Printf("plugins: real Python plugin bridge unavailable, continuing with no plugins loaded: %v", err)
		return nil, err
	}
	log.Printf("loaded plugins: %v", b.Loaded)
	return b, nil
}
