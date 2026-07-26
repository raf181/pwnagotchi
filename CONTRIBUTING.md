# Contributing

## Before Starting

Open an issue for behavior changes, new hardware support, protocol changes, or
new built-in plugins. Keep fixes and refactors separate so each change has a
clear test surface.

Use Go 1.25 or newer. Do not add a Python runtime dependency or restore the
removed Python plugin bridge.

## Development Workflow

```sh
go mod download
go fmt ./...
go test ./...
go test -race ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -o /tmp/pwnagotchi-go ./cmd/pwnagotchi
```

Before submitting:

- Add focused tests for new behavior and failure paths.
- Use existing capability interfaces and injectable runners/clients/clocks.
- Avoid shell command strings unless the configuration explicitly represents a
  shell program.
- Bound network responses, request bodies, queues, and subprocess waits.
- Validate names and paths before filesystem operations.
- Preserve unrelated user/config changes when writing files.
- Update current documentation when behavior, config, CLI, deployment, plugin
  APIs, or hardware status changes.
- Keep `internal/config/defaults.toml` and `pwnagotchi/defaults.toml`
  byte-identical.

Hardware tests must be opt-in and document the exact model, wiring, kernel,
driver, and commands used. Never include reboot, shutdown, hostname changes,
radio disruption, or destructive bus operations in the default test suite.

## Plugin Changes

Read:

- `docs/plugin-development.md`
- `docs/plugin-repository.md`
- `docs/plugin-compatibility-matrix.md`

Third-party examples must import `github.com/jayofelony/pwnagotchi/pkg/plugin`,
not an `internal` package. New RPC methods require manifest validation,
dispatcher implementation, public SDK coverage, protocol tests, limits, and
documentation.

New built-in plugins require:

- a unique validated name
- registration in `registerNativePlugins`
- synchronized defaults when configuration is needed
- lifecycle cleanup
- capability fakes and focused tests
- CSRF/method/input validation for webhooks
- an entry in the compatibility matrix

## Pull Requests

Include:

- what changed and why
- user-visible and compatibility effects
- exact validation commands and results
- hardware evidence when relevant
- security implications
- known gaps that remain

Do not claim hardware support based only on mocks. Do not claim authenticity
from a checksum; checksums are integrity checks, not signatures.

## Developer Certificate of Origin

Contributions must comply with the
[Developer Certificate of Origin 1.1](https://developercertificate.org/).
Sign each commit:

```text
Signed-off-by: Your Name <you@example.com>
```

Git can add the line automatically:

```sh
git commit -s
```

## License

Contributions are licensed under GPL-3.0, consistent with `LICENSE.md`.
