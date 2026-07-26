package pluginhost

import (
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
	"github.com/jayofelony/pwnagotchi/internal/ui/hw"
	"github.com/jayofelony/pwnagotchi/internal/ui/view"
)

func newTestRealView(t *testing.T) *view.View {
	t.Helper()
	cfg := config.Map{
		"main": config.Map{"lang": "en"},
		"ui": config.Map{
			"fps":     float64(0),
			"cursor":  true,
			"faces":   config.Map{"position_x": int64(0), "position_y": int64(40), "png": false},
			"display": config.Map{"type": "dummydisplay"},
		},
		"bettercap":   config.Map{"handshakes": "/nonexistent"},
		"personality": config.Map{"bond_encounters_factor": int64(20000)},
	}
	driver, err := hw.NewDriver(cfg)
	if err != nil {
		t.Fatal(err)
	}
	v, err := view.New(cfg, driver, map[string]interface{}{"name": "pwnagotchi>"}, nil)
	if err != nil {
		t.Fatalf("view.New: %v", err)
	}
	return v
}

// minimalHeadless mimics *cli.HeadlessView's real, narrower method set —
// only Set, nothing display-specific — without importing internal/cli
// (which would need its own fake agent/view wiring just for this test).
type minimalHeadless struct{ sets map[string]interface{} }

func (h *minimalHeadless) Set(key string, value interface{}) {
	if h.sets == nil {
		h.sets = map[string]interface{}{}
	}
	h.sets[key] = value
}

type unreadTarget struct {
	count int
	total int
}

func (u *unreadTarget) OnUnreadMessages(count, total int) {
	u.count = count
	u.total = total
}

func TestViewAdapterWithRealDisplayForwardsEverything(t *testing.T) {
	rv := newTestRealView(t)
	a := View{V: rv}

	if got := a.Kind(); got != "DummyDisplay" {
		t.Fatalf("Kind() = %q, want DummyDisplay", got)
	}

	a.AddText("plugin_text", "-", 1, 2, pluginmanager.FontSmall, false, 0)
	if !a.HasElement("plugin_text") {
		t.Fatal("expected AddText to register a real element")
	}
	a.Set("plugin_text", "hello")
	if got := rv.Get("plugin_text"); got != "hello" {
		t.Fatalf("Get() after Set = %v, want hello", got)
	}
	a.RemoveElement("plugin_text")
	if a.HasElement("plugin_text") {
		t.Fatal("expected RemoveElement to unregister the element")
	}

	a.AddLabeledValue("plugin_lv", "CH", "00", 5, 5, pluginmanager.FontBold, pluginmanager.FontMedium, 5)
	if !a.HasElement("plugin_lv") {
		t.Fatal("expected AddLabeledValue to register a real element")
	}

	a.Update(true) // must not panic against a real *view.View
}

func TestViewAdapterWithHeadlessDegradesSafely(t *testing.T) {
	h := &minimalHeadless{}
	a := View{V: h}

	if got := a.Kind(); got != "" {
		t.Fatalf("Kind() on headless = %q, want empty", got)
	}
	if a.HasElement("anything") {
		t.Fatal("expected HasElement to be false on headless (no state to check)")
	}

	// Must never panic: headless has no AddTextElement/AddLabeledValueElement/
	// RemoveElement/Update method at all.
	a.AddText("x", "y", 0, 0, pluginmanager.FontSmall, false, 0)
	a.AddLabeledValue("x2", "L", "V", 0, 0, pluginmanager.FontBold, pluginmanager.FontMedium, 0)
	a.RemoveElement("x")
	a.Update(true)

	// Set IS real on headless — a plugin's on_ui_update state change must
	// still be observable there, exactly like a built-in element.
	a.Set("status", "hunting")
	if h.sets["status"] != "hunting" {
		t.Fatalf("expected Set to forward to the headless view, got %v", h.sets)
	}
}

func TestViewAdapterForwardsUnreadMessages(t *testing.T) {
	target := &unreadTarget{}
	View{V: target}.OnUnreadMessages(2, 5)
	if target.count != 2 || target.total != 5 {
		t.Fatalf("unread state = %d/%d, want 2/5", target.count, target.total)
	}
}

var _ pluginmanager.ViewCapability = View{}
