# Release plan: cbctl 0.7.0

Status: plan, not started. Base: `main` at `7394ed2` (v0.6.1). Author branch: `mm--release-0-7-0-plan`.

0.7.0 delivers three features:

1. **YAML through JSON** (the Kubernetes pattern): YAML on disk, JSON-tagged structs in code. See [01-yaml-json.md](01-yaml-json.md).
2. **`cbctl apply -f`** manifests (`CouchbaseServer`, `CouchbaseCapella`) from `cbctl-apply-manifest-spec.md`. See [02-manifest-apply.md](02-manifest-apply.md) (model, reuse, spec conflicts) and [02b-manifest-execution.md](02b-manifest-execution.md) (execution, errors, edge cases, acceptance).
3. **Consistent output and color** for every command, from `cbctl-output-color-design.md`. See [03-output.md](03-output.md).

The test plan, release gate, risks, decisions and open questions are in [04-test-and-release.md](04-test-and-release.md).

Decided (2026-11-17, see 04 §4):
- **D1**: `[ok]`/`[info]` stay on stdout.
- **D2**: YAML library is `go.yaml.in/yaml/v3` behind the internal `sigs.k8s.io/yaml`-style wrapper `internal/yamlio`.
- **D3**: `cbctl apply` never rotates credential passwords in v1, and the manifest spec is amended to say so.

Each file covers the six required sections: file and module structure, interfaces and signatures, data models, error handling, edge cases, and acceptance criteria.

## Conventions

- These conventions come from the Phase 0–4 specs and `.cursor/rules/general.mdc`. Tests use the stdlib `testing` package, are table-driven with `t.Run`, and run with `-race`. I/O functions take `context.Context` first. Errors are wrapped with `%w`. Exported identifiers have doc comments. Hermetic CLI tests go through `cli.Run(ctx, args, cli.Deps{...})`.
- Exit codes do not change: `0` ok or no-op, `1` API, transport or wait failure, `2` changes found (`plan`/`drift --detailed-exitcode` only), `3` invalid usage or config, `4` blocked, `5` wait timeout, `130` interrupted.
- Every phase is one PR into `main`. Each phase updates `CHANGELOG.md` under `[Unreleased]`, plus `README.md` and `.cursor/rules/cbctl.mdc` whenever it changes a command or user-visible behaviour (project rule).
- The version comes from the git tag through GoReleaser ldflags (`main.version`). The repo has no version constant to bump. A release means: the CHANGELOG section, the README pin example (`v0.6.1` → `v0.7.0`), and the tag `v0.7.0` on `main`, which triggers `.github/workflows/release.yml`. The 0.6.1 release followed this pattern (branch `mm--release-0-6-1`, commit `chore(release): 0.6.1`, PR #17, tag).

## Phases

Size key: S ≤ 1 day, M 2–4 days, L 1–2 weeks. Phases P1 and P2 can run in parallel. P3 needs P1. P4 and P5 need P1 and P2. P6 needs P2 (it can start as soon as P2 merges). P7 is last.

| # | Branch | Content | Depends on | Size |
|---|---|---|---|---|
| P1 | `mm--yaml-json-codec` | New `internal/yamlio` package. Swap `gopkg.in/yaml.v3` → `go.yaml.in/yaml/v3`. Move profiles, legacy profiles, doctor, completion, presets, sync and migrate onto it. `capellafile` decodes through JSON (Obj stays). Round-trip goldens. | — | L |
| P2 | `mm--output-package` | New `internal/output` package (labels, color decision, streams, renderer). `--color` root flag. `logging` becomes a shim over `output`. `printError`, `printNotice` and wizard lines are routed through it. `[ok]` level. Goldens. | — | M |
| P3 | `mm--manifest-model` | New `internal/manifest` package: envelope, both kinds, strict decode with line numbers, secrets incl. `${base64:…}`, offline validation, inline `data` conversion, `keyField` checks. No commands yet; `cbctl apply` is not registered. | P1 | M |
| P4 | `mm--apply-server` | `cbctl apply -f` for `CouchbaseServer`: `internal/manifest/server` orchestrator over `rest` and `server`. Inline-data and keyed import in `internal/server`. Docker integration test. | P2, P3 | L |
| P5 | `mm--apply-capella` | `cbctl apply -f` for `CouchbaseCapella`: translate to an in-memory `capellafile.Config`, run the existing `plan`/`resources` engine with no file side effects, then Capella import. Fake + sandbox tests. | P2, P3 | L |
| P6 | `mm--output-migration` | Move every remaining command onto `internal/output`: Server, Capella, configure, wizard, sync, guide. `[info]` → `[ok]` rename. Plan symbols and arrows in ASCII mode (see 03 §open). Remove `fatih/color`. Update the `cli-logging.mdc` rule. | P2 | M |
| P7 | `mm--release-0-7-0` | CHANGELOG `[0.7.0]`, README pin, rules, docs index regen (`make docs`), full `make check`, live Capella run, tag. | P1–P6 | S |

The branch `mm--release-0-7-0` already exists locally at `main` and has no commits. P7 reuses it.

### Why this order

- The manifest loader needs the codec (P1), so P1 goes first. P1 also has the widest blast radius on existing behaviour (`capella.yaml`), so it should land early and soak in the nightly.
- `cbctl apply` is new output surface. It is built on `internal/output` (P2) from day one, so it never needs migrating. P2 introduces the package and moves only the shared sinks: `logging`, `printError`, `printNotice`, `progressWriter`. P6 does the bulk of the per-command migration and the visible `[ok]` rename, so that diff can be reviewed on its own.
- P4 and P5 are independent of each other and can be built in parallel once P3 merges.

## Summary of breaking or visible changes in 0.7.0

| Change | Phase | Who is affected |
|---|---|---|
| Success lines change from `[info] - …` to `[ok] - …` (e.g. `[ok] - Cluster created on 10.0.0.1`). | P6 | Scripts that grep for `[info] - Cluster created`. |
| Color: info is no longer blue. Labels are bold green, yellow or red. Color is decided per stream. New `--color auto\|always\|never` flag. `CLICOLOR`, `CLICOLOR_FORCE` and `FORCE_COLOR` are honored. | P2 | Anyone forcing or stripping color. |
| `[error]` and `[warning]` from `printError`, `printNotice` and the wizard are now colored on a TTY (before: never). | P2 | Visual only. |
| `capella.yaml` managed edits and `sync`: no intended byte change. P1 has a byte-identical golden gate. | P1 | No one, if the gate holds. |
| `profiles.yaml` is now written in struct field order rather than yaml.v3 struct order, with the same keys. | P1 | No one: the file is machine-managed. |
| `[ok]`/`[info]` stream: stdout, unchanged (**Decided D1**, see 04 §4). | — | No one. |
| Credential passwords are never rotated by `cbctl apply` in v1 (**Decided D3**). The manifest spec is amended to say so. | P5 | Anyone expecting rotation from a changed `${VAR}`. They use `capella credential update --rotate-password`. |
| `cbctl apply -f` added at the top level. It is new and does not conflict with `cbctl capella apply` (see 02 §conflicts). | P4/P5 | — |

Unchanged stdout contracts, guarded by tests in P2 and P6:
- `exists` prints `true`/`false`.
- `cluster map` prints its text.
- `-o json` prints one document with no ANSI codes.
- Generated passwords are printed once (`password: …`, `"password"`).
- `Cluster already configured`, `Node already configured` and `Collection not empty` keep their text and exit 0.
- `plan`/`drift` exit codes are unchanged.
