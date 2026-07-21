// Package faces ports pwnagotchi/ui/faces.py (pwnagotchi/ui/colors.py is a
// byte-for-byte duplicate of it that nothing in the Python codebase
// actually imports — verified via a repo-wide grep — so it's ported once
// here, not twice).
package faces

// Default face strings, copied verbatim from faces.py/colors.py.
const (
	LookR       = "( ⚆_⚆)"
	LookL       = "(☉_☉ )"
	LookRHappy  = "( ◕‿◕)"
	LookLHappy  = "(◕‿◕ )"
	Sleep       = "(⇀‿‿↼)"
	Sleep2      = "(≖‿‿≖)"
	Awake       = "(◕‿‿◕)"
	Bored       = "(-__-)"
	Intense     = "(°▃▃°)"
	Cool        = "(⌐■_■)"
	Happy       = "(•‿‿•)"
	Grateful    = "(^‿‿^)"
	Excited     = "(ᵔ◡◡ᵔ)"
	Motivated   = "(☼‿‿☼)"
	Demotivated = "(≖__≖)"
	Smart       = "(✜‿‿✜)"
	Lonely      = "(ب__ب)"
	Sad         = "(╥☁╥ )"
	Angry       = "(-_-')"
	Friend      = "(♥‿‿♥)"
	Broken      = "(☓‿‿☓)"
	Debug       = "(#__#)"
	Upload      = "(1__0)"
	Upload1     = "(1__1)"
	Upload2     = "(0__1)"

	DefaultPNG       = false
	DefaultPositionX = 0
	DefaultPositionY = 40
)

// Set holds a (possibly config-overridden) copy of every face string, plus
// the PNG/position settings — mirrors faces.py's module-level globals,
// grouped into a value so multiple components/tests don't share mutable
// package state the way Python's module globals implicitly do.
type Set struct {
	LookR, LookL, LookRHappy, LookLHappy  string
	Sleep, Sleep2, Awake, Bored, Intense  string
	Cool, Happy, Grateful, Excited        string
	Motivated, Demotivated, Smart, Lonely string
	Sad, Angry, Friend, Broken, Debug     string
	Upload, Upload1, Upload2              string

	PNG       bool
	PositionX int
	PositionY int
}

// Default returns the built-in face set, matching faces.py's module-level
// defaults before any load_from_config override.
func Default() *Set {
	return &Set{
		LookR: LookR, LookL: LookL, LookRHappy: LookRHappy, LookLHappy: LookLHappy,
		Sleep: Sleep, Sleep2: Sleep2, Awake: Awake, Bored: Bored, Intense: Intense,
		Cool: Cool, Happy: Happy, Grateful: Grateful, Excited: Excited,
		Motivated: Motivated, Demotivated: Demotivated, Smart: Smart, Lonely: Lonely,
		Sad: Sad, Angry: Angry, Friend: Friend, Broken: Broken, Debug: Debug,
		Upload: Upload, Upload1: Upload1, Upload2: Upload2,
		PNG: DefaultPNG, PositionX: DefaultPositionX, PositionY: DefaultPositionY,
	}
}

// fieldNames maps the uppercase config key (matching Python's
// `globals()[face_name.upper()] = face_value`) to a setter, so
// LoadFromConfig can override exactly the fields a [faces] config table
// names, leaving the rest at their defaults.
var fieldSetters = map[string]func(*Set, string){
	"LOOK_R":       func(s *Set, v string) { s.LookR = v },
	"LOOK_L":       func(s *Set, v string) { s.LookL = v },
	"LOOK_R_HAPPY": func(s *Set, v string) { s.LookRHappy = v },
	"LOOK_L_HAPPY": func(s *Set, v string) { s.LookLHappy = v },
	"SLEEP":        func(s *Set, v string) { s.Sleep = v },
	"SLEEP2":       func(s *Set, v string) { s.Sleep2 = v },
	"AWAKE":        func(s *Set, v string) { s.Awake = v },
	"BORED":        func(s *Set, v string) { s.Bored = v },
	"INTENSE":      func(s *Set, v string) { s.Intense = v },
	"COOL":         func(s *Set, v string) { s.Cool = v },
	"HAPPY":        func(s *Set, v string) { s.Happy = v },
	"GRATEFUL":     func(s *Set, v string) { s.Grateful = v },
	"EXCITED":      func(s *Set, v string) { s.Excited = v },
	"MOTIVATED":    func(s *Set, v string) { s.Motivated = v },
	"DEMOTIVATED":  func(s *Set, v string) { s.Demotivated = v },
	"SMART":        func(s *Set, v string) { s.Smart = v },
	"LONELY":       func(s *Set, v string) { s.Lonely = v },
	"SAD":          func(s *Set, v string) { s.Sad = v },
	"ANGRY":        func(s *Set, v string) { s.Angry = v },
	"FRIEND":       func(s *Set, v string) { s.Friend = v },
	"BROKEN":       func(s *Set, v string) { s.Broken = v },
	"DEBUG":        func(s *Set, v string) { s.Debug = v },
	"UPLOAD":       func(s *Set, v string) { s.Upload = v },
	"UPLOAD1":      func(s *Set, v string) { s.Upload1 = v },
	"UPLOAD2":      func(s *Set, v string) { s.Upload2 = v },
}

// LoadFromConfig ports faces.load_from_config: overrides matching face
// strings by uppercased key (config['ui']['faces'] in defaults.toml,
// e.g. `look_r = "..."`), leaving any unrecognized key silently ignored —
// matching Python's `globals()[...] = value`, which would actually create
// a NEW arbitrary global for an unrecognized key (dead, unused) rather
// than erroring; Go simply ignores unknown keys, since creating unused
// package-level bindings isn't meaningful here.
func (s *Set) LoadFromConfig(cfg map[string]string) {
	for key, value := range cfg {
		if setter, ok := fieldSetters[upperSnake(key)]; ok {
			setter(s, value)
		}
	}
}

func upperSnake(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
