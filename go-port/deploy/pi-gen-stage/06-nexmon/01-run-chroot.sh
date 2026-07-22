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
# KNOWN, UNVERIFIED RISK — disclosed, not silently assumed to work: this
# script runs inside a QEMU-user-emulated arm64 chroot on the x86_64
# build host, not on real booted Raspberry Pi hardware. DKMS's default
# kernel-version targeting uses `uname -r`, which inside this chroot
# reports the BUILD HOST's own kernel (an unrelated GitHub Actions
# runner kernel) — not the target's actual linux-image-rpi-v8/rpi-2712
# kernel that pi-gen's stage0 installs (whose matching
# linux-headers-rpi-v8/linux-headers-rpi-2712 packages are present in
# this chroot for exactly this reason). Whether
# brcmfmac-nexmon-dkms's postinst correctly targets those installed
# headers instead of blindly trusting uname -r is NOT verified here —
# that is a real open question the next build+boot test on real
# hardware needs to answer, not a guarantee this comment is asserting.
cd /tmp

curl -LO https://kali.download/kali/pool/non-free-firmware/f/firmware-nexmon/firmware-nexmon_0.2_all.deb
curl -LO https://http.kali.org/kali/pool/contrib/b/brcmfmac-nexmon-dkms/brcmfmac-nexmon-dkms_6.12.2_all.deb

apt-get remove -y firmware-brcm80211

dpkg -i firmware-nexmon_0.2_all.deb brcmfmac-nexmon-dkms_6.12.2_all.deb

rm -f firmware-nexmon_0.2_all.deb brcmfmac-nexmon-dkms_6.12.2_all.deb
