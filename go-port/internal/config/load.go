package config

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// defaultsTOML is a build-time copy of pwnagotchi/defaults.toml (kept in
// sync manually — there is no Python install to read it from at Go runtime).
// See docs/known-differences.md for the sync process.
//
//go:embed defaults.toml
var defaultsTOML []byte

// Map is the generic config tree type threaded through Load/Save/Merge,
// matching the shape tomlkit/toml produce: string keys, nested maps, native
// scalar types, and []interface{} for arrays.
type Map = map[string]interface{}

// Args is the subset of cli.py's argparse Namespace that utils.load_config
// reads: args.config and args.user_config.
type Args struct {
	Config     string
	UserConfig string
}

// Printf is where LoadConfig sends the same operator-facing lines
// utils.load_config prints to stdout (some of which reproduce real Python
// formatting bugs — see loadConfigPrintBoot below). Tests can capture it;
// production wires it to fmt.Printf.
type Printf func(format string, args ...interface{})

// LoadConfig ports utils.load_config end-to-end: boot-config installation,
// defaults bootstrapping/overwrite-on-drift, user config merge (with legacy
// YAML migration), conf.d dropins, and display-type normalization.
func LoadConfig(args Args, out Printf) (Map, error) {
	if out == nil {
		out = func(string, ...interface{}) {}
	}

	defaultConfigDir := filepath.Dir(args.Config)
	if _, err := os.Stat(defaultConfigDir); os.IsNotExist(err) {
		if err := os.MkdirAll(defaultConfigDir, 0o755); err != nil {
			return nil, err
		}
	}

	if err := installBootConfig(args, out); err != nil {
		return nil, err
	}
	if err := installBootPwnagotchiDir(out); err != nil {
		return nil, err
	}

	if _, err := os.Stat(args.Config); os.IsNotExist(err) {
		out("copying %s to %s ...", "<packaged defaults.toml>", args.Config)
		if err := os.WriteFile(args.Config, defaultsTOML, 0o644); err != nil {
			return nil, err
		}
	} else {
		existing, err := os.ReadFile(args.Config)
		if err != nil {
			return nil, err
		}
		if string(existing) != string(defaultsTOML) {
			out("!!! file in %s is different than release defaults, overwriting !!!", args.Config)
			if err := os.WriteFile(args.Config, defaultsTOML, 0o644); err != nil {
				return nil, err
			}
		}
	}

	cfg, err := loadTOMLFile(args.Config, out)
	if err != nil {
		return nil, err
	}

	userCfg, err := loadUserConfig(args, out)
	if err != nil {
		// Matches Python: logging.error(...); sys.exit(1). Callers of
		// LoadConfig own the actual os.Exit so this stays testable; cli.go
		// exits 1 on this error.
		return nil, fmt.Errorf("there was an error processing the configuration file:\n%w", err)
	}
	if userCfg != nil {
		cfg = MergeConfig(userCfg, cfg)
	}

	if err := applyDropins(cfg, out); err != nil {
		return nil, err
	}

	displayType, _ := digString(cfg, "ui", "display", "type")
	setNestedString(cfg, []string{"ui", "display", "type"}, NormalizeDisplayType(displayType))

	return cfg, nil
}

// BootConfigCandidates mirrors the literal list in utils.load_config.
// Overridable (like internal/unit's *Path vars) so tests don't need real
// root access to /boot.
var BootConfigCandidates = []string{
	"/boot/config.yml",
	"/boot/firmware/config.yml",
	"/boot/config.toml",
	"/boot/firmware/config.toml",
}

// BootPwnagotchiDir and EtcPwnagotchiDir mirror the hardcoded
// '/boot/firmware/pwnagotchi' and '/etc/pwnagotchi' paths in
// utils.load_config's second boot-install step. Overridable for tests.
var (
	BootPwnagotchiDir = "/boot/firmware/pwnagotchi"
	EtcPwnagotchiDir  = "/etc/pwnagotchi"
)

