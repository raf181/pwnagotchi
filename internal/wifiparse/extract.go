package wifiparse

import (
	"fmt"
	"os"

	"github.com/jayofelony/pwnagotchi/internal/mesh"
)

// Field mirrors pwnagotchi/utils.py's WifiInfo enum.
type Field int

const (
	FieldBSSID Field = iota
	FieldESSID
	FieldEncryption
	FieldChannel
	FieldFrequency
	FieldRSSI
)

// Result holds one extracted field's value. Exactly one of the typed
// accessors is meaningful per Field — callers know which from the Field
// they requested. A single interface{}-keyed map (mirroring Python's
// dict return value) is intentionally avoided in favor of typed fields,
// per this port's "no interface{} bags" preference; ExtractFromPCAP
// still returns a map[Field]Result for shape-parity with Python's
// dict-of-results.
type Result struct {
	String string   // BSSID (formatted MAC), ESSID
	Crypto []string // Encryption: e.g. []string{"WPA2"} — an AP can report more than one
	Int    int      // Channel, Frequency (MHz), RSSI (dBm, signed)
}

// FieldNotFoundError mirrors Python's FieldNotFoundError: the requested
// field genuinely could not be found in the capture (not a parse error —
// see the distinct plain error returned for a malformed/truncated file).
type FieldNotFoundError struct{ Field Field }

func (e *FieldNotFoundError) Error() string {
	return fmt.Sprintf("wifiparse: could not find field [%s]", fieldName(e.Field))
}

func fieldName(f Field) string {
	switch f {
	case FieldBSSID:
		return "BSSID"
	case FieldESSID:
		return "ESSID"
	case FieldEncryption:
		return "ENCRYPTION"
	case FieldChannel:
		return "CHANNEL"
	case FieldFrequency:
		return "FREQUENCY"
	case FieldRSSI:
		return "RSSI"
	default:
		return "UNKNOWN"
	}
}

// ExtractFromPCAP ports extract_from_pcap(path, fields): reads the
// classic-pcap file at path and extracts every requested field. A field
// not present in the capture returns a *FieldNotFoundError specific to
// that field wrapped in the returned error (via errors.As-compatible
// wrapping) — matching real Python's per-field FieldNotFoundError — and
// stops at the first missing field, exactly like the original function's
// eager per-field loop (it also raises immediately on the first missing
// field rather than collecting every result first).
func ExtractFromPCAP(path string, fields []Field) (map[Field]Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("wifiparse: reading %s: %w", path, err)
	}
	packets, err := readPCAP(data)
	if err != nil {
		return nil, err
	}

	results := make(map[Field]Result, len(fields))
	for _, field := range fields {
		switch field {
		case FieldBSSID:
			mgmt, ok := findFirstMgmt(packets, subtypeBeacon)
			if !ok {
				return results, &FieldNotFoundError{Field: field}
			}
			results[field] = Result{String: formatMAC(mgmt.addr3)}

		case FieldESSID:
			mgmt, ok := findFirstMgmtAny(packets, subtypeBeacon, subtypeAssocReq, subtypeReassocReq)
			if !ok {
				return results, &FieldNotFoundError{Field: field}
			}
			ie, ok := firstIE(mgmt.ies)
			if !ok {
				return results, &FieldNotFoundError{Field: field}
			}
			results[field] = Result{String: string(ie.value)}

		case FieldEncryption:
			mgmt, ok := findFirstMgmt(packets, subtypeBeacon)
			if !ok {
				return results, &FieldNotFoundError{Field: field}
			}
			crypto := encryptionSet(mgmt.capability, mgmt.ies)
			if len(crypto) == 0 {
				return results, &FieldNotFoundError{Field: field}
			}
			results[field] = Result{Crypto: crypto}

		case FieldChannel, FieldFrequency, FieldRSSI:
			// Real Python takes literally sniff(offline=path, count=1) —
			// the very first packet in the file, unconditionally, no
			// frame-type filter — and reads RadioTap.ChannelFrequency/
			// dBm_AntSignal off it. A capture whose link-layer type
			// isn't RadioTap-prefixed has no RadioTap layer at all in
			// Scapy either, so this always fails for such files —
			// faithfully preserved, not a Go-port limitation.
			if len(packets) == 0 {
				return results, &FieldNotFoundError{Field: field}
			}
			first := packets[0]
			if first.linkType != linkTypeIEEE80211Radio {
				return results, &FieldNotFoundError{Field: field}
			}
			info, _, ok := parseRadiotap(first.data)
			if !ok {
				return results, &FieldNotFoundError{Field: field}
			}
			switch field {
			case FieldChannel:
				if !info.hasFrequency {
					return results, &FieldNotFoundError{Field: field}
				}
				ch, err := mesh.FreqToChannel(int(info.frequencyMHz))
				if err != nil {
					return results, &FieldNotFoundError{Field: field}
				}
				results[field] = Result{Int: ch}
			case FieldFrequency:
				if !info.hasFrequency {
					return results, &FieldNotFoundError{Field: field}
				}
				results[field] = Result{Int: int(info.frequencyMHz)}
			case FieldRSSI:
				if !info.hasSignal {
					return results, &FieldNotFoundError{Field: field}
				}
				results[field] = Result{Int: int(info.signalDBm)}
			}

		default:
			return results, fmt.Errorf("wifiparse: invalid field %d", field)
		}
	}
	return results, nil
}

// dot11FrameBytes returns the 802.11 frame bytes within a captured
// packet, skipping the RadioTap prefix if the file's link-layer type
// requires one. ok=false means the packet couldn't be interpreted at
// all (e.g. a RadioTap header claiming to be longer than the packet).
func dot11FrameBytes(p packet) ([]byte, bool) {
	if p.linkType != linkTypeIEEE80211Radio {
		return p.data, true
	}
	_, headerLen, ok := parseRadiotap(p.data)
	if !ok || headerLen > len(p.data) {
		return nil, false
	}
	return p.data[headerLen:], true
}

func findFirstMgmt(packets []packet, subtype int) (dot11Mgmt, bool) {
	return findFirstMgmtAny(packets, subtype)
}

func findFirstMgmtAny(packets []packet, subtypes ...int) (dot11Mgmt, bool) {
	want := make(map[int]bool, len(subtypes))
	for _, s := range subtypes {
		want[s] = true
	}
	for _, p := range packets {
		frame, ok := dot11FrameBytes(p)
		if !ok {
			continue
		}
		mgmt, ok := parseDot11Mgmt(frame)
		if !ok || !want[mgmt.subtype] {
			continue
		}
		return mgmt, true
	}
	return dot11Mgmt{}, false
}
