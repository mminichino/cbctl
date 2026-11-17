# 0.7.0 Test plan, release gate, risks, open questions

## 1. Release test plan

### 1.1 Unit tests (every phase, `make test` = `go test -race ./internal/...`)

- Table-driven tests with `t.Run`. Hermetic CLI tests go through `cli.Run(ctx, args, cli.Deps{Env, Stdout, Stderr, IsTerminal, Cwd, Home, Clock})`. No test reads the developer's environment, keychain or `~/.config`.
- `yamlio`: conversion, strict decoding, line mapping, multi-document, alias cap, CRLF, BOM, timestamps (both modes), big numbers, `${…}` refs left untouched.
- `manifest`: one row per validation rule in the spec (valid and invalid), secrets (env/file/base64, literal rejected), service vocabulary (alias, set semantics, Server-only `backup`), inline data conversion, `keyField` rules (file and data), C-items in 02 §5.
- `serverapply`: against the existing `rest` fake (`internal/rest/fakecb_test.go`) and a fake `ServerSDK`. Covers the step order, idempotency (zero POSTs on a second run), the membership report, rebalance skip, arbiter, a cluster belonging to another cluster, and Ctrl-C mid-run (exit 130, steps listed).
- `capellaapply`: `Translate` goldens (manifest → capella.yaml text), path re-pointing, immutable pre-check, `Ephemeral` (no pinning, no `.bak`).
- `output`: the precedence table, label bytes, independent streams, glyph selection, concurrency (`-race`, 100 goroutines writing lines, every line whole).

### 1.2 Golden-file tests (`-update` flag regenerates them; diffs are reviewed in the PR)

| Golden set | Location | Variants |
|---|---|---|
| YAML ↔ JSON round trip | `internal/yamlio/testdata/golden/` | `.yaml` → `.json` → `.roundtrip.yaml` |
| capella.yaml compatibility | `internal/config/capellafile/testdata/compat/` | frozen `Obj` JSON for every `testdata/valid/*.yaml`, taken on `main` before P1 |
| Editor byte-identity | existing editor tests | unchanged |
| Manifest issues | `internal/manifest/testdata/invalid/*.golden` | text and `-o json` |
| Output rendering | `internal/output/testdata/golden/` | {ok, info, warning, error, debug} × {color, plain}. Action/notice. Table/Object/secret × {text, JSON}. |
| Command output | `internal/cli/testdata/output/*.golden` (new) | representative commands (`cluster create` already-configured, `bucket exists`, `capella plan`, `capella credential create`, `apply -f` Server/Capella) × {TTY+color, TTY+`NO_COLOR`, pipe, pipe+`--color=always`, `-o json`} |
| Plan text | `internal/plan/testdata` | regenerated once for decision 5 glyphs |
| JSON schemas | `internal/plan/testdata/*.schema.json` + new `apply.manifest.schema.json` | validated with `clitest` schema helpers |

### 1.3 Integration: Couchbase Server (Docker)

- Existing harness: `internal/integration/server_test.go`, `//go:build integration && server`, testcontainers with `couchbase/server:enterprise-8.0.3`, `make test-server-it` (`CBCTL_IT_REQUIRE_DOCKER=1`). The CI `cover` job runs it every push, and it is part of `make cover-gate`.
- Add `TestServerManifestApply` (same tags, same container image). It covers 02b AC 5: first apply, then a second apply with no changes, keyed documents readable, a non-empty collection skipped, exit codes, plus a `-o json` schema check. Add it to `cover-server` so `internal/manifest/serverapply` and `internal/server` import code count toward the tier gate. New packages get tiers in `internal/ci/covergate/tiers.json` (recommend `manifest` ≥ 90 %, `yamlio` ≥ 90 %, `output` ≥ 90 %, `serverapply` ≥ 75 %, `capellaapply` ≥ 80 %).
- Optional multi-node `TestServerManifestMulti` (2 containers on a user network) gated by `CBCTL_IT_SERVER_MULTI=1`, run in the nightly only.
- `TestServerCLIFlow` is updated for `[ok]` in P6.

### 1.4 Integration: Capella

