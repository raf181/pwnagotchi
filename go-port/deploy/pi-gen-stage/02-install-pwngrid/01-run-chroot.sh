#!/bin/bash -e
# pwngrid ships real prebuilt Linux ARM release binaries (unlike
# bettercap — verified directly against its GitHub releases API, see
# ../01-build-bettercap's doc comment), so this downloads and verifies
# one instead of building from source.
PWNGRID_REF="v1.10.3"

ARCH="$(dpkg --print-architecture)"
case "$ARCH" in
	arm64) ASSET="pwngrid_linux_aarch64_${PWNGRID_REF}" ;;
	armhf) ASSET="pwngrid_linux_armhf_${PWNGRID_REF}" ;;
	*) echo "unsupported chroot architecture: $ARCH" >&2; exit 1 ;;
esac

cd /tmp
BASE_URL="https://github.com/evilsocket/pwngrid/releases/download/${PWNGRID_REF}"
curl -fsSLO "${BASE_URL}/${ASSET}.zip"
curl -fsSLO "${BASE_URL}/${ASSET}.sha256"
unzip -o "${ASSET}.zip"

# The release .sha256 file is BSD-style ("SHA256(pwngrid)= <hash>",
# hashing the EXTRACTED binary, not the zip) — not GNU coreutils'
# "<hash>  filename" format `sha256sum -c` expects. Verified directly
# against the real release asset, not assumed.
EXPECTED="$(sed -n 's/^SHA256(pwngrid)= *//p' "${ASSET}.sha256")"
ACTUAL="$(sha256sum pwngrid | cut -d' ' -f1)"
if [ -z "$EXPECTED" ] || [ "$EXPECTED" != "$ACTUAL" ]; then
	echo "pwngrid checksum mismatch: expected ${EXPECTED:-<unparsed>}, got ${ACTUAL}" >&2
	exit 1
fi

install -m 755 pwngrid /usr/local/bin/pwngrid
rm -f "${ASSET}.zip" "${ASSET}.sha256" pwngrid

/usr/local/bin/pwngrid -version
