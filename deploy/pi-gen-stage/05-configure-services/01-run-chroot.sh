#!/bin/bash -e
# Real, confirmed-on-hardware bug: a standalone wpa_supplicant.service
# (independent of NetworkManager, running in its global -u/D-Bus mode)
# was ALSO holding the onboard radio, on top of NetworkManager managing
# wlan0 (see 00-run.sh's NetworkManager conf.d install above) — either
# one alone was enough to make reload_brcm's modprobe -r fail with
# "Module brcmfmac is in use" on every boot. Nothing on this image
# needs it: NetworkManager handles eth0, and pwnlib drives the wifi
# radio directly once masked out of the way.
systemctl mask wpa_supplicant.service

# REAL GAP FIXED HERE: nothing in this pipeline ever enabled the SSH
# server. Stock Raspberry Pi OS Lite images ship openssh-server
# installed but NOT started/enabled by default (a deliberate upstream
# security default, normally opened up by raspi-config or the
# /boot/firmware/ssh marker file during interactive setup — neither of
# which applies to an unattended pi-gen build like this one). Without
# this, the whole point of the USB gadget networking below — "plug in
# via USB, ssh pi@10.0.0.2" — silently doesn't work: the interface comes
# up with real connectivity, but nothing is listening on port 22.
# openssh-server itself is guaranteed present via this stage's own
# 00-packages (not merely assumed from an earlier stock stage).
systemctl enable ssh.service

systemctl daemon-reload
systemctl enable bettercap.service
systemctl enable pwngrid-peer.service
systemctl enable pwnagotchi.service

# Matches real pwnagotchi.defaults.toml's main.name = "pwnagotchi" — see
# docs/rendering-investigation.md / this session's own earlier finding
# that internal/unit.SetName reboots the unit the FIRST time it observes
# a real mismatch between /etc/hostname and the configured main.name.
# Setting it correctly here, once, at image-build time avoids that
# reboot happening (surprisingly, to a first-time operator) on the
# unit's very first real boot.
echo "pwnagotchi" > /etc/hostname
sed -i "s/127.0.1.1.*/127.0.1.1\tpwnagotchi/" /etc/hosts

# USB gadget networking (see 00-run.sh's usb0.nmconnection install) — the
# real original project's own approach, ported from the deleted
# builder's sdcard/boot/cmdline.txt (git show 0fdc2b6d:sdcard/boot/
# cmdline.txt): dtoverlay=dwc2 puts the USB controller into gadget/OTG
# mode, modules-load=dwc2,g_ether loads the actual Ethernet-gadget
# kernel module at boot. Neither was present in this image before —
# only [cm4]/[cm5]/[pi5]-conditional overlays existed in config.txt,
# none of which apply to the Pi Zero 2 W, so USB gadget networking
# never worked at all, independent of anything else.
#
# dr_mode=peripheral (not bare dtoverlay=dwc2, which leaves dr_mode on
# ID-pin auto-negotiation/OTG): real-hardware testing through a USB-C
# dock/hub (not a direct port) repeatedly showed the gadget enumerate
# correctly at the USB descriptor level but never assert carrier —
# consistent with OTG ID-pin sensing being ambiguous through an
# intermediary hub. Forcing peripheral mode explicitly removes that
# ambiguity. Not yet independently confirmed to be necessary on its own
# (tested together with dwc2.lpm_enable=0 below and the usb0 interface
# fixes in 00-run.sh); kept because it matches the deployment topology
# actually being tested against (laptop dock, not a bare port) and has
# no downside for a device-only Pi Zero 2 W gadget port.
#
# REAL BUG FIXED HERE: a bare `>>` append lands wherever the file
# currently ENDS — and stock Raspberry Pi OS config.txt files end with a
# series of hardware-conditional sections (`[cm4]`, `[cm5]`, `[pi5]`,
# etc., each staying in effect until EOF or the next `[section]` line).
# If the base image's LAST section header happens to be one of those
# (very likely — they're appended in Pi-model release order), a plain
# `>> config.txt` append lands INSIDE that section, i.e. our dtoverlay
# would apply ONLY on a Pi 5/CM4/CM5 and silently never take effect on
# the Pi Zero 2 W this image actually targets — a silent, totally
# non-obvious way for USB gadget networking to never work. Real
# Raspberry Pi Foundation-documented fix: force an `[all]` section
# marker first, which unconditionally re-opens the "applies to every
# model" scope regardless of whatever conditional section preceded it,
# and stays in effect until the next `[section]` (there is none after
# this, so it holds for everything we append below too).
if ! grep -q "^dtoverlay=dwc2,dr_mode=peripheral$" /boot/firmware/config.txt; then
  printf '\n[all]\ndtoverlay=dwc2,dr_mode=peripheral\n' >> /boot/firmware/config.txt
fi
# dwc_otg.lpm_enable=0 (dwc2 on this modern kernel, dwc_otg was the
# legacy driver name) — present in the real original project's own
# cmdline.txt (git show 0fdc2b6d:sdcard/boot/cmdline.txt) and never
# ported over until now. Disables USB Link Power Management, a
# well-documented Raspberry Pi gadget-mode fix for exactly the symptom
# seen here: the gadget enumerates but the link never stabilizes.
if ! grep -q "dwc2.lpm_enable=0" /boot/firmware/cmdline.txt; then
  sed -i "s/^/dwc2.lpm_enable=0 /" /boot/firmware/cmdline.txt
fi
if ! grep -q "modules-load=dwc2,g_ether" /boot/firmware/cmdline.txt; then
  sed -i "s/\$/ modules-load=dwc2,g_ether/" /boot/firmware/cmdline.txt
fi

# Real, confirmed-on-hardware bug: a stock systemd udev rule
# (/usr/lib/systemd/network/73-usb-net-by-mac.link, Debian/systemd
# upstream, not ours) renames ANY USB network device to a MAC-based
# name (matches Path=*-usb-*) before NetworkManager ever sees it —
# including g_ether's gadget interface, which the kernel initially
# names "usb0". Since usb0.nmconnection matches on the literal name
# "usb0" (see 00-run.sh), the by-mac rename meant that profile could
# never actually apply. A higher-priority (lower-numbered, sorts first)
# .link file pinning the gadget interface's name back to "usb0" fixes
# this. Matches on the g_ether driver specifically, not just any USB
# net device, so it doesn't affect the TP-Link USB-Ethernet adapter or
# anything else plugged into a real USB port.
install -d /etc/systemd/network
cat > /etc/systemd/network/70-usb0.link <<'EOF'
[Match]
Driver=g_ether

[Link]
Name=usb0
EOF
