package wifiparse

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// --- test fixture builders ---------------------------------------------

func mac(s byte) [6]byte {
	return [6]byte{s, s + 1, s + 2, s + 3, s + 4, s + 5}
}

func macSlice(s byte) []byte {
	m := mac(s)
	return m[:]
}

func bssidSlice(b [6]byte) []byte {
	return b[:]
}

// buildIE encodes one tag/length/value information element.
func buildIE(tag byte, value []byte) []byte {
	out := []byte{tag, byte(len(value))}
	return append(out, value...)
}

// buildBeacon builds a full 802.11 Beacon frame: 24-byte mgmt header +
// 12-byte fixed fields (timestamp/interval/capability) + IEs.
func buildBeacon(bssid [6]byte, capability uint16, ies ...[]byte) []byte {
	frame := make([]byte, 0, 64)
	frame = append(frame, 0x80, 0x00) // frame control: type=mgmt(0), subtype=beacon(8) -> 1000 00 00 = 0x80
	frame = append(frame, 0x00, 0x00) // duration
	frame = append(frame, macSlice(1)...)
	frame = append(frame, macSlice(2)...)
	frame = append(frame, bssidSlice(bssid)...)
	frame = append(frame, 0x00, 0x00) // sequence control

	frame = append(frame, make([]byte, 8)...) // timestamp
	frame = append(frame, 0x64, 0x00)         // beacon interval
	capBytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(capBytes, capability)
	frame = append(frame, capBytes...)
	for _, ie := range ies {
		frame = append(frame, ie...)
	}
	return frame
}

func buildAssocReq(bssid [6]byte, capability uint16, ies ...[]byte) []byte {
	frame := make([]byte, 0, 64)
	frame = append(frame, 0x00, 0x00) // type=mgmt(0), subtype=assoc-req(0) -> 0x00
	frame = append(frame, 0x00, 0x00)
	frame = append(frame, bssidSlice(bssid)...)
	frame = append(frame, macSlice(2)...)
	frame = append(frame, bssidSlice(bssid)...)
	frame = append(frame, 0x00, 0x00)

	capBytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(capBytes, capability)
	frame = append(frame, capBytes...)
	frame = append(frame, 0x00, 0x00) // listen interval
	for _, ie := range ies {
		frame = append(frame, ie...)
	}
	return frame
}

// buildReassocReq builds a Reassociation Request frame: fixed fields are
// Capability(2) + Listen Interval(2) + Current AP address(6), per
// dot11.go's subtypeReassocReq branch (10-byte fixed-field region before
// IEs start).
func buildReassocReq(bssid [6]byte, capability uint16, ies ...[]byte) []byte {
	frame := make([]byte, 0, 64)
	frame = append(frame, 0x20, 0x00) // type=mgmt(0), subtype=reassoc-req(2) -> 0010 0000 = 0x20
	frame = append(frame, 0x00, 0x00)
	frame = append(frame, bssidSlice(bssid)...)
	frame = append(frame, macSlice(2)...)
	frame = append(frame, bssidSlice(bssid)...)
	frame = append(frame, 0x00, 0x00)

	capBytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(capBytes, capability)
	frame = append(frame, capBytes...)
	frame = append(frame, 0x00, 0x00)      // listen interval
	frame = append(frame, macSlice(20)...) // current AP address (6 bytes)
	for _, ie := range ies {
		frame = append(frame, ie...)
	}
	return frame
}

// buildProbeResp builds a Probe Response frame: same fixed-field shape as
// a Beacon (Timestamp(8)+Interval(2)+Capability(2)), per dot11.go's
// shared subtypeBeacon/subtypeProbeResp branch.
func buildProbeResp(bssid [6]byte, capability uint16, ies ...[]byte) []byte {
	frame := make([]byte, 0, 64)
	frame = append(frame, 0x50, 0x00) // type=mgmt(0), subtype=probe-resp(5) -> 0101 0000 = 0x50
	frame = append(frame, 0x00, 0x00)
	frame = append(frame, macSlice(1)...)
	frame = append(frame, macSlice(2)...)
	frame = append(frame, bssidSlice(bssid)...)
	frame = append(frame, 0x00, 0x00)

	frame = append(frame, make([]byte, 8)...)
	frame = append(frame, 0x64, 0x00)
	capBytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(capBytes, capability)
	frame = append(frame, capBytes...)
	for _, ie := range ies {
		frame = append(frame, ie...)
	}
	return frame
}

