#!/usr/bin/env python3
"""Loads every bundled plugin through the real pwnagotchi.plugins loader
inside the built image's own chroot — no Pi hardware needed, just the
real Python package that's actually installed in the image. Used by the
CI image-validation step (see .github/workflows/build-pi-image.yml)."""
import glob
import os
import sys

sys.path.insert(0, "/opt/pwnagotchi-src")
import pwnagotchi.plugins as plugins  # noqa: E402

cfg = {"main": {"plugins": {}, "custom_plugins": None}}
patterns = [
    "/usr/lib/python3*/dist-packages/pwnagotchi/plugins/default/*.py",
    "/usr/local/lib/python3*/dist-packages/pwnagotchi/plugins/default/*.py",
]
found = []
for pattern in patterns:
    found.extend(glob.glob(pattern))

for f in found:
    name = os.path.basename(f)[:-3]
    if name in ("__init__", "example"):
        continue
    cfg["main"]["plugins"][name] = {"enabled": False}

loaded = plugins.load(cfg)
print("LOADED_COUNT=%d" % len(loaded))
for name in sorted(loaded.keys()):
    print("loaded:", name)
