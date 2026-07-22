#!/bin/bash -e
# Installs the real pwnagotchi Python package system-wide, so plain
# `python3` on PATH already has it — matching what
# internal/plugins.PythonInterpreter()'s own doc comment assumes for a
# real deployed unit ("PWNAGOTCHI_PYTHON is unset and plain 'python3'
# already has the real package installed system-wide"; only the dev repo
# needs the env var pointed at a venv). This is what internal/pyplugin's
# bridge runs the real bundled/custom plugins under — see
# docs/known-differences.md's bridge architecture notes.
#
# --break-system-packages: Debian Bookworm+ (PEP 668) blocks system-wide
# pip installs by default. This is a real system image being purpose-
# built for one job, not a general-purpose Python environment shared
# with unrelated tooling, so the usual "don't fight the OS package
# manager" reasoning PEP 668 exists for doesn't apply here.
pip3 install --break-system-packages --no-cache-dir /opt/pwnagotchi-src

install -m 755 /opt/pwnagotchi-go /usr/bin/pwnagotchi-go
