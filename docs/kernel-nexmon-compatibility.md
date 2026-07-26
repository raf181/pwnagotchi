# Kernel / nexmon compatibility — evidence trail

> **Compatibility snapshot, 2026-07-22.** Kernel and repository versions can
> drift. The 2026-07-26 live-device audit did not establish that the inspected
> Pi booted this exact pinned image artifact, so the runtime questions near the
> end of this document remain open until that artifact is identified and tested.

Real findings, not guesses, gathered while diagnosing why
`brcmfmac-nexmon-dkms` failed to build in `pi-gen-stage/06-nexmon` — see
`deploy/pi-gen-stage/05a-pin-kernel/` for the fix this evidence led to.

## The failure (confirmed via a real CI build, run 29944793364)

`dpkg -i brcmfmac-nexmon-dkms_6.12.2_all.deb` failed with a real compiler
error (captured via a `make.log` dump added to the build script), not a
config/permissions problem:

```
cfg80211.c:830:17: error: implicit declaration of function ‘del_timer_sync’
cfg80211.c:3281:25: error: implicit declaration of function ‘from_timer’
cfg80211.c:3281:44: error: ‘escan_timeout’ undeclared
cfg80211.c:5589:29: error: initialization of ‘int (*)(struct wiphy *, int, u32)’
  from incompatible pointer type ‘s32 (*)(struct wiphy *, u32)’
  [-Wincompatible-pointer-types]   (.set_wiphy_params)
cfg80211.c:5594:25: error: ... incompatible pointer type ...  (.set_tx_power)
cfg80211.c:5595:25: error: ... incompatible pointer type ...  (.get_tx_power)
```

Building against kernel `6.18.34+rpt-rpi-2712` (trixie's current default,
confirmed via `archive.raspberrypi.com`'s live `trixie` Packages index at
the time: `linux-image-rpi-v8` = `1:6.18.34-1+rpt1`).

