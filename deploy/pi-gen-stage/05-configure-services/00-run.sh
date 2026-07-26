#!/bin/bash -e
# Runs on the BUILD HOST — copies launcher scripts and systemd unit
# files into the target rootfs (installing them is simple file copying;
# only enabling them needs the real chroot, done in 01-run-chroot.sh).
: "${PWNAGOTCHI_REPO_DIR:?PWNAGOTCHI_REPO_DIR must point at the repo checkout}"
DEPLOY_DIR="${PWNAGOTCHI_REPO_DIR}/deploy"

install -m 755 "${DEPLOY_DIR}/scripts/pwnlib" "${ROOTFS_DIR}/usr/bin/pwnlib"
install -m 755 "${DEPLOY_DIR}/scripts/pwnagotchi-launcher" "${ROOTFS_DIR}/usr/bin/pwnagotchi-launcher"
install -m 755 "${DEPLOY_DIR}/scripts/bettercap-launcher" "${ROOTFS_DIR}/usr/bin/bettercap-launcher"
install -m 755 "${DEPLOY_DIR}/scripts/monstart" "${ROOTFS_DIR}/usr/bin/monstart"
install -m 755 "${DEPLOY_DIR}/scripts/monstop" "${ROOTFS_DIR}/usr/bin/monstop"
install -m 755 "${DEPLOY_DIR}/scripts/validate-nexmon-runtime" "${ROOTFS_DIR}/usr/local/bin/validate-nexmon-runtime"
install -m 755 "${DEPLOY_DIR}/scripts/check-kernel-upgrade-safety" "${ROOTFS_DIR}/usr/local/bin/check-kernel-upgrade-safety"

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

# USB gadget networking — matches the actual upstream jayofelony/
# pwnagotchi release image's own config exactly (confirmed by SSHing
# into a real official 2.9.5.4 release image and inspecting its live
# NetworkManager profiles/modules-load.d, after this pipeline's own
# earlier single-static-IP profile consistently failed to ever bring
# up carrier on real Pi Zero 2 W hardware across many real-hardware
# boot attempts, while the official image worked immediately with this
# exact setup on the same hardware/cable/host).
#
# Two profiles, not one: "client" (ipv4 method=auto, DHCP client,
# autoconnect=false) for when something else offers DHCP (e.g. a Pi4/5
# plugged into a router via its USB-C/Ethernet port), and "shared"
# (ipv4 method=shared, static 10.12.194.1/28, higher autoconnect
# priority so it's preferred) for the normal direct-USB-cable-to-a-PC
# case — NetworkManager's own "shared" method runs its own dnsmasq
# automatically to hand the connecting host a real DHCP lease, instead
# of requiring the host to already know to configure a specific
# hardcoded static IP/subnet to match. NetworkManager requires keyfile
# connections to be exactly 0600.
install -d "${ROOTFS_DIR}/etc/NetworkManager/system-connections"
install -m 600 "${DEPLOY_DIR}/network-manager/usb0-client.nmconnection" \
  "${ROOTFS_DIR}/etc/NetworkManager/system-connections/usb0-client.nmconnection"
install -m 600 "${DEPLOY_DIR}/network-manager/usb0-shared.nmconnection" \
  "${ROOTFS_DIR}/etc/NetworkManager/system-connections/usb0-shared.nmconnection"

# g_ether loaded via the modern modules-load.d mechanism, matching the
# official image exactly — NOT via a `modules-load=dwc2,g_ether` kernel
# cmdline.txt parameter (this pipeline's own earlier approach, which
# also force-added `dwc2.lpm_enable=0`; neither appears anywhere in the
# official image's cmdline.txt at all, confirmed by direct inspection).
install -d "${ROOTFS_DIR}/etc/modules-load.d"
install -m 644 "${DEPLOY_DIR}/modules-load.d/usb-gadget.conf" \
  "${ROOTFS_DIR}/etc/modules-load.d/usb-gadget.conf"

# Real, confirmed-on-hardware gap: this image shipped with swap
# effectively disabled (rpi-swap's own config commented out,
# zram-generator explicitly disabling zram0 unless configured) — see
# ../../rpi-swap/10-enable-zram.conf's own comment for why that's a
# real problem on a 512MB-RAM Pi Zero 2 W running bettercap, pwngrid,
# the Go daemon, and a freshly built nexmon module concurrently at boot.
install -d "${ROOTFS_DIR}/etc/rpi/swap.conf.d"
install -m 644 "${DEPLOY_DIR}/rpi-swap/10-enable-zram.conf" \
  "${ROOTFS_DIR}/etc/rpi/swap.conf.d/10-enable-zram.conf"

# REAL GAP FOUND: this pipeline's cloud.cfg (shipped by the
# cloud-init package itself, unmodified) is configured with
# `datasource_list: [ NoCloud, None ]` and
# `datasource: NoCloud: seedfrom: file:///boot/firmware` — but nothing
# in this pipeline ever wrote a NoCloud seed (user-data/meta-data/
# network-config) to /boot/firmware, leaving that configured seed
# location empty. Confirmed via a real, working official
# jayofelony/pwnagotchi 2.9.5.4 release image (extracted directly from
# its own boot partition, not a live-booted copy) that it ships these
# exact three files at /boot/firmware — their presence is very likely
# why cloud-init completes cleanly on the official image but
# cloud-init.target stalled indefinitely on every real-hardware boot
# attempt of this pipeline's own images before this fix (same
# nexmon/kernel-pin/services otherwise, same cable/host/hardware,
# only this was missing). Content matches the official image
# byte-for-byte (mostly commented-out boilerplate/examples — the only
# live directive is user-data's `rpi: enable_usb_gadget: true`, a
# Raspberry-Pi-specific cloud-init module).
install -m 644 "${DEPLOY_DIR}/cloud-init/user-data" \
  "${ROOTFS_DIR}/boot/firmware/user-data"
install -m 644 "${DEPLOY_DIR}/cloud-init/meta-data" \
  "${ROOTFS_DIR}/boot/firmware/meta-data"
install -m 644 "${DEPLOY_DIR}/cloud-init/network-config" \
  "${ROOTFS_DIR}/boot/firmware/network-config"

install -d "${ROOTFS_DIR}/etc/pwnagotchi/log"
install -d "${ROOTFS_DIR}/etc/pwnagotchi/handshakes"
install -d "${ROOTFS_DIR}/etc/pwnagotchi/conf.d"

# internal/config.LoadConfig's own real default.toml/config.toml
# convention (see cmd/pwnagotchi/main.go) — default.toml is the
# embedded pwnagotchi.toml/defaults.toml written out on first run if
# missing, config.toml is the empty user-override file an operator
# actually edits. Ship an empty user config, not a copy of the defaults,
# so a future default.toml change isn't shadowed by a stale baked-in
# copy — matching how a fresh real install behaves today (first real run
# creates default.toml itself; see internal/config.LoadConfig).
touch "${ROOTFS_DIR}/etc/pwnagotchi/config.toml"