func installBootConfig(args Args, out Printf) error {
	for _, bootConf := range BootConfigCandidates {
		if _, err := os.Stat(bootConf); err != nil {
			continue
		}
		// Python calls merge_config(boot_conf, args.user_config) here with
		// two FILE PATH STRINGS, not loaded dicts. merge_config's own
		// isinstance(user, dict) guard fails immediately, so it just
		// returns the first argument unchanged — and the return value
		// isn't even assigned to anything. The call is dead code; the only
		// real effect below is the unconditional move. Preserved as a
		// documented no-op (see docs/known-differences.md) rather than
		// ported, since porting it faithfully means porting nothing.

		// Python: print("installing new %s to %s ...", boot_conf, args.user_config)
		// — missing '%' operator, so this is print() with 3 positional
		// args (sep=' '), NOT string formatting: the literal "%s"
		// placeholders are printed UNSUBSTITUTED, followed by the actual
		// path values space-separated. Reproduced byte-for-byte via a
		// single %s verb over a pre-built literal string, so Printf's own
		// formatting doesn't accidentally "fix" the bug.
		out("%s", "installing new %s to %s ... "+bootConf+" "+args.UserConfig)

		if err := pyShutilMove(bootConf, args.UserConfig); err != nil {
			return err
		}
		break
	}
	return nil
}

func installBootPwnagotchiDir(out Printf) error {
	src := BootPwnagotchiDir
	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		return nil
	}
	out("installing /boot/firmware/pwnagotchi to /etc/pwnagotchi ...")
	_ = os.RemoveAll(EtcPwnagotchiDir) // shutil.rmtree(..., ignore_errors=True)
	return pyShutilMove(src, EtcPwnagotchiDir)
}

// pyShutilMove mirrors shutil.move: rename, falling back to copy+remove on
// cross-device links (Python's own comment cites exactly this: "OSError:
// [Errno 18] Invalid cross-device link"). If dst is an existing directory,
// shutil.move places src *inside* it; we only need the file-and-fresh-path
// cases load_config actually hits.
func pyShutilMove(src, dst string) error {
	if info, err := os.Stat(dst); err == nil && info.IsDir() {
		dst = filepath.Join(dst, filepath.Base(src))
	}
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) {
		return err
	}
	if copyErr := copyPath(src, dst); copyErr != nil {
		return copyErr
	}
	return os.RemoveAll(src)
}

func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDirTree(src, dst)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	outF, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer outF.Close()
	_, err = io.Copy(outF, in)
	return err
}

func copyDirTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDirTree(s, d); err != nil {
				return err
			}
			continue
		}
		if err := copyPath(s, d); err != nil {
			return err
		}
	}
	return nil
}

// loadTOMLFile mirrors utils.load_config's inner load_toml_file: files
// containing a literal "[main]" table header are parsed as-is; files
// without one are treated as legacy "dotted" TOML, parsed, backed up to
// filename+".ORIG", and rewritten in the canonical (tomlkit-formatted, here:
// toml.Marshal-formatted) layout.
// LoadTOMLFileForEdit exposes loadTOMLFile for callers (like
// internal/plugins.Edit) that need to read back a TOML file a user just
// hand-edited, without going through the full LoadConfig pipeline.
func LoadTOMLFileForEdit(filename string) (Map, error) {
	return loadTOMLFile(filename, func(string, ...interface{}) {})
}

