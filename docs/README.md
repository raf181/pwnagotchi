# Documentation

These documents describe the current Go implementation.

## Current Guides

| Document | Purpose |
|---|---|
| [Architecture](architecture.md) | Runtime composition, startup, plugin processes, web security, and hardware boundaries |
| [Feature matrix](feature-matrix.md) | Current subsystem status and remaining gaps |
| [Known differences](known-differences.md) | Intentional and unresolved differences from the Python implementation |
| [Plugin development](plugin-development.md) | Bundled and third-party Go plugin authoring |
| [Plugin repositories](plugin-repository.md) | Manifest, index, publishing, and release procedure |
| [Plugin compatibility](plugin-compatibility-matrix.md) | Built-in plugin status and hardware caveats |
| [Pi deployment](../deploy/README.md) | Image build, first-boot security, live-device validation, and updates |
| [Kernel/Nexmon compatibility](kernel-nexmon-compatibility.md) | Kernel and onboard-radio compatibility research |

## Historical Records

The following files are retained as migration evidence. They describe earlier
repository states and are not operational instructions:

- [Final port report](final-port-report.md)
- [Migration ledger](migration-ledger.md)
- [Python baseline](python-baseline.md)
- [Rendering investigation](rendering-investigation.md)
- [Repository analysis](repository-analysis.md)

When a historical record conflicts with a current guide or with source code,
the current guide and source code take precedence.
