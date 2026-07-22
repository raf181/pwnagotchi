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
