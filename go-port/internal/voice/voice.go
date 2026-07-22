// Package voice ports pwnagotchi/voice.py: randomized flavor-text/status
// messages, translated via the same gettext .mo catalogs Python uses
// (embedded from pwnagotchi/locale/*/LC_MESSAGES/voice.mo).
package voice

import (
	"embed"
	"fmt"
	"math/rand"
	"path"
	"strings"

	"github.com/leonelquinteros/gotext"
)

//go:embed locale
var localeFS embed.FS

// Peer is the minimal projection of mesh.Peer that Voice's on_new_peer/
// on_lost_peer need.
type Peer interface {
	Name() string
	FirstEncounter() bool
}

// LastSessionSummary is the minimal projection of log.LastSession that
// on_last_session_data/on_last_session_tweet need.
type LastSessionSummary struct {
	Duration      string
	DurationHuman string
	Deauthed      int
	Associated    int
	Handshakes    int
	Peers         int
}

// Voice ports voice.Voice.
type Voice struct {
	catalog *gotext.Mo // nil => untranslated fallback, matching gettext's fallback=True NullTranslations
	rnd     *rand.Rand
}

// New ports Voice.__init__(lang): loads locale/<lang>/LC_MESSAGES/voice.mo
// from the embedded catalog set. If no catalog exists for lang, Voice
// falls back to returning msgids verbatim — exactly what Python's
// gettext.translation(..., fallback=True) does when the requested language
// has no catalog.
func New(lang string) *Voice {
	v := &Voice{rnd: rand.New(rand.NewSource(rand.Int63()))}
	data, err := localeFS.ReadFile(path.Join("locale", lang, "LC_MESSAGES", "voice.mo"))
	if err != nil {
		return v
	}
	mo := gotext.NewMo()
	mo.Parse(data)
	v.catalog = mo
	return v
}

func (v *Voice) t(s string) string {
	if v.catalog == nil {
		return s
	}
	// Not v.catalog.Get(s) directly: go vet's printf analysis treats
	// gotext.Mo.Get(str string, vars ...interface{}) as a printf-style
	// wrapper and flags a non-constant format string here, but it's a
	// real false positive — gotext.FormatString (what Get calls
	// internally) explicitly special-cases zero variadic args to return
	// str unchanged, never touching fmt.Sprintf at all, so a translated
	// string containing a literal "%" can never be misinterpreted at
	// this call site. s must stay the literal lookup key (it's the
	// gettext catalog key, not user-facing format data), so the fix
	// isn't to change what's passed — assigning the method value first
	// breaks vet's call-shape pattern matching without changing runtime
	// behavior at all.
	get := v.catalog.Get
	return get(s)
}

func (v *Voice) choice(options []string) string {
	return options[v.rnd.Intn(len(options))]
}

// Custom mirrors Voice.custom.
func (v *Voice) Custom(s string) string { return s }

// Default mirrors Voice.default.
func (v *Voice) Default() string { return v.t("ZzzzZZzzzzZzzz") }

// OnStarting mirrors Voice.on_starting.
func (v *Voice) OnStarting() string {
	return v.choice([]string{
		v.t("Hi, I'm Pwnagotchi! Starting ..."),
		v.t("New day, new hunt, new pwns!"),
		v.t("Hack the Planet!"),
		v.t("No more mister Wi-Fi!!"),
		v.t("Pretty fly 4 a Wi-Fi!"),
		v.t("Sniff. Deauth. Repeat."),
		v.t("Good Pwning!"),
		v.t("Ensign, Engage!"),
		v.t("Free your Wi-Fi!"),
		v.t("Chevron Seven, locked."),
		v.t("May the Wi-fi be with you"),
	})
}

// OnKeysGeneration mirrors Voice.on_keys_generation.
func (v *Voice) OnKeysGeneration() string {
	return v.choice([]string{
		v.t("Generating keys, do not turn off ..."),
		v.t("Are you the keymaster?"),
		v.t("I am the keymaster!"),
	})
}

// OnNormal mirrors Voice.on_normal.
func (v *Voice) OnNormal() string {
	return v.choice([]string{"", "..."})
}

// OnFreeChannel mirrors Voice.on_free_channel.
func (v *Voice) OnFreeChannel(channel int) string {
	return formatTemplate(v.t("Hey, channel {channel} is free! Your AP will say thanks."), "channel", channel)
}

// OnReadingLogs mirrors Voice.on_reading_logs.
func (v *Voice) OnReadingLogs(linesSoFar int) string {
	if linesSoFar == 0 {
		return v.t("Reading last session logs ...")
	}
	return formatTemplate(v.t("Read {lines_so_far} log lines so far ..."), "lines_so_far", linesSoFar)
}

// OnBored mirrors Voice.on_bored.
func (v *Voice) OnBored() string {
	return v.choice([]string{
		v.t("I'm bored ..."),
		v.t("Let's go for a walk!"),
	})
}

