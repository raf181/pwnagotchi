# Raspberry Pi Deployment

The deployment target is Raspberry Pi OS Lite 64-bit on a Pi Zero 2 W. The
image contains the static Go daemon, bettercap, pwngrid-peer, launch scripts,
systemd units, USB gadget networking, and optional Nexmon components.

Read the security and hardware limitations before flashing or updating a unit.

## Live Device Audit

A Pi Zero 2 W at `10.12.194.1` was inspected on 2026-07-26:

- `pwnagotchi`, `bettercap`, and `pwngrid-peer` services were active.
- The daemon ran as `root`, arm64, with CGO disabled and about 24 MiB RSS.
- The installed daemon reported version `2.9.5.5`, Go `1.26.5`, and build
  commit `bc0feaa2cfa190a1608bb3a01776b45b3cb1044f+dirty`.
- The inspected source checkout was newer. Changes in the current tree are not
  on that Pi until a new binary is deliberately built and deployed.
- `/usr/bin/pwnagotchi-go` existed, but `/usr/bin/pwnagotchi` did not. The
  updated image stage now creates the symlink.
- `plugins doctor` passed all 24 built-in entries, but the older CLI did not
  support useful bare `plugins --help` behavior and did not list built-ins
  clearly. The current source fixes both.
- The web server listened broadly with authentication disabled.
- The SSH account still used a known image-default password.
- Waveshare V4 rendering produced repeated unsupported-driver errors until the
  display was disabled.
- `/sys/class/gpio` and `/dev/gpiochip0` existed. `/dev/i2c-*` did not because
  I2C was not enabled in that deployed image.

Treat the inspected device as an older deployment, not proof that un-deployed
source changes work on hardware.

## First-Boot Security

The web UI can execute host actions and edit configuration. Before placing the
unit on any shared network:

1. Change the SSH password:

   ```sh
   passwd
   ```

2. Install an SSH public key, verify key login, then disable password login in
   `sshd_config`.
3. Enable web auth and replace the placeholder credentials:

   ```toml
   [ui.web]
   enabled = true
   address = "10.12.194.1"
   auth = true
   username = "admin"
   password = "replace-with-a-long-unique-password"
   ```

4. Restrict the UI to the USB gadget or a trusted management network.
5. Never expose ports `8080` or `8081` directly to the public internet.
6. Review enabled plugins. `auto-update`, service repair, config editors, and
   system-action plugins run with root privileges on this image.

The packaged auto-update plugin performs release checks but has
`install = false`. Enabling installation permits checksum-verified
bettercap/pwngrid replacement as root. Pwnagotchi daemon releases are
report-only and must use the explicit binary or image update procedure below.

The daemon logs a warning when web auth is disabled or placeholder credentials
remain.

## USB Gadget Network

The preferred NetworkManager profile configures the Pi as:

```text
10.12.194.1/28
```

NetworkManager shared mode supplies a DHCP lease to the connected host. A
secondary client profile is available when another device supplies DHCP. The
Pi Zero 2 W data/OTG port must be connected with a data-capable cable.

Typical checks:

```sh
ip address show usb0
nmcli connection show --active
ping 10.12.194.1
ssh pi@10.12.194.1
```

## Build the Daemon

From the repository root:

```sh
go test ./...
go test -race ./...
go vet ./...

CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -o /tmp/pwnagotchi-go ./cmd/pwnagotchi

file /tmp/pwnagotchi-go
go version -m /tmp/pwnagotchi-go
```

The daemon itself cross-compiles without CGO. Bettercap does not; the image
pipeline builds bettercap natively inside the arm64 pi-gen chroot because its
pcap/USB dependencies require target libraries.

## Update an Existing Pi

Do not deploy an untested working-tree build blindly. Record the current
binary, config, and service state first.

```sh
scp /tmp/pwnagotchi-go pi@10.12.194.1:/tmp/pwnagotchi-go
ssh pi@10.12.194.1
```

On the Pi:

