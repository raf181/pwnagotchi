#!/bin/bash -e
# Standard pi-gen stage prerun: continue from the previous stage's rootfs.
# See https://github.com/RPi-Distro/pi-gen/blob/master/README.md
if [ ! -d "${ROOTFS_DIR}" ]; then
	copy_previous
fi
