# Getting started with Capella (cbctl)

This guide takes you from a local checkout to a working Capella environment description
you can validate, plan, and apply. Fenced blocks with info string `bash run` are exercised
against `internal/capellafake` by `TestGuides_CAP033_AC5_GettingStartedRun`.

## Quick path (offline / fake)

1. Build: `make build`
2. Unit + hermetic checks: `make check` (or `go test ./internal/...` while `check` is landing)
3. Capella commands against the in-memory fake: see `docs/capella/testing.md`

Configure a real org with `cbctl capella configure` when you have `CAPELLA_API_KEY` / `CAPELLA_ORG_ID`.

## Validate, plan, apply (fake walkthrough)

Assume a `capella.yaml` is already in the working directory (the scenario test seeds one).
Run these in order:

```bash run
cbctl capella validate
```

```bash run
cbctl capella plan
```

```bash run
cbctl capella apply --yes
```

```bash run
cbctl capella drift
```

```bash run
cbctl capella plan --detailed-exitcode
```

A second apply should report no changes:

```bash run
cbctl capella apply --yes
```

## Next steps

- Custom layouts: `docs/capella/reference.md`
- Testing / fake / nightly: `docs/capella/testing.md`
- Backlog and CAP stories: `docs/backlog/README.md`
