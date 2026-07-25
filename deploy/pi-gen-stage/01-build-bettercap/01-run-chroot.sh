#!/bin/bash -e
# Builds real, unmodified bettercap from source, natively (see
# ../00-install-golang/00-run-chroot.sh for why native-in-chroot rather
# than cross-compiled). BETTERCAP_REF pins a known-good tag rather than
# floating on a moving branch, so a rebuild of this image is
# reproducible; bump it deliberately, not silently.
export PATH=$PATH:/usr/local/go/bin

BETTERCAP_REF="v2.41.7"

cd /tmp
git clone --branch "${BETTERCAP_REF}" --depth 1 https://github.com/bettercap/bettercap.git bettercap-src
cd bettercap-src
# Real, confirmed failure on Debian trixie: Go's linker unconditionally
# adds "-fuse-ld=gold" to the external-linker command line for cgo
# GOARCH=arm64 builds (bettercap needs cgo for libpcap/libusb/netfilter),
# but trixie's binutils package no longer ships the gold linker at all —
# gcc's collect2 fails with "cannot find 'ld'" (its generic message for a
# missing -fuse-ld= variant, not literally a missing plain ld). bfd (the
# traditional/default linker) is still present, so force it explicitly;
# -extldflags is appended after Go's own defaults on the exec's argv, and
# gcc's -fuse-ld= is last-flag-wins, so this overrides the gold request.
go build -ldflags="-extldflags=-fuse-ld=bfd" -o /usr/local/bin/bettercap .
cd /tmp
rm -rf bettercap-src

/usr/local/bin/bettercap -version
