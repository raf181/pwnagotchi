#!/usr/bin/env python3
"""Regenerates internal/ui/display/is_methods_gen.go from
testdata/hw_is_methods.json, itself extracted from the real
pwnagotchi/ui/display.py's is_X()/gfxhat() methods via regex. Run from
go-port/.

Each entry is (python_method_name, name_string_compared_against). Several
of these comparisons are REAL Python bugs (the compared string doesn't
match any actual driver's self.name, e.g. is_dfrobot_v1 compares against
"dfrobot_v1" but the real driver's name is "dfrobot_1") — replicated
exactly, not fixed. See docs/known-differences.md.
"""
import json
import os
import re

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def go_method_name(py_method: str) -> str:
    if py_method == "gfxhat":
        # Python's own method is named `gfxhat`, not `is_gfxhat` — a typo
        # preserved exactly, including in the Go method name.
        return "Gfxhat"
    rest = py_method[len("is_"):]
    # CamelCase every underscore-separated segment (e.g. "dummy_display"
    # -> "DummyDisplay", "dfrobot_v1" -> "DfrobotV1"), preserving any
    # existing internal capitalization within a segment (e.g. "1in54V2").
    segments = rest.split("_")
    camel = "".join(seg[0].upper() + seg[1:] if seg else "" for seg in segments)
    return "Is" + camel


def main():
    with open(os.path.join(ROOT, "testdata", "hw_is_methods.json")) as f:
        entries = json.load(f)

    lines = [
        "// Code generated from testdata/hw_is_methods.json by",
        "// scripts/gen_display_is_methods.py; DO NOT EDIT by hand — regenerate instead.",
        "//",
        "// Ports pwnagotchi/ui/display.py's ~93 is_X()/gfxhat() driver-name-check",
        "// methods. Several compare against a string that does not match ANY real",
        "// driver's self.name — verified against the real Python source — and are",
        "// therefore always false in both Python and here; see",
        "// docs/known-differences.md for the specific cases (is_dfrobot_v1,",
        "// is_dfrobot_v2, is_weact2in9, is_dummy_display).",
        "package display",
        "",
    ]
    seen = set()
    for e in entries:
        go_name = go_method_name(e["method"])
        if go_name in seen:
            continue
        seen.add(go_name)
        lines.append(f"// {go_name} ports display.py's {e['method']}().")
        lines.append(f"func (d *Display) {go_name}() bool {{ return d.implName() == {json.dumps(e['type'])} }}")
        lines.append("")

    out_path = os.path.join(ROOT, "internal", "ui", "display", "is_methods_gen.go")
    with open(out_path, "w") as f:
        f.write("\n".join(lines) + "\n")
    print(f"wrote {len(seen)} is_X methods to {out_path}")


if __name__ == "__main__":
    main()
