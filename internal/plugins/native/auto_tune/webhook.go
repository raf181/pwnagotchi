package autotune

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// preset mirrors the JSON shape _save_preset/_load_preset read and write.
type preset struct {
	Personality    map[string]interface{} `json:"personality"`
	PluginSettings map[string]interface{} `json:"plugin_settings"`
	Timestamp      float64                `json:"timestamp"`
	Version        string                 `json:"version"`
}

// listPresetFiles ports _get_preset_files.
func (p *Plugin) listPresetFiles() []string {
	entries, err := os.ReadDir(p.presetsDir)
	if err != nil {
		p.logf("error reading presets directory: %v", err)
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Strings(names)
	return names
}

// savePreset ports _save_preset: snapshots every scalar (bool/int/float/
// string) personality field from the live fullCfg plus every scalar
// plugin option.
func (p *Plugin) savePreset(name string) error {
	if err := os.MkdirAll(p.presetsDir, 0o755); err != nil {
		return err
	}
	data := preset{
		Personality:    map[string]interface{}{},
		PluginSettings: map[string]interface{}{},
		Timestamp:      float64(p.now().Unix()),
		Version:        "1.0",
	}

	p.mu.Lock()
	if p.fullCfg != nil {
		if personality, ok := p.fullCfg["personality"].(config.Map); ok {
			for k, v := range personality {
				if isScalar(v) {
					data.Personality[k] = v
				}
			}
		}
	}
	data.PluginSettings["show_hidden"] = p.opts.ShowHidden
	data.PluginSettings["reset_history"] = p.opts.ResetHistory
	data.PluginSettings["extra_channels"] = p.opts.ExtraChannels
	data.PluginSettings["show_interactions"] = p.opts.ShowInteractions
	p.mu.Unlock()

	blob, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(p.presetsDir, name+".json"), blob, 0o644)
}

// loadPreset ports _load_preset: applies saved personality fields back
// into the live fullCfg (a shared reference — see the package doc) and
// saved plugin options back into p.opts, reporting exactly which
// parameters actually changed.
func (p *Plugin) loadPreset(name string) (bool, string) {
	blob, err := os.ReadFile(filepath.Join(p.presetsDir, name+".json"))
	if err != nil {
		return false, "Preset file not found"
	}
	// UseNumber() + normalizeJSONNumbers (same technique as
	// internal/web/webcfg.go's decodeConfigJSON): encoding/json's default
	// float64-for-every-number decoding would otherwise silently turn a
	// saved whole-number personality/option value (e.g. max_interactions)
	// into a float64 on load, diverging from its original int64 type.
	dec := json.NewDecoder(strings.NewReader(string(blob)))
	dec.UseNumber()
	var data preset
	if err := dec.Decode(&data); err != nil {
		return false, fmt.Sprintf("Error loading preset: %v", err)
	}
	data.Personality = normalizeNumberMap(data.Personality)
	data.PluginSettings = normalizeNumberMap(data.PluginSettings)

	var changes []string
	p.mu.Lock()
	if p.fullCfg != nil {
		if personality, ok := p.fullCfg["personality"].(config.Map); ok {
			for k, v := range data.Personality {
				if old, exists := personality[k]; exists && !equalScalar(old, v) {
					personality[k] = v
					changes = append(changes, fmt.Sprintf("personality.%s: %v -> %v", k, old, v))
				}
			}
		}
	}
	for k, v := range data.PluginSettings {
		changed, old := p.opts.applyIfKnown(k, v)
		if changed {
			changes = append(changes, fmt.Sprintf("plugin.%s: %v -> %v", k, old, v))
		}
	}
	p.mu.Unlock()

	if len(changes) > 0 {
		return true, fmt.Sprintf("Preset '%s' loaded successfully with %d changes", name, len(changes))
	}
	return true, fmt.Sprintf("Preset '%s' loaded (no changes needed)", name)
}

