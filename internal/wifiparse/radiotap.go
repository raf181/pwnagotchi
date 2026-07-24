package wifiparse

import "encoding/binary"

// radiotapInfo is the subset of a RadioTap header's fields this package
// needs, matching what real Python reads off Scapy's RadioTap layer:
// ChannelFrequency and dBm_AntSignal.
type radiotapInfo struct {
	hasFrequency bool
	frequencyMHz uint16
	hasSignal    bool
	signalDBm    int8
}

// radiotapField describes one presence-bit's on-wire shape: its size in
// bytes and its required alignment (fields are aligned to their own size
// per the RadioTap spec, with padding inserted before them as needed).
// Only bits actually used by real captures this package cares about (or
// that must be skipped correctly to keep later offsets aligned) are
// listed; an unknown/unlisted present bit aborts alignment tracking for
// any bits after it (see parseRadiotap), same as "we can't reliably
// locate anything past this point."
var radiotapFields = map[int]struct{ size, align int }{
	0:  {8, 8}, // TSFT (u64)
	1:  {1, 1}, // Flags (u8)
	2:  {1, 1}, // Rate (u8)
	3:  {4, 2}, // Channel: u16 frequency + u16 flags
	4:  {2, 2}, // FHSS
	5:  {1, 1}, // dBm Antenna Signal (s8)
	6:  {1, 1}, // dBm Antenna Noise (s8)
	7:  {2, 2}, // Lock Quality
	8:  {2, 2}, // TX Attenuation
	9:  {2, 2}, // dB TX Attenuation
	10: {1, 1}, // dBm TX Power
	11: {1, 1}, // Antenna
	12: {1, 1}, // dB Antenna Signal
	13: {1, 1}, // dB Antenna Noise
	14: {2, 2}, // RX Flags
}

// parseRadiotap parses a RadioTap header (version/pad/len/present-bitmask
// followed by fields in ascending bit order, each aligned to its natural
// size) far enough to extract channel frequency and antenna signal.
// Returns the header's total declared length (it_len) so the caller can
// skip past it to the real 802.11 frame, and ok=false if the buffer is
// too short or malformed to even read the fixed part of the header
// (never panics on truncated input).
func parseRadiotap(data []byte) (info radiotapInfo, headerLen int, ok bool) {
	if len(data) < 8 {
		return radiotapInfo{}, 0, false
	}
	// data[0] = it_version, data[1] = it_pad (ignored)
	itLen := int(binary.LittleEndian.Uint16(data[2:4]))
	if itLen < 8 || itLen > len(data) {
		return radiotapInfo{}, 0, false
	}

	// Read the (possibly chained) presence bitmask words: bit 31 of each
	// word set means another presence word follows immediately.
	var presentWords []uint32
	pos := 4
	for {
		if pos+4 > itLen || pos+4 > len(data) {
			return radiotapInfo{}, itLen, true // header present but unparseable body; still a valid frame offset
		}
		word := binary.LittleEndian.Uint32(data[pos : pos+4])
		presentWords = append(presentWords, word)
		pos += 4
		if word&0x80000000 == 0 {
			break
		}
	}

	fieldOffset := pos // fields begin right after the presence word(s)
	for wordIdx, word := range presentWords {
		for bit := 0; bit < 31; bit++ {
			if word&(1<<uint(bit)) == 0 {
				continue
			}
			globalBit := wordIdx*32 + bit
			shape, known := radiotapFields[globalBit]
			if !known {
				// An unrecognized present field means we can no longer
				// reliably compute subsequent field offsets (we don't
				// know its size) — stop extracting further fields, but
				// this is not an error: report whatever was already
				// found.
				return info, itLen, true
			}
			// Align fieldOffset up to shape.align.
			if rem := fieldOffset % shape.align; rem != 0 {
				fieldOffset += shape.align - rem
			}
			if fieldOffset+shape.size > len(data) || fieldOffset+shape.size > itLen {
				return info, itLen, true
			}
			switch globalBit {
			case 3:
				info.hasFrequency = true
				info.frequencyMHz = binary.LittleEndian.Uint16(data[fieldOffset : fieldOffset+2])
			case 5:
				info.hasSignal = true
				info.signalDBm = int8(data[fieldOffset])
			}
			fieldOffset += shape.size
		}
	}
	return info, itLen, true
}
