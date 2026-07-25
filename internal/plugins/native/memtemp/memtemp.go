// Package memtemp is the native Go port of
// pwnagotchi/plugins/default/memtemp.py: a small always-on-screen widget
// showing memory usage / CPU load / CPU temperature / CPU frequency.
//
// Original Python author: https://github.com/xenDE (see memtemp.py's own
// __author__/history comments, left untouched). This Go port is by
// raf181.
package memtemp

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
	"github.com/jayofelony/pwnagotchi/internal/unit"
)

const (
	lineSpacingDefault = 10
	labelSpacing       = 0
	fieldWidth         = 4
)

var allowedFields = map[string]bool{"mem": true, "cpu": true, "cpus": true, "temp": true, "freq": true}
var defaultFields = []string{"mem", "cpu", "temp"}

// pos is a plain (x, y) pair — kept local rather than importing
// internal/ui/components.Point, since pluginmanager (and therefore any
// package built only against its capability interfaces) never depends on
// the UI rendering package directly.
type pos struct{ x, y int }

// Plugin ports the MemTemp class.
type Plugin struct {
	mu sync.Mutex

	scale       string
	fields      []string
	orientation string
	lineSpacing int
	hPos, vPos  pos

	view      pluginmanager.ViewCapability
	lastCPU   []int64
	lastCPUOK bool
}

// New ports MemTemp.__init__ (self.options = dict()). Config is read in
// OnLoad instead of here (see OnLoad's doc comment) since the manager
// always calls OnLoad with the real per-plugin config immediately after
// registration — there is no meaningful "constructed but not yet loaded"
// state for this plugin to hold config through.
func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string { return "memtemp" }

func (p *Plugin) Metadata() pluginmanager.Metadata {
	return pluginmanager.Metadata{
		Version:     "1.0.2",
		Author:      "https://github.com/xenDE (original), Go port by raf181",
		License:     "GPL3",
		Description: "A plugin that will display memory/cpu usage and temperature",
	}
}

// OnLoad ports on_loaded (seed _last_cpu_load) + on_ui_setup (parse
// options, add the real UI elements) in one step: a native plugin's
// OnLoad already only runs once real Agent/View capabilities exist (see
// cmd/pwnagotchi/main.go's capsFor ordering), which is functionally the
// same "once, at startup, before the first render" timing Python's
// on_loaded-then-on_ui_setup sequence has — there is no separate
// "ui_setup" event a native plugin needs to wait for.
func (p *Plugin) OnLoad(caps pluginmanager.Capabilities) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.view = caps.View
	p.scale = stringField(caps.Config, "scale", "celsius")
	p.orientation = stringField(caps.Config, "orientation", "horizontal")

	p.fields = parseFields(stringField(caps.Config, "fields", ""))
	p.lineSpacing = intField(caps.Config, "linespacing", lineSpacingDefault)
	defaultH, defaultV := defaultPositions(caps.View)
	p.hPos, p.vPos = parsePosition(caps.Config, p.orientation, defaultH, defaultV)

	if sample, err := unit.ReadCPUStat(); err == nil {
		p.lastCPU = sample
		p.lastCPUOK = true
	}

	if p.view == nil {
		return nil
	}
	if p.orientation == "vertical" {
		for idx, field := range p.fields {
			y := p.vPos.y + (len(p.fields)-3)*-1*p.lineSpacing + idx*p.lineSpacing
			p.view.AddLabeledValue(elementKey(field), padText(field)+":", "-", p.vPos.x, y, pluginmanager.FontSmall, pluginmanager.FontSmall, labelSpacing)
		}
	} else {
		x := p.hPos.x + (len(p.fields)-3)*-1*25
		header := make([]string, len(p.fields))
		blanks := make([]string, len(p.fields))
		for i, f := range p.fields {
			header[i] = padText(f)
			blanks[i] = padText("-")
		}
		p.view.AddText("memtemp_header", strings.Join(header, " "), x, p.hPos.y, pluginmanager.FontSmall, false, 0)
		p.view.AddText("memtemp_data", strings.Join(blanks, " "), x, p.hPos.y+p.lineSpacing, pluginmanager.FontSmall, false, 0)
	}
	return nil
}

// OnUnload ports on_unload: remove exactly the elements this instance
// added.
func (p *Plugin) OnUnload() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.view == nil {
		return nil
	}
	if p.orientation == "vertical" {
		for _, field := range p.fields {
			p.view.RemoveElement(elementKey(field))
		}
	} else {
		p.view.RemoveElement("memtemp_header")
		p.view.RemoveElement("memtemp_data")
	}
	return nil
}

// HandleEvent ports on_ui_update: recompute every configured field and
// push the new values through View.Set, exactly like Python's
// ui.set(...) calls (view.go's real render loop picks up the change on
// its own next Update, same as any built-in element).
func (p *Plugin) HandleEvent(event string, args []interface{}) {
	if event != "ui_update" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.view == nil {
		return
	}
	if p.orientation == "vertical" {
		for _, field := range p.fields {
			p.view.Set(elementKey(field), p.fieldValueLocked(field))
		}
		return
	}
	values := make([]string, len(p.fields))
	for i, f := range p.fields {
		values[i] = padText(p.fieldValueLocked(f))
	}
	p.view.Set("memtemp_data", strings.Join(values, " "))
}

// fieldValueLocked must be called with p.mu held.
func (p *Plugin) fieldValueLocked(field string) string {
	switch field {
	case "mem":
		return p.memUsage()
	case "cpu":
		return p.cpuLoad()
	case "cpus":
		return p.cpuLoadSince()
	case "temp":
		return p.cpuTemp()
	case "freq":
		return p.cpuFreq()
	default:
		return "-"
	}
}

