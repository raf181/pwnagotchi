# Deploying the Go port to a Raspberry Pi (Pi Zero 2 W)

This directory + `.github/workflows/build-pi-image.yml` build a flashable
Raspberry Pi OS Lite (64-bit/arm64) image with the Go `pwnagotchi` daemon,
`bettercap`, and `pwngrid` pre-installed, their systemd services enabled,
and the web UI reachable on first boot.

## Status — read this before flashing anything

**This pipeline has not been run end-to-end and the resulting image has
not been booted on real Pi Zero 2 W hardware.** Everything here was
built by:

- Reading pi-gen's actual current documentation/source
  (`RPi-Distro/pi-gen`, `arm64` branch) rather than guessing its
  conventions.
- Recovering and adapting the original pwnagotchi project's own
  pi-gen-based image-build scripts (deleted from this repo in the "Make
  the Go port primary" commit, recovered from git history:
  `git show a15ae8fc:stage3/...`) as reference for what a real, working
  systemd/launcher-script setup looks like.
- Verifying the two riskiest technical assumptions **directly, locally**
  before writing any of the CI/build files around them:
  - `go-port`'s own binary cross-compiles cleanly for `linux/arm64` with
    zero special setup (`CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build`
    — confirmed, produces a real ARM64 ELF binary).
  - `bettercap` does **not** — verified directly that a plain
    cross-compile from x86_64 fails (`gopacket/pcap` and `google/gousb`
    are real CGO dependencies, needing the target arch's real
    `libpcap-dev`/`libusb-1.0-0-dev`), which is why `01-build-bettercap`
    builds it **natively inside the pi-gen arm64 chroot** instead —
    matching what the original project's own now-deleted build scripts
    already did, for the same reason.
  - `pwngrid` **does** ship real prebuilt `linux/arm64` release binaries
    (verified against its actual GitHub Releases API) — no build needed,
    just a checksum-verified download. Its release `.sha256` files are
    BSD-style (`SHA256(pwngrid)= <hash>`), NOT GNU coreutils format —
    verified directly, `02-install-pwngrid` parses accordingly.
  - `bettercap`'s own releases have **no** Linux ARM binaries at all in
    recent versions (verified: only `darwin_arm64`/`linux_amd64`/
    `windows_amd64` assets exist) — confirming the native-build choice
    above isn't optional.

What is **not** verified: whether a full `pi-gen` run actually completes
successfully end-to-end inside GitHub Actions (disk space, build time,
and QEMU/binfmt reliability in a hosted runner are all real, known pain
points for pi-gen-in-CI setups generally, not specific claims about this
one), and whether the resulting image actually boots and runs correctly
on real hardware. **Treat the first real workflow run as a test, not a
release** — trigger it (`workflow_dispatch`), watch it, fix whatever
breaks, and only then trust its artifact.

## WiFi monitor mode / nexmon — a real, disclosed gap

The original pwnagotchi image patches the Raspberry Pi's onboard
Broadcom/Cypress WiFi driver with **nexmon** to get real monitor-mode
frame injection — this was `stage3/04-nexmon/` in the deleted image-build
scripts: a kernel-module patch built against a specific kernel version,
genuinely one of the most fragile, hardware/firmware-version-specific
parts of the entire original build.

**This pipeline does not attempt to rebuild that.** `pwnlib`'s
`reload_brcm`/`start_monitor_interface` (ported from the original) will
create a `wlan0mon` interface using the stock `brcmfmac` driver, but
without nexmon, real frame injection / full monitor-mode capture on the
Pi Zero 2 W's onboard chip is **not guaranteed to work** — this mirrors
exactly why real pwnagotchi has always needed nexmon in the first place.
Options, not attempted here:

- Apply nexmon separately, post-boot, following the upstream
  [nexmon](https://github.com/seemoo-lab/nexmon) project's own
  instructions for your kernel version.
- Use a USB WiFi adapter with a chipset that supports monitor mode +
  injection natively (no nexmon needed) instead of the onboard radio.

## Architecture

```
.github/workflows/build-pi-image.yml
  build-pwnagotchi   — cross-compiles go-port for linux/arm64 (fast, no QEMU)
  build-image        — runs pi-gen (arm64 branch) with a custom stage:

go-port/deploy/
  pi-gen-stage/
    00-install-golang/    — installs a native Go toolchain INSIDE the arm64 chroot
    01-build-bettercap/   — builds real bettercap from source, natively (see Status)
    02-install-pwngrid/   — downloads + checksum-verifies the real prebuilt pwngrid
    03-install-caplets/   — installs the real bettercap/caplets (pwnagotchi-auto.cap etc.)
    04-install-pwnagotchi/ — installs the real pwnagotchi Python package (plugin bridge
                              dependency) + copies in the pre-cross-compiled Go binary
    05-configure-services/ — installs systemd units + launcher scripts, enables services,
                              sets hostname (see below for why that specifically matters)
  systemd/                — pwnagotchi.service, bettercap.service, pwngrid-peer.service
  scripts/                — pwnlib, pwnagotchi-launcher, bettercap-launcher, monstart, monstop
```

### Why the image sets `/etc/hostname` to `pwnagotchi` at build time

This session's own earlier investigation (see `docs/final-port-report.md`'s
reboot incident note) found that `internal/unit.SetName` — ported
faithfully from real `pwnagotchi.set_name()` — reboots the unit the
*first* time it detects `/etc/hostname` doesn't match the configured
`main.name` (default: `"pwnagotchi"`). A freshly-flashed Raspberry Pi OS
image's default hostname is `raspberrypi`, not `pwnagotchi` — without
this, a real unit's very first boot would silently reboot itself once,
which is confusing for a first-time operator even though it's real,
faithful, intentional Python-parity behavior, not a bug. Setting it
correctly at image-build time avoids that surprise.

### Python plugin bridge

`internal/pyplugin`'s bridge runs real, unmodified bundled/custom plugins
under whatever `python3` is on `PATH` when `PWNAGOTCHI_PYTHON` is unset
(see `internal/plugins.PythonInterpreter()`) — so `04-install-pwnagotchi`
installs the real `pwnagotchi` Python package (and its real
`pyproject.toml` dependencies: Flask, dbus-python, scapy, tomlkit, etc.)
**system-wide** via `pip3 install --break-system-packages`, matching that
assumption directly rather than introducing a venv + extra env var the
rest of this port doesn't otherwise need on a real deployed unit.

## Testing a build

1. Trigger the workflow manually (Actions tab → "Build Raspberry Pi
   image" → Run workflow) or push to `go-port/**`.
2. Download the `pwnagotchi-pi-image` artifact; if the job fails, download
   `pi-gen-build-log` instead and read it — don't re-run blindly.
3. Flash with [Raspberry Pi Imager](https://www.raspberrypi.com/software/)
   or `xzcat pwnagotchi*.img.xz | sudo dd of=/dev/sdX bs=4M status=progress`.
4. Boot it, SSH in (`ENABLE_SSH=1` is set), and check:
   ```
   systemctl status bettercap pwngrid-peer pwnagotchi
   journalctl -u pwnagotchi -f
   ```
5. The web UI should be reachable at `http://<pi-ip>:8080/` once
   `pwnagotchi.service` is up.

Report back whatever actually breaks — this is a first pass through a
pipeline that's never been run, not a finished, field-tested product.