// deletePreset ports _delete_preset.
func (p *Plugin) deletePreset(name string) bool {
	path := filepath.Join(p.presetsDir, name+".json")
	if _, err := os.Stat(path); err != nil {
		return false
	}
	return os.Remove(path) == nil
}

// applyIfKnown mutates the matching Options field if key names one, and
// reports (changed, oldValue).
func (o *Options) applyIfKnown(key string, v interface{}) (bool, interface{}) {
	switch key {
	case "show_hidden":
		old := o.ShowHidden
		if b, ok := v.(bool); ok && b != old {
			o.ShowHidden = b
			return true, old
		}
	case "reset_history":
		old := o.ResetHistory
		if b, ok := v.(bool); ok && b != old {
			o.ResetHistory = b
			return true, old
		}
	case "extra_channels":
		old := o.ExtraChannels
		if n, ok := toInt(v); ok && n != old {
			o.ExtraChannels = n
			return true, old
		}
	case "show_interactions":
		old := o.ShowInteractions
		if b, ok := v.(bool); ok && b != old {
			o.ShowInteractions = b
			return true, old
		}
	}
	return false, nil
}

func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

// normalizeNumberMap converts every json.Number in m to int64 (no
// decimal/exponent) or float64 (otherwise) — see loadPreset's doc
// comment for why this matters.
func normalizeNumberMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		if n, ok := v.(json.Number); ok {
			if i, err := n.Int64(); err == nil && !strings.ContainsAny(string(n), ".eE") {
				out[k] = i
				continue
			}
			f, _ := n.Float64()
			out[k] = f
			continue
		}
		out[k] = v
	}
	return out
}

func isScalar(v interface{}) bool {
	switch v.(type) {
	case bool, int, int64, float64, string:
		return true
	default:
		return false
	}
}

func equalScalar(a, b interface{}) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// showHistogram ports Plugin.showHistogram: an "APs seen / avg per
// epoch" table across every channel that has ever appeared.
func (p *Plugin) showHistogram() string {
	p.mu.Lock()
	loops := p.loops
	histo := make(map[int]int, len(p.histogram))
	for k, v := range p.histogram {
		histo[k] = v
	}
	p.mu.Unlock()

	if loops <= 0 {
		return "<h2>No channel data collected yet</h2>"
	}

	type row struct {
		ch    int
		count int
	}
	rows := make([]row, 0, len(histo))
	for ch, c := range histo {
		rows = append(rows, row{ch, c})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].count > rows[j].count })

	var b strings.Builder
	fmt.Fprintf(&b, "<h2>APs per Channel over %d epochs</h2>", loops)
	b.WriteString("<table border=1 spacing=4 cellspacing=1>")
	var chans, totals, vals strings.Builder
	chans.WriteString("<tr><th>Channel</th>")
	totals.WriteString("<tr><th>APs seen</th>")
	vals.WriteString("<tr><th>Avg APs/epoch</th>")
	for _, r := range rows {
		fmt.Fprintf(&chans, "<th>%d</th>", r.ch)
		fmt.Fprintf(&totals, "<td align=right>%d</td>", r.count)
		fmt.Fprintf(&vals, "<td align=right>%.1f</td>", float64(r.count)/float64(loops))
	}
	chans.WriteString("</tr>")
	totals.WriteString("</tr>")
	vals.WriteString("</tr>")
	b.WriteString(chans.String())
	b.WriteString(totals.String())
	b.WriteString(vals.String())
	b.WriteString("</table>")
	return b.String()
}

