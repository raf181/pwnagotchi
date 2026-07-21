package faces

import "testing"

func TestDefaultMatchesConstants(t *testing.T) {
	s := Default()
	if s.Friend != Friend || s.Angry != Angry || s.PositionY != 40 {
		t.Fatalf("Default() = %+v", s)
	}
	if s.PNG != false || s.PositionX != 0 {
		t.Fatalf("Default() PNG/PositionX = %v/%v", s.PNG, s.PositionX)
	}
}

func TestLoadFromConfigOverridesNamedFields(t *testing.T) {
	s := Default()
	s.LoadFromConfig(map[string]string{
		"look_r": "(TEST)",
		"angry":  "(MAD)",
	})
	if s.LookR != "(TEST)" {
		t.Fatalf("LookR = %q, want overridden", s.LookR)
	}
	if s.Angry != "(MAD)" {
		t.Fatalf("Angry = %q, want overridden", s.Angry)
	}
	// Untouched fields must keep their defaults.
	if s.Friend != Friend {
		t.Fatalf("Friend = %q, should be unchanged default", s.Friend)
	}
}

func TestLoadFromConfigIgnoresUnknownKeys(t *testing.T) {
	s := Default()
	s.LoadFromConfig(map[string]string{"totally_bogus_key": "x"})
	if *s != *Default() {
		t.Fatal("an unrecognized config key must not change any known field")
	}
}
