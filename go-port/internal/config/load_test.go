package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func capturePrintf() (Printf, *[]string) {
	lines := &[]string{}
	return func(format string, args ...interface{}) {
		*lines = append(*lines, fmt.Sprintf(format, args...))
	}, lines
}

func TestLoadConfigFreshDirectoryCopiesDefaults(t *testing.T) {
	dir := t.TempDir()
	args := Args{
		Config:     filepath.Join(dir, "default.toml"),
		UserConfig: filepath.Join(dir, "config.toml"),
	}
	out, lines := capturePrintf()
	cfg, err := LoadConfig(args, out)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	main, _ := cfg["main"].(Map)
	if main["name"] != "pwnagotchi" {
		t.Fatalf("main.name = %v, want pwnagotchi", main["name"])
	}
	ui, _ := cfg["ui"].(Map)
	display, _ := ui["display"].(Map)
	if display["type"] != "waveshare_4" {
		t.Fatalf("ui.display.type = %v, want waveshare_4", display["type"])
	}

	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"bettercap", "fs", "main", "personality", "ui"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("top-level keys = %v, want %v", keys, want)
	}

	found := false
	for _, l := range *lines {
		if strings.HasPrefix(l, "copying") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a 'copying ...' message on first run")
	}
}

func TestLoadConfigUserOverrideAndAliasNormalization(t *testing.T) {
	dir := t.TempDir()
	args := Args{
		Config:     filepath.Join(dir, "default.toml"),
		UserConfig: filepath.Join(dir, "config.toml"),
	}
	if err := os.WriteFile(args.UserConfig, []byte("[main]\nname = \"unit1\"\n\n[ui.display]\ntype = \"ws4\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(args, nil)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	main := cfg["main"].(Map)
	if main["name"] != "unit1" {
		t.Fatalf("main.name = %v, want unit1 (user override wins)", main["name"])
	}
	display := cfg["ui"].(Map)["display"].(Map)
	if display["type"] != "waveshare_4" {
		t.Fatalf("ui.display.type = %v, want waveshare_4 (ws4 alias normalized post-merge)", display["type"])
	}
}

func TestLoadConfigDropinsLastWriterWins(t *testing.T) {
	dir := t.TempDir()
	confd := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(confd, 0o755); err != nil {
		t.Fatal(err)
	}
	args := Args{
		Config:     filepath.Join(dir, "default.toml"),
		UserConfig: filepath.Join(dir, "config.toml"),
	}
	userToml := "[main]\nname = \"unit1\"\nconfd = " + tomlQuote(confd) + "\n"
	if err := os.WriteFile(args.UserConfig, []byte(userToml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confd, "a.toml"), []byte("[main]\nname = \"from-a\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confd, "z.toml"), []byte("[main]\nname = \"from-z\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(args, nil)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg["main"].(Map)["name"]; got != "from-z" {
		t.Fatalf("main.name = %v, want from-z (last dropin alphabetically wins, matches Python capture)", got)
	}
}

func TestLoadConfigDottedTOMLMigration(t *testing.T) {
	dir := t.TempDir()
	args := Args{
		Config:     filepath.Join(dir, "default.toml"),
		UserConfig: filepath.Join(dir, "config.toml"),
	}
	if err := os.WriteFile(args.UserConfig, []byte("main.name = \"dotted1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(args, nil)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg["main"].(Map)["name"]; got != "dotted1" {
		t.Fatalf("main.name = %v, want dotted1", got)
	}
	if _, err := os.Stat(args.UserConfig + ".ORIG"); err != nil {
		t.Fatal("expected a .ORIG backup of the legacy dotted config")
	}
	rewritten, err := os.ReadFile(args.UserConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rewritten), "[main]") {
		t.Fatalf("rewritten user config should now have a [main] table, got:\n%s", rewritten)
	}
}

func TestLoadConfigYAMLMigrationLeavesOriginalFile(t *testing.T) {
	dir := t.TempDir()
	args := Args{
		Config:     filepath.Join(dir, "default.toml"),
		UserConfig: filepath.Join(dir, "config.toml"),
	}
	yamlPath := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(yamlPath, []byte("main:\n  name: from-yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(args, nil)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg["main"].(Map)["name"]; got != "from-yaml" {
		t.Fatalf("main.name = %v, want from-yaml", got)
	}
	if _, err := os.Stat(yamlPath); err != nil {
		t.Fatal("legacy YAML file must NOT be deleted after migration, matching Python (it never unlinks it)")
	}
	if _, err := os.Stat(args.UserConfig); err != nil {
		t.Fatal("expected new toml user config to be written")
	}
}

func TestLoadConfigOverwritesDriftedDefaults(t *testing.T) {
	dir := t.TempDir()
	args := Args{
		Config:     filepath.Join(dir, "default.toml"),
		UserConfig: filepath.Join(dir, "config.toml"),
	}
	if err := os.WriteFile(args.Config, []byte("[main]\nname = \"tampered\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, lines := capturePrintf()
	cfg, err := LoadConfig(args, out)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg["main"].(Map)["name"]; got != "pwnagotchi" {
		t.Fatalf("main.name = %v, want pwnagotchi (drifted defaults must be overwritten)", got)
	}
	found := false
	for _, l := range *lines {
		if strings.Contains(l, "overwriting") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected an overwrite warning message")
	}
}

func TestLoadConfigBootMessageReproducesMissingPercentBug(t *testing.T) {
	dir := t.TempDir()
	bootConf := filepath.Join(dir, "boot_config.toml")
	if err := os.WriteFile(bootConf, []byte("[main]\nname = \"from-boot\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	origCandidates := BootConfigCandidates
	BootConfigCandidates = []string{bootConf}
	t.Cleanup(func() { BootConfigCandidates = origCandidates })

	args := Args{
		Config:     filepath.Join(dir, "default.toml"),
		UserConfig: filepath.Join(dir, "config.toml"),
	}
	out, lines := capturePrintf()
	if _, err := LoadConfig(args, out); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	want := "installing new %s to %s ... " + bootConf + " " + args.UserConfig
	found := false
	for _, l := range *lines {
		if l == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected literal message %q (reproducing Python's missing-%%-operator bug) in %v", want, *lines)
	}

	// The boot config file must have been MOVED onto args.UserConfig (not
	// merged — Python's merge_config call here is dead code, see load.go).
	if _, err := os.Stat(bootConf); !os.IsNotExist(err) {
		t.Fatal("boot config file should have been moved (removed from its original location)")
	}
	data, err := os.ReadFile(args.UserConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "from-boot") {
		t.Fatalf("user config should now contain the moved boot config content, got:\n%s", data)
	}
}

func tomlQuote(s string) string {
	return "\"" + strings.ReplaceAll(s, `\`, `\\`) + "\""
}
