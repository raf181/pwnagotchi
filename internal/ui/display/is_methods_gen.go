// Code generated from testdata/hw_is_methods.json by
// scripts/gen_display_is_methods.py; DO NOT EDIT by hand — regenerate instead.
//
// Ports pwnagotchi/ui/display.py's ~93 is_X()/gfxhat() driver-name-check
// methods. Several compare against a string that does not match ANY real
// driver's self.name — verified against the real Python source — and are
// therefore always false in both Python and here; see
// docs/known-differences.md for the specific cases (is_dfrobot_v1,
// is_dfrobot_v2, is_weact2in9, is_dummy_display).
package display

// IsLcdhat ports display.py's is_lcdhat().
func (d *Display) IsLcdhat() bool { return d.implName() == "lcdhat" }

// IsWhisplay ports display.py's is_whisplay().
func (d *Display) IsWhisplay() bool { return d.implName() == "whisplay" }

// IsWavesharelcd0in96 ports display.py's is_wavesharelcd0in96().
func (d *Display) IsWavesharelcd0in96() bool { return d.implName() == "wavesharelcd0in96" }

// IsWavesharelcd1in3 ports display.py's is_wavesharelcd1in3().
func (d *Display) IsWavesharelcd1in3() bool { return d.implName() == "wavesharelcd1in3" }

// IsWavesharelcd1in8 ports display.py's is_wavesharelcd1in8().
func (d *Display) IsWavesharelcd1in8() bool { return d.implName() == "wavesharelcd1in8" }

// IsWavesharelcd1in9 ports display.py's is_wavesharelcd1in9().
func (d *Display) IsWavesharelcd1in9() bool { return d.implName() == "wavesharelcd1in9" }

// IsWavesharelcd1in14 ports display.py's is_wavesharelcd1in14().
func (d *Display) IsWavesharelcd1in14() bool { return d.implName() == "wavesharelcd1in14" }

// IsWavesharelcd1in28 ports display.py's is_wavesharelcd1in28().
func (d *Display) IsWavesharelcd1in28() bool { return d.implName() == "wavesharelcd1in28" }

// IsWavesharelcd1in47 ports display.py's is_wavesharelcd1in47().
func (d *Display) IsWavesharelcd1in47() bool { return d.implName() == "wavesharelcd1in47" }

// IsWavesharelcd1in54 ports display.py's is_wavesharelcd1in54().
func (d *Display) IsWavesharelcd1in54() bool { return d.implName() == "wavesharelcd1in54" }

// IsWavesharelcd1in69 ports display.py's is_wavesharelcd1in69().
func (d *Display) IsWavesharelcd1in69() bool { return d.implName() == "wavesharelcd1in69" }

// IsWavesharelcd2in0 ports display.py's is_wavesharelcd2in0().
func (d *Display) IsWavesharelcd2in0() bool { return d.implName() == "wavesharelcd2in0" }

// IsWavesharelcd2in4 ports display.py's is_wavesharelcd2in4().
func (d *Display) IsWavesharelcd2in4() bool { return d.implName() == "wavesharelcd2in4" }

// IsWaveshare144lcd ports display.py's is_waveshare144lcd().
func (d *Display) IsWaveshare144lcd() bool { return d.implName() == "waveshare144lcd" }

// IsOledhat ports display.py's is_oledhat().
func (d *Display) IsOledhat() bool { return d.implName() == "oledhat" }

// IsWaveshare1in02 ports display.py's is_waveshare1in02().
func (d *Display) IsWaveshare1in02() bool { return d.implName() == "waveshare1in02" }

// IsWaveshare1in54 ports display.py's is_waveshare1in54().
func (d *Display) IsWaveshare1in54() bool { return d.implName() == "waveshare1in54" }

// IsWaveshare1in54V2 ports display.py's is_waveshare1in54V2().
func (d *Display) IsWaveshare1in54V2() bool { return d.implName() == "waveshare1in54_v2" }

// IsWaveshare1in54b ports display.py's is_waveshare1in54b().
func (d *Display) IsWaveshare1in54b() bool { return d.implName() == "waveshare1in54b" }

// IsWaveshare1in54bV22 ports display.py's is_waveshare1in54bV22().
func (d *Display) IsWaveshare1in54bV22() bool { return d.implName() == "waveshare1in54b_v2" }