```sh
sudo /tmp/pwnagotchi-go --version
sudo /tmp/pwnagotchi-go plugins doctor --all --no-hardware

sudo cp -a /usr/bin/pwnagotchi-go /usr/bin/pwnagotchi-go.backup
sudo cp -a /etc/pwnagotchi/config.toml /etc/pwnagotchi/config.toml.backup
sudo systemctl stop pwnagotchi
sudo install -m 755 /tmp/pwnagotchi-go /usr/bin/pwnagotchi-go
sudo ln -sfn /usr/bin/pwnagotchi-go /usr/bin/pwnagotchi
sudo systemctl start pwnagotchi

sudo systemctl --no-pager --full status pwnagotchi
sudo journalctl -u pwnagotchi -n 100 --no-pager
sudo pwnagotchi plugins doctor --all --no-hardware
```

Rollback if startup fails:

```sh
sudo systemctl stop pwnagotchi
sudo install -m 755 /usr/bin/pwnagotchi-go.backup /usr/bin/pwnagotchi-go
sudo cp -a /etc/pwnagotchi/config.toml.backup /etc/pwnagotchi/config.toml
sudo systemctl start pwnagotchi
```

An executable update does not replace systemd units, launchers, boot config, or
kernel/Nexmon files. Use a rebuilt image or update those files separately when
deployment changes require them.

Do not enable automatic installation as a substitute for this daemon update
procedure. The daemon intentionally cannot stop, replace, and safely restart
itself from an in-process plugin callback.

## Build a Full Image

`.github/workflows/build-pi-image.yml`:

1. Cross-compiles `cmd/pwnagotchi` for `linux/arm64`.
2. Runs pi-gen for Raspberry Pi OS Lite 64-bit.
3. Builds bettercap in the arm64 chroot.
4. Downloads and verifies the pwngrid arm64 release.
5. Installs caplets, the daemon, launchers, and systemd units.
6. Configures USB gadget networking, zram, hostname, SSH, I2C, and service
   enablement.
7. Pins/builds the kernel/Nexmon components described below.
8. Runs non-hardware image validation and exports an xz-compressed image.

The custom stages are under `deploy/pi-gen-stage`. Trigger the workflow
manually, inspect all build logs, and treat upstream archive or kernel changes
as release blockers until validated on a newly flashed card.

The image contains no Python interpreter or Python plugin bridge. Locale data
and fonts are embedded in the Go binary. Third-party plugins are separate Go
executables.

## Monitor Mode and Nexmon

The stock Pi Zero 2 W onboard BCM43430/1 `brcmfmac` stack does not advertise
monitor mode. Creating a monitor interface fails with `Operation not
supported`. Pwnagotchi therefore needs one of:

- a USB adapter whose Linux driver supports monitor mode and injection
- a Nexmon firmware/module build that exactly matches the onboard chip, kernel,
  and firmware

The launcher prefers a non-`brcmfmac` USB adapter when available. NetworkManager
is configured not to manage `wlan0`, and standalone `wpa_supplicant.service` is
masked so those processes do not hold the radio during monitor setup.

The Nexmon stage is sensitive to kernel/archive drift. Read
`docs/kernel-nexmon-compatibility.md` and run:

```sh
sudo /usr/local/bin/validate-nexmon-runtime
sudo /usr/local/bin/check-kernel-upgrade-safety
iw phy
iw dev
```

Do not apply routine kernel upgrades to a working Nexmon unit without first
confirming module and firmware compatibility.

## Displays and Buses

The current Go build does not drive physical displays. `DummyDisplay` and
headless/web rendering work; physical drivers return explicit unsupported
errors. Keep:

```toml
[ui.display]
enabled = false
```

unless a real Go driver has been implemented and tested for the exact panel.

Updated images add `dtparam=i2c_arm=on`. After reboot:

```sh
ls -l /dev/i2c-*
sudo i2cdetect -y 1
```

Only probe a bus when you know the attached devices and voltage levels. I2C
plugins use `/dev/i2c-1`. GPIO plugins use `/sys/class/gpio`; the backend does
not configure pull bias. SPI remains unavailable to the daemon.

## Service Validation

```sh
systemctl is-active bettercap pwngrid-peer pwnagotchi
systemctl show pwnagotchi -p User -p Group -p ExecStart
journalctl -u bettercap -u pwngrid-peer -u pwnagotchi -b --no-pager

pwnagotchi --version
pwnagotchi plugins list --installed
pwnagotchi plugins doctor --all --no-hardware

curl -I http://10.12.194.1:8080/
ss -lntup
```

An authenticated UI normally returns `401 Unauthorized` to the unauthenticated
`curl` request. A `200 OK` response without credentials means auth is disabled
or bypassed and must be investigated.
