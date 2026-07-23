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
#
# Real, confirmed-on-hardware bug (same root cause, more packages than
# just python3-requests): requests hard-requires urllib3, certifi,
# charset_normalizer, idna (imports them directly, not optional); flask
# hard-requires blinker and jinja2 (which itself needs MarkupSafe). At
# the time pip ran, apt-provided python3-* packages satisfied ALL of
# these requirements, so pip skipped installing its own RECORD-tracked
# copies of any of them. The LATER export-image stage's `apt-get
# dist-upgrade --auto-remove --purge` (see pi-gen's export-image/
# 05-finalise) then removed every one of those apt packages as orphaned
# once python3-requests — their direct user, purged below — was gone,
# leaving 7 of the 8 default bundled plugins (every one except `cache`,
# which has no such dependency) failing with `No module named 'X'` at
# runtime. Confirmed via a real chroot+pip3 install against a booted
# card's rootfs: exactly these 6 packages (plus MarkupSafe, pulled in
# transitively by jinja2) were missing and nothing else was, after
# installing urllib3 and MarkupSafe surfaced the next layer of the same
# problem for flask's own hard dependencies.
apt-get remove -y --purge python3-requests python3-urllib3 python3-markupsafe \
  python3-certifi python3-charset-normalizer python3-idna python3-blinker python3-jinja2 2>/dev/null || true
pip3 install --break-system-packages --no-cache-dir /opt/pwnagotchi-src

install -m 755 /opt/pwnagotchi-go /usr/bin/pwnagotchi-go
