// Package wifiparse is a pure-Go (no cgo, no external pcap library) port
// of pwnagotchi/utils.py's extract_from_pcap: reads a classic libpcap
// capture file (the format bettercap/pwnagotchi actually write handshake
// captures in — every *.pcap consumer in this port, e.g.
// internal/config/handshakes.go and internal/plugins/native/pwncrack,
// already assumes the plain ".pcap" extension/format, never ".pcapng")
// and extracts BSSID/ESSID/encryption/channel/frequency/RSSI from the
// captured 802.11 frames, exactly like real Python's Scapy-based
// extract_from_pcap did — required by GO_ONLY_MIGRATION_PROMPT.md's
// "Port Scapy-dependent PCAP/PCAPNG extraction ... to tested pure Go for
// grid and wigle."
package wifiparse

import (
	"encoding/binary"
	"fmt"
)

// linkType values this package understands (libpcap LINKTYPE_*).
const (
	linkTypeIEEE80211      = 105 // DLT_IEEE802_11: plain 802.11 frames, no radio metadata
	linkTypeIEEE80211Radio = 127 // DLT_IEEE802_11_RADIO: RadioTap header prefixed to each frame
)

// pcapGlobalHeaderLen is the classic pcap file header size.
const pcapGlobalHeaderLen = 24

// pcapRecordHeaderLen is the per-packet record header size.
const pcapRecordHeaderLen = 16

// packet is one captured frame plus the file's declared link-layer type.
type packet struct {
	data     []byte
	linkType uint32
}

// readPCAP parses a classic pcap file's global header and every packet
// record, returning the raw captured bytes of each packet (truncated to
// incl_len, matching what a real capture tool would have captured) along
// with the file's link-layer type. Bounds-checked throughout: this parses
// externally-influenced capture data and must never panic on truncated
// or malformed input, only return an error.
func readPCAP(data []byte) ([]packet, error) {
	if len(data) < pcapGlobalHeaderLen {
		return nil, fmt.Errorf("wifiparse: file too short to be a pcap file (%d bytes)", len(data))
	}

	magic := binary.LittleEndian.Uint32(data[0:4])
	var order binary.ByteOrder
	switch magic {
	case 0xa1b2c3d4, 0xa1b23c4d: // microsecond / nanosecond, same-endian
		order = binary.LittleEndian
	case 0xd4c3b2a1, 0x4d3cb2a1: // microsecond / nanosecond, swapped-endian
		order = binary.BigEndian
	default:
		return nil, fmt.Errorf("wifiparse: not a pcap file (bad magic number %#x)", magic)
	}

	linkType := order.Uint32(data[20:24])

	var packets []packet
	offset := pcapGlobalHeaderLen
	for offset < len(data) {
		if offset+pcapRecordHeaderLen > len(data) {
			return nil, fmt.Errorf("wifiparse: truncated packet record header at offset %d", offset)
		}
		record := data[offset : offset+pcapRecordHeaderLen]
		inclLen := order.Uint32(record[8:12])
		offset += pcapRecordHeaderLen

		if uint64(offset)+uint64(inclLen) > uint64(len(data)) {
			return nil, fmt.Errorf("wifiparse: truncated packet data at offset %d (want %d bytes)", offset, inclLen)
		}
		frame := data[offset : offset+int(inclLen)]
		offset += int(inclLen)

		packets = append(packets, packet{data: frame, linkType: linkType})
	}
	return packets, nil
}
