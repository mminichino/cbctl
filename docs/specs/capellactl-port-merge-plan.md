# Closing the gaps before merging mm--capellactl-port to main

Status: executed on `mm--capellactl-port` (release 0.6.0). Source of the criteria:
[capellactl-port-plan.md §3](capellactl-port-plan.md) ("Exit criteria for merging").

## 0. Audit of the exit criteria (2026-11-17, branch tip 945b2c8)

| # | Criterion | State before this plan | Gap |
|---|---|---|---|
| 1 | Functional parity: zero MISSING rows; `TestCapellactlCompat` green | Matrices in `capellactl-port-audit/` are the pre-remediation snapshot (5 + 6 MISSING, 21 PARTIAL rows); almost all were fixed by Phases 1–6 but the matrices still say otherwise. `TestCapellactlCompat` does not exist. `cbctl capella --version` exits 3, yet the migration guide says "replace `capellactl` with `cbctl capella`" | G1, G2, G4 |
| 2 | Migration: `configure migrate`, legacy shims, `doctor` clean on a migrated fixture | migrate, shims and doctor are tested separately; nothing proves that following the guide ends with a clean `doctor` | G3 |
| 3 | Test parity: port table, AC coverage | 100% mapped, 878 tests | none |
| 4 | Coverage: tiered gate | `make cover-gate` green, CI `cover` job blocking | none |
| 5 | Stability: no `os.Exit` in `internal/`, lint, race, hermetic | all green on CI run 44030564732 | G5 (legacy Server defects found while raising coverage) |
| 6 | Docs: docs-check, operator docs, reference, migration guide | all present and checked | none (CHANGELOG release section: G7) |
| 7 | Nightly: three consecutive green scheduled nightlies on the branch tip | **cannot be met before the merge**: GitHub only fires `schedule` from the default branch, and `nightly.yml` is not on `main` yet. No scheduled run has ever happened | G6 |

## 1. File / module structure

| File | Change | Gap |
|---|---|---|
| `internal/cli/capellactl_tree_test.go`, `internal/cli/testdata/capellactl-tree.tsv` | new: `TestCapellactlCompat` over capellactl's dumped click tree (the per-row behaviour tests already live in `capella_compat_test.go`) | G2 |
| `internal/cli/capella_apply_alias.go` | hidden `cluster apply` takes `--project` | G2 |
| `internal/cli/capella_migrate_test.go` | new `TestMigrationEndToEndDoctorClean`, `TestMigrateKeepsOrganizationName` | G3 |
| `internal/settings/profiles.go`, `legacy.go`, `internal/cli/configure.go` | profile keeps `organizationName` | G3 |
| `internal/cli/root.go` | `capella` group accepts `--version` (same text as `cbctl --version`) | G4 |
| `internal/rest/nodes.go` | `ParseHostPort` handles `[v6]:port`, bare IPv6 and `host:port` | G5 |
| `internal/rest/provision.go` | query readiness timeout is a warning, not silently discarded | G5 |
| `internal/rest/cluster.go` | multi-node create keeps a non-default management port on `addNode`; the dead `_ = useExtAPI` is replaced by a comment saying why setup stays internal | G5 |
| `internal/rest/*_test.go` | table tests for the three fixes | G5 |
| `docs/specs/capellactl-port-audit/parity-cli.md`, `parity-engine.md` | every MISSING / PARTIAL row re-checked and given its current status and evidence | G1 |
| `docs/specs/capellactl-port-plan.md` §3 item 7 | reworded: pre-merge = one green dispatched nightly on the branch tip; the three-night streak gates the release tag on `main` | G6 |
| `docs/capella/migrating-from-capellactl.md` | `--version` row | G4 |
| `CHANGELOG.md` | `[Unreleased]` becomes `[0.6.0] - 2026-11-17`; new empty `[Unreleased]` | G7 |

No new packages; dependencies keep flowing `cmd → internal/cli → internal/*`.

## 2. Interfaces and function signatures

- `func ParseHostPort(value string, defaultPort int) (string, int)`: unchanged signature, new behaviour (§3).
- `func TestCapellactlCompat(t *testing.T)`: reads `testdata/capellactl-tree.tsv`
  (`VISIBILITY\tPATH\tFLAGS` per command, `G` line for root options) and resolves each path and
  `--flag` under `cbctl capella`; exceptions are a documented allow-list that fails when stale.
- `func TestMigrationEndToEndDoctorClean(t *testing.T)`: uses the existing `legacyFixture`.
- `capella` command: `Version` is set from `Deps.Version` (or the build version), and `SetVersionTemplate`
  matches root. No new exported identifiers.

## 3. Data models

- Host input to `ParseHostPort`:
  `host` | `host:port` | `[v6]` | `[v6]:port` | bare `v6` (two or more colons, no brackets) | any of these followed by `/path`.
  The host is returned without brackets. An unparsable port gives `defaultPort` (unchanged behaviour).