// showChistos ports Plugin.showChistos(stats=None, sort_key='_all_actions').
func (p *Plugin) showChistos() string {
	p.mu.Lock()
	chistos := make(map[string]map[int]int, len(p.chistos))
	for stat, m := range p.chistos {
		cp := make(map[int]int, len(m))
		for k, v := range m {
			cp[k] = v
		}
		chistos[stat] = cp
	}
	p.mu.Unlock()

	sortKey := "_all_actions"
	type chOrder struct{ ch, count int }
	var order []chOrder
	if sortStat, ok := chistos[sortKey]; ok {
		for ch, c := range sortStat {
			order = append(order, chOrder{ch, c})
		}
		sort.Slice(order, func(i, j int) bool { return order[i].count > order[j].count })
	}

	stats := make([]string, 0, len(chistos))
	for s := range chistos {
		stats = append(stats, s)
	}
	sort.Strings(stats)

	var b strings.Builder
	b.WriteString("<h2>Channel Statistics</h2>\n")
	b.WriteString("<table border=1 cellspacing=4 cellpadding=4>\n")
	b.WriteString("<tr><th>Channel</th>")
	for _, o := range order {
		if o.ch == -1 {
			b.WriteString("<th>All</th>")
		} else {
			fmt.Fprintf(&b, "<th>%d</th>", o.ch)
		}
	}
	b.WriteString("</tr>\n")

	for _, s := range stats {
		if s == sortKey {
			fmt.Fprintf(&b, "<tr><th>%s</th>", html.EscapeString(s))
		} else {
			fmt.Fprintf(&b, "<tr><td>%s</td>", html.EscapeString(s))
		}
		chisto := chistos[s]
		for _, o := range order {
			if v, ok := chisto[o.ch]; ok {
				fmt.Fprintf(&b, "<td align=right>%d</td>", v)
			} else {
				b.WriteString("<td align=center>-</td>")
			}
		}
		b.WriteString("</tr>\n")
	}
	b.WriteString("</table>\n")
	return b.String()
}

// OnWebhook ports on_webhook: GET "/" shows the edit form + stats;
// POST "update" applies parameter/preset changes.
func (p *Plugin) OnWebhook(subpath string, r *http.Request) (pluginmanager.WebhookResponse, error) {
	switch r.Method {
	case http.MethodGet:
		if subpath == "" || subpath == "/" {
			body := p.renderIndexPage()
			return pluginmanager.WebhookResponse{Status: http.StatusOK, Body: []byte(body)}, nil
		}
		return pluginmanager.WebhookResponse{Status: http.StatusNotFound}, nil
	case http.MethodPost:
		if subpath == "update" {
			body := p.handleUpdate(r)
			return pluginmanager.WebhookResponse{Status: http.StatusOK, Body: []byte(body)}, nil
		}
		return pluginmanager.WebhookResponse{Status: http.StatusOK, Body: []byte("<h1>Unknown request</h1>")}, nil
	default:
		return pluginmanager.WebhookResponse{Status: http.StatusMethodNotAllowed}, nil
	}
}

func (p *Plugin) renderIndexPage() string {
	var b strings.Builder
	b.WriteString("<html><head><title>AUTO Tune</title></head><body><h1>AUTO Tune</h1><p>")
	b.WriteString(p.showEditForm())
	b.WriteString(p.showHistogram())
	b.WriteString(p.showChistos())
	p.mu.Lock()
	showInteractions := p.opts.ShowInteractions
	p.mu.Unlock()
	if showInteractions {
		b.WriteString(p.showInteractions())
	}
	b.WriteString("</body></html>")
	return b.String()
}

