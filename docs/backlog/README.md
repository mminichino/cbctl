# Capella backlog

Story IDs continue the capellactl `CAP-NNN` scheme. Releases:

| Release | Meaning in cbctl |
|---|---|
| 1.0.0 | Phases 0–4 of `docs/specs/` (tracked in `release-1.0.0/stories.yaml`; story files not ported) |
| 1.5.0 | Operational-database features not yet built (`release-1.5.0/CAP-*.md`) |
| 2.0.0 | App Services coverage report |

Status flow: `Not started` → `In progress` → `Implemented` (or `Descoped`). Flip the story header (or `stories.yaml` for 1.0.0), add `Test…_CAPNNN_ACn_…` tests, then `make docs`.

See [SOURCE.md](SOURCE.md) for the upstream commit.
