# Pwnagotchi (Go)

The primary implementation of this project is now the Go port in
[`go-port/`](go-port/). Start there for building, running, and
architecture docs — see `go-port/README.md` and `go-port/docs/` (feature
matrix, known differences, final port report).

The original Python daemon (`pwnagotchi/`) is kept only as:
- the bundled/custom **plugin runtime** — `go-port/internal/pyplugin` runs
  the real, unmodified plugin files under `pwnagotchi/plugins/default/`
  in a real Python subprocess, since bundled plugins depend on real
  Python-only libraries (RPi.GPIO, dbus, Flask, requests, ...) that can't
  be reimplemented in Go without losing compatibility;
- the **behavioral oracle** the Go port's differential tests
  (`go-port/tests/compat_*_test.go`, `make -C go-port compatibility-test`)
  diff against, and the reference venv (`venv/`) those tests and the
  plugin bridge run against.

It is not run directly as the daemon anymore and is not the place to add
new features — changes belong in `go-port/`.

[Pwnagotchi](https://pwnagotchi.org/) is a Raspberry Pi leveraging
[bettercap](https://www.bettercap.org/) that survives from its
surrounding Wi-Fi environment to maximize the crackable WPA key material
it captures (either passively, or by performing authentication and
association attacks). This material is collected as PCAP files containing
any form of handshake supported by [hashcat](https://hashcat.net/hashcat/),
including [PMKIDs](https://www.evilsocket.net/2019/02/13/Pwning-WiFi-networks-with-bettercap-and-the-PMKID-client-less-attack/),
full and half WPA handshakes.

Multiple units within close physical proximity can "talk" to each other,
advertising their presence to each other by broadcasting custom
information elements using a parasite protocol
[@evilsocket](https://x.com/evilsocket) built on top of the existing
dot11 standard.

## Links

| &nbsp;    | Official Links                                           |
|-----------|----------------------------------------------------------|
| Website   | [pwnagotchi.org](https://pwnagotchi.org/)                  |
| Chat      | [discord](https://discord.gg/PGgnzFbz4M) |
| Subreddit | [r/pwnagotchi](https://www.reddit.com/r/pwnagotchi/)     |

## License

`pwnagotchi` created by [@evilsocket](https://x.com/evilsocket) and
updated by [us](https://github.com/jayofelony/pwnagotchi/graphs/contributors).
It is released under the GPL3 license.