- Fake: `make test-integration-fake` (`CBCTL_IT_TARGET=fake`, `-tags=integration`) runs every push. Add manifest scenarios: create, re-apply no-op, scale-out, blocked removal, immutable exit 3, import through the fake's data path. If the fake has no KV endpoint, the import step is unit-tested with a fake SDK and only live-tested in the sandbox.
- Sandbox: `make test-integration` (`CBCTL_IT_TARGET=sandbox`, `CAPELLA_SANDBOX_API_KEY` / `CAPELLA_SANDBOX_ORG_ID`, `CBCTL_IT_CLUSTERS=1` for billable parts). Runs nightly via `.github/workflows/nightly.yml`. It is a health signal, not a gate (0.6.0 decision). Add `TestCapellaManifestApply_Live`: project `cbctl-ci-<run>-manifest`, `size: dev`, one bucket, a credential with `${VAR}`, an allowed CIDR for the runner, inline import. Re-apply makes no changes. Teardown uses the harness cleanup.
- Leftovers are removed by the hidden janitor: `make janitor PREFIX=cbctl-ci- HOURS=24` (`cbctl capella janitor --prefix … --older-than-hours … --yes`) and the scheduled `janitor.yml`. Before tagging, run it once manually after the live run, with `--dry-run` first.
- Live smoke before tagging, local, with a personal or sandbox org: `make test-capella-live` (`-run Capella`, `CBCTL_IT_CLUSTERS=1`).

### 1.5 Static checks

- `make vet`, `make fmt-check`, `make lint` (golangci-lint v2.15.0 with `.golangci.yml`; build tag `integration` included), `make tidy-check`.
- `go test -race` for all unit tests and the fake integration suite (`test-integration-fake` already uses `-race`).
- `make docs-check` (index, coverage, reference, links, doc-map) and `make ac-coverage`, `make port-table` (`PORT_MIN=100`, so no capellactl parity regression).
- `make check` aggregates all of the above and is required green on every phase PR. CI runs the `test` matrix (linux/macos/windows), `cover`, `integration-fake`, `docs`, `tidy`, `lint` and `release-smoke`.

## 2. Release gate checklist (all must pass before `v0.7.0` is tagged)

Functional:
- [ ] P1–P6 merged to `main` through PRs with CI green (no direct pushes to `main`).
- [ ] Every acceptance criterion in 01 §8, 02b §6 and 03 §10 is checked off in its PR description.
- [ ] `make check` is green on `main` at the release commit, locally and in CI (all OS in the matrix).
- [ ] `make test-server-it` passes, including `TestServerManifestApply`.
- [ ] One sandbox nightly with the manifest live test is green, or a recorded manual `make test-integration` run. The janitor (`--dry-run`) shows no `cbctl-ci-` leftovers older than 24 h.
- [ ] Byte-identity: `capella sync --stdout` for the fixture orgs on the fake gives identical output on `v0.6.1` and on the release commit (script in the P1 PR).
- [ ] Output contracts table (03 §6) has a passing test per row.

Docs (project rules):
- [ ] `README.md`: new "Manifests (`cbctl apply`)" section with both kinds, units table and Terraform example. "Output and color" section (labels, `--color`, env vars, stream decision). Every `[info] - <success>` example changed to `[ok]`. Install pin `v0.6.1` → `v0.7.0`.
- [ ] `.cursor/rules/cbctl.mdc`: command map line for `cbctl apply -f FILE|- [--dry-run] [-o json] [-y]`; a behaviour section for `apply`; `--color` among the shared flags; agent rule 3 updated to treat `[ok]`/`[info]` "already configured" as success; a `cluster join --arbiter` line (currently undocumented in the rule).
- [ ] `.cursor/rules/cli-logging.mdc` rewritten (it currently mandates blue info and `fatih/color`, which contradicts the design).
- [ ] `docs/manifests.md` exists. `docs/capella/terraform.md` points to the manifest flow. The guide topics `ci.md`/`migrate.md` are updated. `make docs` has been run, so the generated reference, index and doc-map are current.
- [ ] `THIRD_PARTY_NOTICES.md` and `NOTICE` are updated for `go.yaml.in/yaml/v3` (and the removal of `gopkg.in/yaml.v3` and `fatih/color`). `cbdev ci notices` passes.

Release notes (`CHANGELOG.md`, Keep a Changelog):
- [ ] `## [0.7.0] - YYYY-MM-DD` with `### Added` (`cbctl apply -f`, `--color`, `CLICOLOR*`/`FORCE_COLOR`, `[ok]` level), `### Changed` (**Breaking:** `[info]` → `[ok]` for success lines; color scheme; YAML library swap; ASCII glyphs off TTY if chosen), `### Fixed` (capellactl `profiles.yaml` comments kept on `--remove-legacy`; labels now colored on stderr), and a `### Migration` note for scripts matching `[info] - Cluster created`.

