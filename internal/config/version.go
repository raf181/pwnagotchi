// Package config ports pwnagotchi/utils.py: version parsing, config
// merge/load/save, the display-type alias table, whitelisting, and
// StatusFile.
package config

import "strings"

// ParseVersion mirrors utils.parse_version: split on '.', returning a tuple
// of STRINGS, not integers. Intentionally lexical, not numeric — see
// CompareVersions and docs/known-differences.md.
func ParseVersion(version string) []string {
	return strings.Split(version, ".")
}

// CompareVersions replicates Python tuple-of-strings comparison
// (parse_version(a) > parse_version(b)): element-wise lexical string
// comparison, and if one tuple is a prefix of the other, the shorter one
// compares as smaller. Returns -1, 0, or 1.
//
// This reproduces the real (non-numeric) Python bug where '10' < '9'
// lexically, so "2.10.0" compares as OLDER than "2.9.5.5". cli.go's
// --check-update must use this, not a numeric comparison, for parity.
func CompareVersions(a, b string) int {
	pa, pb := ParseVersion(a), ParseVersion(b)
	n := len(pa)
	if len(pb) < n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(pa) < len(pb):
		return -1
	case len(pa) > len(pb):
		return 1
	default:
		return 0
	}
}
