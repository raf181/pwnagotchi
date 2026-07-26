# Plugin Repository Format

A plugin repository is a set of static HTTP files:

```text
linux-arm64/
  index.json
hello-pwnagotchi/
  1.0.0/
    manifest.toml
    hello-pwnagotchi
```

The daemon only needs the URL of `index.json`. It fetches manifests and
executables from the absolute URLs in those documents.

## Repository Index

The JSON schema is strict. Unknown fields, duplicate names, invalid names,
trailing JSON documents, and unsupported versions are rejected.

```json
{
  "index_version": 1,
  "plugins": [
    {
      "name": "hello-pwnagotchi",
      "version": "1.0.0",
      "author": "Example Author",
      "description": "Updates the status line.",
      "manifest_url": "https://plugins.example.net/hello-pwnagotchi/1.0.0/manifest.toml"
    }
  ]
}
```

Rules:

- `index_version` must be exactly `1`.
- Each `name` may appear once.
- `name`, `version`, and `manifest_url` are required.
- `manifest_url` must be absolute HTTP or HTTPS.
- The fetched manifest name and version must match the index entry.
- The complete index is limited to 1 MiB.

One index entry identifies one build target. Publish separate indexes for
different operating-system or architecture combinations, for example
`linux-arm64/index.json` and `linux-amd64/index.json`.

## Manifest

```toml
manifest_version = 1
name = "hello-pwnagotchi"
version = "1.0.0"
author = "Example Author"
license = "GPL-3.0"
description = "Updates the status line."
homepage = "https://example.net/hello-pwnagotchi"
os = "linux"
arch = "arm64"
sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
executable_url = "https://plugins.example.net/hello-pwnagotchi/1.0.0/hello-pwnagotchi"
capabilities = ["Log", "View"]
```

The manifest is strict TOML and limited to 256 KiB. See
[Plugin development](plugin-development.md) for naming rules and the supported
capability groups.

## Release Procedure

1. Build a static binary for the exact target:

   ```sh
   CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
     go build -trimpath -o hello-pwnagotchi .
   ```

2. Compute its checksum:

   ```sh
   sha256sum hello-pwnagotchi
   ```

3. Create `manifest.toml` with that exact lowercase checksum.
4. Upload the binary and manifest to versioned, immutable URLs.
5. Fetch both URLs independently and verify their bytes and checksum.
6. Update `index.json` last, after the release files are available.
7. Validate from a matching Pwnagotchi host:

   ```sh
   sudo pwnagotchi plugins update
   sudo pwnagotchi plugins install hello-pwnagotchi
   sudo pwnagotchi plugins doctor --all --no-hardware
   ```

Never overwrite an existing versioned executable. If a build changes, publish
a new plugin version and checksum. Updating the index last prevents clients
from seeing a release before its manifest or binary is available.

## Client Configuration

```toml
[main]
plugin_repository_index = "https://plugins.example.net/linux-arm64/index.json"
```

Then:

```sh
pwnagotchi plugins update
pwnagotchi plugins search '*'
pwnagotchi plugins list
pwnagotchi plugins upgrade '*'
```

`update` validates repository connectivity and structure; it does not cache or
mutate installed plugins. `upgrade` stages and verifies each newer version
before replacing the old directory.

## Hosting and Security

Use HTTPS and restrict write access to release storage and the index. Set
reasonable server-side size limits and serve complete files, because the client
does not support partial releases.

The current schema has checksums but no signatures. A compromised repository
can replace a manifest, executable, and checksum together. Repository operators
should add an independent signed release process, transparency log, or trusted
artifact-signing system if plugins are distributed beyond a controlled
environment.

Plugins execute with the Pwnagotchi service account's operating-system
permissions, commonly `root` on the image. Publishing a plugin is equivalent to
publishing executable code for that account.

## Server Limits

| Object | Client limit |
|---|---|
| Index | 1 MiB |
| Manifest | 256 KiB |
| Executable | 128 MiB |
| HTTP request duration | 30 seconds |

The executable is downloaded to a temporary file, synchronized, renamed,
checksum-verified, and only then installed. Existing installations are retained
when staging or verification fails.
