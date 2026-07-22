#!/bin/bash -e
# Runs on the BUILD HOST — copies launcher scripts and systemd unit
# files into the target rootfs (installing them is simple file copying;
# only enabling them needs the real chroot, done in 01-run-chroot.sh).
: "${PWNAGOTCHI_REPO_DIR:?PWNAGOTCHI_REPO_DIR must point at the repo checkout}"
DEPLOY_DIR="${PWNAGOTCHI_REPO_DIR}/go-port/deploy"

install -m 755 "${DEPLOY_DIR}/scripts/pwnlib" "${ROOTFS_DIR}/usr/bin/pwnlib"
install -m 755 "${DEPLOY_DIR}/scripts/pwnagotchi-launcher" "${ROOTFS_DIR}/usr/bin/pwnagotchi-launcher"
install -m 755 "${DEPLOY_DIR}/scripts/bettercap-launcher" "${ROOTFS_DIR}/usr/bin/bettercap-launcher"
install -m 755 "${DEPLOY_DIR}/scripts/monstart" "${ROOTFS_DIR}/usr/bin/monstart"
install -m 755 "${DEPLOY_DIR}/scripts/monstop" "${ROOTFS_DIR}/usr/bin/monstop"
install -m 755 "${DEPLOY_DIR}/scripts/validate-nexmon-runtime" "${ROOTFS_DIR}/usr/local/bin/validate-nexmon-runtime"

install -m 644 "${DEPLOY_DIR}/systemd/pwnagotchi.service" "${ROOTFS_DIR}/etc/systemd/system/pwnagotchi.service"
install -m 644 "${DEPLOY_DIR}/systemd/bettercap.service" "${ROOTFS_DIR}/etc/systemd/system/bettercap.service"
install -m 644 "${DEPLOY_DIR}/systemd/pwngrid-peer.service" "${ROOTFS_DIR}/etc/systemd/system/pwngrid-peer.service"

# Real, confirmed-on-hardware bug: NetworkManager manages wlan0 by
# default on this OS release and keeps an open handle on it even while
# disconnected/"unavailable" — this alone was enough to make
# pwnlib's reload_brcm fail with "Module brcmfmac is in use" on every
# single boot, before monitor mode setup ever got a chance to run.
install -d "${ROOTFS_DIR}/etc/NetworkManager/conf.d"
install -m 644 "${DEPLOY_DIR}/network-manager/99-unmanaged-wlan0.conf" \
  "${ROOTFS_DIR}/etc/NetworkManager/conf.d/99-unmanaged-wlan0.conf"

install -d "${ROOTFS_DIR}/etc/pwnagotchi/log"
install -d "${ROOTFS_DIR}/etc/pwnagotchi/handshakes"
install -d "${ROOTFS_DIR}/etc/pwnagotchi/conf.d"

# internal/config.LoadConfig's own real default.toml/config.toml
# convention (see go-port/cmd/pwnagotchi/main.go) — default.toml is the
# embedded pwnagotchi.toml/defaults.toml written out on first run if
# missing, config.toml is the empty user-override file an operator
# actually edits. Ship an empty user config, not a copy of the defaults,
# so a future default.toml change isn't shadowed by a stale baked-in
# copy — matching how a fresh real install behaves today (first real run
# creates default.toml itself; see internal/config.LoadConfig).
touch "${ROOTFS_DIR}/etc/pwnagotchi/config.toml"
