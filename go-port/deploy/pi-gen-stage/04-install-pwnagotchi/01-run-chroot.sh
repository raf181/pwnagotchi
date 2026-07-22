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
#
# Real, confirmed failure: one of pwnagotchi's own deps (requests) pulls
# in a newer version than the one apt already installed as a
# dist-package (/usr/lib/python3/dist-packages, no pip RECORD file since
# dpkg owns it, not pip). pip's normal upgrade path tries to uninstall
# the old one first and fails with "uninstall-no-record-file" since it
# can't account for dpkg-owned files.
#
# A blanket `pip install --ignore-installed` "fixes" this but is too
# broad: it was tried and confirmed to also make pip distrust
# already-satisfied deps like dbus-python (already provided by the
# python3-dbus apt package below), forcing a pointless from-source
# rebuild that then fails needing libglib2.0-dev/meson we don't install.
# Instead, remove only the actual conflicting apt package first so pip's
# own upgrade path (no special flag needed) has a clean RECORD-tracked
# install to replace.
apt-get remove -y --purge python3-requests
pip3 install --break-system-packages --no-cache-dir /opt/pwnagotchi-src

install -m 755 /opt/pwnagotchi-go /usr/bin/pwnagotchi-go