// showEditForm ports Plugin.showEditForm: the preset controls plus an
// editable table of every scalar personality/plugin-option field.
func (p *Plugin) showEditForm() string {
	var b strings.Builder
	b.WriteString(`<form method=post action="update">`)
	b.WriteString(`<div class="preset-section"><h2>Presets</h2>`)
	b.WriteString(`<input type=text name=preset_name size=30 placeholder="Enter preset name">`)
	b.WriteString(`<select name=selected_preset><option value="">Select a preset...</option>`)
	for _, name := range p.listPresetFiles() {
		fmt.Fprintf(&b, `<option value="%s">%s</option>`, html.EscapeString(name), html.EscapeString(name))
	}
	b.WriteString(`</select>`)
	b.WriteString(`<input type=submit name=save_preset value="Save Preset"> `)
	b.WriteString(`<input type=submit name=load_preset value="Load Preset"> `)
	b.WriteString(`<input type=submit name=delete_preset value="Delete Preset">`)
	b.WriteString(`</div><hr>`)

	p.mu.Lock()
	var personality config.Map
	if p.fullCfg != nil {
		personality, _ = p.fullCfg["personality"].(config.Map)
	}
	opts := p.opts
	p.mu.Unlock()

	b.WriteString("<h2>Personality Variables</h2><table>\n")
	names := make([]string, 0, len(personality))
	for k := range personality {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		v := personality[name]
		if isScalar(v) {
			writeEditRow(&b, "personality", name, v)
		}
	}
	b.WriteString("</table>")

	b.WriteString("<h2>AUTO Tune Variables</h2><table>\n")
	writeEditRow(&b, "plugin", "show_hidden", opts.ShowHidden)
	writeEditRow(&b, "plugin", "reset_history", opts.ResetHistory)
	writeEditRow(&b, "plugin", "extra_channels", opts.ExtraChannels)
	writeEditRow(&b, "plugin", "show_interactions", opts.ShowInteractions)
	b.WriteString("</table>")
	b.WriteString(`<input type=submit name=submit value="update"></form><p>`)
	return b.String()
}

func writeEditRow(b *strings.Builder, section, name string, v interface{}) {
	fmt.Fprintf(b, `<tr><th>%s</th><td><input name="newval,%v,%s,%s" value="%v"></td></tr>`,
		html.EscapeString(name), v, name, goTypeName(v), v)
	_ = section
}

func goTypeName(v interface{}) string {
	switch v.(type) {
	case bool:
		return "bool"
	case int, int64:
		return "int"
	case float64:
		return "float"
	default:
		return "str"
	}
}

// showInteractions ports Plugin.showInteractions: a table of every
// known AP's per-context interaction counts, respecting show_hidden.
func (p *Plugin) showInteractions() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	type row struct {
		id  string
		rec *apRecord
	}
	rows := make([]row, 0, len(p.knownAPs))
	for id, rec := range p.knownAPs {
		rows = append(rows, row{id, rec})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].rec.lastSeen.After(rows[j].rec.lastSeen) })

	var b strings.Builder
	b.WriteString("<h2>Interactions per endpoint</h2>")
	b.WriteString("<table border=1 spacing=4 cellspacing=4 cellpadding=4>")
	b.WriteString("<tr><th>Hostname</th><th>MAC</th><th>Channel</th><th>Age</th><th>RSSI</th>" +
		"<th>Encounters</th><th>Associates</th><th>Deauths</th><th>Handshakes</th></tr>")
	now := p.now()
	for _, r := range rows {
		hostname, _ := r.rec.fields["hostname"].(string)
		if (hostname == "" || hostname == "<hidden>") && !p.opts.ShowHidden {
			continue
		}
		mac, _ := r.rec.fields["mac"].(string)
		ch := channelOf(r.rec.fields)
		rssi := r.rec.fields["rssi"]
		if r.rec.visible {
			fmt.Fprintf(&b, "<tr><td>%s</td>", html.EscapeString(hostname))
		} else {
			fmt.Fprintf(&b, "<tr><td><i>%s</i></td>", html.EscapeString(hostname))
		}
		fmt.Fprintf(&b, "<td>%s</td><td>%d</td>", mac, ch)
		fmt.Fprintf(&b, "<td>%d</td>", int(now.Sub(r.rec.lastSeen).Seconds()))
		fmt.Fprintf(&b, "<td>%v</td>", rssi)
		fmt.Fprintf(&b, "<td>%d</td><td>%d</td><td>%d</td><td>%d</td>", r.rec.seen, r.rec.assoc, r.rec.deauth, r.rec.handshake)
		b.WriteString("</tr>\n")
	}
	b.WriteString("</table>\n")
	return b.String()
}