- Compat table rows come from the migration guide's "Commands" table. A row whose cbctl spelling
  changes must change the guide in the same commit.

## 4. Error handling

| Failure | Detection | Surfaced as | Recovery |
|---|---|---|---|
| Query service not ready within 30 s after provisioner create | `WaitForQueryReady` error | `[warning]` on stderr naming the node; exit stays 0 (the node is initialised, and query readiness is not required for create to succeed) | re-run `cluster test` |
| `addNode` to a node on a custom port | REST error from the rally node | unchanged: `failed to create Couchbase Server cluster: …`, exit 1 | fix the node spec |
| Compat row drifts from the guide | `TestCapellactlCompat` fails in CI | test failure naming the row | update code or guide |
| Scheduled nightly not yet possible (G6) | `release-gate` finds fewer than 3 scheduled runs | release workflow fails before goreleaser, so nothing is published | merge, wait for three green nights, then tag `v0.6.0` on `main` |

## 5. Edge cases

- `ParseHostPort("::1", 8091)` → `("::1", 8091)`; `"[::1]:9000"` → `("::1", 9000)`; `"[fe80::1]"` → default port;
  `"host:abc"` → default port; `""` → default host; `"10.0.0.1:18091/x"` → `("10.0.0.1", 18091)`.
- Multi-node create: a node on port 8091 still sends a bare host (so requests are byte-for-byte what they were before).
- `cbctl capella --version` with other flags (`-q`, `-o json`) prints the version only; `cbctl capella cluster --version` is still an unknown flag (exit 3), as with root.
- Migrated fixture: an env var still exported (e.g. `BEARER_TOKEN`) is a real leftover. The test unsets it after migrating, as the guide says, then expects a clean result.
- Release before the streak: a `v0.6.0` tag pushed before three scheduled nights is blocked by the gate. That is intended.

## 6. Acceptance criteria

1. Every MISSING row in `parity-cli.md` and `parity-engine.md` is closed (MATCH, RENAMED with a guide entry, or INTENTIONALLY-DROPPED with a justification), with code or doc evidence.
2. `go test ./internal/cli -run 'TestCapellactlCompat|TestMigrationEndToEndDoctorClean'` passes.
3. `cbctl capella --version` exits 0 and prints the same text as `cbctl --version`.
4. The `ParseHostPort` cases in §5 pass; the provisioner and multi-node tests in `internal/rest` pass with the new behaviour.
5. `make lint`, `go test -race ./...`, `make docs-check`, `make port-table`, `make cover-gate` and the hermetic test are green locally and on CI.
6. One `workflow_dispatch` run of `nightly.yml` on the branch tip is green.
7. `CHANGELOG.md` has `## [0.6.0] - 2026-11-17`. `make release-snapshot` (or `goreleaser build --snapshot`) with `GORELEASER_CURRENT_TAG=v0.6.0` reports version 0.6.0.
8. After the merge: three consecutive green scheduled nightlies on `main`, then tag `v0.6.0` on `main` (the release workflow's gate enforces this).

## 7. Outcome

| Gap | Result |
|---|---|
| G1 | `parity-cli.md` §0 and `parity-engine.md` §0 record every former MISSING / PARTIAL / DIFFERENT row with its current status and evidence. No MISSING row remains. |
| G2 | `TestCapellactlCompat` loads capellactl's real click tree (`internal/cli/testdata/capellactl-tree.tsv`, 98 commands, dumped from capellactl 6389d5a) and checks every path and long option. It found one gap: hidden `cluster apply` lacked `--project`. Fixed; it now maps to `cluster/P/NAME`. |
| G3 | `TestMigrationEndToEndDoctorClean`. It also showed that `organizationName` was dropped by `migrate`, against criterion 1 (INTENTIONALLY-DROPPED only for features capellactl never shipped). It is now stored, migrated and shown by `configure list` (`TestMigrateKeepsOrganizationName`). |
| G4 | `cbctl capella --version` = `cbctl --version` (`TestCLI_CAP001_AC1_CapellaVersionMatchesRoot`). |
| G5 | IPv6 parsing and URL bracketing, `addNode` port, `--ext-api` alternate-address setup, query-readiness warning. All tested. |
| G6 | §3 item 7 reworded (dispatched nightly before the merge; scheduled streak gates the tag). |
| G7 | `CHANGELOG.md` `[0.6.0] - 2026-11-17`; a build with goreleaser's ldflags reports `cbctl version 0.6.0`. |

Known limitation, not fixed: the deprecated legacy `internal/capella` `Buckets.List` (behind the hidden
`capella bucket create --database` / `bucket exists`) reads one page of buckets. The v4 commands
page through every bucket (`capellaapi.ListBuckets` → `Paginate`). The legacy commands are removed
in the next release.
