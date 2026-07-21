package voice

import (
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
