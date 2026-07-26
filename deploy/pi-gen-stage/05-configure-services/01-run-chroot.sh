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

# REAL, CONFIRMED-ON-HARDWARE bug: this image has no HDMI/keyboard on
# the Pi Zero 2 W's single USB-OTG port (already used for gadget
# networking), yet two independent interactive first-boot wizards were
# still enabled and blocked boot indefinitely waiting for console
# input that could never arrive:
#   - userconfig.service (raspberrypi-sys-mods' userconf-pi) prompts to
#     create a user account. It's gated on /boot/firmware/userconf.txt
#     existing, NOT on whether pi-gen's own FIRST_USER_NAME/PASS already
#     created one via chpasswd at build time (confirmed: the "pi" user's
#     /etc/shadow entry already has a real password hash from the build,
#     yet this still prompted on real hardware) — the two mechanisms are
#     independent, so setting FIRST_USER_PASS in pi-gen/config alone
#     does not suppress it.
#   - systemd-firstboot.service (--prompt-locale --prompt-keymap
#     --prompt-timezone --prompt-root-password) is gated on
#     ConditionFirstBoot=yes, which evaluates true on real hardware
#     since a fresh /etc/machine-id gets generated on first real boot
#     (confirmed via forensic inspection of a hung card). Locale/keymap/
#     timezone are already set at build time via pi-gen's own
#     LOCALE_DEFAULT/KEYBOARD_KEYMAP/TIMEZONE_DEFAULT, so this unit has
#     nothing left to usefully prompt for anyway.
#
# REAL BUG in a first attempt at this fix: plain `systemctl disable
# userconfig.service` here was silently undone later in the same build
# — confirmed via build log evidence: userconf-pi's package gets
# (re)processed by dpkg AFTER this script runs (triggered by the later
# 05a-pin-kernel stage's own apt purge/install cycle firing dpkg
# triggers for already-installed packages), which re-creates the
# multi-user.target.wants symlink from the package's own default
# preset, undoing a plain disable. `mask` doesn't have this problem —
# it points the unit straight at /dev/null, and dpkg's own
# deb-systemd-helper explicitly skips re-enabling units it finds
# already masked — confirmed effective for systemd-firstboot.service
# below under the exact same later-stage conditions.
systemctl mask userconfig.service
systemctl mask systemd-firstboot.service

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

# USB gadget networking — REPLACES this pipeline's own earlier
# hand-rolled approach (single static-IP NetworkManager profile,
# modules-load=dwc2,g_ether + dwc2.lpm_enable=0 via cmdline.txt, and a
# custom 70-usb0.link rename rule) after that approach consistently
# never brought up carrier across many real Pi Zero 2 W boot attempts.
# This is instead ported EXACTLY from a real, working official
# jayofelony/pwnagotchi 2.9.5.4 release image, confirmed by SSHing into
# it live and inspecting its actual config on the same hardware/cable/
# host that the old approach failed on:
#   - dtoverlay=dwc2,dr_mode=peripheral in config.txt (unchanged, this
#     part was already correct and matches the official image)
#   - g_ether loaded via /etc/modules-load.d/usb-gadget.conf (see
#     00-run.sh), NOT cmdline.txt's modules-load= parameter — the
#     official image's cmdline.txt has neither modules-load=dwc2,g_ether
#     nor dwc2.lpm_enable=0 at all
#   - two NetworkManager profiles (usb0-client.nmconnection,
#     usb0-shared.nmconnection, see 00-run.sh) instead of one static
#     profile — the "shared" one runs NetworkManager's own dnsmasq to
#     hand the connecting host a real DHCP lease automatically
#   - NO custom .link rename rule: the official image only has the
#     stock 73-usb-net-by-mac.link (Debian/systemd upstream), yet its
#     gadget interface still shows up as "usb0" — that by-mac rename
#     rule apparently doesn't trigger the same way on the gadget/device
#     side as it does on a host observing an external USB-Ethernet
#     adapter, so the custom rename file this pipeline added earlier
#     was based on a misdiagnosis and is unnecessary
#
# REAL BUG FIXED HERE (still applies): a bare `>>` append lands
# wherever the file currently ENDS — and stock Raspberry Pi OS
# config.txt files end with a series of hardware-conditional sections
# (`[cm4]`, `[cm5]`, `[pi5]`, etc., each staying in effect until EOF or
# the next `[section]` line). If the base image's LAST section header
# happens to be one of those (very likely — they're appended in
# Pi-model release order), a plain `>> config.txt` append lands INSIDE
# that section, i.e. our dtoverlay would apply ONLY on a Pi 5/CM4/CM5
# and silently never take effect on the Pi Zero 2 W this image actually
# targets. Real Raspberry Pi Foundation-documented fix: force an
# `[all]` section marker first, which unconditionally re-opens the
# "applies to every model" scope regardless of whatever conditional
# section preceded it.
if ! grep -q "^dtoverlay=dwc2,dr_mode=peripheral$" /boot/firmware/config.txt; then
  printf '\n[all]\ndtoverlay=dwc2,dr_mode=peripheral\n' >> /boot/firmware/config.txt
fi
if ! grep -q "^dtparam=i2c_arm=on$" /boot/firmware/config.txt; then
  printf '\n[all]\ndtparam=i2c_arm=on\n' >> /boot/firmware/config.txt
fi
