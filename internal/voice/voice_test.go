package voice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestItalianCatalogMatchesPython verifies the embedded voice.mo for
// Italian produces the exact same translated strings the real Python
// gettext.translation(...).gettext(...) call does (checked directly
// against the running Python interpreter).
func TestItalianCatalogMatchesPython(t *testing.T) {
	v := New("it")
	if got := v.t("hour"); got != "ora" {
		t.Fatalf(`t("hour") = %q, want "ora"`, got)
	}
	if got := v.t("hours"); got != "ore" {
		t.Fatalf(`t("hours") = %q, want "ore"`, got)
	}
}

func TestUnknownLanguageFallsBackToVerbatimMsgid(t *testing.T) {
	v := New("xx-not-a-real-lang")
	if got := v.t("hour"); got != "hour" {
		t.Fatalf(`unknown-language fallback: t("hour") = %q, want verbatim "hour"`, got)
	}
	if got := v.Hhmmss(1, "h"); got != "hour" {
		t.Fatalf("Hhmmss(1, h) = %q, want hour", got)
	}
}

func TestHhmmssPluralization(t *testing.T) {
	v := New("en")
	cases := []struct {
		count  int64
		format string
		want   string
	}{
		{1, "h", "hour"},
		{2, "h", "hours"},
		{1, "m", "minute"},
		{5, "m", "minutes"},
		{1, "s", "second"},
		{0, "s", "second"}, // count>1 is false for 0 too -> singular branch
		{3, "s", "seconds"},
		{1, "x", "x"}, // unknown format falls through to returning format itself
	}
	for _, c := range cases {
		if got := v.Hhmmss(c.count, c.format); got != c.want {
			t.Errorf("Hhmmss(%d, %q) = %q, want %q", c.count, c.format, got, c.want)
		}
	}
}

func TestOnFreeChannelFormatsPlaceholder(t *testing.T) {
	v := New("en")
	got := v.OnFreeChannel(11)
	want := "Hey, channel 11 is free! Your AP will say thanks."
	if got != want {
		t.Fatalf("OnFreeChannel(11) = %q, want %q", got, want)
	}
}

func TestOnHandshakesPluralization(t *testing.T) {
	v := New("en")
	if got := v.OnHandshakes(1); got != "Cool, we got 1 new handshake!" {
		t.Fatalf("OnHandshakes(1) = %q", got)
	}
	if got := v.OnHandshakes(3); got != "Cool, we got 3 new handshakes!" {
		t.Fatalf("OnHandshakes(3) = %q", got)
	}
}

type fakePeer struct {
	name  string
	first bool
}

func (p fakePeer) Name() string         { return p.name }
func (p fakePeer) FirstEncounter() bool { return p.first }

func TestOnNewPeerFirstEncounterVsReturning(t *testing.T) {
	v := New("en")
	got := v.OnNewPeer(fakePeer{name: "unit1", first: true})
	want := "Hello unit1! Nice to meet you."
	if got != want {
		t.Fatalf("first encounter: got %q, want %q", got, want)
	}

	got = v.OnNewPeer(fakePeer{name: "unit2", first: false})
	valid := map[string]bool{
		"Yo unit2! Sup?":               true,
		"Hey unit2 how are you doing?": true,
		"Unit unit2 is nearby!":        true,
	}
	if !valid[got] {
		t.Fatalf("returning peer: got unexpected message %q", got)
	}
}

func TestOnLastSessionData(t *testing.T) {
	v := New("en")
	got := v.OnLastSessionData(LastSessionSummary{Deauthed: 3, Associated: 2, Handshakes: 1, Peers: 1})
	want := "Kicked 3 stations\nMade 2 new friends\nGot 1 handshakes\nMet 1 peer"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestOnLastSessionDataOver999Friends(t *testing.T) {
	v := New("en")
	got := v.OnLastSessionData(LastSessionSummary{Associated: 1000})
	if !strings.Contains(got, "Made >999 new friends") {
		t.Fatalf("got %q, want the >999 branch", got)
	}
}

// TestEveryEmbeddedCatalogLoads proves every single embedded .mo catalog
// (one per language directory under locale/) is present and parses
// successfully via the real gotext loader New() uses at runtime — not
// just the one or two languages exercised by name elsewhere in this
// file. This is the "test that every catalog is present and loadable"
// gate the locale migration requires (see locale-src/README.md
// for how a catalog gets here from its editable .po source).
func TestEveryEmbeddedCatalogLoads(t *testing.T) {
	entries, err := localeFS.ReadDir("locale")
	if err != nil {
		t.Fatalf("reading embedded locale dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one embedded locale catalog, found none")
	}

	var loaded, failed int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		lang := e.Name()
		data, err := localeFS.ReadFile("locale/" + lang + "/LC_MESSAGES/voice.mo")
		if err != nil {
			t.Errorf("lang %q: expected voice.mo at the standard LC_MESSAGES path: %v", lang, err)
			failed++
			continue
		}
		if len(data) == 0 {
			t.Errorf("lang %q: voice.mo is empty", lang)
			failed++
			continue
		}
		v := New(lang)
		if v.catalog == nil {
			t.Errorf("lang %q: New(%q) did not load a catalog despite the embedded .mo existing", lang, lang)
			failed++
			continue
		}
		loaded++
	}
	if failed > 0 {
		t.Fatalf("%d of %d language catalogs failed to load", failed, loaded+failed)
	}
	t.Logf("verified %d embedded language catalogs all load successfully", loaded)
}

// TestCatalogCountMatchesEditableSources cross-checks the embedded
// runtime .mo catalogs against the editable .po sources preserved at
// locale-src/ — the two must stay in lockstep (every .po should
// have a compiled, embedded .mo; nothing should be embedded without a
// corresponding editable source), or a translator's edit could silently
// stop taking effect (or a stale .mo could silently outlive its source).
func TestCatalogCountMatchesEditableSources(t *testing.T) {
	moEntries, err := localeFS.ReadDir("locale")
	if err != nil {
		t.Fatalf("reading embedded locale dir: %v", err)
	}
	moLangs := map[string]bool{}
	for _, e := range moEntries {
		if e.IsDir() {
			moLangs[e.Name()] = true
		}
	}

	poLangs := readEditableSourceLangs(t)

	for lang := range moLangs {
		if !poLangs[lang] {
			t.Errorf("embedded catalog %q has no corresponding editable .po source under locale-src/", lang)
		}
	}
	for lang := range poLangs {
		if !moLangs[lang] {
			t.Errorf("editable .po source %q has no corresponding embedded, compiled .mo catalog", lang)
		}
	}
}

// readEditableSourceLangs walks locale-src/ relative to this
// package's location (../../locale-src) to list every language with a
// voice.po file. Skipped (not failed) if the source tree isn't present
// at all — e.g. a vendored/extracted copy of this module without the
// repository's non-embedded source directories — since that's not a
// runtime catalog-loading defect, just an environment without the full
// repo checked out.
func readEditableSourceLangs(t *testing.T) map[string]bool {
	t.Helper()
	root := filepath.Join("..", "..", "locale-src")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("locale-src/ not found at %s (not a full repo checkout?): %v", root, err)
		return nil
	}
	langs := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), "LC_MESSAGES", "voice.po")); err == nil {
			langs[e.Name()] = true
		}
	}
	return langs
}
