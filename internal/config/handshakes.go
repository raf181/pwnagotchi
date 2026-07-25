package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

func normalizeAlnum(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	return strings.ToLower(b.String())
}

// RemoveWhitelisted mirrors utils.remove_whitelisted: drops any path whose
// basename (with a trailing ".pcap" stripped via Python's str.rstrip, which
// strips a CHARACTER SET, not a suffix — replicated exactly below) contains
// a normalized (alnum-only, lowercased) whitelist entry as a substring.
func RemoveWhitelisted(handshakes, whitelist []string, validOnError bool) []string {
	filtered := make([]string, 0, len(handshakes))
	for _, h := range handshakes {
		normalized, err := normalizeHandshakeName(h)
		if err != nil {
			if validOnError {
				filtered = append(filtered, h)
			}
			continue
		}
		matched := false
		for _, w := range whitelist {
			if strings.Contains(normalized, normalizeAlnum(w)) {
				matched = true
				break
			}
		}
		if !matched {
			filtered = append(filtered, h)
		}
	}
	return filtered
}

// normalizeHandshakeName mirrors os.path.basename(h).rstrip('.pcap'), i.e.
// Python's str.rstrip(chars) strips any trailing run of characters that are
// members of the set {'.', 'p', 'a', 'c'} — NOT the literal suffix ".pcap".
// E.g. "handshake.pcap" and "handshakeaaapcp" both lose more than just an
// extension. Replicated intentionally; see docs/known-differences.md.
func normalizeHandshakeName(h string) (string, error) {
	base := filepath.Base(h)
	const cutset = ".pcap"
	end := len(base)
	for end > 0 && strings.ContainsRune(cutset, rune(base[end-1])) {
		end--
	}
	return normalizeAlnum(base[:end]), nil
}

// SecsToHHMMSS mirrors utils.secs_to_hhmmss.
func SecsToHHMMSS(secs int64) string {
	mins := secs / 60
	s := secs % 60
	hours := mins / 60
	m := mins % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, m, s)
}

// SecsToHHMMSSFloat mirrors utils.secs_to_hhmmss called with a float
// (Python's divmod and "%d" formatting both work on floats, truncating):
// epoch.py's `duration_secs`/`slept_for_secs` fields are floats
// (time.time() deltas), so callers logging them need this variant to match
// Python's exact output rather than losing the fractional-second remainder
// before formatting.
func SecsToHHMMSSFloat(secs float64) string {
	mins := floorDiv(secs, 60)
	s := secs - mins*60
	hours := floorDiv(mins, 60)
	m := mins - hours*60
	return fmt.Sprintf("%02d:%02d:%02d", int64(hours), int64(m), int64(s))
}

func floorDiv(a, b float64) float64 {
	q := a / b
	if q < 0 && q != float64(int64(q)) {
		return float64(int64(q)) - 1
	}
	return float64(int64(q))
}

// TotalUniqueHandshakes mirrors utils.total_unique_handshakes: count of
// *.pcap files directly inside path (non-recursive glob, matching Python's
// glob.glob, not a walk).
func TotalUniqueHandshakes(globFn func(pattern string) ([]string, error), path string) (int, error) {
	matches, err := globFn(filepath.Join(path, "*.pcap"))
	if err != nil {
		return 0, err
	}
	return len(matches), nil
}