// IsWaveshare1in54c ports display.py's is_waveshare1in54c().
func (d *Display) IsWaveshare1in54c() bool { return d.implName() == "waveshare1in54c" }

// IsWaveshare1in64g ports display.py's is_waveshare1in64g().
func (d *Display) IsWaveshare1in64g() bool { return d.implName() == "waveshare1in64g" }

// IsWaveshare2in7 ports display.py's is_waveshare2in7().
func (d *Display) IsWaveshare2in7() bool { return d.implName() == "waveshare2in7" }

// IsWaveshare2in7V2 ports display.py's is_waveshare2in7V2().
func (d *Display) IsWaveshare2in7V2() bool { return d.implName() == "waveshare2in7_v2" }

// IsWaveshare2in9 ports display.py's is_waveshare2in9().
func (d *Display) IsWaveshare2in9() bool { return d.implName() == "waveshare2in9" }

// IsWaveshare2in9V2 ports display.py's is_waveshare2in9V2().
func (d *Display) IsWaveshare2in9V2() bool { return d.implName() == "waveshare2in9_v2" }

// IsWaveshare2in9bV3 ports display.py's is_waveshare2in9bV3().
func (d *Display) IsWaveshare2in9bV3() bool { return d.implName() == "waveshare2in9b_v3" }

// IsWaveshare2in9bV4 ports display.py's is_waveshare2in9bV4().
func (d *Display) IsWaveshare2in9bV4() bool { return d.implName() == "waveshare2in9b_v4" }

// IsWaveshare2in9bc ports display.py's is_waveshare2in9bc().
func (d *Display) IsWaveshare2in9bc() bool { return d.implName() == "waveshare2in9bc" }

// IsWaveshare2in9d ports display.py's is_waveshare2in9d().
func (d *Display) IsWaveshare2in9d() bool { return d.implName() == "waveshare2in9d" }

// IsWaveshareV1 ports display.py's is_waveshare_v1().
func (d *Display) IsWaveshareV1() bool { return d.implName() == "waveshare_1" }

// IsWaveshareV2 ports display.py's is_waveshare_v2().
func (d *Display) IsWaveshareV2() bool { return d.implName() == "waveshare_2" }

// IsWaveshareV3 ports display.py's is_waveshare_v3().
func (d *Display) IsWaveshareV3() bool { return d.implName() == "waveshare_3" }

// IsWaveshareV4 ports display.py's is_waveshare_v4().
func (d *Display) IsWaveshareV4() bool { return d.implName() == "waveshare_4" }

// IsWaveshare2in13bV3 ports display.py's is_waveshare2in13b_v3().
func (d *Display) IsWaveshare2in13bV3() bool { return d.implName() == "waveshare2in13b_v3" }

// IsWaveshare2in13bV4 ports display.py's is_waveshare2in13b_v4().
func (d *Display) IsWaveshare2in13bV4() bool { return d.implName() == "waveshare2in13b_v4" }

// IsWaveshare2in13bc ports display.py's is_waveshare2in13bc().
func (d *Display) IsWaveshare2in13bc() bool { return d.implName() == "waveshare2in13bc" }

// IsWaveshare2in13d ports display.py's is_waveshare2in13d().
func (d *Display) IsWaveshare2in13d() bool { return d.implName() == "waveshare2in13d" }

// IsWaveshare2in13g ports display.py's is_waveshare2in13g().
func (d *Display) IsWaveshare2in13g() bool { return d.implName() == "waveshare2in13g" }

// IsWaveshare2in36g ports display.py's is_waveshare2in36g().
func (d *Display) IsWaveshare2in36g() bool { return d.implName() == "waveshare2in36g" }

// IsWaveshare2in66 ports display.py's is_waveshare2in66().
func (d *Display) IsWaveshare2in66() bool { return d.implName() == "waveshare2in66" }

// IsWaveshare2in66b ports display.py's is_waveshare2in66b().
func (d *Display) IsWaveshare2in66b() bool { return d.implName() == "waveshare2in66b" }

// IsWaveshare2in66g ports display.py's is_waveshare2in66g().
func (d *Display) IsWaveshare2in66g() bool { return d.implName() == "waveshare2in66g" }