func (p *Plugin) memUsage() string {
	v, err := unit.MemUsage()
	if err != nil {
		return "-"
	}
	return fmt.Sprintf("%d%%", int(v*100))
}

// cpuLoad ports MemTemp.cpu_load: the untagged pwnagotchi.cpu_load()
// form, which always samples twice 0.1s apart.
func (p *Plugin) cpuLoad() string {
	v, err := unit.CPULoad("")
	if err != nil {
		return "-"
	}
	return fmt.Sprintf("%d%%", int(v*100))
}

// cpuLoadSince ports MemTemp.cpu_load_since: diffs against THIS plugin's
// own last sample (seeded once in OnLoad, updated every call), never
// sleeping — a different, longer/instance-lifetime averaging window than
// cpuLoad's fixed 0.1s snapshot, exactly matching the real Python
// plugin's separate _last_cpu_load state instead of pwnagotchi.cpu_load's
// shared tag-cache semantics (internal/unit.CPUStats.Load).
func (p *Plugin) cpuLoadSince() string {
	sample, err := unit.ReadCPUStat()
	if err != nil {
		return "-"
	}
	if !p.lastCPUOK {
		p.lastCPU = sample
		p.lastCPUOK = true
		return "-"
	}
	prev := p.lastCPU
	p.lastCPU = sample
	n := len(prev)
	if len(sample) < n {
		n = len(sample)
	}
	if n < 8 {
		return "-"
	}
	diff := make([]int64, n)
	for i := 0; i < n; i++ {
		diff[i] = sample[i] - prev[i]
	}
	user, nice, sys, idle, iowait, irq, softirq, steal := diff[0], diff[1], diff[2], diff[3], diff[4], diff[5], diff[6], diff[7]
	idleSum := idle + iowait
	nonIdleSum := user + nice + sys + irq + softirq + steal
	total := idleSum + nonIdleSum
	if total == 0 {
		return "0%"
	}
	return fmt.Sprintf("%d%%", int(float64(nonIdleSum)/float64(total)*100))
}

func (p *Plugin) cpuTemp() string {
	switch p.scale {
	case "fahrenheit":
		v, err := unit.Fahrenheit()
		if err != nil {
			return "-"
		}
		return fmt.Sprintf("%v F", v)
	case "kelvin":
		c, err := unit.Celsius()
		if err != nil {
			return "-"
		}
		return fmt.Sprintf("%vK", float64(c)+273.15)
	default:
		c, err := unit.Celsius()
		if err != nil {
			return "-"
		}
		return fmt.Sprintf("%dC", c)
	}
}

// cpuFreqPath is overridable for tests (matches internal/unit's own
// package-level-var override pattern for /proc and /sys paths).
var cpuFreqPath = "/sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq"

func (p *Plugin) cpuFreq() string {
	data, err := os.ReadFile(cpuFreqPath)
	if err != nil {
		return "-"
	}
	khz, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return "-"
	}
	ghz := khz / 1_000_000
	return fmt.Sprintf("%.1fG", ghz)
}

func elementKey(field string) string { return "memtemp_" + field }

func padText(s string) string {
	if len(s) >= fieldWidth {
		return s
	}
	return strings.Repeat(" ", fieldWidth-len(s)) + s
}

func parseFields(raw string) []string {
	if raw == "" {
		return append([]string(nil), defaultFields...)
	}
	var out []string
	for _, f := range strings.Split(raw, ",") {
		f = strings.TrimSpace(f)
		if allowedFields[f] {
			out = append(out, f)
		}
		if len(out) == 3 {
			break
		}
	}
	if len(out) == 0 {
		return append([]string(nil), defaultFields...)
	}
	return out
}

// defaultPositions ports on_ui_setup's per-display-model default (h_pos,
// v_pos) fallback table, keyed off View.Kind() (see internal/ui/view's
// Kind method / internal/ui/display's generated is_X() predicates this
// replaces with one string comparison per case instead of one Go method
// per display model).
func defaultPositions(view pluginmanager.ViewCapability) (h, v pos) {
	kind := ""
	if view != nil {
		kind = view.Kind()
	}
	switch kind {
	case "waveshare_2":
		return pos{175, 84}, pos{197, 74}
	case "waveshare_1":
		return pos{170, 80}, pos{165, 61}
	case "waveshare144lcd":
		return pos{53, 77}, pos{73, 67}
	case "inky":
		return pos{140, 68}, pos{160, 54}
	case "waveshare2in7":
		return pos{192, 138}, pos{211, 122}
	case "waveshare1in54_v2":
		return pos{53, 77}, pos{154, 65}
	default:
		return pos{155, 76}, pos{175, 61}
	}
}

func parsePosition(cfg config.Map, orientation string, defaultH, defaultV pos) (h, v pos) {
	h, v = defaultH, defaultV
	raw := stringField(cfg, "position", "")
	if raw == "" {
		return h, v
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 2 {
		return h, v
	}
	x, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	y, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return h, v
	}
	if orientation == "vertical" {
		v = pos{x, y}
	} else {
		h = pos{x, y}
	}
	return h, v
}

func stringField(m config.Map, key, def string) string {
	if m == nil {
		return def
	}
	if s, ok := m[key].(string); ok && s != "" {
		return s
	}
	return def
}

func intField(m config.Map, key string, def int) int {
	if m == nil {
		return def
	}
	switch v := m[key].(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

var _ pluginmanager.Plugin = (*Plugin)(nil)
var _ pluginmanager.Loader = (*Plugin)(nil)
var _ pluginmanager.Unloader = (*Plugin)(nil)
var _ pluginmanager.EventHandler = (*Plugin)(nil)
