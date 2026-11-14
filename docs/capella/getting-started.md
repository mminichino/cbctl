# Getting started with Capella (cbctl)

> Stub guide (Phase 4). Full walkthrough lands with the docs port of capellactl guides.

## Quick path (offline)

1. Build: `make build`
2. Unit + hermetic checks: `make check` (or `go test ./internal/...` while `check` is still landing)
3. Capella commands against the in-memory fake: see `internal/capellafake` and `docs/capella/testing.md`

Configure a real org with `cbctl capella configure` when you have `CAPELLA_API_KEY` / `CAPELLA_ORG_ID`.
