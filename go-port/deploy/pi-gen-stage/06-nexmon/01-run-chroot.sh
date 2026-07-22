#!/bin/bash -e
# Gives the onboard Pi Zero 2 W chip real monitor-mode + frame injection,
# closing the gap deploy/README.md's "WiFi monitor mode / nexmon"
# section previously disclosed as not attempted. Ported from the
# original (pre-Go-port) pwnagotchi image's
# stage3/04-nexmon/01-run-chroot.sh — see git history at commit a15ae8fc
# (deleted in b77965a0, "Make the Go port the primary implementation").
#
# Real, confirmed original approach: install prebuilt nexmon-patched
# firmware plus a DKMS-packaged nexmon-patched brcmfmac kernel module,
# both maintained as .debs by Kali (not built from nexmon source here —
# that was true of the original too), after removing the stock
# non-nexmon firmware package pi-gen's stage2 installs by default
# (stage2/02-net-tweaks pulls in firmware-brcm80211).
#
# The original uname-based-kernel-targeting risk this comment used to
# flag turned out NOT to be the problem — confirmed via a real build:
# DKMS correctly detected and targeted the actual installed
# 6.18.34+rpt-rpi-2712/rpi-v8 kernels (not the build host's), so its
# kernel detection does not simply trust the chroot's own `uname -r`.
#
# The real, current, CONFIRMED failure instead: the module build itself
# fails to compile ("Building module(s)........(bad exit status: 2)").
# make itself only prints a one-line summary to the install log — the
# actual compiler error is in DKMS's own make.log, which normally never
# leaves the chroot before it's torn down on failure. Dumped below on
# failure so the real error is visible in CI output instead of just the
# uninformative summary. This kernel (6.18.34) is considerably newer
# than nexmon upstream's typically-tested kernel range, and nexmon's
# patches touch driver internals that are known to be kernel-API-version
# sensitive — a genuine version incompatibility, not a config problem,
# is a real possibility here and not yet ruled out.
cd /tmp

curl -LO https://kali.download/kali/pool/non-free-firmware/f/firmware-nexmon/firmware-nexmon_0.2_all.deb
curl -LO https://http.kali.org/kali/pool/contrib/b/brcmfmac-nexmon-dkms/brcmfmac-nexmon-dkms_6.12.2_all.deb

apt-get remove -y firmware-brcm80211

if ! dpkg -i firmware-nexmon_0.2_all.deb brcmfmac-nexmon-dkms_6.12.2_all.deb; then
  echo "=== brcmfmac-nexmon-dkms build failed; dumping DKMS make.log(s) ==="
  find /var/lib/dkms -name make.log -exec echo "--- {} ---" \; -exec cat {} \;
  exit 1
fi

rm -f firmware-nexmon_0.2_all.deb brcmfmac-nexmon-dkms_6.12.2_all.deb
