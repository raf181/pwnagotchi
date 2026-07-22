#!/bin/bash -e
# Installs the real, unmodified bettercap caplets (including
# pwnagotchi-auto.cap / pwnagotchi-manual.cap, which bettercap-launcher
# invokes) from the official community repo — verified this is where
# they actually come from: this dev environment's own
# /usr/local/share/bettercap/caplets/ has the same LICENSE.md/Makefile
# structure as github.com/bettercap/caplets, not something bundled with
# bettercap itself.
CAPLETS_REF="master"

cd /tmp
git clone --branch "${CAPLETS_REF}" --depth 1 https://github.com/bettercap/caplets.git
cd caplets
make install
cd /tmp
rm -rf caplets