Release process (same as 0.6.1):
- [ ] On `mm--release-0-7-0` (it exists locally at `main`; rebase it onto `main`): commit `chore(release): 0.7.0` with the CHANGELOG, README pin and rules. Open a PR with `gh pr create`. CI must be green. Merge with `gh pr merge`.
- [ ] `git tag -a v0.7.0 -m "cbctl 0.7.0"` on the merge commit of `main`, then `git push origin v0.7.0`. `release.yml` runs GoReleaser (version from the tag through `-X main.version`). `nightly-health` is informational.
- [ ] Verify the release: assets for linux/darwin (amd64, arm64) and windows/amd64, `checksums.txt`, and `cbctl --version` printing `cbctl version 0.7.0 (commit=… date=…)`. Smoke `cbctl apply -f` with the README Server example against a local container.
- [ ] Move `[Unreleased]` to empty.

## 3. Risks and mitigations

| Risk | Likelihood / impact | Mitigation |
|---|---|---|
| The YAML swap silently changes how `capella.yaml` is interpreted (numbers, timestamps, booleans) | M / H (wrong plan against prod) | Frozen-`Obj` compat goldens (01 AC5). Byte-identity of sync output. P1 merges first and soaks through at least 3 nightlies before P5. |
| Picking `sigs.k8s.io/yaml` later "for purity" brings YAML 1.1 booleans | L / H | Documented rejection (01 §2). A test asserts `weekend: off` stays `"off"`. |
| Comment loss in managed edits | L / H | The editor algorithm is unchanged. The existing byte-identity tests gate it. |
| `[ok]` rename breaks user scripts | H / M | CHANGELOG breaking note and README migration tip. Exit codes and JSON are unchanged. Hold the rename to P6 so it is one reviewable diff. |
| A per-stream color bug leaks ANSI into JSON or `exists` | L / H | Contract tests (03 §6) across the env matrix. |
| A Capella manifest turns deletion protection off (spec default `false`) | M / H | C18 presence semantics. Protection-off needs `-y` without a TTY (C13). |
| Capella import cannot reach the cluster (allow-list) | H / L | C4 hint. The import runs last, so cluster resources are already applied. |
| `rest` has no context: Ctrl-C during a long rebalance waits for the REST call | M / L | Check `ctx` between steps. Backlog item to thread `ctx` through `rest`. |
| Scope creep: engine refactor to typed structs | M / M | Explicitly out of scope (01 §1). `Obj` stays. |
| Live Capella costs and leftovers | M / M | `size: dev`, `CBCTL_IT_CLUSTERS` gate, `cbctl-ci-` prefix, janitor. |
| Windows color: legacy console shows raw escapes | L / L | VT enable, otherwise no color. A Windows CI job asserts no escapes when it is not a TTY. |

## 4. Open questions for the user (recommended defaults in bold)

1. **Q1 Stream for `[ok]`/`[info]`:** **Option B, stay on stdout**, or Option A (stderr, clig.dev)?
2. **Q2 YAML library:** **`go.yaml.in/yaml/v3` v3.0.6 under an internal `sigs.k8s.io/yaml`-style API (`internal/yamlio`)**, or depend on `sigs.k8s.io/yaml` v1.6.0 directly and accept YAML 1.1 booleans, no line numbers and sorted output keys?
3. **Q3 Unicode glyphs (`→ · ± … ••••`):** **Unicode on a color TTY, ASCII otherwise**, or ASCII everywhere (changes `--version` and the plan on a TTY too)?
4. **Q4 Capella manifest confirmations:** **no prompt for additive changes; `-y` required without a TTY for protection-off and scale-in**, or always require `-y` without a TTY like `capella apply`?
5. **Q5 `cbctl apply --dry-run` (plan, exit 2 on changes):** **add it in 0.7.0**, or defer?
6. **Q6 Server TLS CA:** **add optional `spec.tls.caFile`**, or system trust only?
7. **Q7 Credential rotation (C6):** **never rotate in v1, and amend the spec text**, or add `rotate: true`?
8. **Q8 Absent `deletionProtection`/`description` on existing clusters (C18):** **leave as is (presence semantics) and amend the spec**, or apply the spec defaults?
9. **Q9 `size: dev` vs default `availability: multi` and the 3-node minimum (C2/C3):** **the preset wins, single-node replicas default to 0**?
10. **Q10 Mixed kinds in one file (C12):** **allowed, in order, stop at the first failure**?
11. **Q11 `FORCE_COLOR` alias:** **yes**. `[debug]` dim: **yes**. Remove `fatih/color`: **yes**.
