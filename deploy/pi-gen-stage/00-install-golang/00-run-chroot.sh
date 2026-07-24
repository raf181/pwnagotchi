#!/bin/bash -e
# Installs a native Go toolchain INSIDE the target chroot (arm64), so
# bettercap (see ../01-build-bettercap) can be built natively rather than
# cross-compiled from the x86_64 build host. This is a deliberate,
# verified choice, not the obvious-looking option:
#
# bettercap depends on gopacket/pcap (libpcap cgo bindings) and
# google/gousb (libusb cgo bindings) — real CGO_ENABLED=1 dependencies.
# Cross-compiling CGO code needs a full matching cross C toolchain PLUS
# the target arch's libpcap-dev/libusb-1.0-0-dev headers and libs, which
# is exactly the kind of fragile setup pi-gen's QEMU-emulated chroot
# exists to avoid — apt already has real arm64 libpcap-dev/libusb-1.0-0-dev
# packages available natively inside this chroot. Verified directly
# (not assumed): cross-compiling bettercap from an x86_64 host with
# CGO_ENABLED=0 fails outright (undefined pcap/libusb symbols); building
# it here, natively, with real arm64 dev packages installed, is the
# same approach the original pi-gen-based pwnagotchi image used (see
# git history: stage3/05-install-pwnagotchi/01-run-chroot.sh installed a
# native Go toolchain into the chroot the same way, for the same reason).
#
# The pwnagotchi binary itself has NO cgo dependencies (verified:
# `GOOS=linux GOARCH=arm64 go build` succeeds from a plain x86_64 host
# with zero special setup) so it's cross-compiled separately, fast, on
# the actual GitHub Actions runner — see
# .github/workflows/build-pi-image.yml's build-pwnagotchi job — and just
# copied into this chroot by 04-install-pwnagotchi, not built here.

GO_VERSION=1.25.0

ARCH="$(dpkg --print-architecture)"
case "$ARCH" in
	arm64) GOARCH=arm64 ;;
	armhf) GOARCH=armv6l ;;
	*) echo "unsupported chroot architecture: $ARCH" >&2; exit 1 ;;
esac

FILE="go${GO_VERSION}.linux-${GOARCH}.tar.gz"
cd /tmp
curl -fsSLO "https://go.dev/dl/${FILE}"
rm -rf /usr/local/go
tar -C /usr/local -xzf "${FILE}"
rm -f "${FILE}"

echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/golang.sh
chmod 644 /etc/profile.d/golang.sh
