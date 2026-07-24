#!/bin/bash -e
# Runs on the BUILD HOST — copies the bookworm kernel apt source + pin
# preference into the target rootfs (see 01-run-chroot.sh for why, and
# docs/kernel-nexmon-compatibility.md for the full evidence trail).
install -m 644 files/bookworm-kernel.sources \
  "${ROOTFS_DIR}/etc/apt/sources.list.d/bookworm-kernel.sources"
install -d "${ROOTFS_DIR}/etc/apt/preferences.d"
install -m 644 files/90-nexmon-kernel-pin \
  "${ROOTFS_DIR}/etc/apt/preferences.d/90-nexmon-kernel-pin"
