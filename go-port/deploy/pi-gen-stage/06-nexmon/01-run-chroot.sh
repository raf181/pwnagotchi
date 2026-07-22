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
# flag was confirmed NOT to be the problem — a real build showed DKMS
# correctly detects and targets the actual installed kernel headers
# (not the build host's own uname -r).
#
# The real, CONFIRMED, now-FIXED failure: the DKMS module build failed
# to compile against trixie's default kernel (1:6.18.34-1+rpt1) —
# del_timer_sync/from_timer implicit-declaration errors and
# cfg80211_ops incompatible-pointer-type errors, i.e. a genuine kernel
# API version mismatch, not a config problem. Kali's own blog
# (kali.org/blog/raspberry-pi-wi-fi-glow-up) confirms
# brcmfmac-nexmon-dkms was specifically validated against the 6.12
# kernel line (they were stuck on 5.15 for over a year because 6.6
# broke nexmon, and only 6.12 was confirmed stable) — hence
# 05a-pin-kernel, which runs before this stage and pins the kernel to
# archive.raspberrypi.com's bookworm suite (1:6.12.93-1+rpt1 as of this
# writing) instead of trixie's. See
# docs/kernel-nexmon-compatibility.md for the full evidence trail.
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

# Verify the module actually exists for the pinned kernel and is
# loadable metadata-wise — do not just trust dpkg's exit code. modinfo
# is given the .ko path directly (not -k/module-name, which would fall
# back to this chroot's own uname -r, i.e. the build host's unrelated
# kernel) so this check is meaningful under QEMU-chroot too.
echo "=== dkms status (evidence) ==="
dkms status

TARGET_KVER="$(dpkg-query -W -f='${Package}\n' 'linux-image-6.12.*+rpt-rpi-v8' | head -1 | sed 's/^linux-image-//')"
if [ -z "$TARGET_KVER" ]; then
  echo "FATAL: could not determine the pinned kernel version to verify the built module against." >&2
  exit 1
fi

depmod -a "$TARGET_KVER"

MODULE_PATH="$(find "/lib/modules/${TARGET_KVER}" -name 'brcmfmac.ko*' 2>/dev/null | head -1)"
if [ -z "$MODULE_PATH" ]; then
  echo "FATAL: no brcmfmac.ko found under /lib/modules/${TARGET_KVER} after a reported-successful DKMS install." >&2
  find "/lib/modules/${TARGET_KVER}" -iname "*brcmfmac*" 2>&1
  exit 1
fi

echo "=== modinfo for the built module (evidence) ==="
modinfo "$MODULE_PATH"

if ! modinfo "$MODULE_PATH" | grep -qi nexmon; then
  echo "FATAL: ${MODULE_PATH} does not identify itself as nexmon-patched (no 'nexmon' string in modinfo output) — this may be the stock module, not the patched one." >&2
  exit 1
fi

echo "Confirmed: nexmon-patched brcmfmac.ko built for ${TARGET_KVER} at ${MODULE_PATH}"
