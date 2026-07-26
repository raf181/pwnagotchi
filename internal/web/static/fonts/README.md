# VT323 Web Font

This directory already contains the locally hosted VT323 files used by the Go
web UI:

- `VT323-Regular.ttf`
- `VT323-Regular.woff2`
- `VT323-Regular.woff`

`internal/web/static/css/style.css` serves these through `/static/fonts/`.
Keeping the font local avoids a browser dependency on the Google Fonts CDN and
allows the UI to render on an isolated USB gadget network.

These files are mirrored in `pwnagotchi/ui/web/static/fonts` for the retained
Python source baseline. When updating the font, update both directories with
the same bytes and verify the web asset tests:

```sh
cmp internal/web/static/fonts/VT323-Regular.ttf \
  pwnagotchi/ui/web/static/fonts/VT323-Regular.ttf
go test ./internal/web
```

VT323 is distributed by the Google Fonts project under its upstream font
license. Preserve the upstream license and provenance when replacing the files.