// handleUpdate ports the POST "update" branch: preset operations plus
// per-parameter edits, parsed from the `newval,<oldvalue>,<name>,<type>`
// field-name encoding the edit form emits.
func (p *Plugin) handleUpdate(r *http.Request) string {
	if err := r.ParseForm(); err != nil {
		return fmt.Sprintf("<h1>Error parsing form: %v</h1>", err)
	}

	var b strings.Builder
	b.WriteString("<html><head><title>AUTO Tune Update!</title></head><body><h1>AUTO Tune Update</h1>")

	if r.PostForm.Get("save_preset") != "" {
		if name := strings.TrimSpace(r.PostForm.Get("preset_name")); name != "" {
			if err := p.savePreset(name); err != nil {
				fmt.Fprintf(&b, `<div class="error">Error saving preset: %v</div>`, err)
			} else {
				fmt.Fprintf(&b, `<div class="success">Preset '%s' saved successfully!</div>`, html.EscapeString(name))
			}
		} else {
			b.WriteString(`<div class="error">Please enter a preset name</div>`)
		}
	} else if r.PostForm.Get("load_preset") != "" {
		if name := r.PostForm.Get("selected_preset"); name != "" {
			ok, msg := p.loadPreset(name)
			cls := "success"
			if !ok {
				cls = "error"
			}
			fmt.Fprintf(&b, `<div class="%s">%s</div>`, cls, html.EscapeString(msg))
		} else {
			b.WriteString(`<div class="error">Please select a preset to load</div>`)
		}
	} else if r.PostForm.Get("delete_preset") != "" {
		if name := r.PostForm.Get("selected_preset"); name != "" {
			if p.deletePreset(name) {
				fmt.Fprintf(&b, `<div class="success">Preset '%s' deleted successfully!</div>`, html.EscapeString(name))
			} else {
				fmt.Fprintf(&b, `<div class="error">Error deleting preset '%s'</div>`, html.EscapeString(name))
			}
		} else {
			b.WriteString(`<div class="error">Please select a preset to delete</div>`)
		}
	}

	b.WriteString("<h2>Processing changes</h2><ul>")
	for key, vals := range r.PostForm {
		if len(vals) == 0 || !strings.HasPrefix(key, "newval,") {
			continue
		}
		parts := strings.SplitN(key, ",", 4)
		if len(parts) != 4 {
			continue
		}
		oldStr, name, vtype := parts[1], parts[2], parts[3]
		newVal := vals[0]
		if oldStr == newVal {
			continue
		}
		if p.applyEdit(name, vtype, newVal) {
			fmt.Fprintf(&b, "<li>%s: %s -> %s</li>\n", html.EscapeString(name), html.EscapeString(oldStr), html.EscapeString(newVal))
		}
	}
	b.WriteString("</ul>")
	b.WriteString(p.showEditForm())
	b.WriteString(p.showHistogram())
	b.WriteString(p.showChistos())
	b.WriteString("</body></html>")
	return b.String()
}

// applyEdit ports update_parameter against either the live personality
// config or this plugin's own options, keyed by whichever section
// actually has that parameter name (matching Python's `if parameter in
// personality ... elif parameter in self.options` precedence).
func (p *Plugin) applyEdit(name, vtype, val string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.fullCfg != nil {
		if personality, ok := p.fullCfg["personality"].(config.Map); ok {
			if _, exists := personality[name]; exists {
				parsed, ok := parseTyped(vtype, val)
				if !ok {
					return false
				}
				personality[name] = parsed
				return true
			}
		}
	}
	changed, _ := p.opts.applyIfKnown(name, parseTypedOptionsValue(vtype, val))
	return changed
}

func parseTyped(vtype, val string) (interface{}, bool) {
	switch vtype {
	case "int":
		n, err := strconv.Atoi(val)
		return n, err == nil
	case "float":
		f, err := strconv.ParseFloat(val, 64)
		return f, err == nil
	case "bool":
		return val == "True" || val == "true", true
	default:
		return val, true
	}
}

func parseTypedOptionsValue(vtype, val string) interface{} {
	v, _ := parseTyped(vtype, val)
	return v
}
