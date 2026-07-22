#!/bin/bash -e
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