// IsWeact2in9 ports display.py's is_weact2in9().
func (d *Display) IsWeact2in9() bool { return d.implName() == "weact2in9" }

// IsWaveshare3in0g ports display.py's is_waveshare3in0g().
func (d *Display) IsWaveshare3in0g() bool { return d.implName() == "waveshare3in0g" }

// IsWaveshare3in7 ports display.py's is_waveshare3in7().
func (d *Display) IsWaveshare3in7() bool { return d.implName() == "waveshare3in7" }

// IsWaveshare3in52 ports display.py's is_waveshare3in52().
func (d *Display) IsWaveshare3in52() bool { return d.implName() == "waveshare3in52" }

// IsWaveshare4in01f ports display.py's is_waveshare4in01f().
func (d *Display) IsWaveshare4in01f() bool { return d.implName() == "waveshare4in01f" }

// IsWaveshare4in2 ports display.py's is_waveshare4in2().
func (d *Display) IsWaveshare4in2() bool { return d.implName() == "waveshare4in2" }

// IsWaveshare4in2V2 ports display.py's is_waveshare4in2V2().
func (d *Display) IsWaveshare4in2V2() bool { return d.implName() == "waveshare4in2_v2" }

// IsWaveshare4in2bV2 ports display.py's is_waveshare4in2bV2().
func (d *Display) IsWaveshare4in2bV2() bool { return d.implName() == "waveshare4in2b_v2" }

// IsWaveshare4in2bc ports display.py's is_waveshare4in2bc().
func (d *Display) IsWaveshare4in2bc() bool { return d.implName() == "waveshare4in2bc" }

// IsWaveshare4in26 ports display.py's is_waveshare4in26().
func (d *Display) IsWaveshare4in26() bool { return d.implName() == "waveshare4in26" }

// IsWaveshare4in37g ports display.py's is_waveshare4in37g().
func (d *Display) IsWaveshare4in37g() bool { return d.implName() == "waveshare4in37g" }

// IsWaveshare5in65f ports display.py's is_waveshare5in65f().
func (d *Display) IsWaveshare5in65f() bool { return d.implName() == "waveshare5in65f" }

// IsWaveshare5in79 ports display.py's is_waveshare5in79().
func (d *Display) IsWaveshare5in79() bool { return d.implName() == "waveshare5in79" }

// IsWaveshare5in79b ports display.py's is_waveshare5in79b().
func (d *Display) IsWaveshare5in79b() bool { return d.implName() == "waveshare5in79b" }

// IsWaveshare5in83 ports display.py's is_waveshare5in83().
func (d *Display) IsWaveshare5in83() bool { return d.implName() == "waveshare5in83" }

// IsWaveshare5in83V2 ports display.py's is_waveshare5in83V2().
func (d *Display) IsWaveshare5in83V2() bool { return d.implName() == "waveshare5in83_v2" }

// IsWaveshare5in83bV2 ports display.py's is_waveshare5in83bV2().
func (d *Display) IsWaveshare5in83bV2() bool { return d.implName() == "waveshare5in83b_v2" }

// IsWaveshare5in83bc ports display.py's is_waveshare5in83bc().
func (d *Display) IsWaveshare5in83bc() bool { return d.implName() == "waveshare5in83bc" }

// IsWaveshare7in3f ports display.py's is_waveshare7in3f().
func (d *Display) IsWaveshare7in3f() bool { return d.implName() == "waveshare7in3f" }

// IsWaveshare7in3g ports display.py's is_waveshare7in3g().
func (d *Display) IsWaveshare7in3g() bool { return d.implName() == "waveshare7in3g" }

// IsWaveshare7in5 ports display.py's is_waveshare7in5().
func (d *Display) IsWaveshare7in5() bool { return d.implName() == "waveshare7in5" }

// IsWaveshare7in5HD ports display.py's is_waveshare7in5HD().
func (d *Display) IsWaveshare7in5HD() bool { return d.implName() == "waveshare7in5_HD" }

// IsWaveshare7in5V2 ports display.py's is_waveshare7in5V2().
func (d *Display) IsWaveshare7in5V2() bool { return d.implName() == "waveshare7in5_v2" }

