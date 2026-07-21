#!/usr/bin/env python3
"""Regenerates internal/ui/hw/layouts_gen.go from testdata/hw_layouts.json,
itself extracted (via Python's ast module, not regex — see
scripts/gen_hw_layouts_extract.py for the extraction logic recorded in the
session history) from every real pwnagotchi/ui/hw/*.py driver's layout()
method. layout() is pure data (widget positions, canvas size, font sizes) —
no hardware I/O — so it is ported for every driver, even though
Initialize/Render/Clear remain Interface-only pending real hardware.
Run from go-port/.
"""
import json
import os

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def pt(v):
    if v is None:
        return "0, 0"
    return f"{int(v[0])}, {int(v[1])}"


def rect4(v):
    if v is None:
        return "0, 0, 0, 0"
    return ", ".join(str(int(x)) for x in v)


def main():
    with open(os.path.join(ROOT, "testdata", "hw_layouts.json")) as f:
        layouts = json.load(f)

    lines = [
        "// Code generated from testdata/hw_layouts.json by",
        "// scripts/gen_hw_layouts.py; DO NOT EDIT by hand — regenerate instead.",
        "//",
        "// Each entry is the REAL layout() data (widget positions, canvas",
        "// dimensions, font sizes) extracted from the corresponding real",
        "// pwnagotchi/ui/hw/*.py driver via Python's ast module. layout() has no",
        "// hardware I/O in Python either — it's pure data — so unlike",
        "// Initialize/Render/Clear, this is real ported behavior, not a stub.",
        "package hw",
        "",
        "type layoutData struct {",
        "\tWidth, Height                                          int",
        "\tFace, Name, Channel, APs, Uptime                       [2]int",
        "\tFriendFace, FriendName, Shakes, Mode                   [2]int",
        "\tLine1, Line2                                           [4]int",
        "\tStatusPos                                              [2]int",
        "\tStatusMax                                               int",
        "\t// StatusFontBase is which of fonts.Set.Small/Medium the driver",
        "\t// passes to fonts.status_font() as the base size (only these two",
        "\t// occur across every real driver — verified exhaustively).",
        "\tStatusFontBase string",
        "\t// FontsSetup is (bold, boldSmall, medium, huge, boldBig, small),",
        "\t// the exact args each driver's layout() passes to fonts.setup().",
        "\tFontsSetup [6]int",
        "}",
        "",
        "var layoutTable = map[string]layoutData{",
    ]

    for type_str, d in layouts.items():
        if type_str in ("dummydisplay", "weact2in9"):
            continue  # handled by real Go code / ErrNoDriverInPythonEither

        fs = d.get("_fonts_setup") or [10, 8, 10, 25, 25, 9]
        status = d.get("status") or {}
        entry = (
            f'\t{json.dumps(type_str)}: {{\n'
            f'\t\tWidth: {int(d["width"]) if d.get("width") is not None else 0}, '
            f'Height: {int(d["height"]) if d.get("height") is not None else 0},\n'
            f'\t\tFace: [2]int{{{pt(d.get("face"))}}}, Name: [2]int{{{pt(d.get("name"))}}}, '
            f'Channel: [2]int{{{pt(d.get("channel"))}}}, APs: [2]int{{{pt(d.get("aps"))}}}, '
            f'Uptime: [2]int{{{pt(d.get("uptime"))}}},\n'
            f'\t\tFriendFace: [2]int{{{pt(d.get("friend_face"))}}}, '
            f'FriendName: [2]int{{{pt(d.get("friend_name"))}}}, '
            f'Shakes: [2]int{{{pt(d.get("shakes"))}}}, Mode: [2]int{{{pt(d.get("mode"))}}},\n'
            f'\t\tLine1: [4]int{{{rect4(d.get("line1"))}}}, '
            f'Line2: [4]int{{{rect4(d.get("line2"))}}},\n'
            f'\t\tStatusPos: [2]int{{{pt(status.get("pos"))}}}, StatusMax: {int(status.get("max") or 20)},\n'
            f'\t\tStatusFontBase: {json.dumps(d.get("_status_font_base") or "Medium")},\n'
            f'\t\tFontsSetup: [6]int{{{", ".join(str(int(x)) for x in fs)}}},\n'
            f'\t}},'
        )
        lines.append(entry)

    lines.append("}")
    lines.append("")

    out_path = os.path.join(ROOT, "internal", "ui", "hw", "layouts_gen.go")
    with open(out_path, "w") as f:
        f.write("\n".join(lines) + "\n")
    print(f"wrote {len(layouts) - 2} layout entries to {out_path}")


if __name__ == "__main__":
    main()
