# VT323 Web Font

This directory already contains the locally hosted VT323 files retained with
the Python source baseline:

- `VT323-Regular.ttf`
- `VT323-Regular.woff2`
- `VT323-Regular.woff`

The runtime copies live in `internal/web/static/fonts` and are embedded into
the Go daemon. Keep both directories byte-identical when updating the font:

```sh
cmp internal/web/static/fonts/VT323-Regular.ttf \
  pwnagotchi/ui/web/static/fonts/VT323-Regular.ttf
go test ./internal/web
```

VT323 is distributed by the Google Fonts project under its upstream font
license. Preserve the upstream license and provenance when replacing the files.
