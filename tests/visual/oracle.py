#!/usr/bin/env python3
"""Real-Python visual oracle: builds a real pwnagotchi.ui.view.View driven
by a fake DisplayImpl whose layout() returns the exact real WaveshareV4
(config type "waveshare_4", the default hardware: 250x122) layout dict,
sets it to a fixed, deterministic state via the real update(new_data=...)
path, and saves the real resulting canvas (mode '1' PIL Image, produced by
the genuine Text/LabeledValue/Line.draw() call chain) as a PNG.

This is the ground truth for the Go visual regression tests in
tests/visual/. Run with the repo's venv:
  PWNAGOTCHI_REPO_ROOT=/root/pwnagotchi ../../venv/bin/python3 oracle.py out.png
(PWNAGOTCHI_REPO_ROOT defaults to "../.." — this script's own repo-relative
location — when unset, matching go-port/Makefile's compatibility-test
target convention for every other tests/compat_*_test.go.)
"""
import sys
import os

sys.path.insert(0, os.environ.get(
    "PWNAGOTCHI_REPO_ROOT",
    os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."),
))

import pwnagotchi.ui.fonts as fonts
import pwnagotchi.ui.faces as faces
from pwnagotchi.ui.view import View


class FakeImpl:
    """Same layout() body as pwnagotchi/ui/hw/waveshare2in13_V4.py's
    WaveshareV4.layout(), copied verbatim (not reimplemented) so this
    oracle exercises the exact real layout data real hardware selection
    would produce for config['ui']['display']['type'] = 'waveshare_4'."""

    def __init__(self):
        self._layout = {}

    def layout(self):
        fonts.setup(10, 9, 10, 35, 25, 9)
        self._layout['width'] = 250
        self._layout['height'] = 122
        self._layout['face'] = (0, 40)
        self._layout['name'] = (5, 20)
        self._layout['channel'] = (0, 0)
        self._layout['aps'] = (28, 0)
        self._layout['uptime'] = (185, 0)
        self._layout['line1'] = [0, 14, 250, 14]
        self._layout['line2'] = [0, 108, 250, 108]
        self._layout['friend_face'] = (0, 92)
        self._layout['friend_name'] = (40, 94)
        self._layout['shakes'] = (0, 109)
        self._layout['mode'] = (225, 109)
        self._layout['status'] = {
            'pos': (125, 20),
            'font': fonts.status_font(fonts.Medium),
            'max': 20,
        }
        return self._layout

    def initialize(self):
        pass

    def render(self, canvas):
        pass

    def clear(self):
        pass


def build_config():
    return {
        'main': {'lang': 'en', 'plugins': {}},
        'ui': {
            'invert': False,
            'fps': 0.0,
            # No face-string keys here: real defaults.toml supplies those as
            # LISTS for random selection elsewhere (Agent._get_random_face),
            # and faces.load_from_config does a blind globals()[key.upper()]
            # = value for whatever keys ARE present, so omitting them keeps
            # faces.py's real plain-string module defaults (faces.HAPPY,
            # etc.) intact, matching Go's faces.Default().
            'faces': {'position_x': 0, 'position_y': 40, 'png': False},
            'font': {'name': 'DejaVuSansMono', 'size_offset': 0},
            'display': {'type': 'waveshare_4', 'rotation': 0},
        },
    }


def main():
    out_path = sys.argv[1]
    cfg = build_config()
    fonts.init(cfg)
    faces.load_from_config(cfg['ui']['faces'])
    impl = FakeImpl()
    v = View(cfg, impl)

    # Fixed, deterministic scripted state matching the Go golden test.
    v.update(force=True, new_data={
        'channel': '6',
        'aps': '3 (11)',
        'uptime': '00:12:34',
        'name': 'pwnagotchi>',
        'face': faces.HAPPY,
        'status': 'Hello world, this is a test of the emergency broadcast system.',
        'shakes': '2 (04)',
        'mode': 'AUTO',
    })

    v._canvas.save(out_path)
    print(f"saved {out_path} mode={v._canvas.mode} size={v._canvas.size}")


if __name__ == "__main__":
    main()