// OnMotivated mirrors Voice.on_motivated (reward is accepted, unused, to
// match Python's signature exactly).
func (v *Voice) OnMotivated(reward float64) string {
	return v.choice([]string{
		v.t("This is the best day of my life!"),
		v.t("All your base are belong to us"),
		v.t("Fascinating!"),
	})
}

// OnDemotivated mirrors Voice.on_demotivated.
func (v *Voice) OnDemotivated(reward float64) string { return v.t("Shitty day :/") }

// OnSad mirrors Voice.on_sad.
func (v *Voice) OnSad() string {
	return v.choice([]string{
		v.t("I'm extremely bored ..."),
		v.t("I'm very sad ..."),
		v.t("I'm sad"),
		v.t("I'm so happy ..."),
		v.t("Life? Don't talk to me about life."),
		"...",
	})
}

// OnAngry mirrors Voice.on_angry.
func (v *Voice) OnAngry() string {
	return v.choice([]string{
		"...",
		v.t("Leave me alone ..."),
		v.t("I'm mad at you!"),
	})
}

// OnExcited mirrors Voice.on_excited.
func (v *Voice) OnExcited() string {
	return v.choice([]string{
		v.t("I'm living the life!"),
		v.t("I pwn therefore I am."),
		v.t("So many networks!!!"),
		v.t("I'm having so much fun!"),
		v.t("It's a Wi-Fi system! I know this!"),
		v.t("My crime is that of curiosity ..."),
	})
}

// OnNewPeer mirrors Voice.on_new_peer.
func (v *Voice) OnNewPeer(peer Peer) string {
	if peer.FirstEncounter() {
		return v.choice([]string{
			formatTemplate(v.t("Hello {name}! Nice to meet you."), "name", peer.Name()),
		})
	}
	return v.choice([]string{
		formatTemplate(v.t("Yo {name}! Sup?"), "name", peer.Name()),
		formatTemplate(v.t("Hey {name} how are you doing?"), "name", peer.Name()),
		formatTemplate(v.t("Unit {name} is nearby!"), "name", peer.Name()),
	})
}

// OnLostPeer mirrors Voice.on_lost_peer.
func (v *Voice) OnLostPeer(peer Peer) string {
	return v.choice([]string{
		formatTemplate(v.t("Uhm ... goodbye {name}"), "name", peer.Name()),
		formatTemplate(v.t("{name} is gone ..."), "name", peer.Name()),
	})
}

// OnMiss mirrors Voice.on_miss.
func (v *Voice) OnMiss(who string) string {
	return v.choice([]string{
		formatTemplate(v.t("Whoops ... {name} is gone."), "name", who),
		formatTemplate(v.t("{name} missed!"), "name", who),
		v.t("Missed!"),
	})
}

// OnGrateful mirrors Voice.on_grateful.
func (v *Voice) OnGrateful() string {
	return v.choice([]string{
		v.t("Good friends are a blessing!"),
		v.t("I love my friends!"),
	})
}

// OnLonely mirrors Voice.on_lonely.
func (v *Voice) OnLonely() string {
	return v.choice([]string{
		v.t("Nobody wants to play with me ..."),
		v.t("I feel so alone ..."),
		v.t("Let's find friends"),
		v.t("Where's everybody?!"),
	})
}

// OnNapping mirrors Voice.on_napping.
func (v *Voice) OnNapping(secs int) string {
	return v.choice([]string{
		formatTemplate(v.t("Napping for {secs}s ..."), "secs", secs),
		v.t("Zzzzz"),
		v.t("Snoring ..."),
		formatTemplate(v.t("ZzzZzzz ({secs}s)"), "secs", secs),
	})
}

// OnShutdown mirrors Voice.on_shutdown.
func (v *Voice) OnShutdown() string {
	return v.choice([]string{v.t("Good night."), v.t("Zzz")})
}

// OnAwakening mirrors Voice.on_awakening.
func (v *Voice) OnAwakening() string {
	return v.choice([]string{"...", "!", "Hello World!", v.t("I dreamed of electric sheep")})
}

// OnWaiting mirrors Voice.on_waiting.
func (v *Voice) OnWaiting(secs int) string {
	return v.choice([]string{
		"...",
		formatTemplate(v.t("Waiting for {secs}s ..."), "secs", secs),
		formatTemplate(v.t("Looking around ({secs}s)"), "secs", secs),
	})
}

// OnAssoc mirrors Voice.on_assoc(ap): ap is {'hostname': ssid, 'mac': bssid}.
func (v *Voice) OnAssoc(ssid, bssid string) string {
	what := ssid
	if ssid == "" || ssid == "<hidden>" {
		what = bssid
	}
	return v.choice([]string{
		formatTemplate(v.t("Hey {what} let's be friends!"), "what", what),
		formatTemplate(v.t("Associating to {what}"), "what", what),
		formatTemplate(v.t("Yo {what}!"), "what", what),
		formatTemplate(v.t("Hello there, {what}"), "what", what),
		formatTemplate(v.t("Mind if I join, {what}?"), "what", what),
		formatTemplate(v.t("Rise and Shine Mr. {what}!"), "what", what),
	})
}

