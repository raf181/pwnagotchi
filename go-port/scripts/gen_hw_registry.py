#!/usr/bin/env python3
"""Regenerates internal/ui/hw/registry_gen.go from
testdata/hw_driver_registry.json, itself extracted from the real
pwnagotchi/ui/hw/__init__.py dispatch table via a regex (see the session
transcript / git history for the extraction script). Run from go-port/.
"""
import json
import os

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def main():
    with open(os.path.join(ROOT, "testdata", "hw_driver_registry.json")) as f:
        entries = json.load(f)

    lines = [
        "// Code generated from testdata/hw_driver_registry.json by",
        "// scripts/gen_hw_registry.py; DO NOT EDIT by hand — regenerate instead.",
        "//",
        "// This is the Go equivalent of pwnagotchi/ui/hw/__init__.py's",
        "// display_for() dispatch table: one entry per real hardware driver",
        "// type string. Every entry except \"dummydisplay\" currently resolves to",
        "// an unsupportedDriver (a real, typed \"not implemented on this",
        "// hardware/build yet\" error on every operation) — see driver.go's",
        "// package doc for why that's the honest status, not a placeholder.",
        "package hw",
        "",
        "// pythonClassNames maps each config alias to a driver's original",
        "// Python class name, purely for diagnostic error text (\"<ClassName>:",
        "// this display driver has a real Go interface but no native",
        "// implementation yet\").",
        "var pythonClassNames = map[string]string{",
    ]
    for e in entries:
        lines.append(f'\t{json.dumps(e["type"])}: {json.dumps(e["class"])},')
    lines.append("}")
    lines.append("")
    lines.append("// registeredTypes lists every display type string NewDriver accepts,")
    lines.append("// in the same order as the original Python if/elif chain.")
    lines.append("var registeredTypes = []string{")
    for e in entries:
        lines.append(f'\t{json.dumps(e["type"])},')
    lines.append('\t"dummydisplay",')
    lines.append("}")
    lines.append("")

    out_path = os.path.join(ROOT, "internal", "ui", "hw", "registry_gen.go")
    with open(out_path, "w") as f:
        f.write("\n".join(lines) + "\n")
    print(f"wrote {len(entries)} driver entries to {out_path}")


if __name__ == "__main__":
    main()
