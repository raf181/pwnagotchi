package example

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// fakeLogger records every line, for asserting on_loaded's warning fired.
type fakeLogger struct{ lines []string }

func (f *fakeLogger) Printf(format string, args ...interface{}) {
	f.lines = append(f.lines, format)
}

// fakeView is a minimal in-memory pluginmanager.ViewCapability.
type fakeView struct {
	values  map[string]string
	labeled map[string]bool
	removed []string
}

func newFakeView() *fakeView {
	return &fakeView{values: map[string]string{}, labeled: map[string]bool{}}
}

func (f *fakeView) Set(key, value string) { f.values[key] = value }
func (f *fakeView) Update(bool)           {}
func (f *fakeView) Kind() string          { return "dummydisplay" }
func (f *fakeView) HasElement(key string) bool {
	_, ok := f.values[key]
	return ok
}
func (f *fakeView) RemoveElement(key string) {
	delete(f.values, key)
	f.removed = append(f.removed, key)
}
func (f *fakeView) AddText(key, value string, x, y int, font pluginmanager.FontStyle, wrap bool, maxLength int) {
	f.values[key] = value
}
func (f *fakeView) AddLabeledValue(key, label, value string, x, y int, labelFont, valueFont pluginmanager.FontStyle, labelSpacing int) {
	f.values[key] = value
	f.labeled[key] = true
}
func (f *fakeView) OnUploading(to string) {}
func (f *fakeView) OnNormal()             {}
func (f *fakeView) Width() int            { return 250 }
func (f *fakeView) Height() int           { return 122 }

var _ pluginmanager.ViewCapability = (*fakeView)(nil)

func TestMetadataMatchesRealPythonPlugin(t *testing.T) {
	p := New()
	meta := p.Metadata()
	// Author now credits both the original Python plugin's author and
	// this Go port, rather than the original author alone — see
	// cmd/pwnagotchi/main.go's registerNativePlugins for the same
	// pattern applied to webcfg/logtail.
	if meta.Author != "evilsocket@gmail.com (original), Go port by raf181" || meta.Version != "1.0.0" || meta.License != "GPL3" {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
	if !meta.HasWebhook {
		t.Fatal("expected HasWebhook=true (this plugin implements OnWebhook)")
	}
}

func TestOnLoadWarnsAndAddsUPSElement(t *testing.T) {
	log := &fakeLogger{}
	view := newFakeView()
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{
		Config: config.Map{"foo": "bar"},
		Log:    log,
		View:   view,
	}); err != nil {
		t.Fatalf("OnLoad: %v", err)
	}
	if len(log.lines) != 1 {
		t.Fatalf("expected exactly 1 warning logged, got %v", log.lines)
	}
	if !view.HasElement("ups") {
		t.Fatal("expected the 'ups' element to be added")
	}
	if !view.labeled["ups"] {
		t.Fatal("expected 'ups' to be added as a LabeledValue, not a plain Text")
	}
}

func TestOnLoadWithoutCapabilitiesDoesNotPanic(t *testing.T) {
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{}); err != nil {
		t.Fatalf("OnLoad: %v", err)
	}
	// Log and View are both nil here; HandleEvent/OnUnload must not panic.
	p.HandleEvent("ui_update", nil)
	if err := p.OnUnload(); err != nil {
		t.Fatalf("OnUnload: %v", err)
	}
}

func TestOnUnloadRemovesUPSElement(t *testing.T) {
	view := newFakeView()
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{View: view}); err != nil {
		t.Fatal(err)
	}
	if err := p.OnUnload(); err != nil {
		t.Fatal(err)
	}
	if view.HasElement("ups") {
		t.Fatal("expected 'ups' to be removed")
	}
}

func TestHandleEventUiUpdateSetsFormattedValue(t *testing.T) {
	view := newFakeView()
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{View: view}); err != nil {
		t.Fatal(err)
	}
	p.HandleEvent("ui_update", nil)
	if got := view.values["ups"]; got != "0.10V/100%" {
		t.Fatalf("ups value = %q, want %q", got, "0.10V/100%")
	}
}

func TestHandleEventReadyLogsUnitIsReady(t *testing.T) {
	log := &fakeLogger{}
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{Log: log}); err != nil {
		t.Fatal(err)
	}
	log.lines = nil // clear the on_loaded warning line
	p.HandleEvent("ready", []interface{}{nil})
	if len(log.lines) != 1 || log.lines[0] != "unit is ready" {
		t.Fatalf("expected 'unit is ready' logged, got %v", log.lines)
	}
}

// TestHandleEventEveryOtherKnownEventIsANoOp ports the fact that real
// example.py implements every remaining callback as a no-op `pass` body —
// this must never panic for any of them, with or without realistic args.
func TestHandleEventEveryOtherKnownEventIsANoOp(t *testing.T) {
	p := New()
	if err := p.OnLoad(pluginmanager.Capabilities{}); err != nil {
		t.Fatal(err)
	}
	events := []string{
		"free_channel", "bored", "sad", "excited", "lonely", "rebooting",
		"wait", "sleep", "wifi_update", "unfiltered_ap_list", "association",
		"deauthentication", "channel_hop", "handshake", "epoch",
		"peer_detected", "peer_lost", "some_future_event_name",
	}
	for _, ev := range events {
		p.HandleEvent(ev, []interface{}{"agent", "extra", 42})
	}
}

func TestOnWebhookReturnsRealResponse(t *testing.T) {
	p := New()
	req := httptest.NewRequest(http.MethodGet, "/plugins/example/status", nil)
	resp, err := p.OnWebhook("status", req)
	if err != nil {
		t.Fatalf("OnWebhook: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Status)
	}
	if string(resp.Body) != "example plugin webhook: status" {
		t.Fatalf("body = %q", resp.Body)
	}
}
