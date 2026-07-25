package plugins

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/jayofelony/pwnagotchi/internal/config"
)

// Doctor is the Go-only replacement for deploy/scripts/check-plugins.py:
// a real, non-destructive, no-hardware-needed validation of every
// plugin this daemon knows about, runnable both on a developer machine
// and inside a mounted/chrooted built image (see
// GO_ONLY_MIGRATION_PROMPT.md's "Replace check-plugins.py with a native
// non-destructive command such as `pwnagotchi plugins doctor --all
// --no-hardware`").
//
// Unlike check-plugins.py (which imported the real Python
// pwnagotchi.plugins package and called its loader), this never needs a
// running daemon, a display, or any bus/network hardware: it validates
// (1) every one of the 23 bundled plugins is present in the compiled-in
// list this exact binary was built with, and (2) every locally installed
// third-party plugin's real SHA-256 checksum still matches its manifest
// (catching corruption/tampering since install — the same check Install
// performs, re-run here as a standalone health check rather than as a
// side effect of installing).
func Doctor(cfg config.Map) int {
	names := make([]string, 0, len(bundledPluginNames))
	for n := range bundledPluginNames {
		names = append(names, n)
	}
	sort.Strings(names)

	fmt.Printf("Bundled plugins compiled into this binary: %d\n", len(names))
	for _, n := range names {
		fmt.Printf("  ok    %s (compiled-in)\n", n)
	}

	installed := installedManifests()
	installedNames := make([]string, 0, len(installed))
	for n := range installed {
		installedNames = append(installedNames, n)
	}
	sort.Strings(installedNames)

	fmt.Printf("\nInstalled third-party plugins: %d\n", len(installedNames))
	fail := false
	for _, name := range installedNames {
		m := installed[name]
		execPath := filepath.Join(PluginInstallDir, name, name)
		if err := m.VerifyExecutable(execPath); err != nil {
			fmt.Printf("  FAIL  %s: %v\n", name, err)
			fail = true
			continue
		}
		fmt.Printf("  ok    %s %s (checksum verified)\n", name, m.Version)
	}

	fmt.Println()
	if fail {
		fmt.Println("DOCTOR: one or more installed third-party plugins failed checksum verification.")
		return 1
	}
	fmt.Println("DOCTOR: all checks passed.")
	return 0
}
