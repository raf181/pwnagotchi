package wifiparse

import (
	"encoding/binary"
	"fmt"
)

// 802.11 management frame subtypes this package looks at (frame type 0 =
// management; subtype is the high nibble of frame-control byte 0).
const (
	subtypeAssocReq   = 0
	subtypeReassocReq = 2
	subtypeProbeResp  = 5
	subtypeBeacon     = 8
)

// Information element tags used to derive encryption/channel.
const (
	ieTagSSID           = 0
	ieTagDSParameterSet = 3
	ieTagRSN            = 48
	ieTagVendorSpecific = 221
)

// dot11ie is one raw (tag, value) information element.
type dot11ie struct {
	tag   byte
	value []byte
}

// dot11Mgmt is the subset of a parsed 802.11 management frame this
// package needs.
type dot11Mgmt struct {
	subtype    int
	addr3      [6]byte // BSSID for all four subtypes this package parses
	capability uint16  // only meaningful for beacon/probe-resp/assoc-req/reassoc-req
	ies        []dot11ie
}

// mgmtFrameHeaderLen is the fixed 802.11 header before the frame body:
// 2 (frame control) + 2 (duration) + 6+6+6 (addr1/2/3) + 2 (seq control).
const mgmtFrameHeaderLen = 24

// parseDot11Mgmt parses an 802.11 frame, returning ok=false (never an
// error/panic) for anything that isn't a management frame of a subtype
// this package understands, or that's too short to be well-formed —
// this parses attacker-influenced capture data and must degrade to
// "frame skipped" rather than crash.
func parseDot11Mgmt(frame []byte) (dot11Mgmt, bool) {
	if len(frame) < mgmtFrameHeaderLen {
		return dot11Mgmt{}, false
	}
	fc0 := frame[0]
	frameType := (fc0 >> 2) & 0x3
	subtype := int((fc0 >> 4) & 0xF)
	if frameType != 0 { // 0 == management
		return dot11Mgmt{}, false
	}

	var m dot11Mgmt
	m.subtype = subtype
	copy(m.addr3[:], frame[16:22])
	body := frame[mgmtFrameHeaderLen:]

	switch subtype {
	case subtypeBeacon, subtypeProbeResp:
		// Fixed fields: Timestamp(8) + Beacon Interval(2) + Capability(2).
		if len(body) < 12 {
			return dot11Mgmt{}, false
		}
		m.capability = binary.LittleEndian.Uint16(body[10:12])
		m.ies = parseIEs(body[12:])
	case subtypeAssocReq:
		// Fixed fields: Capability(2) + Listen Interval(2).
		if len(body) < 4 {
			return dot11Mgmt{}, false
		}
		m.capability = binary.LittleEndian.Uint16(body[0:2])
		m.ies = parseIEs(body[4:])
	case subtypeReassocReq:
		// Fixed fields: Capability(2) + Listen Interval(2) + Current AP(6).
		if len(body) < 10 {
			return dot11Mgmt{}, false
		}
		m.capability = binary.LittleEndian.Uint16(body[0:2])
		m.ies = parseIEs(body[10:])
	default:
		return dot11Mgmt{}, false
	}
	return m, true
}

// parseIEs walks a frame body's tag/length/value information elements.
// Malformed trailing bytes (a truncated final IE) stop parsing rather
// than erroring — matching "extract everything well-formed, ignore the
// rest," never panicking on a short slice.
func parseIEs(body []byte) []dot11ie {
	var ies []dot11ie
	i := 0
	for i+2 <= len(body) {
		tag := body[i]
		length := int(body[i+1])
		i += 2
		if i+length > len(body) {
			break
		}
		ies = append(ies, dot11ie{tag: tag, value: body[i : i+length]})
		i += length
	}
	return ies
}

func firstIE(ies []dot11ie) (dot11ie, bool) {
	if len(ies) == 0 {
		return dot11ie{}, false
	}
	return ies[0], true
}

func findIE(ies []dot11ie, tag byte) (dot11ie, bool) {
	for _, e := range ies {
		if e.tag == tag {
			return e, true
		}
	}
	return dot11ie{}, false
}

// formatMAC matches Scapy's addr3 string rendering: lowercase
// colon-separated hex.
func formatMAC(mac [6]byte) string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5])
}

// capabilityPrivacyBit is bit 4 (0x0010) of the little-endian Capability
// Info field.
const capabilityPrivacyBit = 0x0010

// wpaOUIType1 identifies the legacy WPA vendor-specific IE: OUI
// 00:50:F2, vendor type 1 (WPA).
var wpaOUIType1 = [4]byte{0x00, 0x50, 0xF2, 0x01}

// encryptionSet reproduces Scapy's Dot11Beacon.network_stats()['crypto']
// heuristic: RSN IE present -> "WPA2"; legacy WPA vendor IE present ->
// "WPA"; Privacy capability bit set with neither -> "WEP"; Privacy bit
// clear -> "OPN" (an AP can report multiple simultaneously-supported
// values, matching Scapy's set semantics — e.g. a mixed WPA/WPA2 AP
// reports both "WPA" and "WPA2"). Scapy's own source wasn't available to
// read verbatim in this environment; this is the standard, widely
// documented derivation (also used by aircrack-ng/Wireshark) rather than
// a guess.
func encryptionSet(capability uint16, ies []dot11ie) []string {
	_, hasRSN := findIE(ies, ieTagRSN)
	hasWPA := false
	for _, e := range ies {
		if e.tag == ieTagVendorSpecific && len(e.value) >= 4 &&
			e.value[0] == wpaOUIType1[0] && e.value[1] == wpaOUIType1[1] &&
			e.value[2] == wpaOUIType1[2] && e.value[3] == wpaOUIType1[3] {
			hasWPA = true
			break
		}
	}
	privacy := capability&capabilityPrivacyBit != 0

	var out []string
	if hasRSN {
		out = append(out, "WPA2")
	}
	if hasWPA {
		out = append(out, "WPA")
	}
	if privacy && !hasRSN && !hasWPA {
		out = append(out, "WEP")
	}
	if !privacy {
		out = append(out, "OPN")
	}
	return out
}
