#!/bin/bash -e
# Installs the real pwnagotchi Go binary. No Python, no pip, no PEP 517
# build, no dist-package/apt-vs-pip dependency-conflict handling — none
# of that machinery exists anymore now that every bundled plugin (and
# the daemon itself) is native Go, compiled directly into this one
# binary (see cmd/pwnagotchi/main.go's registerNativePlugins).
install -m 755 /opt/pwnagotchi-go /usr/bin/pwnagotchi-go
ln -sf /usr/bin/pwnagotchi-go /usr/bin/pwnagotchi
