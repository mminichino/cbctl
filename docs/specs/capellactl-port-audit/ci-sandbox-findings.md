# Capella port: audit completion and remediation plan

Read-only audit + plan. No repo files were changed, nothing was committed or pushed, no branches were created, and no live Capella APIs were called.

## Decisions (2026-11-16)

| ID | Question | Decision | Phases affected |
|---|---|---|---|
| D1 (was Q1) | How should the janitor and suite authenticate in CI (F-01)? | **Map the sandbox secrets in workflow env:** `CAPELLA_API_KEY: ${{ secrets.CAPELLA_SANDBOX_API_KEY }}` and `CAPELLA_ORG_ID: ${{ secrets.CAPELLA_SANDBOX_ORG_ID }}`. The CLI's `internal/settings` will **not** read `CAPELLA_SANDBOX_*`. The phase-4 spec §5.4 text is updated to match. | Phase 1, §4 |
| D2 (was Q2) | What happens to the legacy `capella cluster create\|exists\|destroy\|test` and `capella bucket create\|exists`? | **Hide, deprecate and make safe.** Each gets a stderr deprecation warning naming the replacement command. There's no `0.0.0.0/0` default unless `--allow-any-ip` is passed, and no hard-coded `Administrator`/`password` user, per cbctl.mdc. | Phase 3, §4 |

The other questions are still open (§5).

## 1. Audit summary

| Item | Value |
|---|---|
| Repo | `/Users/michael/IdeaProjects/cbctl` (module `github.com/mminichino/cbctl`) |
| Ref audited | `mm--capellactl-port` @ `b05642b78172407c7548abaa528d73fdc44da2c4` (= `origin/mm--capellactl-port`). Base `origin/main` @ `5932cb6`. |
| Checkout note | The local checkout was **already on `mm--capellactl-port` @ `b05642b`** with a clean tree, not on `cursor/cp15-scaling-wait-0454` as the brief said. I read files in place, didn't switch branches, and didn't need a worktree. `git status --porcelain` was empty at the end. |
| Spec sources | `.cursor/rules/cbctl.mdc`, `.cursor/rules/general.mdc`, `README.md`, `docs/specs/capella-fold-in-phase-4.md` (§5.4, §5.x workflow, line 270 env rules) |
| Handoff | `bc-41c22bad…/files/internal/capella-port-audit-pause-handoff.md`, plus the prior report `bc-41c22bad…/files/docs/capella-port-test-automation-review.md` |

### 1.1 What the handoff had already covered and what I did

The handoff scope has three areas: (1) GitHub automations, (2) unit/hermetic/regression/scenario/live integration tests, and (3) live Capella tenant isolation and cleanup. The prior agent's report is marked COMPLETE. Its remaining checklist was to re-check citations against `b05642b` and send a top-findings summary.

What I did:

1. **Re-checked every prior citation at `b05642b`** against local paths. Most hold. Corrections:
   - Prior §5.2 #10, "bare `cbctl-ci-` override wipes all concurrent runs", **does not reproduce**. `newLiveCapella` always adds an 8-hex random suffix (`capella_live_test.go:61-69`), and `janitorPrefix()` (`:107-113`) is therefore always run-unique. I retracted it and replaced it with F-24.
   - Prior P1, "shared nightly concurrency causes mid-mutation races", is **mostly wrong**. A single constant group with `cancel-in-progress: false` *serializes* runs. The real risk is different: GitHub keeps only one *pending* run per group, so a newer queued run **cancels the older pending one**. That can drop a scheduled nightly (F-14).
2. **New findings in the same three areas** that the prior report missed: F-02 (sharpened), F-03, F-06, F-07, F-08, F-09, F-11 (sharpened), F-13, F-18, F-20, F-25, F-31, F-32, F-33, F-34.
3. **Spot check of the spec contract** (cbctl.mdc/README) where it touches the audited areas, mainly janitor, env, and the legacy Capella commands the suite and docs mention. This produced F-27, F-28, F-29, F-35 and F-36.
4. Ran the verification commands below.

### 1.2 Verification results (run now, locally, at `b05642b`)

| Command | Result |
|---|---|
| `go build ./...` | OK |
| `go vet ./...` | OK |
| `gofmt -l .` | clean |
| `go test ./...` | All packages pass except `internal/docgen/port` **inside the agent sandbox**: `TestPort_RefusesDirtySourceUnlessForce` fails with `.git/hooks/: Operation not permitted`. Re-run outside the sandbox: `ok internal/docgen/port 3.155s`. This is environmental, but the test does depend on the environment (F-34). |
| `go test -race ./internal/...` | Same result: only the sandbox-only `docgen/port` failure. |
| `make cover-engine` | OK: plan 97.9%, resources **85.1%** (0.1 pp above the 85% gate), capellafile 89.6% |
| `make docs-check` | OK (index, coverage, reference, links) |
| `make ac-coverage` | `AC coverage OK: 0/0 acceptance criteria in 0 stories`. **Passes vacuously** (F-20). |
| `CBCTL_IT_TARGET=fake go test -tags=integration ./internal/integration/...` | **Not re-run.** In the sandbox the module cache couldn't unzip `testcontainers-go` (`.vscode` mkdir EPERM). Auto-review declined the unsandboxed re-run as outside the audit-only scope. The prior agent reported it green with intentional SKIPs. |
| `golangci-lint` | Not installed, and no `.golangci*` config exists (F-33) |
| Live Capella / sandbox suite | Not run (constraint) |

## 2. Findings

