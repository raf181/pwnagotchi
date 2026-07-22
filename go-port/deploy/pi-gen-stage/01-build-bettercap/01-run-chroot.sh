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
go build -o /usr/local/bin/bettercap .
cd /tmp
rm -rf bettercap-src

/usr/local/bin/bettercap -version
