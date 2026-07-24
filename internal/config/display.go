package config

import "strings"

// DefaultDisplayType is the fallback normalized type when nothing in
// displayAliasTable matches, mirroring the trailing `else:` branch of
// utils.load_config (which also logs
// "using dummy display, as your display type is unsupported").
const DefaultDisplayType = "dummydisplay"

// NormalizeDisplayType mirrors the ~100-branch if/elif chain at the end of
// utils.load_config that canonicalizes config['ui']['display']['type'].
// Table entries are evaluated in the same order as the Python source
// (see displaytable_gen.go, generated from testdata/display_type_alias_table.json).
func NormalizeDisplayType(raw string) string {
	for _, e := range displayAliasTable {
		if e.exactSet {
			for _, alias := range e.aliasesOrLiteral {
				if raw == alias {
					return e.normalizedType
				}
			}
		} else {
			// Python bug: `raw in 'literal'` is a substring test, so this
			// matches whenever raw appears anywhere inside the literal
			// (including raw == "" always matching).
			if strings.Contains(e.aliasesOrLiteral[0], raw) {
				return e.normalizedType
			}
		}
	}
	return DefaultDisplayType
}
