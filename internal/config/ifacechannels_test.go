package config

import "testing"

func TestIfaceChannels(t *testing.T) {
	fake := func(name string, args ...string) string {
		if name != "iw" {
			t.Fatalf("unexpected command %q", name)
		}
		switch {
		case len(args) == 2 && args[0] == "wlan0mon" && args[1] == "info":
			return "Interface wlan0mon\n\tifindex 3\n\twdev 0x1\n\taddr aa:bb:cc:dd:ee:ff\n\ttype monitor\n\twiphy 0\n\tchannel 6\n"
		case len(args) == 2 && args[0] == "phy0" && args[1] == "channels":
			return "Band 1:\n" +
				"\t* 2412 MHz [1] (20.0 dBm)\n" +
				"\t* 2417 MHz [2] (20.0 dBm)\n" +
				"\t* 2472 MHz [13] (disabled)\n" +
				"\t* 5825 MHz [165] (20.0 dBm)\n" +
				"not a channel line\n"
		default:
			t.Fatalf("unexpected args %v", args)
		}
		return ""
	}

	got := IfaceChannels(fake, "wlan0mon")
	want := []int{1, 2, 165}
	if len(got) != len(want) {
		t.Fatalf("IfaceChannels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("IfaceChannels = %v, want %v", got, want)
		}
	}
}

func TestIfaceChannelsNoWiphyLineReturnsEmpty(t *testing.T) {
	fake := func(name string, args ...string) string { return "no wiphy info here\n" }
	got := IfaceChannels(fake, "wlan0mon")
	if len(got) != 0 {
		t.Fatalf("expected no channels when wiphy can't be resolved, got %v", got)
	}
}
