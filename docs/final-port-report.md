# Final Port Report

This document has been superseded by **`docs/migration-ledger.md`**, which
is now the single running, evidence-based record of what's been ported,
what's deliberately deferred, and why — kept up to date incrementally
rather than periodically rewritten.

Earlier revisions of this file tracked the same information across a long
series of self-correcting "Update:" notes (a subprocess/IPC Python plugin
bridge that has since been deleted entirely, a UI/web layer that was
"Python-only" and is now fully native Go, compatibility tests that no
longer exist) — consolidating into one document avoids two files
disagreeing about current status. See:

- `docs/migration-ledger.md` — what's ported, what's deferred, evidence.
- `docs/feature-matrix.md` — per-subsystem Python→Go status table.
- `docs/plugin-compatibility-matrix.md` — per-plugin architecture/verification.
- `docs/known-differences.md` — every intentional/structural Python↔Go divergence.
- Root `README.md`'s "Status and known gaps" section — the short version.