An earlier hypothesis — that DKMS might target the QEMU-chroot build
host's own `uname -r` instead of the real installed Raspberry Pi kernel
headers — was directly **ruled out**: the make.log shows DKMS correctly
built against `6.18.34+rpt-rpi-2712` / `rpi-v8` (the real target kernels,
whose matching `linux-headers-rpi-v8`/`linux-headers-rpi-2712` packages
pi-gen's stage0 installs for exactly this purpose), not some unrelated
x86_64 GitHub Actions runner kernel version.

## Why: nexmon lags kernel API changes, and Kali says so directly

From Kali's own blog post,
[kali.org/blog/raspberry-pi-wi-fi-glow-up](https://www.kali.org/blog/raspberry-pi-wi-fi-glow-up/)
(the source of the `brcmfmac-nexmon-dkms`/`firmware-nexmon` packages
this pipeline uses):

> Kali Linux had remained on kernel 5.15 for over a year because kernel
> 6.6 caused compatibility issues with Nexmon patches. However, the
> switch to kernel 6.12 made this integration stable.

The `brcmfmac-nexmon-dkms` package's own version number, `6.12.2`, is not
arbitrary — it corresponds to the kernel line Kali actually validated it
against. The DKMS module is *supposed* to rebuild against "your kernel"
going forward, but the compile errors above prove that stops holding by
`6.18.34` — six minor versions past the last point Kali confirmed stable,
comfortably enough range for real `cfg80211_ops` signature changes (e.g.
additional `link_id`-style parameters landing for multi-link/Wi-Fi 7
support in recent kernel cycles) to break an unmaintained-for-that-range
driver patch.

## Package-availability research (real `apt`-repo queries, not guesses)

Live trixie suite (`archive.raspberrypi.com/debian/dists/trixie/main/binary-arm64/Packages`,
fetched directly, confirmed HTTP 200):

| Package | Version |
|---|---|
| `linux-image-rpi-v8` | `1:6.18.34-1+rpt1` |
| `linux-image-rpi-2712` | `1:6.18.34-1+rpt1` |
| `linux-headers-rpi-v8` | `1:6.18.34-1+rpt1` |
| `raspi-firmware` | `1:1.20260521-3` |

No older/historical versions are visible via the live Packages index —
apt repos generally don't retain superseded versions, so pinning an
exact `6.18.x`-suite version string was not an option.

Live `bookworm` suite, same repo (`archive.raspberrypi.com/debian/dists/bookworm/main/binary-arm64/Packages`,
also confirmed HTTP 200, same day):

| Package | Version |
|---|---|
| `linux-image-rpi-v8` | `1:6.12.93-1+rpt1` |
| `linux-image-rpi-2712` | `1:6.12.93-1+rpt1` |
| `linux-headers-rpi-v8` | `1:6.12.93-1+rpt1` |

The `bookworm` suite's own `Release` file confirms it is still actively
maintained, not an abandoned/frozen archive:

```
Origin: Raspberry Pi Foundation
Label: Raspberry Pi Foundation
Suite: oldstable
Codename: bookworm
Date: Wed, 22 Jul 2026 18:50:31 UTC   ← same day as this investigation
```

`1:6.12.93-1+rpt1` is in the `6.12` line Kali validated. Kernel stable/LTS
branch policy is specifically to keep external/DKMS-facing driver APIs
compatible across point releases within the same major.minor — so a late
point release (`.93`) is expected to remain nexmon-compatible, though
this specific point release was not itself individually tested upstream
by Kali (their blog doesn't enumerate point releases) — this is a
reasoned inference from stable-branch policy, not a re-confirmed fact,
and the build's own fail-fast checks (see below) exist partly to catch
the case where that inference turns out wrong.

## The fix: narrowly-scoped kernel pin, not a distribution downgrade

`deploy/pi-gen-stage/05a-pin-kernel/` (runs before `06-nexmon`):

1. Adds `archive.raspberrypi.com`'s `bookworm` suite as an *additional*
   apt source (`files/bookworm-kernel.sources`), reusing the same
   already-trusted `raspberrypi-archive-keyring.pgp` pi-gen's stage0
   already installs — no new key to trust.
2. Adds an apt preferences pin (`files/90-nexmon-kernel-pin`) scoped to
   `Package: linux-image-* linux-headers-*` from
   `release o=Raspberry Pi Foundation, n=bookworm`, for just these
   package names, nothing else. **Real CI finding, not a first-try
   success**: priority 990 was tried first and confirmed insufficient —
   `apt-cache policy` showed bookworm's `1:6.12.93` correctly at
   priority 990 in the version table, but `Candidate:` stayed on
   trixie's `1:6.18.34` regardless, because pi-gen's stage0 already
   installs trixie's kernel *before* this stage runs, and per
   `apt_preferences(5)`, priority in `(500,990]` explicitly won't
   downgrade an already-installed newer version — only `>1000`
   ("install even if this constitutes a downgrade") does. Fixed by
   raising the pin to `1001`.
3. `apt-get install --allow-downgrades` the pinned kernel/headers,
   replacing whatever trixie's stage0 already installed.
4. Fails the build immediately (`exit 1`, before attempting the install)
   if the resolved candidate isn't a `1:6.12.x` version — if
   `archive.raspberrypi.com` ever moves `bookworm`'s own kernel past the
   nexmon-compatible line, or stops serving it, this pipeline stops
   instead of silently building against whatever trixie has.
5. Verifies the *installed* version matches `1:6.12.x` (not just the
   candidate) after install, and that `/lib/modules/<version>/build`
   actually exists — the exact thing DKMS needs in `06-nexmon` — failing
   loudly otherwise.
6. `apt-mark hold`s both the meta-packages (`linux-image-rpi-v8` etc.)
   and the real underlying versioned packages
   (`linux-image-6.12.*+rpt-rpi-v8` etc.), so a plain `apt upgrade` on
   the deployed device can't silently pull trixie's newer kernel back in.
   Fully reversible via `apt-mark unhold` — not a distribution-wide
   freeze, and does not touch `apt-get dist-upgrade`/`full-upgrade`'s
   ability to update anything else.

`06-nexmon/01-run-chroot.sh` itself was also hardened at the same time:
on a DKMS install failure it now dumps the real `make.log` (previously
only a one-line "bad exit status" summary reached CI output — this is
what surfaced the actual compiler errors above), and on success it
verifies the built module directly (`modinfo` on the `.ko` path itself,
not `-k <name>`, since the latter would resolve against the QEMU-chroot
build host's own unrelated `uname -r`) and confirms it self-identifies as
nexmon-patched (`modinfo | grep -qi nexmon`) rather than trusting `dpkg`'s
exit code alone.

## Build confirmed working end-to-end (real evidence, not projected)

The pin + purge fix (see commit history: priority 990→1001, dpkg -l
wildcard fixes, and the `06-nexmon` old-kernel purge) was validated by a
**full local pi-gen build** (not CI — a real KVM VM with working loop
devices, since neither this repo's original authoring sandbox nor
GitHub Actions runners gave fast enough iteration for the number of
real bugs this took to shake out). Real, observed results:

- `apt-get install --allow-downgrades` correctly landed
  `linux-image-rpi-v8`/`rpi-2712` and their headers on `1:6.12.93-1+rpt1`.
- The old `1:6.18.34-1+rpt1` kernel/header packages were purged (`apt-get
  purge`, ~134 MB freed) — confirmed necessary: DKMS's postinst builds
  against *every* installed kernel's headers under `/usr/src`, not just
  the one the meta-packages point to, so leaving the old kernel installed
  (even just superseded/held, not removed) made the overall `dpkg -i`
  fail even though the 6.12.93 build itself succeeded.
- `brcmfmac-nexmon-dkms` compiled cleanly against **both**
  `6.12.93+rpt-rpi-v8` and `6.12.93+rpt-rpi-2712` — real `make` output,
  `# exit code: 0` for both, no `cfg80211`/timer API errors this time.
- This session's own build-time verification step (`modinfo` on the
  built `.ko`, checking for the `nexmon` string) passed for real:
  `Confirmed: nexmon-patched brcmfmac.ko built for 6.12.93+rpt-rpi-v8`.
- `pi-gen`'s own `export-image` stage completed (`[HH:MM:SS] Build
  finished`), producing a real, complete `.img.xz` (~780 MB), which was
  transferred back and flashed to a physical SD card.

This resolves the "not yet build-tested" uncertainty from this
document's first version — the pin is now proven to produce a working
build artifact, not just a theory backed by partial CI evidence.

## Still open — real risks not yet resolved by this pin

- **Not yet boot-tested on real hardware.** The image above was flashed
  to a physical SD card but has not yet been booted in the target Pi
  Zero 2 W as of this note — whether the pinned kernel actually boots,
  whether `/boot/firmware` ends up correctly referencing it, and whether
  the nexmon module actually loads and creates a working monitor
  interface at runtime are real, separate questions a build succeeding
  cannot answer by itself. See the target-runtime validation script
  (`deploy/scripts/validate-nexmon-runtime`) and its actual output on
  real hardware for that evidence once available.
- **Whether `brcmfmac_cyw` (the Cypress-family companion module found
  splitting `brcmfmac` on the previously-tested `6.18.34` kernel — see
  `deploy/README.md`'s WiFi monitor mode section) exists on `6.12.93` at
  all, and if so whether nexmon's patch (which targets `brcmfmac`
  specifically, not any Cypress-specific companion module) is compatible
  with it, is unknown until real hardware confirms `lsmod` output for
  this exact pinned kernel.**
- **This is not future-proof.** If Raspberry Pi Foundation ever bumps
  `bookworm`'s own kernel past whatever remains nexmon-compatible, this
  pin will need re-investigating — the build-time fail-fast check (step
  4 above) is designed to make that loud and immediate rather than a
  silent regression, not to prevent it from ever happening.