// buildRadiotap builds a minimal RadioTap header with only the Channel
// (bit 3) and dBm Antenna Signal (bit 5) fields present, at their
// correctly-aligned offsets.
func buildRadiotap(freqMHz uint16, signalDBm int8) []byte {
	// Layout: [0]=version [1]=pad [2:4]=len [4:8]=present
	// then Channel field must be 2-aligned relative to header start
	// (offset 8 already even) -> frequency(2)+flags(2) at offset 8..12
	// then Signal (1-aligned) at offset 12.
	present := uint32(1<<3 | 1<<5)
	header := make([]byte, 13)
	header[0] = 0 // version
	header[1] = 0 // pad
	binary.LittleEndian.PutUint16(header[2:4], 13)
	binary.LittleEndian.PutUint32(header[4:8], present)
	binary.LittleEndian.PutUint16(header[8:10], freqMHz)
	binary.LittleEndian.PutUint16(header[10:12], 0) // channel flags
	header[12] = byte(signalDBm)
	return header
}

// buildPCAP assembles a full classic-pcap file: global header + one
// record per given frame. linkType selects DLT_IEEE802_11 or
// DLT_IEEE802_11_RADIO.
func buildPCAP(t *testing.T, linkType uint32, frames ...[]byte) string {
	t.Helper()
	buf := make([]byte, 0, 256)

	global := make([]byte, pcapGlobalHeaderLen)
	binary.LittleEndian.PutUint32(global[0:4], 0xa1b2c3d4)
	binary.LittleEndian.PutUint16(global[4:6], 2)
	binary.LittleEndian.PutUint16(global[6:8], 4)
	binary.LittleEndian.PutUint32(global[16:20], 65535) // snaplen
	binary.LittleEndian.PutUint32(global[20:24], linkType)
	buf = append(buf, global...)

	for _, frame := range frames {
		record := make([]byte, pcapRecordHeaderLen)
		binary.LittleEndian.PutUint32(record[8:12], uint32(len(frame)))
		binary.LittleEndian.PutUint32(record[12:16], uint32(len(frame)))
		buf = append(buf, record...)
		buf = append(buf, frame...)
	}

	path := filepath.Join(t.TempDir(), "test.pcap")
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func withRadiotap(rt []byte, frame []byte) []byte {
	return append(append([]byte{}, rt...), frame...)
}

// buildPCAPWithMagic is buildPCAP but with an explicit magic number, so
// tests can exercise the nanosecond-resolution variant (0xa1b23c4d) in
// addition to buildPCAP's default microsecond magic (0xa1b2c3d4) — this
// package never reads packet timestamps at all (only incl_len/offset
// walking), so the only thing that actually varies with the magic number
// as far as extraction is concerned is byte-order selection (covered
// separately by TestBigEndianMagicNumberSwapsByteOrder); this test
// exists to confirm a real nanosecond-header capture (which real
// tcpdump/dumpcap can produce with `-t nano` for greater timestamp
// precision) is still accepted and parsed correctly end-to-end, not just
// "the byte-order logic happens to be shared code."
func buildPCAPWithMagic(t *testing.T, magic uint32, linkType uint32, frames ...[]byte) string {
	t.Helper()
	buf := make([]byte, 0, 256)

	global := make([]byte, pcapGlobalHeaderLen)
	binary.LittleEndian.PutUint32(global[0:4], magic)
	binary.LittleEndian.PutUint16(global[4:6], 2)
	binary.LittleEndian.PutUint16(global[6:8], 4)
	binary.LittleEndian.PutUint32(global[16:20], 65535)
	binary.LittleEndian.PutUint32(global[20:24], linkType)
	buf = append(buf, global...)

	for _, frame := range frames {
		record := make([]byte, pcapRecordHeaderLen)
		binary.LittleEndian.PutUint32(record[8:12], uint32(len(frame)))
		binary.LittleEndian.PutUint32(record[12:16], uint32(len(frame)))
		buf = append(buf, record...)
		buf = append(buf, frame...)
	}

	path := filepath.Join(t.TempDir(), "test.pcap")
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- tests ---------------------------------------------------------------

func TestExtractBSSIDFromBeacon(t *testing.T) {
	bssid := mac(0xAA)
	ssidIE := buildIE(ieTagSSID, []byte("TestNet"))
	beacon := buildBeacon(bssid, 0, ssidIE)
	path := buildPCAP(t, linkTypeIEEE80211, beacon)

	res, err := ExtractFromPCAP(path, []Field{FieldBSSID})
	if err != nil {
		t.Fatalf("ExtractFromPCAP: %v", err)
	}
	want := "aa:ab:ac:ad:ae:af"
	if got := res[FieldBSSID].String; got != want {
		t.Fatalf("BSSID = %q, want %q", got, want)
	}
}

func TestExtractESSIDFromBeacon(t *testing.T) {
	bssid := mac(1)
	ssidIE := buildIE(ieTagSSID, []byte("MyHomeNetwork"))
	dsIE := buildIE(ieTagDSParameterSet, []byte{6})
	beacon := buildBeacon(bssid, 0, ssidIE, dsIE)
	path := buildPCAP(t, linkTypeIEEE80211, beacon)

	res, err := ExtractFromPCAP(path, []Field{FieldESSID})
	if err != nil {
		t.Fatalf("ExtractFromPCAP: %v", err)
	}
	if got := res[FieldESSID].String; got != "MyHomeNetwork" {
		t.Fatalf("ESSID = %q, want %q", got, "MyHomeNetwork")
	}
}

func TestExtractESSIDFromAssocReq(t *testing.T) {
	bssid := mac(2)
	ssidIE := buildIE(ieTagSSID, []byte("AssocNet"))
	assoc := buildAssocReq(bssid, 0, ssidIE)
	path := buildPCAP(t, linkTypeIEEE80211, assoc)

	res, err := ExtractFromPCAP(path, []Field{FieldESSID})
	if err != nil {
		t.Fatalf("ExtractFromPCAP: %v", err)
	}
	if got := res[FieldESSID].String; got != "AssocNet" {
		t.Fatalf("ESSID = %q, want %q", got, "AssocNet")
	}
}

func TestExtractEncryptionOpen(t *testing.T) {
	bssid := mac(3)
	ssidIE := buildIE(ieTagSSID, []byte("OpenNet"))
	beacon := buildBeacon(bssid, 0x0000, ssidIE) // privacy bit clear
	path := buildPCAP(t, linkTypeIEEE80211, beacon)

	res, err := ExtractFromPCAP(path, []Field{FieldEncryption})
	if err != nil {
		t.Fatalf("ExtractFromPCAP: %v", err)
	}
	crypto := res[FieldEncryption].Crypto
	if len(crypto) != 1 || crypto[0] != "OPN" {
		t.Fatalf("crypto = %v, want [OPN]", crypto)
	}
}

func TestExtractEncryptionWPA2(t *testing.T) {
	bssid := mac(4)
	ssidIE := buildIE(ieTagSSID, []byte("SecureNet"))
	rsnIE := buildIE(ieTagRSN, []byte{0x01, 0x00})      // minimal RSN body, content unchecked
	beacon := buildBeacon(bssid, 0x0010, ssidIE, rsnIE) // privacy bit set
	path := buildPCAP(t, linkTypeIEEE80211, beacon)

	res, err := ExtractFromPCAP(path, []Field{FieldEncryption})
	if err != nil {
		t.Fatalf("ExtractFromPCAP: %v", err)
	}
	crypto := res[FieldEncryption].Crypto
	if len(crypto) != 1 || crypto[0] != "WPA2" {
		t.Fatalf("crypto = %v, want [WPA2]", crypto)
	}
}

func TestExtractEncryptionWEP(t *testing.T) {
	bssid := mac(5)
	ssidIE := buildIE(ieTagSSID, []byte("WepNet"))
	beacon := buildBeacon(bssid, 0x0010, ssidIE) // privacy bit set, no RSN/WPA IE
	path := buildPCAP(t, linkTypeIEEE80211, beacon)

	res, err := ExtractFromPCAP(path, []Field{FieldEncryption})
	if err != nil {
		t.Fatalf("ExtractFromPCAP: %v", err)
	}
	crypto := res[FieldEncryption].Crypto
	if len(crypto) != 1 || crypto[0] != "WEP" {
		t.Fatalf("crypto = %v, want [WEP]", crypto)
	}
}

func TestExtractChannelFrequencyRSSIFromRadiotap(t *testing.T) {
	bssid := mac(6)
	ssidIE := buildIE(ieTagSSID, []byte("RTNet"))
	beacon := buildBeacon(bssid, 0, ssidIE)
	rt := buildRadiotap(2437, -42) // channel 6
	framed := withRadiotap(rt, beacon)
	path := buildPCAP(t, linkTypeIEEE80211Radio, framed)

	res, err := ExtractFromPCAP(path, []Field{FieldChannel, FieldFrequency, FieldRSSI})
	if err != nil {
		t.Fatalf("ExtractFromPCAP: %v", err)
	}
	if got := res[FieldChannel].Int; got != 6 {
		t.Fatalf("Channel = %d, want 6", got)
	}
	if got := res[FieldFrequency].Int; got != 2437 {
		t.Fatalf("Frequency = %d, want 2437", got)
	}
	if got := res[FieldRSSI].Int; got != -42 {
		t.Fatalf("RSSI = %d, want -42", got)
	}
}

func TestChannelFrequencyRSSIFailWithoutRadiotap(t *testing.T) {
	bssid := mac(7)
	ssidIE := buildIE(ieTagSSID, []byte("NoRadiotap"))
	beacon := buildBeacon(bssid, 0, ssidIE)
	path := buildPCAP(t, linkTypeIEEE80211, beacon) // plain 802.11, no RadioTap

	for _, f := range []Field{FieldChannel, FieldFrequency, FieldRSSI} {
		_, err := ExtractFromPCAP(path, []Field{f})
		if err == nil {
			t.Fatalf("field %v: expected FieldNotFoundError for a non-RadioTap capture, got nil", f)
		}
		var fnf *FieldNotFoundError
		if !asFieldNotFound(err, &fnf) {
			t.Fatalf("field %v: expected *FieldNotFoundError, got %v (%T)", f, err, err)
		}
	}
}

func asFieldNotFound(err error, target **FieldNotFoundError) bool {
	if fnf, ok := err.(*FieldNotFoundError); ok {
		*target = fnf
		return true
	}
	return false
}

func TestFieldNotFoundWhenAbsent(t *testing.T) {
	// A beacon with no SSID IE at all: BSSID still found, ESSID not.
	bssid := mac(8)
	beacon := buildBeacon(bssid, 0)
	path := buildPCAP(t, linkTypeIEEE80211, beacon)

	if _, err := ExtractFromPCAP(path, []Field{FieldBSSID}); err != nil {
		t.Fatalf("expected BSSID to be found even with no IEs: %v", err)
	}
	_, err := ExtractFromPCAP(path, []Field{FieldESSID})
	if err == nil {
		t.Fatal("expected FieldNotFoundError for ESSID with no IEs present")
	}
}

func TestNoBeaconAtAllFailsBSSID(t *testing.T) {
	// Only an assoc-req in the file: BSSID (which requires a beacon
	// specifically) must fail even though a management frame exists.
	bssid := mac(9)
	assoc := buildAssocReq(bssid, 0, buildIE(ieTagSSID, []byte("X")))
	path := buildPCAP(t, linkTypeIEEE80211, assoc)

	_, err := ExtractFromPCAP(path, []Field{FieldBSSID})
	if err == nil {
		t.Fatal("expected FieldNotFoundError: BSSID requires a Beacon frame specifically")
	}
}

func TestCorruptFileReturnsErrorNotPanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.pcap")
	if err := os.WriteFile(path, []byte{0x01, 0x02, 0x03}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractFromPCAP(path, []Field{FieldBSSID}); err == nil {
		t.Fatal("expected an error for a too-short/corrupt file")
	}
}

func TestTruncatedPacketRecordReturnsErrorNotPanic(t *testing.T) {
	global := make([]byte, pcapGlobalHeaderLen)
	binary.LittleEndian.PutUint32(global[0:4], 0xa1b2c3d4)
	binary.LittleEndian.PutUint32(global[20:24], linkTypeIEEE80211)
	// Declare a huge incl_len but provide no packet data at all.
	record := make([]byte, pcapRecordHeaderLen)
	binary.LittleEndian.PutUint32(record[8:12], 9999)
	buf := append(global, record...)

	dir := t.TempDir()
	path := filepath.Join(dir, "truncated.pcap")
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractFromPCAP(path, []Field{FieldBSSID}); err == nil {
		t.Fatal("expected an error for a truncated packet record")
	}
}

func TestMissingFileReturnsError(t *testing.T) {
	if _, err := ExtractFromPCAP("/nonexistent/path.pcap", []Field{FieldBSSID}); err == nil {
		t.Fatal("expected an error for a nonexistent file")
	}
}

func TestBigEndianMagicNumberSwapsByteOrder(t *testing.T) {
	bssid := mac(10)
	beacon := buildBeacon(bssid, 0, buildIE(ieTagSSID, []byte("BE")))

	global := make([]byte, pcapGlobalHeaderLen)
	binary.BigEndian.PutUint32(global[0:4], 0xa1b2c3d4) // swapped magic on disk
	binary.BigEndian.PutUint32(global[20:24], linkTypeIEEE80211)
	record := make([]byte, pcapRecordHeaderLen)
	binary.BigEndian.PutUint32(record[8:12], uint32(len(beacon)))
	binary.BigEndian.PutUint32(record[12:16], uint32(len(beacon)))
	buf := append(global, record...)
	buf = append(buf, beacon...)

	dir := t.TempDir()
	path := filepath.Join(dir, "be.pcap")
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := ExtractFromPCAP(path, []Field{FieldBSSID})
	if err != nil {
		t.Fatalf("ExtractFromPCAP: %v", err)
	}
	if got := res[FieldBSSID].String; got != "0a:0b:0c:0d:0e:0f" {
		t.Fatalf("BSSID = %q, want %q", got, "0a:0b:0c:0d:0e:0f")
	}
}

// TestExtractESSIDFromProbeResp mirrors real Python: the ESSID branch of
// extract_from_pcap only adds 'beacon', 'assoc-req', and 'reassoc-req' to
// its bpf_filter subtypes set — 'probe-resp' is never included (verified
// directly against the real source's ESSID branch; a Dot11ProbeResp
// import does appear in the neighboring BSSID branch, but unused there
// too — a leftover, not a functional inclusion). A probe-resp-only
// capture must therefore NOT satisfy an ESSID request, exactly matching
// that omission — not a Go-port gap.
func TestExtractESSIDFromProbeResp(t *testing.T) {
	bssid := mac(30)
	probeResp := buildProbeResp(bssid, 0, buildIE(ieTagSSID, []byte("ProbeRespNet")))
	path := buildPCAP(t, linkTypeIEEE80211, probeResp)

	_, err := ExtractFromPCAP(path, []Field{FieldESSID})
	if err == nil {
		t.Fatal("expected FieldNotFoundError: real Python's ESSID extraction does not match probe-resp frames")
	}
}

func TestExtractESSIDFromReassocReq(t *testing.T) {
	bssid := mac(11)
	reassoc := buildReassocReq(bssid, 0, buildIE(ieTagSSID, []byte("ReassocNet")))
	path := buildPCAP(t, linkTypeIEEE80211, reassoc)

	res, err := ExtractFromPCAP(path, []Field{FieldESSID})
	if err != nil {
		t.Fatalf("ExtractFromPCAP: %v", err)
	}
	if got := res[FieldESSID].String; got != "ReassocNet" {
		t.Fatalf("ESSID = %q, want %q", got, "ReassocNet")
	}
}

// TestHiddenSSIDReturnsEmptyStringNotError ports real Python's behavior
// for a zero-length SSID information element (a hidden-SSID AP): Scapy's
// Dot11Elt.info for a length-0 IE decodes to b”.decode('utf-8') == "",
// a real (falsy-but-present) empty string, not a raised
// FieldNotFoundError — the IE itself was found, it's just empty.
func TestHiddenSSIDReturnsEmptyStringNotError(t *testing.T) {
	bssid := mac(12)
	hiddenSSID := buildIE(ieTagSSID, []byte{}) // zero-length IE
	beacon := buildBeacon(bssid, 0, hiddenSSID)
	path := buildPCAP(t, linkTypeIEEE80211, beacon)

	res, err := ExtractFromPCAP(path, []Field{FieldESSID})
	if err != nil {
		t.Fatalf("expected a hidden (zero-length) SSID IE to be found, not FieldNotFoundError: %v", err)
	}
	if got := res[FieldESSID].String; got != "" {
		t.Fatalf("ESSID = %q, want empty string for hidden SSID", got)
	}
}

// TestMultiPacketFieldInLaterPacket proves extraction scans every packet
// in the file, not just the first — the matching Beacon is the 3rd
// packet, preceded by two irrelevant frames (a bare data-ish/unknown
// frame and an assoc-req without the field of interest, which BSSID's
// beacon-only filter must skip over).
func TestMultiPacketFieldInLaterPacket(t *testing.T) {
	decoy1 := []byte{0x08, 0x00, 0x00, 0x00} // frame type=2 (data), too short to matter either way
	decoy2 := buildAssocReq(mac(40), 0, buildIE(ieTagSSID, []byte("DecoyAssoc")))
	bssid := mac(13)
	realBeacon := buildBeacon(bssid, 0, buildIE(ieTagSSID, []byte("RealNet")))
	path := buildPCAP(t, linkTypeIEEE80211, decoy1, decoy2, realBeacon)

	res, err := ExtractFromPCAP(path, []Field{FieldBSSID, FieldESSID})
	if err != nil {
		t.Fatalf("ExtractFromPCAP: %v", err)
	}
	wantBSSID := "0d:0e:0f:10:11:12"
	if got := res[FieldBSSID].String; got != wantBSSID {
		t.Fatalf("BSSID = %q, want %q (from the 3rd packet, not the decoys)", got, wantBSSID)
	}
	if got := res[FieldESSID].String; got != "DecoyAssoc" {
		// Real Python's ESSID filter matches beacon/assoc-req/reassoc-req,
		// so the 2nd packet (assoc-req) legitimately wins for ESSID even
		// though the 3rd packet (beacon) wins for BSSID — different
		// filters, first-match-per-field, exactly like real Python's
		// independent per-field sniff() calls.
		t.Fatalf("ESSID = %q, want %q (first matching frame for ESSID's broader filter)", got, "DecoyAssoc")
	}
}

func TestNanosecondTimestampMagicNumberParsesCorrectly(t *testing.T) {
	bssid := mac(14)
	beacon := buildBeacon(bssid, 0, buildIE(ieTagSSID, []byte("NanoNet")))
	path := buildPCAPWithMagic(t, 0xa1b23c4d, linkTypeIEEE80211, beacon)

	res, err := ExtractFromPCAP(path, []Field{FieldBSSID, FieldESSID})
	if err != nil {
		t.Fatalf("ExtractFromPCAP with nanosecond-timestamp magic: %v", err)
	}
	if got := res[FieldESSID].String; got != "NanoNet" {
		t.Fatalf("ESSID = %q, want %q", got, "NanoNet")
	}
}
