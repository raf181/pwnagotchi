#!/usr/bin/env python3
"""Regenerates internal/config/displaytable_gen.go from
testdata/display_type_alias_table.json (itself hand-extracted from the
if/elif chain in pwnagotchi/utils.py:load_config). Run from go-port/.
"""
import json
import os

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def main():
    with open(os.path.join(ROOT, "testdata", "display_type_alias_table.json")) as f:
        table = json.load(f)

    lines = [
        "// Code generated from testdata/display_type_alias_table.json by",
        "// scripts/gen_display_table.py; DO NOT EDIT by hand — regenerate instead.",
        "package config",
        "",
        "// displayAliasEntry is one branch of utils.load_config's ~100-way",
        "// if/elif chain that normalizes config['ui']['display']['type'].",
        "type displayAliasEntry struct {",
        "\t// exactSet: true membership test (elif type in (a, b, c)).",
        "\t// false (substringOfLiteral): Python's real bug where a single-element",
        "\t// parenthesized string with no trailing comma, e.g. `in ('whisplay')`",
        "\t// written as `in 'whisplay'`, is a SUBSTRING test against that literal,",
        "\t// not a set-membership test. Preserved intentionally — see",
        "\t// docs/known-differences.md.",
        "\texactSet bool",
        "\taliasesOrLiteral []string",
        "\tnormalizedType   string",
        "}",
        "",
        "var displayAliasTable = []displayAliasEntry{",
    ]
    for entry in table:
        exact = "true" if entry["match_mode"] == "exact_set" else "false"
        aliases = ", ".join(json.dumps(a) for a in entry["aliases_or_literal"])
        lines.append(
            f'\t{{exactSet: {exact}, aliasesOrLiteral: []string{{{aliases}}}, normalizedType: {json.dumps(entry["normalized_type"])}}},'
        )
    lines.append("}")
    lines.append("")

    out_path = os.path.join(ROOT, "internal", "config", "displaytable_gen.go")
    with open(out_path, "w") as f:
        f.write("\n".join(lines) + "\n")
    print(f"wrote {len(table)} entries to {out_path}")


if __name__ == "__main__":
    main()
