#!/bin/bash -e
# Pins the kernel + headers to Raspberry Pi Foundation's bookworm suite
# (1:6.12.93-1+rpt1 as of this writing) instead of trixie's default
# (1:6.18.34-1+rpt1, confirmed too new for nexmon — see
# 06-nexmon/01-run-chroot.sh's own comment and
# docs/kernel-nexmon-compatibility.md for the full evidence trail).
# Scoped to ONLY the kernel image/header package names via
# /etc/apt/preferences.d/90-nexmon-kernel-pin (installed by 00-run.sh) —
# not a distribution-wide downgrade.
set -x

apt-get update

echo "=== apt-cache policy after adding the bookworm source (evidence) ==="
apt-cache policy linux-image-rpi-v8 linux-image-rpi-2712 linux-headers-rpi-v8 linux-headers-rpi-2712

echo "=== apt-cache madison (evidence) ==="
apt-cache madison linux-image-rpi-v8 linux-image-rpi-2712 linux-headers-rpi-v8 linux-headers-rpi-2712

# Fail fast — before attempting the install — if the pin didn't actually
# take effect (e.g. the bookworm suite stopped serving these packages,
# or its kernel moved past the nexmon-compatible line), rather than
# proceeding and getting a confusing failure later in this stage or in
# 06-nexmon.
CANDIDATE="$(apt-cache policy linux-image-rpi-v8 | awk '/Candidate:/{print $2}')"
case "$CANDIDATE" in
  1:6.12.*)
    echo "Pin confirmed: linux-image-rpi-v8 candidate is ${CANDIDATE}"
    ;;
  *)
    echo "FATAL: expected a 1:6.12.x candidate for linux-image-rpi-v8 from the bookworm pin, got '${CANDIDATE}'. The bookworm suite may no longer serve a nexmon-compatible kernel — this needs a real re-investigation, not silently building against whatever trixie has." >&2
    exit 1
    ;;
esac

apt-get install -y --allow-downgrades \
  linux-image-rpi-v8 linux-image-rpi-2712 \
  linux-headers-rpi-v8 linux-headers-rpi-2712

echo "=== installed kernel/header packages after pin (evidence) ==="
dpkg -l | grep -E "linux-image|linux-headers"

INSTALLED_VER="$(dpkg-query -W -f='${Version}' linux-image-rpi-v8)"
case "$INSTALLED_VER" in
  1:6.12.*) : ;;
  *)
    echo "FATAL: linux-image-rpi-v8 installed version is '${INSTALLED_VER}', not the expected 1:6.12.x — the downgrade did not take effect." >&2
    exit 1
    ;;
esac

# Hold both the meta-packages and the real underlying versioned kernel
# packages so a plain `apt upgrade` on the deployed device can't
# silently pull trixie's newer, nexmon-incompatible kernel back in.
# Reversible via `apt-mark unhold` — this is not a distribution-wide
# freeze, just these specific packages.
#
# Real, confirmed CI finding: dpkg-query -W lists ANY package name dpkg
# has SOME record of matching the pattern (e.g.
# linux-image-6.12.93+rpt-rpi-v8-unsigned — a name dpkg apparently knows
# of but that is neither installed nor has a candidate here), not just
# ones actually installed. Passing that to apt-mark hold failed with
# "Can't select installed nor candidate version". `dpkg -l`'s second
# status-flag column ('i' = installed) is what actually matters here —
# NOT literally matching "ii", which real evidence (a second, idempotent
# run of this same script, packages already held from a previous run)
# showed becomes "hi" once a package is already on hold. Match on the
# second character being 'i' regardless of the first (hold) flag.
HELD_PACKAGES="$(dpkg -l 'linux-image-rpi-v8' 'linux-image-rpi-2712' \
  'linux-headers-rpi-v8' 'linux-headers-rpi-2712' \
  'linux-image-6.12.*' 'linux-headers-6.12.*' 2>/dev/null \
  | awk '/^.i/{print $2}')"
if [ -z "$HELD_PACKAGES" ]; then
  echo "FATAL: no kernel packages matched for apt-mark hold — refusing to continue with an unprotected pin." >&2
  exit 1
fi
# shellcheck disable=SC2086
apt-mark hold $HELD_PACKAGES

echo "=== held packages (evidence) ==="
apt-mark showhold

# Real, confirmed CI finding: leaving trixie's old 6.18.34 kernel
# image/headers installed alongside the pinned 6.12.93 ones (we only
# re-pointed the meta-packages and downgraded/held them — nothing
# removed the old real packages) breaks nexmon in 06-nexmon: DKMS's
# postinst builds against EVERY installed kernel's headers it finds
# under /usr/src, not just the one the meta-packages point to. The
# 6.12.93 build succeeded cleanly (confirmed, exit code 0, for both
# rpi-v8 and rpi-2712) but the ALSO-attempted 6.18.34 build still hits
# the original cfg80211/timer API errors and fails the whole `dpkg -i`.
# Purging the old kernel outright — not just superseding it — is what
# "pin to 6.12.x" actually requires here.
OLD_KERNEL_PACKAGES="$(dpkg -l 'linux-image-6.18.*' 'linux-headers-6.18.*' 'linux-kbuild-6.18.*' 2>/dev/null | awk '/^.i/{print $2}')"
if [ -n "$OLD_KERNEL_PACKAGES" ]; then
  echo "=== purging superseded trixie kernel packages (evidence) ==="
  # shellcheck disable=SC2086
  apt-get purge -y $OLD_KERNEL_PACKAGES
else
  echo "No 6.18.x kernel packages found to purge (already clean, or naming changed — verify against evidence above)."
fi

echo "=== /boot/firmware kernel image files after pin (evidence) ==="
ls -la /boot/firmware/*.img /boot/firmware/kernel*.img 2>&1 || true

echo "=== /lib/modules for the pinned kernel (evidence DKMS will need this) ==="
ls -la /lib/modules/ 2>&1
# Real, confirmed CI finding: this more-specific compound glob
# ('linux-image-6.12.*+rpt-rpi-v8') matched nothing via dpkg -l's own
# pattern engine, even though the broader 'linux-image-6.12.*' (used
# above for HELD_PACKAGES) demonstrably matched the exact same package
# moments earlier in this same script run. Reusing that proven pattern
# and filtering the -rpi-v8 specificity in bash instead of trusting a
# more elaborate dpkg glob.
# Real, confirmed CI finding: two consecutive attempts at deriving this
# via dpkg -l pattern matching both produced empty output for reasons
# that didn't reproduce under manual reasoning about fnmatch semantics -
# not worth a third guess. /lib/modules/ is the actual ground truth this
# whole check exists to verify in the first place; read it directly.
REAL_KVER="$(ls /lib/modules/ 2>/dev/null | grep -E '^6\.12\.[0-9]+\+rpt-rpi-v8$' | head -1)"
if [ -z "$REAL_KVER" ] || [ ! -d "/lib/modules/${REAL_KVER}/build" ]; then
  echo "FATAL: /lib/modules/${REAL_KVER}/build does not exist — the pinned headers did not link up correctly, DKMS in the next stage would fail or silently target the wrong kernel." >&2
  exit 1
fi
echo "Confirmed: /lib/modules/${REAL_KVER}/build exists"
