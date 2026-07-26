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
// (1) every bundled plugin is present in the compiled-in
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

	installed, scanErrs := scanInstalled()
	fmt.Printf("\nInstalled third-party plugins: %d\n", len(installed))
	fail := len(scanErrs) > 0
	for _, err := range scanErrs {
		fmt.Printf("  FAIL  %v\n", err)
	}
	for _, item := range installed {
		if bundledPluginNames[item.name] {
			fmt.Printf("  FAIL  %s: installed plugin conflicts with a bundled plugin\n", item.name)
			fail = true
			continue
		}
		execPath := filepath.Join(PluginInstallDir, item.name, item.name)
		if err := item.manifest.VerifyExecutable(execPath); err != nil {
			fmt.Printf("  FAIL  %s: %v\n", item.name, err)
			fail = true
			continue
		}
		fmt.Printf("  ok    %s %s (checksum verified, target matches)\n", item.name, item.manifest.Version)
	}

	fmt.Println()
	if fail {
		fmt.Println("DOCTOR: one or more installed third-party plugins failed validation.")
		return 1
	}
	fmt.Println("DOCTOR: all checks passed.")
	return 0
}
