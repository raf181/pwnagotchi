// Package mesh ports pwnagotchi/mesh/*.py: the Wi-Fi frequency/channel
// helper (wifi.py) and (eventually) peer discovery/advertisement.
package mesh

import "fmt"

// NumChannels mirrors mesh.wifi.NumChannels.
const NumChannels = 233

// FreqToChannel mirrors mesh.wifi.freq_to_channel: converts a Wi-Fi
// frequency in MHz to its channel number across the 2.4/5/6 GHz bands.
// Returns an error (matching Python's ValueError) for frequencies outside
// all known bands.
//
// freq is an integer: real callers (utils.extract_from_pcap, reading
// scapy's RadioTap.ChannelFrequency) always pass whole-MHz integers, and the
// error message's exact text depends on that — Python's f-string renders
// int(0) as "0" but float(0.0) as "0.0"; the golden fixture
// (testdata/python_golden.json) was captured with integer inputs, matching
// real usage, so this signature is int, not float64.
func FreqToChannel(freq int) (int, error) {
	f := float64(freq)
	switch {
	case freq >= 2412 && freq <= 2472:
		return int(((f - 2412) / 5) + 1), nil
	case freq == 2484:
		return 14, nil
	case freq >= 5150 && freq <= 5850:
		switch {
		case freq >= 5150 && freq <= 5350:
			return int(((f - 5180) / 20) + 36), nil
		case freq >= 5470 && freq <= 5725:
			return int(((f - 5500) / 20) + 100), nil
		default:
			return int(((f - 5745) / 20) + 149), nil
		}
	case freq >= 5925 && freq <= 7125:
		return int(((f - 5950) / 20) + 11), nil
	default:
		return 0, fmt.Errorf("The frequency %d MHz is not a valid Wi-Fi frequency.", freq)
	}
}