func loadTOMLFile(filename string, out Printf) (Map, error) {
	text, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	if strings.Contains(string(text), "[main]") {
		var data Map
		if _, err := toml.Decode(string(text), &data); err != nil {
			return nil, err
		}
		return data, nil
	}

	preview := string(text)
	if r := []rune(preview); len(r) > 100 {
		preview = string(r[:100])
	}
	out("Converting dotted toml %s: %s", filename, preview)

	var data Map
	if _, err := toml.Decode(string(text), &data); err != nil {
		return nil, err
	}

	backup := filename + ".ORIG"
	if err := os.Rename(filename, backup); err != nil {
		out("Unable to convert %s to new format: %s", backup, err.Error())
		return data, nil
	}
	f, err := os.Create(filename)
	if err != nil {
		out("Unable to convert %s to new format: %s", backup, err.Error())
		return data, nil
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(data); err != nil {
		out("Unable to convert %s to new format: %s", backup, err.Error())
		return data, nil
	}
	out("Converted to new format. Original saved at %s", backup)
	return data, nil
}

func loadUserConfig(args Args, out Printf) (Map, error) {
	// Python: args.user_config.replace('.toml', '.yml') — str.replace with
	// no count replaces EVERY occurrence, not just a trailing suffix.
	yamlName := strings.ReplaceAll(args.UserConfig, ".toml", ".yml")

	_, userExists := os.Stat(args.UserConfig)
	_, yamlExists := os.Stat(yamlName)

	switch {
	case os.IsNotExist(userExists) && yamlExists == nil:
		out("Old yaml-config found. Converting to toml...")
		yamlData, err := os.ReadFile(yamlName)
		if err != nil {
			return nil, err
		}
		var raw interface{}
		if err := yaml.Unmarshal(yamlData, &raw); err != nil {
			return nil, err
		}
		strified := KeysToStr(raw)
		m, ok := strified.(Map)
		if !ok {
			m = Map{}
		}
		f, err := os.Create(args.UserConfig)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if err := toml.NewEncoder(f).Encode(m); err != nil {
			return nil, err
		}
		return m, nil

	case userExists == nil:
		return loadTOMLFile(args.UserConfig, out)

	default:
		return nil, nil
	}
}

func applyDropins(cfg Map, out Printf) error {
	dropin, _ := digString(cfg, "main", "confd")
	if dropin == "" {
		return nil
	}
	info, err := os.Stat(dropin)
	if err != nil || !info.IsDir() {
		return nil
	}
	pattern := dropin + "*.toml"
	if !strings.HasSuffix(dropin, "/") {
		pattern = dropin + "/*.toml"
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	// Python's glob.glob order is filesystem-dependent (itself
	// nondeterministic); we sort for reproducibility — a documented,
	// deliberate normalization, see docs/known-differences.md.
	sort.Strings(matches)
	for _, m := range matches {
		additional, err := loadTOMLFile(m, out)
		if err != nil {
			return err
		}
		for k, v := range MergeConfig(additional, cfg) {
			cfg[k] = v
		}
	}
	return nil
}

// SaveConfig mirrors utils.save_config: a plain (non-atomic) overwrite —
// intentionally NOT routed through fs.EnsureWrite, matching the Python
// original's own gap.
func SaveConfig(cfg Map, target string) error {
	f, err := os.Create(target)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

// DigFloat looks up a nested numeric config value (TOML integers decode as
// int64, floats as float64 via BurntSushi/toml into interface{}) and
// returns it as a float64, matching how Python config values are used
// directly in arithmetic regardless of int/float. Exported for other
// ported packages (epoch, automata, ...) that read personality/main
// thresholds out of the generic config tree.
func DigFloat(m Map, keys ...string) (float64, bool) {
	var cur interface{} = m
	for _, k := range keys {
		asMap, ok := cur.(Map)
		if !ok {
			return 0, false
		}
		cur, ok = asMap[k]
		if !ok {
			return 0, false
		}
	}
	switch v := cur.(type) {
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case int:
		return float64(v), true
	default:
		return 0, false
	}
}

func digString(m Map, keys ...string) (string, bool) {
	var cur interface{} = m
	for _, k := range keys {
		asMap, ok := cur.(Map)
		if !ok {
			return "", false
		}
		cur, ok = asMap[k]
		if !ok {
			return "", false
		}
	}
	s, ok := cur.(string)
	return s, ok
}

func setNestedString(m Map, path []string, value string) {
	cur := m
	for i, k := range path {
		if i == len(path)-1 {
			cur[k] = value
			return
		}
		next, ok := cur[k].(Map)
		if !ok {
			next = Map{}
			cur[k] = next
		}
		cur = next
	}
}
