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
# "Can't select installed nor candidate version". `dpkg -l | awk
# '/^ii/'` only matches packages in the real "installed" state.
HELD_PACKAGES="$(dpkg -l 'linux-image-rpi-v8' 'linux-image-rpi-2712' \
  'linux-headers-rpi-v8' 'linux-headers-rpi-2712' \
  'linux-image-6.12.*' 'linux-headers-6.12.*' 2>/dev/null \
  | awk '/^ii/{print $2}')"
if [ -z "$HELD_PACKAGES" ]; then
  echo "FATAL: no kernel packages matched for apt-mark hold — refusing to continue with an unprotected pin." >&2
  exit 1
fi
# shellcheck disable=SC2086
apt-mark hold $HELD_PACKAGES

echo "=== held packages (evidence) ==="
apt-mark showhold

echo "=== /boot/firmware kernel image files after pin (evidence) ==="
ls -la /boot/firmware/*.img /boot/firmware/kernel*.img 2>&1 || true

echo "=== /lib/modules for the pinned kernel (evidence DKMS will need this) ==="
ls -la /lib/modules/ 2>&1
REAL_KVER="$(dpkg -l 'linux-image-6.12.*+rpt-rpi-v8' 2>/dev/null | awk '/^ii/{print $2}' | head -1 | sed 's/^linux-image-//')"
if [ -z "$REAL_KVER" ] || [ ! -d "/lib/modules/${REAL_KVER}/build" ]; then
  echo "FATAL: /lib/modules/${REAL_KVER}/build does not exist — the pinned headers did not link up correctly, DKMS in the next stage would fail or silently target the wrong kernel." >&2
  exit 1
fi
echo "Confirmed: /lib/modules/${REAL_KVER}/build exists"
