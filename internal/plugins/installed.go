package plugins

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
	"github.com/jayofelony/pwnagotchi/internal/pluginrpc"
)

type installedPlugin struct {
	name     string
	dir      string
	execPath string
	manifest *pluginrpc.Manifest
}

func scanInstalled() ([]installedPlugin, []error) {
	entries, err := os.ReadDir(PluginInstallDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{fmt.Errorf("plugins: reading install directory: %w", err)}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var installed []installedPlugin
	var errs []error
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !entry.IsDir() {
			errs = append(errs, fmt.Errorf("plugins: unexpected non-directory entry %q in install root", entry.Name()))
			continue
		}
		if err := pluginrpc.ValidatePluginName(entry.Name()); err != nil {
			errs = append(errs, fmt.Errorf("plugins: invalid install directory %q: %w", entry.Name(), err))
			continue
		}

		dir := filepath.Join(PluginInstallDir, entry.Name())
		manifestPath := filepath.Join(dir, "manifest.toml")
		manifest, _, err := pluginrpc.ReadManifestFile(manifestPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("plugins: %s: reading manifest: %w", entry.Name(), err))
			continue
		}
		if manifest.Name != entry.Name() {
			errs = append(errs, fmt.Errorf("plugins: install directory %q contains manifest for %q", entry.Name(), manifest.Name))
			continue
		}
		if err := manifest.ValidateTarget(runtime.GOOS, runtime.GOARCH); err != nil {
			errs = append(errs, err)
			continue
		}

		installed = append(installed, installedPlugin{
			name:     manifest.Name,
			dir:      dir,
			execPath: filepath.Join(dir, manifest.Name),
			manifest: manifest,
		})
	}
	return installed, errs
}

func installedManifests() map[string]*pluginrpc.Manifest {
	installed, errs := scanInstalled()
	for _, err := range errs {
		log.Print(err)
	}
	out := make(map[string]*pluginrpc.Manifest, len(installed))
	for _, item := range installed {
		out[item.name] = item.manifest
	}
	return out
}

// RegisterInstalled adds every structurally valid, checksum-verified
// third-party plugin to the daemon's manager. Activation is still controlled
// by main.plugins.<name>.enabled through Manager.LoadAll.
func RegisterInstalled(mgr *pluginmanager.Manager) []error {
	installed, errs := scanInstalled()
	for _, item := range installed {
		if bundledPluginNames[item.name] {
			errs = append(errs, fmt.Errorf("plugins: installed plugin %q conflicts with a bundled plugin", item.name))
			continue
		}
		if err := item.manifest.VerifyExecutable(item.execPath); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := mgr.Register(pluginrpc.NewRemotePlugin(item.name, item.manifest, item.execPath)); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
