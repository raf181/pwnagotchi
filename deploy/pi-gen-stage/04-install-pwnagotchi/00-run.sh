#!/bin/bash -e
# Runs on the BUILD HOST (not inside the chroot) — copies the
# pre-cross-compiled Go binary into the target rootfs, for
# 01-run-chroot.sh to install. No Python source tree is copied anymore:
# the daemon is a single, statically linked (CGO_ENABLED=0) Go binary
# with fonts/locale catalogs embedded via go:embed (see
# internal/ui/fonts, internal/voice) — there is no
# separate Python package to install alongside it.
#
# PWNAGOTCHI_GO_BINARY is set by .github/workflows/build-pi-image.yml
# before invoking pi-gen's build.sh; see that workflow for exactly what
# it points at.
: "${PWNAGOTCHI_GO_BINARY:?PWNAGOTCHI_GO_BINARY must point at the pre-built linux/arm64 pwnagotchi-go binary}"

install -m 755 "${PWNAGOTCHI_GO_BINARY}" "${ROOTFS_DIR}/opt/pwnagotchi-go"
