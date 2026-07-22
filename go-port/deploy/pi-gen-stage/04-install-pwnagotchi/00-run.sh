#!/bin/bash -e
# Runs on the BUILD HOST (not inside the chroot) — copies the real
# pwnagotchi Python package source and the pre-cross-compiled Go binary
# into the target rootfs, for 01-run-chroot.sh to install.
#
# PWNAGOTCHI_REPO_DIR and PWNAGOTCHI_GO_BINARY are set by
# .github/workflows/build-pi-image.yml before invoking pi-gen's build.sh;
# see that workflow for exactly what they point at.
: "${PWNAGOTCHI_REPO_DIR:?PWNAGOTCHI_REPO_DIR must point at the repo checkout (contains pwnagotchi/ and pyproject.toml)}"
: "${PWNAGOTCHI_GO_BINARY:?PWNAGOTCHI_GO_BINARY must point at the pre-built linux/arm64 pwnagotchi-go binary}"

install -d "${ROOTFS_DIR}/opt/pwnagotchi-src"
cp -r "${PWNAGOTCHI_REPO_DIR}/pwnagotchi" "${ROOTFS_DIR}/opt/pwnagotchi-src/pwnagotchi"
cp "${PWNAGOTCHI_REPO_DIR}/pyproject.toml" "${ROOTFS_DIR}/opt/pwnagotchi-src/pyproject.toml"
# pwnagotchi-src is a real installable Python package (pyproject.toml
# declares it); pip needs SOME setup/build backend metadata present —
# pyproject.toml alone is sufficient for a PEP 517 build, no setup.py
# needed.

install -m 755 "${PWNAGOTCHI_GO_BINARY}" "${ROOTFS_DIR}/opt/pwnagotchi-go"