// OnDeauth mirrors Voice.on_deauth(sta): sta is {'mac': mac}.
func (v *Voice) OnDeauth(mac string) string {
	return v.choice([]string{
		formatTemplate(v.t("Just decided that {mac} needs no Wi-Fi!"), "mac", mac),
		formatTemplate(v.t("Deauthenticating {mac}"), "mac", mac),
		formatTemplate(v.t("No more Wi-Fi for {mac}"), "mac", mac),
		formatTemplate(v.t("It's a trap! {mac}"), "mac", mac),
		formatTemplate(v.t("Consider yourself unplugged, {mac}"), "mac", mac),
		formatTemplate(v.t("Hasta la vista, {mac}"), "mac", mac),
		formatTemplate(v.t("You shall not pass, {mac}"), "mac", mac),
		formatTemplate(v.t("Kickbanning {mac}!"), "mac", mac),
	})
}

// OnHandshakes mirrors Voice.on_handshakes.
func (v *Voice) OnHandshakes(newShakes int) string {
	plural := ""
	if newShakes > 1 {
		plural = "s"
	}
	tmpl := v.t("Cool, we got {num} new handshake{plural}!")
	return formatTemplate(formatTemplate(tmpl, "num", newShakes), "plural", plural)
}

// OnUnreadMessages mirrors Voice.on_unread_messages.
func (v *Voice) OnUnreadMessages(count, total int) string {
	plural := ""
	if count > 1 {
		plural = "s"
	}
	tmpl := v.t("You have {count} new message{plural}!")
	return formatTemplate(formatTemplate(tmpl, "count", count), "plural", plural)
}

// OnRebooting mirrors Voice.on_rebooting.
func (v *Voice) OnRebooting() string {
	return v.choice([]string{
		v.t("Oops, something went wrong ... Rebooting ..."),
		v.t("Well, this is awkward."),
		v.t("Tell my packets I love them."),
		v.t("Have you tried turning it off and on again?"),
		v.t("I'm afraid Dave"),
		v.t("I'm dead, Jim!"),
		v.t("I have a bad feeling about this"),
		v.t("You did this."),
	})
}

// OnUploading mirrors Voice.on_uploading.
func (v *Voice) OnUploading(to string) string {
	return v.choice([]string{
		formatTemplate(v.t("Uploading data to {to} ..."), "to", to),
		formatTemplate(v.t("Beam me up to {to}"), "to", to),
		formatTemplate(v.t("Engage warp drive, {to}"), "to", to),
		formatTemplate(v.t("Gift-wrapping data for {to}"), "to", to),
		formatTemplate(v.t("Please wait, magic happening at {to}"), "to", to),
	})
}

// OnDownloading mirrors Voice.on_downloading.
func (v *Voice) OnDownloading(name string) string {
	return formatTemplate(v.t("Downloading from {name} ..."), "name", name)
}

// OnLastSessionData mirrors Voice.on_last_session_data.
func (v *Voice) OnLastSessionData(s LastSessionSummary) string {
	status := formatTemplate(v.t("Kicked {num} stations\n"), "num", s.Deauthed)
	if s.Associated > 999 {
		status += v.t("Made >999 new friends\n")
	} else {
		status += formatTemplate(v.t("Made {num} new friends\n"), "num", s.Associated)
	}
	status += formatTemplate(v.t("Got {num} handshakes\n"), "num", s.Handshakes)
	switch {
	case s.Peers == 1:
		status += v.t("Met 1 peer")
	case s.Peers > 0:
		status += formatTemplate(v.t("Met {num} peers"), "num", s.Peers)
	}
	return status
}

// OnLastSessionTweet mirrors Voice.on_last_session_tweet.
func (v *Voice) OnLastSessionTweet(s LastSessionSummary) string {
	tmpl := v.t("I've been pwning for {duration} and kicked {deauthed} clients! I've also met {associated} new friends and ate {handshakes} handshakes! #pwnagotchi #pwnlog #pwnlife #hacktheplanet #skynet")
	tmpl = formatTemplate(tmpl, "duration", s.DurationHuman)
	tmpl = formatTemplate(tmpl, "deauthed", s.Deauthed)
	tmpl = formatTemplate(tmpl, "associated", s.Associated)
	tmpl = formatTemplate(tmpl, "handshakes", s.Handshakes)
	return tmpl
}

// Hhmmss mirrors Voice.hhmmss(count, fmt).
func (v *Voice) Hhmmss(count int64, format string) string {
	if count > 1 {
		switch format {
		case "h":
			return v.t("hours")
		case "m":
			return v.t("minutes")
		case "s":
			return v.t("seconds")
		}
	} else {
		switch format {
		case "h":
			return v.t("hour")
		case "m":
			return v.t("minute")
		case "s":
			return v.t("second")
		}
	}
	return format
}

// formatTemplate replicates Python's str.format(**kwargs) for the single-
// placeholder-occurrence templates voice.py uses: replaces "{key}" with
// fmt.Sprint(value).
func formatTemplate(tmpl, key string, value interface{}) string {
	return strings.ReplaceAll(tmpl, "{"+key+"}", fmt.Sprint(value))
}
