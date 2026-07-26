# Locale sources

This directory holds the **editable** gettext translation sources for
Pwnagotchi's voice/status-message strings — the `.po` files translators
actually edit, plus the `voice.pot` template they're generated from.

These are moved here (not deleted) from the Python tree's
`pwnagotchi/locale/`, as required by the Go-only migration: a Go-owned
project must not depend on files living under a Python package that gets
deleted.

## Why these aren't embedded in the binary

`internal/voice` only embeds the **compiled** `.mo` catalogs
(`internal/voice/locale/<lang>/LC_MESSAGES/voice.mo`) via `go:embed` —
that's the only artifact the daemon reads at runtime. The `.po` sources
here are plain-text, larger in aggregate than the compiled `.mo` files,
and never read by the running daemon, so embedding them would only bloat
the binary. They live here as real, Go-owned repository source instead.

## Regenerating a `.mo` from a `.po`

After editing a `.po` file here, recompile it to the runtime location
with the standard GNU gettext `msgfmt` tool (part of the `gettext`
package on any Linux distribution — this is a generic, widely-packaged
gettext-suite utility, not Python tooling, so using it here doesn't
reintroduce a Python dependency into the Go build/runtime):

```sh
msgfmt locale-src/<lang>/LC_MESSAGES/voice.po \
  -o internal/voice/locale/<lang>/LC_MESSAGES/voice.mo
```

`internal/voice`'s own tests (`TestEveryEmbeddedCatalogLoads` in
`voice_test.go`) verify every embedded `.mo` file parses successfully via
the real `gotext` catalog loader used at runtime — run
`go test ./internal/voice/...` after regenerating to confirm.

## Adding a new language

1. Add `<lang>/LC_MESSAGES/voice.po` here, translated from `voice.pot`.
2. Compile it to `internal/voice/locale/<lang>/LC_MESSAGES/voice.mo`
   with `msgfmt` as above.
3. Run `go test ./internal/voice/...` — the catalog-count/loadability test
   will pick up the new language automatically (no code changes needed;
   `internal/voice.New(lang)` resolves catalogs purely from the embedded
   filesystem, matching the real Python `gettext.translation(...,
   fallback=True)` behavior of silently falling back to untranslated
   strings for any language with no catalog).