// IsWaveshare7in5bHD ports display.py's is_waveshare7in5bHD().
func (d *Display) IsWaveshare7in5bHD() bool { return d.implName() == "waveshare7in5b_HD" }

// IsWaveshare7in5bV2 ports display.py's is_waveshare7in5bV2().
func (d *Display) IsWaveshare7in5bV2() bool { return d.implName() == "waveshare7in5b_v2" }

// IsWaveshare7in5bc ports display.py's is_waveshare7in5bc().
func (d *Display) IsWaveshare7in5bc() bool { return d.implName() == "waveshare7in5bc" }

// IsWaveshare13in3k ports display.py's is_waveshare13in3k().
func (d *Display) IsWaveshare13in3k() bool { return d.implName() == "waveshare13in3k" }

// IsInky ports display.py's is_inky().
func (d *Display) IsInky() bool { return d.implName() == "inky" }

// IsInkyv2 ports display.py's is_inkyv2().
func (d *Display) IsInkyv2() bool { return d.implName() == "inkyv2" }

// IsDummyDisplay ports display.py's is_dummy_display().
func (d *Display) IsDummyDisplay() bool { return d.implName() == "dummydisplay" }

// IsPapirus ports display.py's is_papirus().
func (d *Display) IsPapirus() bool { return d.implName() == "papirus" }

// IsDfrobotV1 ports display.py's is_dfrobot_v1().
func (d *Display) IsDfrobotV1() bool { return d.implName() == "dfrobot_v1" }

// IsDfrobotV2 ports display.py's is_dfrobot_v2().
func (d *Display) IsDfrobotV2() bool { return d.implName() == "dfrobot_v2" }

// IsSpotpear24inch ports display.py's is_spotpear24inch().
func (d *Display) IsSpotpear24inch() bool { return d.implName() == "spotpear24inch" }

// IsSpotpear154lcd ports display.py's is_spotpear154lcd().
func (d *Display) IsSpotpear154lcd() bool { return d.implName() == "spotpear154lcd" }

// IsDisplayhatmini ports display.py's is_displayhatmini().
func (d *Display) IsDisplayhatmini() bool { return d.implName() == "displayhatmini" }

// IsGamepi20 ports display.py's is_gamepi20().
func (d *Display) IsGamepi20() bool { return d.implName() == "gamepi20" }

// IsGamepi15 ports display.py's is_gamepi15().
func (d *Display) IsGamepi15() bool { return d.implName() == "gamepi15" }

// IsPirateaudio ports display.py's is_pirateaudio().
func (d *Display) IsPirateaudio() bool { return d.implName() == "pirateaudio" }

// Gfxhat ports display.py's gfxhat().
func (d *Display) Gfxhat() bool { return d.implName() == "gfxhat" }

// IsArgonpod ports display.py's is_argonpod().
func (d *Display) IsArgonpod() bool { return d.implName() == "argonpod" }

// IsPitft ports display.py's is_pitft().
func (d *Display) IsPitft() bool { return d.implName() == "pitft" }

// IsMinipitft ports display.py's is_minipitft().
func (d *Display) IsMinipitft() bool { return d.implName() == "minipitft" }

// IsMinipitft2 ports display.py's is_minipitft2().
func (d *Display) IsMinipitft2() bool { return d.implName() == "minipitft2" }

// IsTftbonnet ports display.py's is_tftbonnet().
func (d *Display) IsTftbonnet() bool { return d.implName() == "tftbonnet" }

// IsWaveshareoledlcd ports display.py's is_waveshareoledlcd().
func (d *Display) IsWaveshareoledlcd() bool { return d.implName() == "waveshareoledlcd" }

// IsWaveshareoledlcdvert ports display.py's is_waveshareoledlcdvert().
func (d *Display) IsWaveshareoledlcdvert() bool { return d.implName() == "waveshareoledlcdvert" }

// IsI2coled ports display.py's is_i2coled().
func (d *Display) IsI2coled() bool { return d.implName() == "i2coled" }

// IsWaveshare35lcd ports display.py's is_waveshare35lcd().
func (d *Display) IsWaveshare35lcd() bool { return d.implName() == "waveshare35lcd" }

// IsAdfruit213v3 ports display.py's is_adfruit213v3().
func (d *Display) IsAdfruit213v3() bool { return d.implName() == "adafruit2in13_v3" }