Kinds: **BUG** = confirmed defect (behavior contradicts the code's own intent or the spec, with evidence); **DRIFT** = code vs spec/doc mismatch; **DOC** = documentation gap; **TEST** = test/gate gap; **HARDEN** = defense-in-depth. Origin: **P** = in the prior report (re-verified); **N** = new in this pass; **C** = prior finding corrected.

Counts: **Critical 1, High 9, Medium 15, Low 11 (36 total).**

### Critical

**F-01 · Critical · BUG · Automations / tenant cleanup · P**
The nightly janitor can't authenticate with the secrets the workflow provides.
- Evidence: `.github/workflows/nightly.yml:80-98` gives the janitor job only `CAPELLA_SANDBOX_API_KEY`/`CAPELLA_SANDBOX_ORG_ID`. `internal/settings/settings.go:30-32,168-178,196-206` resolves only `--token`, `CAPELLA_API_KEY`, `CAPELLA_TOKEN`, the keychain, and `CAPELLA_ORG_ID`.
- Effect: `cbctl capella janitor` exits 3 with "Capella API key not set".
- Spec: phase-4 §5.4 (`docs/specs/capella-fold-in-phase-4.md:333`) says "Credentials: CAPELLA_SANDBOX_* preferred, else CAPELLA_API_KEY…".
- Why it matters: the weekly sweep is the **only** backstop for F-02 and F-03. Orphaned multi-node clusters keep billing indefinitely.

### High

**F-02 · High · BUG · Tenant cleanup · P (sharpened)**
Teardown is skipped on test timeout and on job cancel.
- Evidence: `nightly.yml:29` (`timeout-minutes: 300`) and `:60` (`go test -timeout 290m`). When `go test` hits its timeout it panics the test binary. `t.Cleanup` (`internal/integration/cleanup.go:29-54`) and `TestMain`'s post-`m.Run` work (`main_test.go:23-39`: `FinishShared`, report write) **don't run**. There's also no SIGINT/SIGTERM handling, so a GHA cancel kills the process with no cleanup.
- There's no `if: always()` run-scoped sweep step either.
- Effect: any hung step (for example a stuck scale wait) orphans every project from that run.

**F-03 · High · BUG · Tenant cleanup · N**
The shared sandbox project is orphaned when the shared apply partly fails.
- Evidence: `harness.go:235-247`. `sharedSandbox` is assigned only after `tryApplyShared` succeeds. `tryApplyShared` (`:260-284`) returns `nil, err` after `apply` may already have created the project and a 3-node `developer pro` multi-AZ cluster (`:342-369`). `FinishShared` (`report.go:76-79`) returns early when `sharedSandbox == nil`, so nothing deletes `cbctl-ci-<run>-shared`.
- Effect: combined with F-01, a failed shared apply leaves the most expensive resource in the suite running.

**F-04 · High · BUG · Automations · P**
The secret scan fails open and scans files that don't exist.
- Evidence: `nightly.yml:62-68` ends the step with `|| true`.
- `CBCTL_IT_LOG: integration.log` and `CBCTL_IT_REPORT: integration-report.json` (`:35-36`) are relative. `suiteLogPath` (`target.go:217-231`) and `TestMain` (`main_test.go:27-28`) use them as-is. `go test` runs the binary with CWD = package dir, so the files land in `internal/integration/`.
- The scanner runs at the repo root, gets "no such file", and returns 2 (`cmd/cbdev/ci_scan.go:62-73`), which `|| true` then masks.
- Effect: a green nightly proves nothing about secret leakage.

**F-05 · High · DRIFT · Automations · P**
The release gate never blocks.
- Evidence: `release.yml:31-36` passes `--report-only`, and `cmd/cbdev/ci_gate.go:63-68` then always returns 0. `goreleaser` `needs: gate` (`release.yml:38-39`) is satisfied on any outcome, and the floating `latest` force-push (`:65-86`) follows.
- Spec: the nightly.yml header (`:4-5`) says "A release tag needs three consecutive green scheduled runs".

**F-06 · High · BUG · Automations · N**
The release gate miscounts the weekly janitor run as a "nightly".
- Evidence: `internal/ci/releasegate/gate.go:29-35` counts every `completed` + `event == "schedule"` run of `nightly.yml`. `FetchRuns` (`:90-92`) requests `event=schedule` with no job filter. `nightly.yml` has two crons (`:9-10`); on Sundays the 09:00 janitor run is also a scheduled run of the same workflow.
- Results:
  - (a) Sunday has two runs on one UTC date, so the "consecutive nights" check (`gate.go:64-68`, diff must be 24h) fails for every window that spans Sunday.
  - (b) A janitor-only run whose integration job is skipped still concludes `success`. It counts as a green "night" without any integration testing.
  - (c) Because of F-01, the janitor run fails, which blocks the gate for three days every week.

**F-07 · High · BUG · Automations / tests · N**
With secrets missing, every test skips and the nightly goes green.
- Evidence: the nightly sets `CBCTL_IT_TARGET=sandbox`, but `sandboxFromEnv` (`target.go:146-158,193-195`) turns missing credentials into `t.Skip(skipMsg)` for every test. `TestCapellaLive` also skips (`capella_live_test.go:51-53`). `nightly.yml:54-60` adds another soft-skip when the harness is absent.
- Effect: deleted or expired secrets give a green "nightly" that the release gate would count as a pass.

**F-09 · High · DRIFT · Tenant isolation · N**
The live binary test falls back to developer credentials without an opt-in.
- Evidence: `capella_live_test.go:45-55` uses `CAPELLA_API_KEY`/`CAPELLA_ORG_ID` whenever the sandbox variables are unset. It also loads `.env` (`:34-41`) and doesn't require `CBCTL_IT_TARGET=sandbox`. With `CBCTL_IT_CLUSTERS=1` it creates clusters (`:179-229`).
- Spec: phase-4 line 270 says plain `CAPELLA_API_KEY` "**not** used by the suite unless `CBCTL_IT_TARGET=sandbox` is set explicitly". `make test-capella-live` (Makefile:32-33) and README:336 advertise exactly this path.
- Effect: a developer's production org gets billable test resources.

**F-27 · High · BUG/DRIFT · Capella CLI (spec check) · N**
Direct-mode `capella cluster create` opens the cluster to the world and creates a default-password user.
- Evidence: `wrapManaged` (`internal/cli/capella_managed.go:232-243`) sends "no capella.yaml or `--direct`" to the legacy `RunE` (`internal/cli/capella.go:58-83`). That path uses `capellaFlags` (`internal/cli/flags.go:73-88`), where `--allow-cidr` defaults to `0.0.0.0/0` and `-u/-p` default to `Administrator`/`password` (`internal/config/config.go:5-6,44`). `internal/capella/orchestrate.go:95-105` then creates the allow-list entry and a database credential from those defaults.
- The managed flags (`--size/--group/--cidr/...`, `capella_managed_ops.go:1250-1276`) are described as "file-managed mode", and the direct path ignores them.
- Contract violations (cbctl.mdc):
  - "Capella commands never take `--host`/`-u`/`-p`"
  - "Never pass a password as a flag value"
  - `cidr create` needs `--allow-any-ip` for `0.0.0.0/0` (`capella_cidr.go:136-143`)
  - Managed `cluster create` is documented as `--name N [--size …] [--group …]`
- I confirmed this by reading the code. Phase 5 pins the behavior with a fake test first.

**F-35 · High · BUG/DRIFT · Capella CLI (spec check) · N**
Legacy `capella cluster destroy` has no confirmation and ignores deletion protection.
- Evidence: `internal/cli/capella.go:105-131`. There's no `-y`/TTY check and no deletion-protection check. A missing cluster only gives a warning.
- Contract: cbctl.mdc says `cluster delete` needs `-y` without a terminal (exit 4) and "is refused while deletion protection is on (exit 4)". `destroy`, `exists`, `test` and `bucket create/exists` under `capella cluster`/`capella bucket` are not in the cbctl.mdc command map. Rule 5: "do not assume… Capella commands beyond those documented".

### Medium

**F-08 · Medium · TEST · Automations · N**
CI doesn't run on PRs that target the port branch. `ci.yml:3-7` triggers only on `push`/`pull_request` to `main`, so PRs into `mm--capellactl-port` (for example #8–#10) get no vet/unit/fake-IT/docs checks.

**F-10 · Medium · HARDEN · Tenant isolation · P**
There's no org allowlist; the `cbctl-ci-` name prefix is the only guard.
- The sandbox key is org-scoped. `ListProjects`/`ListClusters` run org-wide (`internal/ci/janitor/janitor.go:56-67,104-108`; `capella_live_test.go:146`).
- Nothing checks `OrgID` against an expected sandbox org.

**F-11 · Medium · BUG · Tests / secret scan · P (sharpened)**
`CBCTL_IT_SECRETS_DIR` is never written to, so generated passwords are never scanned.
- `suiteSecretsDir` (`target.go:234-250`) only creates the dir. No test writes credentials there: `lifecycle_test.go:33` uses `credentialsOutput: ./.cbctl/credentials`, which resolves under the test's temp dir (`internal/config/capellafile/model.go:112-120`).
- `secretscan.SecretsInDir` (`internal/ci/secretscan/scan.go:47-70`) and `redaction_test.go:43` therefore check an empty set. The `IT_CRED_PASSWORD` constant (`credentials_test.go:31`) isn't fed to the scanner either.
- Spec: phase-4 line 413 says "the suite honours `CBCTL_IT_SECRETS_DIR` for `credentialsOutput`".

**F-12 · Medium · TEST · Automations · P**
Artifacts are missing, and that only warns. `nightly.yml:70-78` uploads `integration.log`/`integration-report.json` from the repo root with `if-no-files-found: warn`. Because of F-04 the files are never there, so failed runs can't be debugged.

**F-13 · Medium · BUG · Tenant cleanup · N**
A partial failure in `DeleteProjectTree` abandons the rest of the tree.
- `janitor.go:68-85` returns on the first cluster's protection-off or delete failure, so it skips the remaining clusters and the project.
- `DeleteProjectsNamed` (`:110-119`) stops at the first failing project.
- The janitor CLI loops over projects (`capella_janitor.go:107-117`) but has a 60-minute default per-project timeout (`:59`) inside a 120-minute job (`nightly.yml:83`), so two stuck projects exhaust the job.

**F-15 · Medium · HARDEN · Automations · P**
- Actions are pinned by major tag (`actions/checkout@v4`, `setup-go@v5`, `upload-artifact@v4`, `goreleaser-action@v6`) in all three workflows, not by SHA.
- `release.yml` has workflow-level `permissions: contents: write` (`:9-10`) and no `timeout-minutes` on `goreleaser` (`:38-41`).

**F-16 · Medium · HARDEN · Tenant isolation · P**
The live test collides with the org's free-tier singleton. `capella_live_test.go:190-191,232-244` uses free tier whenever it's "available", which conflicts with any other free cluster in the org and with concurrent dispatches.

**F-17 · Medium · TEST · Hermetic tests · P**
The hermetic gate is soft and narrow, and `Prompter` is dead code in production.
- `hermetic_test.go:97-143` scans only 7 packages and skips files on an allow-list. It doesn't cover `internal/capella` (which has `time.Sleep`/`time.Now` at `cluster.go:274-286` and `connectivity.go:52-130`), `internal/rest`, `internal/redact` (`redact.go:107-114`), or `internal/capellaapi`.
- `Deps.Prompter` (`deps.go:36-37`) is only set by `clitest` (`internal/clitest/scenario.go:113`). Production `confirm` (`capella_output.go:333-351`) reads `ttyCheck` and stdin directly, so `ScriptedPrompter` doesn't drive real prompts.

**F-19 · Medium · TEST/DRIFT · Scenario port · P**
- `internal/cli/testdata/scenario-port.md` shows about 8 of 63 ported, 47 partial, 10 planned and about 5 N/A.
- The table header (`:5`) claims ``cbdev docs ac-coverage --port-table` checks that every test_*.py…``, but `cmd/cbdev/docs_check.go:33-35` prints "not implemented yet (skipped)".

**F-20 · Medium · TEST · Traceability · N (sharpened)**
AC coverage checks the wrong release.
- `Makefile:88-89` runs `ac-coverage --docs docs/backlog/release-1.5.0`, which is the *future* roadmap (CAP-064..089), so the result is 0/0.
- The release that actually landed is `release-1.0.0/stories.yaml`: 58 `Not started`, 1 `Implemented`, 4 `Descoped`, even though the commands exist.

**F-21 · Medium · TEST · Docs tests · P**
- `internal/docs/` contains only `workflows_test.go`. The Phase 4 §8.2 `doccmds`/`examples`/`links` tests are missing.
- `workflows_test.go:22-88` asserts none of the fail-closed properties: no `|| true` on the scan, no `--report-only`, absolute artifact paths, janitor env matching settings, or SHA pins.

**F-22 · Medium · TEST · Integration coverage · P**
- These tests are skipped:
  - SDK connect (`credentials_test.go:43`, which skips after creating the credential)
  - sandbox power (`power_test.go:23`)
  - clone (`clone_test.go:16-18`)
  - on-demand backup/restore (`backups_test.go:22-26`)
- `TestServerCLIFlow` (`server_test.go`, Docker plus testcontainers) runs inside the Capella sandbox job and can fail it for reasons unrelated to Capella.

**F-24 · Medium · HARDEN · Tenant isolation · C (replaces prior #10)**
- `projectPrefix()` (`target.go:283-292`) accepts any `CBCTL_TEST_PREFIX` and silently adds `-`, where the spec (phase-4:270) says "must end with `-`", meaning reject.
- Projects created under a custom prefix are **never** swept by the weekly janitor, which only targets `cbctl-ci-` (`nightly.yml:98`, `janitor.Prefix`).
- The prior "bare prefix wipes everything" claim is retracted (see §1.1).

**F-28 · Medium · DOC/DRIFT · Janitor contract · N**
The janitor docs don't match the code.
- cbctl.mdc:110,236: "`--prefix P [--yes]`… deletes projects starting with P" and "`--prefix` needs at least 3 characters". The code (`capella_janitor.go:38-61`) requires **≥4 characters ending in `-`**. It also has `--older-than-hours` (default **24**), `--dry-run`, `--fail-on-leftovers` and `--timeout-minutes`.
- README:316 says it "deletes every project whose name starts with `P`". README:337 `make janitor PREFIX=cbctl-ci-` deletes nothing younger than 24h.
- `Makefile:36-37` still says "Age filtering lands with the Phase-4 janitor CLI flags".

**F-36 · Medium · BUG · CLI runtime contract · N**
Legacy Capella commands call `os.Exit(1)` inside `RunE`.
- `exitErr()` (`internal/cli/root.go:90-92`) is used by the legacy Capella commands (`capella.go:66-68,118-120,146-148,163-166,…`).
- `cli.Run` promises "It never calls os.Exit" (`run.go:19-20`). Any legacy failure under the in-process harness kills the test binary, skips every `t.Cleanup` (orphans, like F-02), and bypasses `HandleError` exit-code mapping and redaction.

### Low

**F-14 · Low · HARDEN · Automations · C**
- Constant concurrency group `nightly-sandbox` (`nightly.yml:21-23`) serializes runs, which is good.
- But GitHub allows only one pending run per group: a newer `workflow_dispatch` replaces (cancels) a pending scheduled nightly, so a night goes missing and the gate streak breaks.

**F-18 · Low · BUG · Hermetic tests · N**
`redact.Reset()` runs **before** `runMu.Lock()` (`run.go:23-28`). A concurrent `Run` with `ResetRedaction: true` (`hermetic_test.go:47,80`) can wipe secrets registered by an in-flight run, so test output could leak unredacted values.

**F-23 · Low · TEST · Coverage · P**
`internal/resources` is at 85.1%, against an 85% gate. `cover_helpers_test.go` tests helpers line by line (`TestCoverHelpers_util`) instead of behavior, so the gate is one refactor from flapping.

**F-25 · Low · HARDEN · Live test · N**
`liveCapella.exec` (`capella_live_test.go:95-104`) uses `exec.Command` without a context or timeout. A hung `cbctl` consumes the whole `go test -timeout` budget, which then causes F-02.

**F-26 · Low · HARDEN · Tenant cleanup · P · DEFERRED**
App Services and resources outside the prefix are never swept. `TurnClusterOn` can pass `withAppService` (`internal/capellaapi/endpoints_cluster.go:76-84`), but the suite creates no App Services today.

**F-29 · Low · DOC · README / testing guide · N**
- README:335 says `make test-integration # Docker required; Couchbase enterprise image`. That Makefile target (`:23-29`) is actually the **Capella sandbox** suite.
- README:336,341 and `capella_live_test.go:24` document the deprecated `CBCTL_LIVE_CLUSTER`; the code prefers `CBCTL_IT_CLUSTERS` (`target.go:263-276`).
- `docs/capella/testing.md` is a 12-line stub.

**F-30 · Low · TEST · Automations · P**
The soft-skips for a missing harness (`Makefile:15-21,24-29`; `nightly.yml:55-58`) are dead now that `harness.go` exists, but they still fail open.

**F-31 · Low · BUG · Janitor · N**
- `createdAt` (`janitor.go:137`): `!strings.ContainsAny(CreatedAt, "Z+-")` is never true, because any date contains `-`, so the branch is dead.
- A project with unreadable `createdAt` is skipped forever (`:39-43`) without escalation.

**F-32 · Low · DRIFT · Release gate · N**
`ci_gate.go:18` falls back to `couchbaselabs/cbctl`, but the module is `github.com/mminichino/cbctl`. Running it locally without `GITHUB_REPOSITORY` queries the wrong repo.

**F-33 · Low · TEST · Tooling · N**
general.mdc says "Code must pass `go vet` and `golangci-lint`", but there's no `.golangci.yml` and no CI lint job.

**F-34 · Low · TEST · Env-sensitive test · N**
`internal/docgen/port` `TestPort_RefusesDirtySourceUnlessForce` (`port_dirty_test.go:20`) needs a writable `.git/hooks` template dir and fails under sandboxed or read-only runners.

## 3. Remediation plan

Phase order follows risk: stop orphan billing and fail-open CI first, then the user-facing security defect, then evidence, then gates. Each phase is one reviewable PR off `mm--capellactl-port`, or off `main` once the port merges. Phase 3 is independent and can be done in parallel with Phases 1–2.

| Phase | Branch | Resolves |
|---|---|---|
| 1 | `mm--it-sandbox-auth-preflight` | F-01, F-07, F-09, F-10, F-16, F-24, F-30 |
| 2 | `mm--it-teardown-hardening` | F-02, F-03, F-13, F-25, F-28, F-31 |
| 3 | `mm--capella-legacy-cmd-safety` | F-27, F-35, F-36 |
| 4 | `mm--it-evidence-secret-scan` | F-04, F-11, F-12, F-18 |
| 5 | `mm--release-gate-correctness` | F-05, F-06, F-08, F-14, F-15, F-32 |
| 6 | `mm--test-traceability-gates` | F-17, F-19, F-20, F-21, F-22, F-23, F-29, F-33, F-34 |
| Deferred | none | F-26: the suite creates no App Services today. Revisit when App Service commands land (sweep App Services in `DeleteProjectTree` before clusters). |

### Phase 1: Sandbox auth, preflight and tenant guards (`mm--it-sandbox-auth-preflight`)
Resolves F-01, F-07, F-09, F-10, F-16, F-24, F-30. Implements **D1**: the fix lives only in workflow env mapping, with no CLI settings change.

**File/module structure**
- `.github/workflows/nightly.yml` (changed):
  - Add the D1 mapping to the job-level `env:` of **both** the `integration` and `janitor` jobs:
    `CAPELLA_API_KEY: ${{ secrets.CAPELLA_SANDBOX_API_KEY }}`
    `CAPELLA_ORG_ID: ${{ secrets.CAPELLA_SANDBOX_ORG_ID }}`
    The janitor step (`go run ./cmd/cbctl capella janitor …`) then authenticates through the normal `settings.Resolve` precedence (`CAPELLA_API_KEY` → `SourceEnv`).
  - Keep the existing `CAPELLA_SANDBOX_*` env entries only for the integration harness (`internal/integration/target.go`, which is test code) and for `scan-secrets --env CAPELLA_SANDBOX_API_KEY`. The janitor job drops them, since nothing there reads them.
  - Add `CBCTL_IT_REQUIRE_TARGET: "1"` and `CBCTL_IT_ALLOWED_ORG_IDS: ${{ vars.CAPELLA_SANDBOX_ORG_ID }}` (a repo *variable*, so it isn't redacted and can be compared).
  - Add a first "Preflight credentials" step to both jobs: `test -n "$CAPELLA_API_KEY" && test -n "$CAPELLA_ORG_ID" || { echo "::error::CAPELLA_SANDBOX_API_KEY/ORG_ID secrets are not configured"; exit 1; }`.
  - Remove the harness-absent `exit 0` branch.
- `Makefile` (changed): drop the harness-absent soft-skips in `test-integration-fake` and `test-integration`.
- `internal/integration/guard.go` (**new**, `//go:build integration`): org allowlist, prefix validation, and the "require target" policy.
- `internal/integration/target.go` (changed): call the guards from `TargetFromEnv`, `sandboxFromEnv` and `projectPrefix`.
- `internal/integration/capella_live_test.go` (changed): use the guards. Fall back to `CAPELLA_API_KEY` only when `CBCTL_IT_TARGET=sandbox`. Use free tier only when `CBCTL_IT_FREE_TIER=1`.
- `internal/integration/main_test.go` (changed): after `m.Run`, exit 1 when the target is required but no target ran.
- `internal/integration/guard_test.go` (**new**): table tests (runs under the `integration` tag with `CBCTL_IT_TARGET=fake` in CI).
- `internal/settings/settings_test.go` (changed): add a regression test that `Resolve` with only `CAPELLA_SANDBOX_*` set still returns `APIKey.Source == SourceNone`. This locks in D1: test-only names never reach the product resolver.
- `internal/docs/workflows_test.go` (changed): assertions for the mapping (see Acceptance).
- `docs/specs/capella-fold-in-phase-4.md` §5.4 (changed): replace "`CAPELLA_SANDBOX_*` preferred, else `CAPELLA_API_KEY` (via settings precedence)" with "the CLI reads only `CAPELLA_API_KEY`/`CAPELLA_ORG_ID`; CI maps the sandbox secrets onto them in workflow env".
- **Not changed:** `internal/settings/settings.go`, `internal/cli/capella_janitor.go`. Dependency direction is unchanged: `integration` → `cli`, `ci/janitor`, `capellaapi`.

**Interfaces and function signatures**
```go
// guard.go
type GuardPolicy struct {
    RequireTarget bool     // CBCTL_IT_REQUIRE_TARGET=1
    AllowedOrgs   []string // CBCTL_IT_ALLOWED_ORG_IDS, comma-separated, trimmed
    InCI          bool     // GITHUB_ACTIONS=="true"
}
func PolicyFromEnv(env func(string) string) GuardPolicy
func (p GuardPolicy) CheckOrg(orgID string) error            // ErrOrgNotAllowed
func ValidatePrefix(p string, inCI bool) (string, error)       // ErrBadPrefix
func (p GuardPolicy) SkipOrFail(t testing.TB, reason string)   // t.Fatalf when RequireTarget, else t.Skip

var (
    ErrOrgNotAllowed = errors.New("sandbox org is not in CBCTL_IT_ALLOWED_ORG_IDS")
    ErrBadPrefix     = errors.New("CBCTL_TEST_PREFIX must start with cbctl-ci- and end with '-'")
)

// target.go
func projectPrefix() (string, error) // was: func projectPrefix() string
func SuiteRequiredButUnused() bool    // for TestMain

// settings_test.go (regression for D1; no production signature changes)
func TestResolve_IgnoresSandboxEnv(t *testing.T)
```
No settings or janitor function signatures change. `settings.Resolve(Inputs) (*Settings, error)` and `(*runtime).runJanitor(...)` stay as they are.

**Data models**
- `GuardPolicy` as above.
- Prefix rule: `^cbctl-ci-([a-z0-9]+-)*$`, length 9–40. When `InCI` is true, any value other than the default `cbctl-ci-` is rejected, so the weekly janitor always matches.
- Allowed orgs: a set of UUID strings. An empty set with `InCI` is an error. An empty set locally gives a warning (`t.Log`) and is allowed.

**Error handling**

| Failure | Detected | Surfaced | Recovery |
|---|---|---|---|
| Secrets missing in CI (the mapped `CAPELLA_API_KEY`/`CAPELLA_ORG_ID` come out empty) | Preflight step `test -n` in each job | `::error::` annotation, step exit 1, job red, before any `go run`/`go test` | Configure the repo secrets |
| Mapping removed or typo'd in a later edit | `workflows_test.go` in CI | Unit test failure on the PR | Restore the mapping |
| Secrets missing, `RequireTarget` | `SkipOrFail` | `t.Fatalf`, and `TestMain` exits 1 | Same |
| Org not allowlisted | `CheckOrg` in `sandboxFromEnv`/`newLiveCapella` **before any API call** | `t.Fatalf` with the org ID (not secret) | Fix the secret/variable |
| Bad prefix | `ValidatePrefix` | `t.Fatalf` | Unset or fix `CBCTL_TEST_PREFIX` |
| Janitor auth (F-01) still fails (key revoked or wrong org) | `settings.Resolve` ok, then the API returns 401/403 | Janitor exits 1 ("listing projects: …"); exit 3 "API key not set" is now impossible past preflight | Rotate the secret |

**Edge cases**
- Secret values with trailing newlines or quotes: `settings.SanitizeAPIKey` already handles these on the `CAPELLA_API_KEY` path, so no extra handling is needed.
- A job that also sets `CAPELLA_TOKEN`: `CAPELLA_API_KEY` wins by existing precedence (`settings.go:173-178`). The workflow must not set `CAPELLA_TOKEN`, and `workflows_test` asserts that.
- Redaction: the mapped value is registered by `settings.Resolve` (`redact.Register`), and GitHub also masks it as a secret.
- A local developer who has `CAPELLA_SANDBOX_*` exported and runs `cbctl capella janitor` by hand: per D1 it is **not** picked up. They must export `CAPELLA_API_KEY`/`CAPELLA_ORG_ID`, and README says so.
- A local developer with only `CAPELLA_API_KEY` and no `CBCTL_IT_TARGET`: every test skips, including `TestCapellaLive` (changed from today).
- `CBCTL_IT_TARGET=sandbox` with only `CAPELLA_API_KEY`: allowed, logged, and the org check still applies.
- An allowlist with whitespace or an empty entry: trimmed and dropped.
- `.env` loaded by `TestMain`: the guards run after `.env` is applied, so `.env` can't bypass them.
- Free tier unavailable but `CBCTL_IT_FREE_TIER=1`: fall back to provisioned and log it.
- Concurrent `sync.Once` targets: the guards are pure functions, so there's nothing shared to race on.

**Acceptance criteria**
- `go test -tags=integration -run TestGuard ./internal/integration/` (fake) covers: allowlisted org passes; non-listed org returns `ErrOrgNotAllowed`; `cbctl-ci-x-` passes locally and fails in CI; `foo` fails; `RequireTarget` turns a skip into a failure.
- In `env -i PATH=$PATH HOME=$HOME CAPELLA_API_KEY=x CAPELLA_ORG_ID=y go test -tags=integration -run TestCapellaLive ./internal/integration/`, `TestCapellaLive` **skips**.
- In `CBCTL_IT_TARGET=sandbox CBCTL_IT_REQUIRE_TARGET=1 go test -tags=integration ./internal/integration/` with no credentials, the exit is non-zero.
- `internal/docs/workflows_test.go` asserts, by parsing `jobs.<job>.env` as YAML:
  - in both the `integration` and `janitor` jobs, `CAPELLA_API_KEY == "${{ secrets.CAPELLA_SANDBOX_API_KEY }}"` and `CAPELLA_ORG_ID == "${{ secrets.CAPELLA_SANDBOX_ORG_ID }}"`;
  - neither job sets `CAPELLA_TOKEN`;
  - a preflight step is the first non-checkout step in each job;
  - `CBCTL_IT_REQUIRE_TARGET: "1"` is set;
  - there's no `exit 0` in the integration step.
- `go test -run TestResolve_IgnoresSandboxEnv ./internal/settings/` passes. `rg -n CAPELLA_SANDBOX internal/settings internal/cli --glob '!*_test.go'` returns nothing.
- Locally, `env -i PATH=$PATH HOME=$(mktemp -d) CAPELLA_SANDBOX_API_KEY=x CAPELLA_SANDBOX_ORG_ID=y go run ./cmd/cbctl capella janitor --dry-run` exits 3 (unchanged, as intended). With `CAPELLA_API_KEY=x CAPELLA_ORG_ID=y` instead it gets past settings resolution and fails at the API with exit 1, which shows the mapped names are the ones used.
- A manual `workflow_dispatch` of the `janitor` job on the sandbox reaches `ListProjects` (no exit 3).

### Phase 2: Teardown that survives timeout, cancel and partial failure (`mm--it-teardown-hardening`)
Resolves F-02, F-03, F-13, F-25, F-28, F-31.

**File/module structure**
- `internal/ci/janitor/janitor.go` (changed):
  - `DeleteProjectTree` keeps going after a per-cluster error and returns a joined error.
  - `DeleteProjectsNamed` keeps going across projects.
  - New `DeleteRun` sweeps by run prefix.
  - Fix the `createdAt` dead branch.
- `internal/ci/janitor/janitor_test.go` (changed): partial-failure tables against `capellafake`.
- `internal/integration/harness.go` (changed): `tryApplyShared` registers the shared project name for teardown **before** `apply`.
- `internal/integration/report.go` (changed): `FinishShared` tears down by name even when `sharedSandbox == nil`, using a recorded `sharedProjectName`.
- `internal/integration/main_test.go` (changed):
  - Add a deadline watchdog: when `-test.timeout` minus a margin is reached, run the run-prefix sweep, then let the binary exit.
  - Add a SIGINT/SIGTERM handler that runs the same sweep.
- `internal/integration/capella_live_test.go` (changed): `exec.CommandContext` with a per-command timeout.
- `.github/workflows/nightly.yml` (changed):
  - `go test -timeout 270m`, leaving 30 minutes for the sweep inside the 300-minute job.
  - New step `if: always()`: `go run ./cmd/cbctl capella janitor --prefix "cbctl-ci-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}-" --older-than-hours 0 --yes --timeout-minutes 20`.
  - Janitor job: `--timeout-minutes 25`.
- `internal/cli/capella_janitor.go` (changed): add `--max-total-minutes` (overall budget). Keep the existing flags.
- `Makefile` (changed): `janitor` target passes `--older-than-hours $(or $(HOURS),24)`, and the stale comment goes.
- `README.md` and `.cursor/rules/cbctl.mdc` (changed): janitor contract (F-28), see §4.

**Interfaces and function signatures**
```go
// janitor
func DeleteProjectTree(ctx context.Context, c *capellaapi.Client, p capellaapi.Project, timeout time.Duration, clk engine.Clock) ([]string, error) // now: best-effort over all clusters; errors.Join
func DeleteProjectsNamed(ctx context.Context, c *capellaapi.Client, name string, timeout time.Duration, clk engine.Clock) ([]string, error)           // now: continues across projects
func DeleteRun(ctx context.Context, c *capellaapi.Client, runPrefix string, timeout time.Duration, clk engine.Clock) (Summary, error)
type Summary struct{ Deleted, Failed []string; Steps []string }

// integration
func RunPrefix() string                                      // projectPrefix()+runID()+"-"
func SweepRun(ctx context.Context, tg *Target) (Summary, error) // used by watchdog, signal handler, FinishShared
func installTeardownWatchdog(m *testing.M, margin time.Duration) (stop func())
```

**Data models**
- `Summary` holds ordered names (exact project names) for deleted and failed projects, plus the step log.
- `ReportEntry` gains `Teardown` values `"swept: watchdog"` and `"swept: signal"`.
- Run prefix format: `cbctl-ci-<GITHUB_RUN_ID>-<ATTEMPT>-` (or `local<unix>-`). It must pass Phase 1's `ValidatePrefix`, with `DeleteRun` additionally requiring `len(runPrefix) > len("cbctl-ci-")` so a sweep can never be org-wide.

**Error handling**

| Failure | Detected | Surfaced | Recovery |
|---|---|---|---|
| One cluster delete fails | `retry` error | Collected; other clusters and the project still attempted; `errors.Join` returned; janitor exit 1 | Weekly janitor |
| Shared apply fails after the project was created (F-03) | `tryApplyShared` error | Shared name already registered; `FinishShared` deletes it; report entry `FAILED`/`ok` | Automatic |
| `go test` deadline near (F-02) | Watchdog timer from `flag.Lookup("test.timeout")` | `SweepRun` then stderr summary; process exit non-zero | Post-step janitor sweep (always()) |
| SIGTERM/SIGINT | `signal.NotifyContext` | Same sweep with a bounded context (10 min) | Post-step sweep |
| Total janitor budget exceeded | `--max-total-minutes` | Remaining projects reported as `skipped (budget)`, exit 1 (exit 5 is kept for the product's wait-timeout semantics) | Next run |
| Hung CLI subprocess (F-25) | `CommandContext` deadline | `t.Fatalf` "timed out after N" | Cleanup runs normally |

**Edge cases**
- The watchdog margin has to be shorter than the time left but long enough for a delete. Use `min(25m, timeout/10)`. If `-test.timeout` is 0, there's no watchdog.
- A sweep running at the same time as an in-flight `t.Cleanup` deleting the same project: a 404 is treated as success (`ErrNotFound` is already handled at `janitor.go:63`). Extend that to the per-cluster delete and `DeleteProject`.
- Deletion protection toggled back on by a test mid-sweep: the retry loop calls `SetDeletionProtection(false)` again before each attempt.
- `createdAt` with no zone: parse as UTC explicitly (fix the `ContainsAny` logic, F-31). Unreadable timestamps still get skipped, but the janitor now prints a `warning` line and lists them under `res.Unreadable`.
- A `DeleteRun` prefix equal to the default `cbctl-ci-`: refused with `ErrBadPrefix` (exit 3 at the CLI).

**Acceptance criteria**
- `go test ./internal/ci/janitor/ -run 'TestDeleteProjectTree_PartialFailure|TestDeleteProjectsNamed_Continues|TestDeleteRun_RefusesBarePrefix|TestCreatedAt_NoZoneIsUTC'` passes (fake injects 500 on cluster #1 and expects cluster #2 and the project to still get deletion attempts).
- Fake IT `TestShared_ApplyFailureStillTornDown`: the fake rejects cluster create after project create; after `TestMain`, `fake.Projects()` contains no `-shared`.
- Fake IT `TestWatchdog_SweepsOnDeadline` with an injected short timeout and FakeClock: the project is deleted, and the report says `swept: watchdog`.
- `workflows_test.go` asserts: an `always()` sweep step exists and uses `GITHUB_RUN_ID`; the `go test -timeout` is at least 20 minutes below `timeout-minutes`.
- `cbctl capella janitor --help` lists every flag the docs mention, checked by a doc test that greps README/cbctl.mdc for janitor flags.

### Phase 3: Legacy Capella commands meet the cbctl.mdc contract (`mm--capella-legacy-cmd-safety`)
Resolves F-27, F-35, F-36. Implements **D2**. Independent of Phases 1–2.

**D2 scope and replacement map.** "Hidden" means `cmd.Hidden = true`: still callable, but not listed in `--help` or the generated reference.

| Legacy command | Replacement named in the warning | Safety fix |
|---|---|---|
| `capella cluster create` (**legacy direct path only**, see the note below) | `cbctl capella cluster create --name N [--size …] [--group …] --direct`, then `cbctl capella cidr create` / `cbctl capella credential create` | `--allow-cidr` has no default; `0.0.0.0/0`/`::/0` need `--allow-any-ip`; no credential unless `--username` + `--password-env/--password-file` |
| `capella cluster exists` | `cbctl capella cluster get <cluster>` (exit 3 when not found) | none (read-only); stdout stays `true`/`false` |
| `capella cluster destroy` | `cbctl capella cluster delete <cluster> -y` | Delegates to `cluster delete`: needs `-y` without a TTY (exit 4) and is refused while deletion protection is on (exit 4) |
| `capella cluster test` | `cbctl capella credential create <cluster> <name> …`, then `cbctl cluster test --host <cluster host> --ssl -u <name> -p …` (Server CLI). **Confirm in this PR that `cluster test` accepts a Capella connection host; if it doesn't, the warning names `cbctl capella cluster get` only.** | No `Administrator`/`password` default: `--username` required; password only from `--password-env`/`--password-file`; a literal `-p` gives exit 3 |
| `capella bucket create` | `cbctl capella bucket add <cluster> <name> [--memory MB] [--replicas N]` | Same credential rule as `cluster test` (the legacy path connects with database credentials) |
| `capella bucket exists` | `cbctl capella bucket get <cluster> <name>` | Same credential rule |

**Note on `cluster create`:** this is an interpretation of D2. The `cluster create` *command* stays visible, because it's the documented managed command in cbctl.mdc. D2 applies to its **legacy direct implementation**, meaning the path that runs when there's no capella.yaml or `--direct` is passed and that reads the legacy `capellaFlags`. Those legacy-only flags (`--database`, `--database-id`, `--org`, `--project-id`, `--user-email`, `--user-id`, `--allow-cidr`, `-u/--username`, `-p/--password`) are hidden. Using any of them prints the deprecation warning. Direct-mode `cluster create` driven by the managed flags (`--name`, `--size`, …) gets the new builder path from F-27, with no warning. Flag this interpretation to the user in the PR description.

**File/module structure**
- `internal/cli/capella_legacy.go` (**new**):
  - Holds the `legacyCommands` table above and the `deprecateLegacy` wrapper.
  - The wrapper sets `Hidden`, prepends "Deprecated: use …" to `Long`, and wraps `RunE` so the warning goes to **stderr** before the original runs.
  - Cobra's built-in `Command.Deprecated` and `MarkDeprecated` are **not** used: cobra prints those through `OutOrStderr()`, which is stdout once `cli.Run` calls `root.SetOut`. That would corrupt the `true`/`false` stdout of `exists`.
- `internal/cli/capella.go` (changed):
  - Call `deprecateLegacy` for `cluster exists|destroy|test` and `bucket create|exists`.
  - `cluster destroy` `RunE` now resolves the cluster and calls the same function as `cluster delete`, inheriting `confirm`, the protection check and the wait.
  - `cluster create`'s direct path splits: legacy flags set → warning plus the safe legacy path; otherwise → `runDirectClusterCreate` (builder).
- `internal/cli/flags.go` (changed), `capellaFlags`:
  - `username` default `""` (was `config.DefaultUser`).
  - `password` flag removed and replaced by `--password-env` and `--password-file`. A hidden `-p/--password` stays registered only so a literal value can be rejected with exit 3 and a hint.
  - `allowCIDR` default `""` (was `config.DefaultCapellaAllowCIDR`).
  - New `--allow-any-ip`.
  - Legacy-only flags get `MarkHidden`.
- `internal/capella/orchestrate.go` (changed): `allowCIDR == ""` means no allow-list call (no `DefaultCapellaAllowCIDR` fallback, `:97`). An empty username or password means no `CreateCredential` call (`:104-105`).
- `internal/config/config.go` (changed):
  - Delete `DefaultCapellaAllowCIDR`.
  - `DefaultUser`/`DefaultPassword` stay for **Server** commands only, and a comment says so.
  - `capellaFlags.configFrom` no longer seeds `Password: DefaultPassword` into Capella configs (`config.go:69`, used by `flags.go:148`).
- `internal/cli/root.go` (changed): delete `exitErr()`. Every legacy call site returns `Errorf(ExitError, …)` (F-36).
- `internal/cli/capella_managed_ops.go` (changed): the builder is reusable by `runDirectClusterCreate`.
- `internal/docgen/reference` (changed if needed): skip hidden commands, or list them under "Deprecated" with the replacement, so `docs reference --check` stays deterministic.
- Tests:
  - `internal/cli/capella_legacy_test.go` (**new**)
  - `internal/cli/capella_cluster_create_direct_test.go` (**new**)
  - `internal/cli/capella_cluster_lifecycle_test.go` (changed)
- Docs: README, `.cursor/rules/cbctl.mdc`, CHANGELOG (§4).

**Interfaces and function signatures**
```go
// capella_legacy.go
type legacyCommand struct {
    Path        []string // e.g. {"cluster", "exists"}
    Replacement string   // shown verbatim in the warning
}
var legacyCommands = []legacyCommand{ /* table above */ }
func deprecateLegacy(cmd *cobra.Command, replacement string)        // Hidden=true; Long prefix; RunE wrapper
func warnDeprecated(w io.Writer, path, replacement string)          // "[warning] - 'cbctl capella cluster exists' is deprecated and will be removed; use 'cbctl capella cluster get <cluster>'"
func usesLegacyFlags(cmd *cobra.Command) bool                       // any hidden legacy flag Changed()

// flags.go
func (f *capellaFlags) resolvePassword(env func(string) string) (string, error) // --password-env/--password-file; literal -p → exit 3
func validateAllowCIDR(cidr string, allowAny bool) (string, error)            // shared with `cidr create` (capella_cidr.go:136-143)

// capella_managed_ops.go
func buildClusterCreate(cmd *cobra.Command, provider string) (capellaapi.CreateClusterRequest, error)
// capella.go
func runDirectClusterCreate(rt *runtime, req capellaapi.CreateClusterRequest, access directAccess) error
func runLegacyClusterCreate(cmd *cobra.Command, cf *capellaFlags) error       // safe legacy path, returns errors (no os.Exit)
type directAccess struct {
    AllowCIDR   string // "" = none
    AllowAnyIP  bool
    CredName    string
    PasswordRef string // from --password-env / --password-file; never a literal
}
func (a directAccess) Validate() error // exit 3 on 0.0.0.0/0 without AllowAnyIP, on password without CredName
```

**Data models**
- `legacyCommand` table: 6 entries. Each `Path` must resolve with `capella.Find(Path)`; startup panics if it doesn't, the same as `installManagedModes`. `Replacement` must name a command that exists in cbctl.mdc, and a test enforces that.
- `directAccess` as above.
- `capellaFlags` changes:
  - `username string` (default `""`)
  - `passwordEnv string`, `passwordFile string`
  - `allowCIDR string` (default `""`)
  - `allowAnyIP bool`
  - the `password` literal flag is kept hidden only so it can be rejected
- Validation:
  - `AllowCIDR` parses with `net.ParseCIDR`, or as a bare IP that becomes `/32` or `/128`, matching `cidr create`.
  - `CredName`/`--username` matches the existing credential-name rule.
  - The resolved password is at least 8 characters.
  - A username without a password source (or the reverse) gives exit 3.

**Error handling**

| Failure | Exit |
|---|---|
| `0.0.0.0/0`/`::/0` without `--allow-any-ip` (legacy or direct create) | 3 (usage: "pass --allow-any-ip if you really mean it") |
| Literal `-p VALUE` on a Capella command | 3 (usage: "Capella commands never take a password flag value; use --password-env or --password-file") |
| `cluster test` / `bucket create|exists` without `--username` plus a password source | 3 (usage), and **no** fallback to `Administrator`/`password` |
| `cluster destroy` with no TTY and no `-y`; deletion protection on | 4 (same as `cluster delete`) |
| Create or wait timeout | 5 |
| Ctrl-C while waiting | 130 (the operation continues in Capella; existing wait semantics) |
| API errors, including failures in the legacy backend | 1, via `HandleError` (redacted). No more `os.Exit` from `RunE` |
| Already exists | Prints "already exists" and exits 0, per cbctl.mdc |

The deprecation warning never changes the exit code. It's printed once per invocation, before validation, so a script that fails still shows it.

**Edge cases**
- `capella cluster exists` in scripts that parse stdout: stdout is still exactly `true`/`false`, and the warning only goes to stderr.
- `-o json` on a legacy command: the warning stays on stderr, so the JSON document on stdout stays valid.
- `--quiet`/`CBCTL_QUIET`: the warning is **still** printed, because it's a contract change users must see. This is documented.
- A hidden command invoked with `--help`: it still works, and the help starts with "Deprecated: use …".
- Cobra prefix matching (`EnablePrefixMatching`): hidden commands remain matchable. Add tests that `capella cluster ex` still resolves and isn't ambiguous.
- Scripts that relied on the open `0.0.0.0/0` default or on `Administrator`/`password`: they now fail fast with exit 3, and the CHANGELOG has a "Breaking" note.
- `--database` used as a legacy alias for `--name` without other legacy flags: it's accepted on the builder path, with no warning.
- Free tier via `--free-tier` in direct mode: routed to the free-tier create endpoint; `--group/--size` are rejected (exit 3).
- `cluster destroy` of a missing cluster: no-op, exit 0, matching `cluster delete`.
- A `capella.yaml` that exists alongside `--direct`: the existing notice "capella.yaml was not updated" is kept.

**Acceptance criteria**
- **Hidden:** `TestLegacyCommandsHidden` (table over `legacyCommands`) checks that each one has `cmd.Hidden == true`. `cbctl capella cluster --help` and `cbctl capella bucket --help` don't list `exists`, `destroy`, `test` or `create` (bucket), but do still list the managed `cluster create`.
- **Deprecation warning:** `TestLegacyCommandsWarn` runs each legacy command through `cli.Run` against `capellafake` and checks three things:
  - stderr contains `[warning] - 'cbctl capella <path>' is deprecated` plus that entry's `Replacement`;
  - stdout does **not** contain `deprecated`;
  - for `cluster exists`, stdout is exactly `true\n` or `false\n`.
- **Warning on a legacy create:** `cbctl capella cluster create --database c1 --direct` (legacy flag) prints the warning naming `cbctl capella cluster create --name N … --direct`. `cbctl capella cluster create --name c1 --size small --direct` prints **no** warning.
- **Replacements exist:** `TestLegacyReplacementsDocumented` checks that every `Replacement` command path resolves with `Find`, and that `.cursor/rules/cbctl.mdc` contains it.
- **No open default:** legacy `cluster create` without `--allow-cidr` makes **no** allowed-CIDR POST (fake request log). `--allow-cidr 0.0.0.0/0` exits 3. `--allow-cidr 0.0.0.0/0 --allow-any-ip` posts it.
- **No hard-coded user:** legacy `cluster create` without `--username` makes **no** credential POST. `rg -n 'DefaultPassword|DefaultUser' internal/cli internal/capella --glob '!*_test.go'` matches only Server code paths. `cluster test` without `--username` exits 3. `-p secret` on any Capella command exits 3 with the password-flag hint. `--username app --password-env APP_PW` works, and the fake credential POST carries `app`.
- **Builder path:** `cbctl capella cluster create --name c1 --size small --direct` gives a fake POST body whose node count, CPU and RAM match the `small` preset, with no CIDR or credential POSTs.
- **Destroy safety:** `cbctl capella cluster destroy c1` with no TTY and no `-y` exits 4. With protection on and `-y`, it also exits 4. A missing cluster exits 0.
- `rg -n 'exitErr\(|os\.Exit\(' internal/cli --glob '!*_test.go'` matches only `cmd/cbctl/main.go`-adjacent entry code. A unit test runs a failing legacy command through `cli.Run` and gets code 1 without the process exiting.
- **Docs:** `make docs-check` passes with `docs/capella/reference.md` regenerated. Hidden commands are absent, or listed under "Deprecated" with the replacement. README and cbctl.mdc carry the deprecation notes in §4. `rg -n 'capella cluster (exists|destroy|test)|capella bucket (create|exists)' README.md .cursor/rules/cbctl.mdc` matches only the deprecation notes.

### Phase 4: Evidence and a secret scan that fails closed (`mm--it-evidence-secret-scan`)
Resolves F-04, F-11, F-12, F-18.

**File/module structure**
- `internal/integration/target.go` (changed): `suiteLogPath`, `suiteSecretsDir` and a new `suiteReportPath` resolve relative paths against `GITHUB_WORKSPACE`, falling back to the module root (the `go.mod` directory).
- `internal/integration/main_test.go` (changed): use `suiteReportPath()`.
- `internal/integration/harness.go` (changed): new `(*Target).CredentialsDir(purpose)` returning `<SecretsDir>/<run>-<purpose>`.
- `internal/integration/lifecycle_test.go` and `credentials_test.go` (changed): `credentialsOutput` points at `tg.CredentialsDir(...)`. Supplied passwords (`IT_CRED_PASSWORD`) get written to `<SecretsDir>/supplied-<name>.json` as `{"password": "…"}` (0600) so the scanner knows them.
- `cmd/cbdev/ci_scan.go` (changed): new `--require-secrets N` (fail when fewer than N directory secrets are found) and `--allow-missing FILE` (explicitly optional files). A missing file is still exit 2.
- `.github/workflows/nightly.yml` (changed):
  - Remove `|| true`.
  - Use `${{ github.workspace }}/integration.log` and `.../integration-report.json`.
  - Use `if-no-files-found: error`.
  - Pass `--require-secrets 1` when `CBCTL_IT_CLUSTERS == "1"`.
- `internal/cli/run.go` (changed): move `redact.Reset()` inside `runMu` (F-18).
- `internal/ci/secretscan/scan.go` (changed): `SecretsInDir` also reads credential files in the format `credentialsOutput` actually writes (confirm the format in this PR; add a parser for it if it isn't JSON with a `password` field).

**Interfaces and function signatures**
```go
func resolveArtifactPath(p string, env func(string) string) (string, error) // abs passthrough; rel → $GITHUB_WORKSPACE or module root
func findModuleRoot(start string) (string, error)                           // walks up to go.mod; ErrNoModuleRoot
func suiteReportPath() string
func (tg *Target) CredentialsDir(purpose string) string                     // creates 0700
func RecordSuppliedSecret(tg *Target, name, value string) error             // writes 0600 JSON for the scanner
// cbdev
func parseScanArgs(args []string) (scanArgs, error)
type scanArgs struct{ Files, Envs, Dirs, ValueFiles, AllowMissing []string; RequireSecrets int }
```

**Data models**
- `scanArgs` as above.
- The supplied-secret file is `{"password": string}` with mode 0600, inside `SecretsDir` only.
- Report JSON is unchanged: `{target, run, projects[{project, failure, teardown}]}`.

**Error handling**

| Failure | Detected | Exit / surfaced |
|---|---|---|
| Hit (a secret in the logs) | `Scan` | 1, prints file:line + source label (never the value) |
| Log/report missing | `os.ReadFile` | 2. The step fails, and the upload step fails too (`error`) |
| Fewer directory secrets than `--require-secrets` | count | 2 "expected ≥N generated secrets in DIR, found M" |
| No module root and no `GITHUB_WORKSPACE` | `findModuleRoot` | Fall back to `os.MkdirTemp` and log the path (local runs only) |
| Write failure on the supplied secret | `RecordSuppliedSecret` | `t.Fatalf` |

**Edge cases**
- An all-skip run: Phase 1 makes this fail before the scan. Locally, `--allow-missing` keeps the scan usable.
- Secrets shorter than `MinLength` (8): still skipped with a stderr note, and they don't count toward `--require-secrets`.
- The same secret in several files: deduplicated by value.
- Windows paths: use `filepath`. The integration suite runs on Linux only.
- Concurrent writers to the log: the harness opens with `O_APPEND` and does one write per invocation. Keep that, and add a mutex around the write in `runCLI` so lines don't interleave under `t.Parallel`.

**Acceptance criteria**
- `go test ./cmd/cbdev/...` (new `ci_scan_test.go`) covers missing file → 2, `--allow-missing` → 0, `--require-secrets 1` with an empty directory → 2, and a hit → 1 whose output doesn't contain the value.
- Fake IT: after `CBCTL_IT_LOG=integration.log go test -tags=integration ./internal/integration/...`, the file `$(git rev-parse --show-toplevel)/integration.log` exists, and `internal/integration/integration.log` does **not**.
- Fake IT: `TestRedaction` finds at least 2 directory secrets (generated plus supplied).
- `go test -race -run TestRunIsHermetic -count=50 ./internal/cli/` passes, with a new assertion that a secret registered by run A is still redacted while run B resets.
- `workflows_test.go` asserts that `scan-secrets` has no `|| true`, that the artifact paths contain `github.workspace`, and that `if-no-files-found: error` is set.

### Phase 5: A release gate that actually gates (`mm--release-gate-correctness`)
Resolves F-05, F-06, F-08, F-14, F-15, F-32. Depends on Phases 1, 2 and 4 so that nightlies are meaningful before the gate blocks on them.

**File/module structure**
- `.github/workflows/janitor.yml` (**new**): the weekly janitor moves out of `nightly.yml`, with its own concurrency group `sandbox-janitor`. Nightly scheduled runs are then integration-only, which fixes F-06(a)/(b) structurally.
- `.github/workflows/nightly.yml` (changed):
  - One cron.
  - Concurrency `group: nightly-sandbox-${{ github.event_name }}`, so a dispatch can't evict a pending scheduled run (F-14). Overlap is still prevented by a run-level lock project. Alternatively keep a single group and document it (**open question Q3**).
- `internal/ci/releasegate/gate.go` (changed):
  - `Evaluate` dedupes to one run per UTC date (the newest) and ignores runs whose integration job was skipped. `FetchRuns` gains a job-level check through `gh api …/runs/{id}/jobs`.
  - Replace the bubble sort with `sort.SliceStable`.
- `cmd/cbdev/ci_gate.go` (changed): the default `--repo` comes from `git remote get-url origin`, falling back to `mminichino/cbctl` (F-32). `--report-only` stays as an opt-in for local use only.
- `.github/workflows/release.yml` (changed):
  - Remove `--report-only`.
  - Workflow-level `permissions: contents: read`; `goreleaser` job `permissions: contents: write`.
  - `timeout-minutes: 30`.
  - SHA-pinned actions.
- `.github/workflows/ci.yml` (changed):
  - `pull_request.branches: [main, 'mm--**']` (F-08).
  - SHA-pinned actions.
- `.github/dependabot.yml` (**new**): `github-actions` ecosystem, weekly, to keep the SHA pins current.

**Interfaces and function signatures**
```go
type Run struct { /* existing */ ID int64; IntegrationRan bool }
func Evaluate(runs []Run, commit string, nights int) (ok bool, lines []string) // semantics: one run per UTC day; IntegrationRan required
func FetchRuns(ctx context.Context, repo, workflow, branch string) ([]Run, error)
func fetchIntegrationRan(ctx context.Context, repo string, runID int64) (bool, error) // job "integration" conclusion != skipped
func defaultRepo(ctx context.Context) string
```

**Data models**
- `Run.ID` comes from `workflow_runs[].id`.
- `IntegrationRan` is true when the jobs list contains a job named `integration` with `conclusion` in `{success, failure, cancelled, timed_out}`, that is, not `skipped`.
- Day key: `RunStartedAt` (or `CreatedAt`), truncated to a UTC date.

**Error handling**

| Failure | Exit / surfaced |
|---|---|
| `gh api` failure, or rate limit while fetching jobs | Exit 1 with stderr naming the call, which blocks the release |
| Fewer than N distinct green days | Exit 1 with a per-day table |
| Newest green run isn't the tag commit | Exit 1 with the existing message |
| goreleaser over 30 minutes | The job is cancelled; `latest` isn't moved, because the tag step comes after goreleaser |

`--report-only` stays for local dry runs and the docs say it must not appear in workflows; `workflows_test` enforces that.

**Edge cases**
- A re-run attempt on the same day: one per day, newest attempt wins.
- A DST/UTC boundary around 07:00 UTC: no effect.
- A gap day (a cancelled nightly): the window resets, so tagging requires 3 new consecutive green days. Documented.
- `workflow_dispatch` runs: still excluded (`event == schedule`).
- More than 30 runs: `per_page=30` is enough for 3 nights now that the janitor has moved out.

**Acceptance criteria**
- `go test ./internal/ci/releasegate/` adds these cases: two runs on one date count as one day; a run where integration was skipped doesn't count; a Sunday janitor fixture from the old layout doesn't break the streak; `defaultRepo` falls back correctly.
- `workflows_test.go` asserts:
  - no `--report-only` in `release.yml`;
  - every `uses:` matches `@[0-9a-f]{40}`;
  - `goreleaser.timeout-minutes` is set;
  - the workflow-level permission is `contents: read`;
  - `ci.yml` PR branches include the `mm--**` pattern;
  - `janitor.yml` exists and `nightly.yml` has exactly one cron.
- A tag pushed when the nightlies aren't green fails the `gate` job, and `goreleaser` is skipped. Verify on a throwaway `v0.0.0-gatetest` tag in a fork, or with `act` and a fixture via `--runs-json`.

### Phase 6: Traceability, hermetic and test-depth gates (`mm--test-traceability-gates`)
Resolves F-17, F-19, F-20, F-21, F-22, F-23, F-29, F-33, F-34. Large; it may be split into 6a (gates/tooling: F-17, F-20, F-21, F-33, F-34) and 6b (test depth: F-19, F-22, F-23, F-29).

**File/module structure**
- `cmd/cbdev/docs_check.go`, plus new `internal/docgen/porttable/porttable.go` and `_test.go`: implement `ac-coverage --port-table PATH --source-list FILE`.
  - Every listed `test_*.py` must appear exactly once.
  - Every `ported` row's Go counterpart file must exist and contain a test function.
  - The `ported` count must be at least `--min-ported N`, a ratchet stored in the Makefile.
- `Makefile` (changed):
  - `ac-coverage` targets `docs/backlog/release-1.0.0` **and** 1.5.0, with `--min-acs 1` for 1.0.0 (fails on 0/0).
  - Add a `lint` target running `golangci-lint run`.
  - Add `check: … lint`.
- `docs/backlog/release-1.0.0/stories.yaml` (changed): set `status` to `Implemented` for every CAP whose command exists. Tag tests with `// CAP-0NN AC#` so ac-coverage counts them.
- `internal/cli/hermetic_test.go` (changed):
  - Add `internal/capella`, `internal/capellaapi`, `internal/rest` and `internal/redact` to `pkgs`.
  - Change the allow-list to **file + needle** pairs, each with a justification comment.
  - New violations fail hard.
- `internal/cli/capella_output.go` (changed): `confirm` uses `Deps.Prompter` when set, falling back to TTY. The init and configure prompts do the same.
- `internal/capella/cluster.go`, `connectivity.go` (changed): take an `engine.Clock` instead of `time.Sleep`/`time.Now`.
- `internal/docs/doccmds_test.go`, `examples_test.go` (**new**): every `cbctl …` line in README and cbctl.mdc parses with `cobra` `Find` and has known flags. Links are covered by the existing `links --check`.
- `internal/integration/credentials_test.go`: implement the gocb KV put/get on the sandbox. `power_test.go`: enable on sandbox behind `CBCTL_IT_POWER=1`. `backups_test.go`, `clone_test.go`: implement on fake at minimum.
- `internal/integration/server_test.go`: build tag `integration && server`, run from a separate `server-it` CI job, and out of the Capella nightly.
- `internal/resources/*_test.go`: replace `cover_helpers_test.go` with behavioral table tests through `Plan`/`Apply` on `capellafake`. Raise the gate to 87%.
- `internal/docgen/port/port_dirty_test.go`: `git init --template=` (empty template), so no hooks directory is needed.
- `.golangci.yml` (**new**): `govet`, `errcheck`, `staticcheck`, `ineffassign`, `unused`, `gofmt`, `goimports`; CI job `lint`.
- `README.md`, `docs/capella/testing.md`, `.cursor/rules/cbctl.mdc`: see §4.

**Interfaces and function signatures**
```go
// porttable
type Row struct{ N int; Source, CAP, GoCounterpart, Status, Notes string }
type Status string // "ported" | "partial" | "planned" | "N/A"
func Parse(md []byte) ([]Row, error)
func Check(rows []Row, sources []string, repoRoot string, minPorted int) []Problem
type Problem struct{ Row int; Msg string }
// cli
func confirm(rt *runtime, question string) (bool, error) // uses rt.deps.Prompter when non-nil
// capella
func (c *Cluster) waitReady(ctx context.Context, clk engine.Clock, timeout time.Duration) error
```

**Data models**
- `Row`/`Status`: the status values are case-normalized. `Ported` and `ported` are both accepted today, so normalize and fail on unknown values.
- Hermetic allow-list: `map[string][]string{"internal/cli/deps.go": {"os.Getenv", …}}`, each entry with a reason.
- Story status enum: `Not started|In progress|Implemented|Descoped`, validated by the stories package.

**Error handling**
- `cbdev` exits: 1 on problems (listing row number and message), 2 on usage errors.
- Hermetic violations: `t.Errorf` naming the file and needle.
- Prompter errors: an EOF from the scripted prompter means "no", matching the TTY EOF behavior.
- With no Prompter and no TTY, `confirm` still returns exit 4 per contract.
- Sandbox SDK connect failure: `t.Fatalf` with the redacted connection string.

**Edge cases**
- A source test list that drifts from capellactl HEAD: `--source-list` is a checked-in file (`internal/cli/testdata/capellactl-scenarios.txt`) pinned to `docs/backlog/SOURCE.md` HEAD.
- `N/A` rows must have Notes.
- Multiple Go counterparts in one cell: split on `,` and require each to exist.
- Coverage ratchet: when a package is removed, the gate lists it as missing instead of passing silently.
- `golangci-lint` missing locally: the `make lint` target prints the install hint and exits 1. CI installs a pinned version.

**Acceptance criteria**
- `go run ./cmd/cbdev docs ac-coverage --port-table internal/cli/testdata/scenario-port.md --source-list internal/cli/testdata/capellactl-scenarios.txt --min-ported 8` exits 0 today and 1 when a row is deleted.
- `make ac-coverage` fails when release-1.0.0 has 0 tagged ACs and passes once stories and tags land. It prints a non-zero numerator.
- `TestHermeticGrep` fails on a deliberately added `time.Sleep(` in `internal/capella/x.go`.
- New scenario `TestJanitor_PromptsViaPrompter`: `ScriptedPrompter.Push("y")` deletes, and `Push("n")` reports "cancelled". Neither uses `--yes`.
- `make cover-engine` passes at 87% after `cover_helpers_test.go` is deleted.
- `go test ./internal/docgen/port/` passes with `HOME` and the git template directory read-only.
- `make lint` passes, and the CI `lint` job is green.
- `rg CBCTL_LIVE_CLUSTER README.md` returns nothing, and README's `make test-integration` comment says "Capella sandbox".

## 4. Docs updates (per `general.mdc`)

| Phase | `README.md` | `.cursor/rules/cbctl.mdc` | Other |
|---|---|---|---|
| 1 | Development section: sandbox env (`CAPELLA_SANDBOX_*` is **test-harness only**), `CBCTL_IT_TARGET`, `CBCTL_IT_REQUIRE_TARGET`, `CBCTL_IT_ALLOWED_ORG_IDS`, `CBCTL_IT_FREE_TIER`; `make test-capella-live` needs `CBCTL_IT_TARGET=sandbox`; a manual `make janitor` needs `CAPELLA_API_KEY`/`CAPELLA_ORG_ID` (the CLI never reads `CAPELLA_SANDBOX_*`, D1) | (none: the CLI auth contract is unchanged) | `docs/specs/capella-fold-in-phase-4.md` §5.4: replace "sandbox vars preferred via settings" with "the CLI reads only `CAPELLA_API_KEY`/`CAPELLA_ORG_ID`; the workflow maps `secrets.CAPELLA_SANDBOX_*` onto them"; `docs/capella/testing.md` |
| 2 | Lines 316/337: janitor flags `--older-than-hours` (default 24), `--dry-run`, `--fail-on-leftovers`, `--timeout-minutes`, `--max-total-minutes`; `make janitor HOURS=0` | Lines 110 and 236: `cbctl capella janitor [--prefix P] [--older-than-hours H] [--dry-run] [--fail-on-leftovers] [--timeout-minutes M] [--max-total-minutes M] [--yes]`; "prefix ≥4 chars ending in `-`; exit 3 otherwise; exit 1 on any failure or `--fail-on-leftovers`" | Makefile comment |
| 3 | Capella section: a "Deprecated commands" subsection that lists the six legacy commands, each with its replacement (the D2 table); notes that they're hidden and print a stderr warning; direct `cluster create` flags; `--allow-cidr` has no default; `0.0.0.0/0` needs `--allow-any-ip`; no default database user, so pass `--username` with `--password-env`/`--password-file` | Add a line to the "Capella clusters" block: `# Deprecated (hidden; warn on stderr): capella cluster exists\|destroy\|test, capella bucket create\|exists → use cluster get / cluster delete / credential create + cluster test / bucket add / bucket get`. Add to "Capella resource behavior": "Legacy direct `cluster create` adds no allow-list entry unless `--allow-cidr` is given (`0.0.0.0/0` needs `--allow-any-ip`) and creates no database user unless `--username` plus `--password-env/--password-file` is given; a literal `-p` exits 3." Add to Agent usage rules: "Never use the deprecated Capella commands; use their replacements." | CHANGELOG "Deprecated" and "Breaking" entries; `docs/capella/reference.md` regenerated |
| 4 | Development: artifact locations (`$GITHUB_WORKSPACE`/module root), secrets dir | (none) | `docs/capella/testing.md` secret-scan section |
| 5 | Release section: the gate needs 3 consecutive green **scheduled integration** days on the tag commit; `--report-only` is for local use only | (none) | Spec phase-4 workflow section (janitor.yml split) |
| 6 | Line 335: `make test-integration` is the Capella sandbox suite (Docker Server IT moves to `make test-server-it`); remove `CBCTL_LIVE_CLUSTER`; add `make lint` | (none, D2 is handled in Phase 3) | `docs/capella/testing.md` turned from stub into a full guide; `scenario-port.md` header matches the implemented `--port-table` |

## 5. Open questions for the user

Decided on 2026-11-16 (see Decisions at the top):
- ~~Q1~~ → **D1**: map the sandbox secrets in workflow env; no CLI settings change.
- ~~Q2~~ → **D2**: hide, deprecate and make safe.

**Still open:**
- **Q3 (Phase 5, open):** Nightly concurrency: per-event groups plus a lock, or keep the single group and accept that a dispatch can evict a pending scheduled run?
- **Q4 (Phase 1, open):** Can a repo **variable** `CAPELLA_SANDBOX_ORG_ID` (not secret) be added for the org allowlist? If not, the guard compares against a SHA-256 of the org ID stored in the repo. Either way it's independent of D1, since the allowlist only *checks* the org; it doesn't authenticate.
- **Q5 (Phase 6, open):** Target ratchet for scenario porting (for example ported ≥ 30/63 before merging to `main`), and whether to raise the coverage threshold to 87%.
- **Q6 (open):** The brief said the checkout was `cursor/cp15-scaling-wait-0454`, but it is actually `mm--capellactl-port` @ `b05642b`. Was the CP15 scaling-wait branch meant to be audited too? `b05642b` already contains "wait out Capella healthy flicker after scale PUT (#10)".

*End of plan.*
