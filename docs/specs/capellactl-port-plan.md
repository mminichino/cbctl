# capellactl → cbctl port: consolidated audit and remediation plan (v2)

Date: 2026-11-16. Ref: `mm--capellactl-port` @ `b05642b` (matches origin). Source: `capellactl` @ `6389d5a`.

This plan supersedes the phase list in `capellactl-port-audit/ci-sandbox-findings.md` (v1). v1 keeps the full evidence for findings F-01..F-36. The parity reports keep the full evidence for P-CLI-*, P-ENG-* and P-TST-*:

| Source | Findings | Scope |
|---|---|---|
| `capellactl-port-audit/ci-sandbox-findings.md` | F-01..F-36 (1 critical, 9 high, 15 medium, 11 low) | CI workflows, integration harness, sandbox isolation and cleanup, legacy-command safety |
| `capellactl-port-audit/parity-cli.md` | P-CLI-01..16 (7 high, 6 medium, 3 low); 85-row command and flag mapping | capellactl CLI surface vs cbctl |
| `capellactl-port-audit/parity-engine.md` | P-ENG-01..17 (5 high, 5 medium, 7 low); 103-row feature/field/endpoint mapping | config file, engine, API, settings, error handling |
| `capellactl-port-audit/parity-tests.md` | P-TST-01..18 (5 high, 9 medium, 4 low); 151-file / 878-test mapping | test parity and coverage |

## 0. Goals and decisions

**Primary goal:** a capellactl user can switch to `cbctl` with no loss of functionality.
**Secondary goal:** `cbctl` is stable, handles errors fully, and has enough automated tests to prove both.

| ID | Decision | Effect on the plan |
|---|---|---|
| D1 | CI maps `secrets.CAPELLA_SANDBOX_API_KEY` to `CAPELLA_API_KEY` in workflow env. The CLI never reads `CAPELLA_SANDBOX_*`. | Phase 5 |
| D2 | The legacy (pre-port) Capella commands are hidden, deprecated and made safe. **Refined by the parity audit:** they aren't capellactl commands, but they occupy capellactl's verbs (`cluster create`, `bucket/scope/collection create`). Those verbs must behave like capellactl. The legacy behavior is reachable only through its legacy flags or arity, and always prints a warning. | Phase 2 |
| D3 | Nightly keeps the single concurrency group, with no lock. It adds duplicate-run protection. | Phase 5 |
| D4 | A repo **variable** `CAPELLA_SANDBOX_ORG_ID` holds the test org ID. | Phase 5 |
| D5 | **Porting target is 100%.** Every capellactl command, flag, env var, file location and behavior has a working cbctl equivalent, or a documented rename with a migration path. Every capellactl test (878) is COVERED by a Go test, or N/A with a written reason. Coverage targets are per tier (§3, Phase 6). | All |
| D6 | Branches created by Cursor agents are out of scope for this audit. | — |
| D7 | **All work happens on `mm--capellactl-port`.** No per-phase branches. Each phase is one or more commits on this branch and is pushed when the phase is complete. | All |

Decisions this plan makes by default, each flagged in its phase and reversible:
- **Parity over cbctl's own pre-port defaults.** `bucket add` defaults to 1024 MB as in capellactl (P-CLI-11). `cluster create NAME` takes the positional name (P-CLI-03). capellactl's interactive `cluster create` wizard is restored when there's a terminal (P-CLI-03, P-TST-10).
- **cbctl's exit-code contract wins where capellactl differs.** No terminal and no `-y` exits 4, not 1 (P-CLI-08). Usage errors exit 3, not 2 (P-CLI-16). These are documented in the migration guide, not reverted.
- **Deprecation warnings go to stderr through a custom wrapper, not cobra's `Deprecated` field.** Cobra prints that field to `OutOrStderr()`, which is stdout after `cli.Run` calls `SetOut`. That would corrupt the `true`/`false` output of `exists` (v1 Phase 3).

## 1. Current state in one table

| Area | State |
|---|---|
| Declarative config file | **Compatible.** `apiVersion: capellactl/v1` files validate unchanged. The schema is a superset. No converter is needed (P-ENG-16). |
| Capella API v4 | **Complete.** All 57 endpoints capellactl calls exist, with matching pagination, ETag, retry and error decoding. |
| Engine (plan/apply/drift/sync/destroy) | **Faithful.** 79 of 103 mapping rows MATCH. The gaps are outside the engine. |
| CLI surface | **Breaks capellactl scripts.** These fail: `cluster create NAME`, `bucket/scope/collection create`, removed verbs that exit 0 silently, `--timeout 90`, `import`. |
| Settings and local state | **Not migrated.** `CAPELLACTL_*` env vars are ignored, as are the `~/.config/capellactl` profiles, the `capellactl` keychain entry and `./.capellactl/credentials`. |
| Docs | **No migration guide.** The config reference is 13 rows (capellactl's has 103). Five operator docs weren't ported. |
| Stability | `os.Exit` inside `RunE`. The legacy backend has no context and no HTTP timeouts, and it swallows poll errors. Rotation restore swallows errors. |
| CI and sandbox | Sandbox cleanup can't authenticate. Teardown is skipped on timeout. The secret scan and release gate can't fail. |
| Tests | 55% of capellactl tests are COVERED, 22% PARTIAL, 17% MISSING, 6% N/A. `internal/cli` is at 74% coverage. The gate covers only 3 packages. |

## 2. Phase order

The order follows risk and dependency. Earlier phases make later work verifiable.

| Phase | Name | Resolves |
|---|---|---|
| 0 | CI on the port branch, lint, environment-proof tests | F-08, F-15 (pins), F-33, F-34 |
| 1 | Runtime contract and stability foundation | P-CLI-01, P-CLI-16 (usage exit), F-36, F-18, P-ENG-09, P-ENG-10, P-ENG-11, P-ENG-12, P-ENG-13, P-ENG-14, P-ENG-17 |
| 2 | capellactl command compatibility and legacy-command safety (D2) | P-CLI-03, 04, 07, 09, 10, 11, 13, 14, 15; P-ENG-07, P-ENG-08; F-27, F-35 |
| 3 | Migration of settings, profiles, keychain and credentials | P-CLI-02, 06, 12; P-ENG-01, 02, 04, 15, 16; P-ENG-06 (`doctor`) |
| 4 | Documentation parity and migration guide | P-CLI-05, P-CLI-08 (doc), P-CLI-16 (doc); P-ENG-03, 05, 06 (guide); F-28, F-29 |
| 5 | Sandbox integration safety: auth, guards, teardown, evidence, duplicate runs | F-01, 02, 03, 04, 07, 09, 10, 11, 12, 13, 14, 16, 24, 25, 30, 31 |
| 6 | Test parity to 100% and tiered coverage gates | P-TST-01..18; F-17, F-19, F-20, F-21, F-22, F-23 |
| 7 | Release gate that blocks | F-05, F-06, F-15 (permissions/timeouts), F-32 |
| — | Deferred | F-26: App Services sweep. Neither tool manages App Services yet. |

Phases 1–4 deliver the primary goal. Phases 0, 5, 6 and 7 deliver the secondary goal. Phase 6 runs alongside 1–4: every phase adds its own tests, and Phase 6 closes the remaining gaps and turns the gates on.

**Done means merge-ready to `main`.** Every phase's acceptance criteria pass, the §3 exit criteria hold, and the release gate (Phase 7) has seen 3 consecutive green scheduled nightlies on the branch tip.

## Phase 0: CI on the port branch, lint, environment-proof tests

**File/module structure**
- `.github/workflows/ci.yml` (changed):
  - Add `push.branches: [main, mm--capellactl-port]` and `pull_request.branches: [main, mm--capellactl-port]`.
  - SHA-pin every `uses:`.
  - Add a `lint` job.
  - Add `go test -race`.
- `.github/workflows/nightly.yml`, `release.yml` (changed): SHA-pin actions only. Behavior changes come in Phases 5 and 7.
- `.github/dependabot.yml` (new): `github-actions`, weekly.
- `.golangci.yml` (new): `govet`, `errcheck`, `staticcheck`, `ineffassign`, `unused`, `gofmt`, `goimports`.
- `Makefile` (changed): add a `lint` target, and add `lint` to `check`.
- `internal/docgen/port/port_dirty_test.go` (changed): `git init --template=` so the test needs no hooks dir (F-34).

**Interfaces and function signatures:** none in product code. The test helper is `func initRepo(t *testing.T, dir string)`, which uses `git init --template= -q`.

**Data models:** the `.golangci.yml` config pins the `golangci-lint` version (e.g. `v2.x`), and CI installs that exact version.

**Error handling**

| Failure | Detected | Surfaced | Recovery |
|---|---|---|---|
| Existing lint violations on first enable | `make lint` | CI `lint` job red | Fix them in the same phase. No baseline file; `//nolint` only with a reason. |
| SHA pin points at an action that was later removed or renamed | Workflow fails to resolve the action | Dependabot PR | Bump the pin |

**Edge cases**
- `errcheck` will flag the `_ =` discards that P-ENG-11/12 fix in Phase 1. Allow them temporarily with `//nolint:errcheck // fixed in Phase 1` and remove the directives in Phase 1.
- `golangci-lint` isn't installed locally: `make lint` prints the install command and exits 1.

**Acceptance criteria**
- A push to `mm--capellactl-port` runs `ci.yml`: vet, unit, race, fake-IT, docs-check and lint are all green.
- `rg -n 'uses: [^@]+@v[0-9]' .github/workflows` returns nothing.
- `go test ./internal/docgen/port/` passes inside the agent sandbox, where `.git/hooks` isn't writable.

## Phase 1: Runtime contract and stability foundation

Purpose: every failure returns a typed error through `HandleError`, so exit codes (0/1/3/4/5/130), redaction and cleanup always apply. Network calls are bounded and cancellable.

**File/module structure**
- `internal/cli/root.go` (changed):
  - Delete `exitErr()`.
  - Add `unknownSubcommand` as the `RunE` of every command group.
  - `SetFlagErrorFunc` maps cobra flag errors to exit 3.
  - Add a `removedVerbs` hint table.
- `internal/cli/capella.go`, `cluster.go`, `keyspace.go`, `keyspace_cmd.go` (changed): return errors instead of calling `exitErr()`, and pass `cmd.Context()` instead of `context.Background()` (P-ENG-10).
- `internal/capella/client.go`, `cluster.go`, `orchestrate.go` (changed):
  - Thread `ctx` through the legacy backend.
  - Drop the custom `&http.Client{}` so the tuned `capellaapi` transport is used.
  - Replace `Cluster.Wait` with `engine.WaitForCluster`.
  - `ClusterExists` returns `(bool, error)` (P-ENG-09).
- `internal/cli/run.go` (changed): move `redact.Reset()` inside `runMu` (F-18).
- `internal/secrets/files.go` (changed):
  - `RestoreRotatedCredential` returns an error (P-ENG-11).
  - `chmod` failures produce a warning.
  - `GitWarning` runs `git -C <dir>` (P-ENG-12).
- `internal/capellaapi/client.go` (changed): bounded body read (P-ENG-13).
- `internal/secrets/password.go` (changed): `Generate` returns `(string, error)` (P-ENG-14).
- `internal/cli/capella_globals.go` (changed): `requireCapella` errors on a nil context (P-ENG-17).
- Callers of the changed signatures: the credential update and apply rotation paths in `internal/cli` and `internal/resources`.

**Interfaces and function signatures**
```go
// internal/cli/root.go
func unknownSubcommand(cmd *cobra.Command, args []string) error // no args → help, nil; args → *CLIError{Code: ExitUsage}
var removedVerbs = map[string]string{
    "capella cluster schedule apply": "cbctl capella apply --target schedule/NAME",
    "capella cluster apply":          "cbctl capella apply --target cluster/NAME",
    "capella project apply":          "cbctl capella apply --target project/NAME",
}
func flagError(cmd *cobra.Command, err error) error // → Errorf(ExitUsage, ...)

// internal/capella (legacy backend)
type Backend interface {
    CreateCluster(ctx context.Context, cfg config.Capella) error
    DestroyCluster(ctx context.Context, cfg config.Capella) error
    ClusterExists(ctx context.Context, cfg config.Capella) (bool, error)
}

// internal/secrets
func RestoreRotatedCredential(path, backup string) error
func Generate(length int) (string, error)

// internal/capellaapi
const maxResponseBody = 32 << 20
```

**Data models**
- `CLIError{Code int; Msg, Hint string}` is unchanged. Each new usage error sets `Hint`: a "Did you mean …?" suggestion (`SuggestionsMinimumDistance = 2`) or the entry from `removedVerbs`.
- No schema changes.

**Error handling**

| Failure | Exit | Message |
|---|---|---|
| Unknown subcommand, or a removed verb | 3 | `unknown command "x" for "cbctl capella …"`, followed by `→ Did you mean …?` or `→ use <replacement>` |
| Bad flag or flag value | 3 | The cobra message, with usage on stderr |
| Legacy backend: 401/403 during a wait | 1, immediately | The API error, redacted |
| Legacy backend: context deadline | 5 | `stopped waiting after …; the operation continues in Capella` |
| Legacy backend: Ctrl-C | 130 | Same as the managed path |
| Rotation PUT fails and the restore also fails | 1 | The primary API error, then `[error] - could not restore previous password file`, then `→ copy <backup> to <path>` |
| Response body larger than 32 MiB | 1 | `response too large` |
| `crypto/rand` failure | 1 | `generating password: …` (no panic) |

**Edge cases**
- `cbctl capella` alone, and any group alone, still print help and exit 0 (cbctl.mdc: "Subgroups with no args → help").
- Prefix matching (`EnablePrefixMatching`) keeps working. An ambiguous prefix exits 3 and lists the candidates.
- A positional that looks like a command (`cluster get schedule`) is an argument to the leaf, not an unknown command. `unknownSubcommand` is attached to groups only.
- `redact.Reset` under concurrent `Run` calls: secrets from an in-flight run stay registered until that run releases `runMu`.
- `chmod` on filesystems that don't support it (FAT, some network mounts): warn once per dir, not per file.

**Acceptance criteria**
- `rg -n 'os\.Exit\(' internal/ --glob '!*_test.go'` returns nothing, and `rg -n 'exitErr' internal/` returns nothing.
- `TestUnknownSubcommand`, a table over every group in `newRootCmd()`: no args exits 0; `bogus` exits 3 with a suggestion; each `removedVerbs` key exits 3 with its hint.
- `cbctl capella --bogus-flag` exits 3.
- Legacy backend against a stalled `httptest` server with a 2s context: `context.DeadlineExceeded` → exit 5. A 403 during the wait → exit 1 within one poll. A 500 from `ClusterExists` → an error, not `false`.
- `TestRestoreRotatedCredential_Error`, with a read-only dir and a fake PUT failure: the output names both paths and the exit is 1.
- `go test -race -count=50 -run TestRunIsHermetic ./internal/cli/` passes, with the new assertion from v1 Phase 4.
- Every `cmd.Context()` replacement is covered by a test where a cancelled context gives exit 130 (keyspace import checks `ctx.Err()` between batches).

## Phase 2: capellactl command compatibility and legacy-command safety (D2)

Purpose: every capellactl invocation, after the documented `capellactl X` → `cbctl capella X` rewrite, does what it did in capellactl. The legacy cbctl-only behavior survives, but only through its legacy flags or arity, and always with a warning.

**Routing rules**

| Invocation | Route |
|---|---|
| `capella cluster create [NAME]` with managed flags, or no flags | Managed create. The positional sets `--name`; a different positional and `--name` together exit 3. Uses capella.yaml if present; otherwise direct v4. On a TTY with missing required fields, the capellactl wizard prompts for provider, region, size or groups, and CIDR. |
| `capella cluster create` with any of `--database --database-id --org --project-id --user-email --user-id --allow-cidr -u -p` | Legacy path: deprecation warning, and safe defaults (no `0.0.0.0/0` without `--allow-any-ip`; no database user unless `--username` and `--password-env` or `--password-file`; literal `-p` exits 3). |
| `capella bucket create CLUSTER NAME`, `scope create CLUSTER BUCKET SCOPE`, `collection create CLUSTER BUCKET SCOPE NAME` (capellactl arity) | Forward to `add`, with a stderr note `'… create' is '… add' in cbctl`. |
| `capella bucket create NAME --database …` (legacy arity) | Legacy path with a warning. |
| `capella cluster exists`, `destroy`, `test`; `capella bucket exists` | Hidden and deprecated, with a warning that names the replacement. `destroy` delegates to `cluster delete`: it needs `-y` without a TTY and is refused while deletion protection is on. |
| `capella import` with no positionals, or with sync-only flags (`--project --project-id --cluster --cluster-id --file --full --stdout --dry-run`) | Exit 3 with the hint `capellactl's 'import' is 'cbctl capella sync'`. With `FILE KEYSPACE`, the JSONL import is unchanged. |
| `capella cluster schedule apply NAME` | New leaf; same as `capella apply --target schedule/NAME`. |
| `capella cluster apply NAME`, `capella project apply NAME` | Hidden aliases of `apply --target KIND/NAME`, with a warning. These replace the hint entries Phase 1 added for these two verbs. |
| `capella help [TOPIC]` | Hidden; forwards to `capella guide [TOPIC]`. |

**File/module structure**
- `internal/cli/capella_legacy.go` (new): the legacy table, `deprecateLegacy`, `warnDeprecated`, `usesLegacyFlags` and `capellactlArity` (D2 wrapper; see v1 Phase 3 for the full design).
- `internal/cli/capella.go` (changed): legacy commands wrapped and hidden; `destroy` delegates to the `cluster delete` implementation.
- `internal/cli/capella_managed_ops.go` (changed):
  - `cluster create` takes `Args: cobra.MaximumNArgs(1)`.
  - Routing as in the table above.
  - The builder is extracted for direct mode.
  - The wizard runs on a TTY.
- `internal/cli/capella_cluster_wizard.go` (new): interactive prompts through `Deps.Prompter` (made real in Phase 6: `confirm` and prompts use `Deps.Prompter`).
- `internal/cli/capella_schedule.go` (changed): `schedule apply`.
- `internal/cli/capella_env_cmds.go` (changed): hidden `cluster apply` and `project apply` aliases.
- `internal/cli/capella_import_cmd.go` (or wherever `capella import` lives) (changed): the sync-hint guard.
- `internal/cli/capella_guide.go` (changed): hidden `help` alias.
- `internal/cli/capella_globals.go` (changed): `--timeout` uses `capellaTimeout`; `CBCTL_QUIET` accepts `1/true/yes/on`.
- `internal/cli/capella_bucket.go` (changed):
  - `defaultBucketMemoryMB = 1024` (parity default).
  - Direct mode doesn't send `storageBackend` unless the flag is set.
  - Hidden `--no-flush` on `add`.
- `internal/cli/configure.go` (changed), P-CLI-13:
  - Hidden `--api-base-url` alias.
  - `--import/--no-import`.
  - The default profile comes from `CBCTL_PROFILE`.
  - With no keychain, save the profile and warn.
  - `--output-format legacy` for capellactl's list/show JSON shape (maps instead of arrays).
- `internal/cli/completion.go` (new): `ValidArgsFunction` for cluster names (P-CLI-15).
- `internal/cli/flags.go`, `internal/config/config.go`, `internal/capella/orchestrate.go` (changed): the v1 Phase 3 safety changes. No `DefaultCapellaAllowCIDR`, and no `Administrator`/`password` seeding for Capella.
- Tests:
  - `capella_legacy_test.go`
  - `capella_compat_test.go`: one subtest per row of `parity-cli.md` §1–4 that has status RENAMED, DIFFERENT, PARTIAL or MISSING.
  - `capella_cluster_create_direct_test.go`
  - `capella_cluster_wizard_test.go`
  - `capella_timeout_test.go`

**Interfaces and function signatures**
```go
// capella_legacy.go
type legacyCommand struct{ Path []string; Replacement string }
func deprecateLegacy(cmd *cobra.Command, replacement string)
func warnDeprecated(w io.Writer, path, replacement string)
func usesLegacyFlags(cmd *cobra.Command) bool
func capellactlArity(kind string, args []string) bool // bucket:2, scope:3, collection:4

// cluster create
func newCapellaClusterCreateCmd() *cobra.Command // Args: cobra.MaximumNArgs(1)
func resolveClusterName(args []string, flag string) (string, error) // conflict → ExitUsage
func buildClusterCreate(cmd *cobra.Command, provider string) (capellaapi.CreateClusterRequest, error)
func runDirectClusterCreate(rt *runtime, req capellaapi.CreateClusterRequest) error
func runClusterWizard(rt *runtime, partial ClusterSpec) (ClusterSpec, error) // ErrCancelled → exit 1 "cancelled"
func runLegacyClusterCreate(cmd *cobra.Command, cf *capellaFlags) error

// schedule apply
func newCapellaScheduleApplyCmd() *cobra.Command // Args: cobra.ExactArgs(1)

// timeout flag
type capellaTimeout time.Duration
func (t *capellaTimeout) Set(s string) error // "90" → 90s; units case-insensitive; ≤0 → error
func (t *capellaTimeout) String() string
func (t *capellaTimeout) Type() string // "duration"

// access safety (from v1 Phase 3)
type directAccess struct{ AllowCIDR string; AllowAnyIP bool; CredName, PasswordRef string }
func (a directAccess) Validate() error
func validateAllowCIDR(cidr string, allowAny bool) (string, error)
```

**Data models**
- `legacyCommand` table: 6 entries. Every `Path` resolves with `Find` (a panic at startup if not, like `installManagedModes`), and every `Replacement` exists in cbctl.mdc, enforced by a test.
- `ClusterSpec` (wizard result) has the same fields as the managed builder input: `Name`, `Provider` (`aws|gcp|azure`), `Region`, `Size` (`dev|small|medium|large|custom`), `Groups []GroupSpec`, `CIDR`, `SupportPlan`, `FreeTier`. Validation is shared with `parseGroupSpec` and the presets.
- Bucket defaults: `MemoryMB 1024`, `StorageBackend ""` (Capella's default), `Replicas` unset (Capella's default).
- `--timeout`: duration > 0, at most 24h; default 1h. The 1h default is kept because capellactl's default was 60m.

**Error handling**

| Failure | Exit |
|---|---|
| Positional `NAME` and `--name` differ | 3 |
| Managed and legacy flags mixed on `cluster create` | 3: `cannot combine --size/--group with legacy --database/-u/-p` |
| `0.0.0.0/0` or `::/0` without `--allow-any-ip` | 3 |
| Literal `-p VALUE` on any Capella command | 3, with a hint to use `--password-env` or `--password-file` |
| `destroy` with no TTY and no `-y`, or deletion protection on | 4 |
| Wizard input fails validation | Re-ask, up to 3 times, then 3. EOF or Ctrl-D means cancelled, exit 1 with "Cancelled; nothing was changed." |
| `import` called with sync intent | 3, with the `sync` hint |
| `--timeout 0`, `-1` or `abc` | 3 |
| Already exists | 0, with "already exists" (contract) |

The deprecation warning never changes the exit code, prints once per invocation before validation, and is not suppressed by `--quiet`.

**Edge cases**
- `exists` stdout stays exactly `true\n` or `false\n`, and `-o json` stdout stays valid JSON. Warnings go to stderr only.
- `bucket create c1 orders` where a capella.yaml exists: it forwards to `add`, which follows the managed-edit flow (edit, plan, ask, save, apply), exactly like capellactl.
- `bucket create orders` (one arg, no `--database`) is neither capellactl arity nor legacy. It exits 3 with a hint naming both forms.
- Wizard without a TTY: no prompts. Missing required fields exit 3 and list the flags to pass, so CI never blocks on stdin.
- Free tier in direct mode: `--size` and `--group` are rejected (exit 3).
- Completion with no credentials or no network: return no suggestions silently. Never print an error inside completion.
- `CBCTL_QUIET=false` or `0` means not quiet; an unknown value warns and means not quiet.

**Acceptance criteria**
- `TestCapellactlCompat` covers every non-MATCH row in `parity-cli.md` §1–4. Each capellactl invocation (with the prefix rewrite) gives the capellactl-equivalent writes against `capellafake` and the expected exit code. Rows that are intentionally different are listed in the migration guide (Phase 4), and the test asserts the documented behavior.
- `cluster create foo --size small --provider aws --region us-east-1`, with no capella.yaml, sends one v4 POST with the `small` preset body, no CIDR POST and no credential POST. With a capella.yaml, it edits the file and applies.
- `bucket create c1 orders` gives the same requests as `bucket add c1 orders`, plus a stderr note. The request body has `memoryAllocationInMb: 1024` and no `storageBackend`.
- `capella cluster destroy c1` with no TTY exits 4 with zero DELETE calls. With protection on, it exits 4. A missing cluster exits 0.
- `capella import --full` exits 3 and the hint names `sync`. `capella import f.jsonl b.s.c` still imports.
- `cluster schedule apply nightly` gives a plan and writes identical to `apply --target schedule/nightly`.
- `--timeout 90`, `90S`, `5m` and `1h30m` parse. `0` and `-1` exit 3.
- The v1 Phase 3 acceptance criteria all hold: hidden commands, warnings on stderr only, no open CIDR default, no hard-coded user, and documented replacements.
- `TestClusterWizard`, using `ScriptedPrompter`: two groups; an invalid answer is re-asked; EOF cancels with no writes; the written capella.yaml matches a golden file.

## Phase 3: Migration of settings, profiles, keychain and credentials

Purpose: a capellactl user's environment (env vars, profiles, stored API keys, credential files) keeps working, either automatically with a warning or through one migration command.

**File/module structure**
- `internal/settings/legacy.go` (new): the legacy env map, `lookupWithLegacy`, the legacy home resolution and the legacy profile loader.
- `internal/settings/settings.go` (changed): `Resolve` uses `lookupWithLegacy`, and notices go into `Settings.Warnings`.
- `internal/settings/keychain.go` (changed): lazy fallback from the `cbctl` keychain service to `capellactl`, warning once.
- `internal/settings/migrate.go` (new): `MigrateLegacy`.
- `internal/config/capellafile/discover.go` (changed): honor `CAPELLACTL_CONFIG` through the shim.
- `internal/cli/configure_migrate.go` (new): `cbctl configure migrate`. Hidden alias: `import-capellactl`.
- `internal/cli/capella_doctor.go` (new): `cbctl capella doctor [--detailed-exitcode]`. It is read-only and offline.
- `internal/secrets/files.go` (changed): legacy credentials-dir selection, and `GitWarning` blocks when the dir isn't gitignored.
- `internal/cli/capella_init.go`, `capella_sync.go` (changed): append `.cbctl/` to `.gitignore` inside a git work tree.
- `internal/importer/render.go` (changed): keep `apiVersion` from the input file (P-ENG-15), plus `sync --api-version capellactl/v1`.
- `internal/config/capellafile/testdata/capellactl/` (new): snapshot of every capellactl example and doc code fence (P-ENG-16).

**Interfaces and function signatures**
```go
// settings/legacy.go
var legacyEnv = map[string]string{ // legacy → cbctl
    "CAPELLACTL_CONFIG": "CBCTL_CONFIG", "CAPELLACTL_PROFILE": "CBCTL_PROFILE",
    "CAPELLACTL_QUIET": "CBCTL_QUIET", "CAPELLACTL_HOME": "CBCTL_HOME",
    "BEARER_TOKEN": "CAPELLA_API_KEY", "ORG_ID": "CAPELLA_ORG_ID", "API_BASE_URL": "CAPELLA_API_BASE_URL",
}
type Notice struct{ Legacy, Replacement, Detail string }
func lookupWithLegacy(env func(string) string, name string) (val, source string, notice *Notice)
func LegacyHome(env func(string) string) string // CAPELLACTL_HOME | %APPDATA%\capellactl | $XDG_CONFIG_HOME/capellactl | ~/.config/capellactl
func LoadLegacyProfiles(dir string) (map[string]Profile, error)

// settings/migrate.go
type Keyring interface {
    Get(service, user string) (string, error)
    Set(service, user, secret string) error
    Delete(service, user string) error
}
type MigrateOptions struct{ FromDir string; Profiles []string; DryRun, Force, RemoveLegacy bool }
type MigrationReport struct {
    Imported, Skipped, KeyMissing []string
    Conflicts map[string]string // profile → reason
}
func MigrateLegacy(ctx context.Context, env func(string) string, kr Keyring, opts MigrateOptions) (*MigrationReport, error)

// secrets
func ResolveCredentialsDir(configured string, cwd string) (dir string, legacy bool, err error)

// doctor
type DoctorItem struct{ Kind, Detail, Action string }
type DoctorReport struct{ Items []DoctorItem }
func runDoctor(ctx context.Context, d *Deps) (*DoctorReport, error)
```

**Data models**
- capellactl `profiles.yaml` maps to the cbctl profile as follows:
  - `organizationId` → `OrgID`
  - `apiBaseUrl` → `APIHost` (the scheme is stripped and validated as a host)
  - `configFile` → `ConfigFile`
  - `organizationName` is dropped (informational).
  - Unknown keys warn and are ignored.
- Keychain: service `capellactl` → `cbctl`, with the same user key (the profile name).
- Precedence for each setting: flag > cbctl env > legacy env (warns) > cbctl profile > legacy profile (warns; migrate suggested) > default.
- Credentials dir: if `credentialsOutput` is unset, `./.capellactl/credentials` exists and `./.cbctl/credentials` doesn't, use the legacy dir and warn. If both exist, use `.cbctl` and warn that the legacy dir is ignored.
- `DoctorItem.Kind` values: `legacy-env`, `legacy-profile`, `legacy-keychain`, `legacy-credentials-dir`, `credentials-not-ignored`, `apiversion-capellactl`.

**Error handling**

| Failure | Exit | Surfaced |
|---|---|---|
| Legacy and cbctl env both set with different values | 0 (cbctl wins) | `[warning]` naming both variables |
| Legacy `profiles.yaml` malformed | 3 | file:line from the YAML decoder |
| Profile exists in both stores with different values, no `--force` | 4 | Conflicts listed; nothing written for that profile |
| Keychain locked or unavailable during migrate | 0 | Profile imported; listed under `KeyMissing`, with an action line `cbctl configure --profile P --api-key-stdin` |
| No legacy home | 0 | `nothing to migrate` |
| Credentials dir not gitignored inside a git repo | 4 | Unless `--force-overwrite-secret`; no file written |
| Doctor finds migration items with `--detailed-exitcode` | 4 (0 without the flag) | One item per line, each with its action |

Secrets read from legacy sources are passed to `redact.Register` before any output. Keys are never printed, not even with `--dry-run`.

**Edge cases**
- Windows `%APPDATA%` and macOS `~/Library/Application Support` locations for the capellactl home: confirm against `capellactl/config/settings.py:27-37` and cover each in a table test.
- `BEARER_TOKEN` holding a token instead of a key: same sanitize path as `CAPELLA_TOKEN`.
- A migrate run while cbctl profiles are being edited concurrently: write atomically (temp file plus rename) and hold the existing profile-file lock, if there is one.
- `--remove-legacy` only after every key copied successfully, and never for profiles listed in `Conflicts` or `KeyMissing`.
- `-o json` on `configure migrate` and `doctor`: warnings stay on stderr, and stdout is one JSON document.
- `sync` on a `capellactl/v1` file keeps `capellactl/v1`. A full render with no input file writes `cbctl/v1` unless `--api-version` says otherwise.

**Acceptance criteria**
- `TestLookupWithLegacy`, a table over all 7 names: legacy only (resolves and warns once); both set and equal (no warning); both set and different (cbctl wins, warning); neither set.
- With only `CAPELLACTL_CONFIG=x.yaml`, `cbctl capella plan` loads `x.yaml` and prints exactly one deprecation warning.
- `TestMigrateLegacy`, using a fixture legacy home and an in-memory keyring:
  - 2 profiles and 2 keys imported.
  - A second run skips both.
  - `--force` overwrites.
  - `--dry-run` writes nothing.
  - A conflict exits 4.
- After the migration, `capella project list --profile prod` resolves its key from the `cbctl` keychain service.
- Lazy keychain fallback: with only a `capellactl` keychain entry, the command works and warns once per process.
- Credentials dir: in a fixture with only `.capellactl/credentials/c1/app.json`, `credential update --rotate-password` writes there and creates the `.bak` there. In a git repo where `.cbctl/` isn't ignored, credential generation exits 4 and writes nothing.
- `TestCapellactlExamples`: every snapshot under `testdata/capellactl/` validates with exit 0, and plan against an empty fake succeeds.
- `cbctl capella doctor --detailed-exitcode` on the fully legacy fixture lists all 6 kinds and exits 4. On a clean fixture it exits 0.

## Phase 4: Documentation parity and migration guide

**File/module structure**
- `docs/capella/migrating-from-capellactl.md` (new). Sections:
  - Command map: prefix rewrite, `create`→`add`, `import`→`sync`, `help`→`guide`, removed and alias verbs.
  - Env vars, file locations and the keychain, plus `configure migrate` and `doctor`.
  - Exit-code differences (P-CLI-08, P-CLI-16).
  - Default changes (`--password-env` naming, credentials dir, JSON shapes).
  - `apiVersion` and rollback.
  - Every intentionally different row from `parity-cli.md` and `parity-engine.md`.
- `internal/guide/topics/migrate.md` (new): `cbctl capella guide migrate`.
- Ported from capellactl through `internal/docgen/port`:
  - `docs/capella/runbook.md`
  - `custom-clusters.md`
  - `terraform.md`
  - `migrating-from-scale-script.md`
  - `backup-restore.md`
- `internal/docgen/reference/reference.go` (changed): dereference `$ref` in `isSection` and in row emission (P-ENG-03). `docs/capella/reference.md` is regenerated.
- `internal/docgen/coverage/` (changed): every capellactl `docs/*.md` must map to a cbctl doc or to a `dropped:` entry with a reason.
- `internal/docs/doccmds_test.go` (new): every `cbctl …` line in README, cbctl.mdc, `docs/capella/*.md` and the guide topics parses with `Find` and uses known flags.
- `README.md` (changed):
  - Link to the migration guide.
  - Janitor contract (F-28).
  - `make test-integration` is the Capella sandbox suite (F-29).
  - Remove `CBCTL_LIVE_CLUSTER`.
- `.cursor/rules/cbctl.mdc` (changed), from Phases 1–3:
  - Unknown subcommands exit 3.
  - `cluster create [NAME]` and the wizard.
  - `bucket/scope/collection create` alias `add`.
  - `cluster schedule apply`.
  - Hidden `cluster apply` and `project apply`.
  - `configure migrate` and `capella doctor`.
  - Legacy env var support.
  - Credentials-dir rules.
  - Deprecated legacy commands and their replacements.
  - `--timeout` accepts seconds.
  - Janitor flags (F-28).
  - `bucket add` default 1024 MB.
- `docs/capella/testing.md` (changed): a full guide replacing the stub (F-29).
- `CHANGELOG.md`: "Deprecated", "Breaking" and "Migration" sections.

**Interfaces and function signatures**
```go
// docgen/reference
func deref(node map[string]any, root map[string]any) map[string]any
func isSection(node, root map[string]any) bool
// docgen/coverage
type DocMapping struct{ Source, Target, Dropped string } // exactly one of Target/Dropped
func CheckDocCoverage(sourceDocs []string, mappings []DocMapping, repoRoot string) []Problem
```

**Data models:** `docs/capella/doc-map.yaml` (new) holds a list of `DocMapping`. Validation: every capellactl doc is listed exactly once, every `Target` exists, and every `Dropped` is non-empty.

**Error handling:** the `cbdev docs` checks exit 1 with file:line for each problem and exit 2 on usage errors. Doc tests use `t.Errorf` per bad command line, so all problems are reported in one run.

**Edge cases**
- Code fences that intentionally show failing commands: mark them `<!-- doccmds:skip -->` and include a reason.
- Commands with placeholders (`<cluster>`, `NAME`): substitute dummy values before calling `Find`.
- The port rewriter maps `capellactl configure` to `cbctl configure`, without the `capella` prefix. Add a rewrite rule and a test.

**Acceptance criteria**
- `make docs-check` passes.
- `docs/capella/reference.md` has at least 103 rows and contains `projects.clusters.serviceGroups.services`, `projects.clusters.buckets.scopes.collections.maxTTL` and `projects.clusters.credentials.access`. A test asserts that every leaf in `schema.json` appears.
- `go test ./internal/docgen/...` fails if a capellactl doc has no mapping.
- `TestDocCommands` passes for every command line in every doc. `cbctl capella guide topics` lists `migrate`.
- The migration guide names every removed or renamed capellactl verb. A test cross-checks it against `removedVerbs` and the legacy table.

## Phase 5: Sandbox integration safety

This phase combines v1 Phases 1, 2 and 4, with D3 and D4 applied. v1 has the full per-file detail. Only the changes are restated here, plus the six sections in short form.

**Changes from v1**
- **D4:** the org allowlist reads `CBCTL_IT_ALLOWED_ORG_IDS: ${{ vars.CAPELLA_SANDBOX_ORG_ID }}`. `CAPELLA_ORG_ID` comes from the same variable. The org ID isn't secret, so `secrets.CAPELLA_SANDBOX_ORG_ID` goes away and the API key stays a secret (D1). The guard also calls `GET /v4/organizations/{id}` once, so a key/org mismatch fails in preflight and not halfway through a test.
- **D3:** the nightly keeps `concurrency: { group: nightly-sandbox, cancel-in-progress: false }`, with no lock and no per-event groups. Duplicate-run protection: a new first job, `dedupe`, runs `go run ./cmd/cbdev ci dedupe`.
  - It sets `skip=true` when another run of `nightly.yml` for the same `head_sha` and the same inputs is `in_progress` or `queued`, or when a `workflow_dispatch` run duplicates a run that succeeded on that SHA in the last 20h.
  - Scheduled runs are never skipped for a success on an earlier day, because the release gate needs one per night.
  - Dependent jobs use `if: needs.dedupe.outputs.skip != 'true'`.
- The weekly janitor moves to `.github/workflows/janitor.yml` (from v1 Phase 5), so nightly has one cron. It gets its own concurrency group `sandbox-janitor` and the same D1/D4 env.

**File/module structure (combined)**
- `.github/workflows/nightly.yml`, `janitor.yml` (new):
  - D1/D4 env.
  - A preflight step that fails on empty credentials.
  - `CBCTL_IT_REQUIRE_TARGET=1`.
  - Absolute artifact paths.
  - No `|| true`.
  - `if-no-files-found: error`.
  - An `always()` run-prefix sweep.
  - `go test -timeout` 30 minutes below the job timeout.
  - The `dedupe` job.
- `internal/integration/guard.go`, `guard_test.go` (new): `GuardPolicy`, `CheckOrg`, `ValidatePrefix`, `SkipOrFail`.
- `internal/integration/target.go`, `harness.go`, `report.go`, `main_test.go`, `capella_live_test.go`, `lifecycle_test.go`, `credentials_test.go` (changed):
  - Guards.
  - The shared project is registered before apply.
  - Watchdog and signal sweep.
  - `CommandContext`.
  - `CredentialsDir`.
  - `RecordSuppliedSecret`.
  - Paths resolved against the workspace.
- `internal/ci/janitor/janitor.go` (changed):
  - Best-effort tree delete with `errors.Join`.
  - `DeleteRun`.
  - `createdAt` fix.
  - Unreadable timestamps reported.
- `internal/cli/capella_janitor.go` (changed): `--max-total-minutes`.
- `cmd/cbdev/ci_scan.go` (changed): `--require-secrets`, `--allow-missing`.
- `cmd/cbdev/ci_dedupe.go`, `internal/ci/dedupe/` (new).
- `internal/settings/settings_test.go` (changed): `TestResolve_IgnoresSandboxEnv`.
- `internal/docs/workflows_test.go` (changed): workflow assertions.

**Interfaces and function signatures (new in v2; v1 Phase 1/2/4 signatures are unchanged)**
```go
// internal/ci/dedupe
type RunRef struct{ ID int64; HeadSHA, Event, Status, Conclusion string; CreatedAt time.Time; Inputs map[string]string }
type Lister interface {
    ListRuns(ctx context.Context, repo, workflow string) ([]RunRef, error)
}
func Decide(self RunRef, others []RunRef, window time.Duration) (skip bool, reason string)
// cmd/cbdev
func runCIDedupe(ctx context.Context, args []string, out io.Writer) int // writes "skip=true|false" to $GITHUB_OUTPUT

// internal/integration
func (p GuardPolicy) VerifyOrgReachable(ctx context.Context, c *capellaapi.Client, orgID string) error
```

**Data models**
- `RunRef` as above. `Decide` ignores `self.ID` and runs from other workflows, and it treats a re-run attempt of the same run ID as not a duplicate.
- The allowlist is a set of UUIDs taken from `vars.CAPELLA_SANDBOX_ORG_ID`, comma-separated. An empty set in CI is an error.
- The run prefix is `cbctl-ci-<run_id>-<attempt>-`. `DeleteRun` refuses the bare `cbctl-ci-`.

**Error handling**

| Failure | Exit / surfaced |
|---|---|
| Empty `CAPELLA_API_KEY`/`CAPELLA_ORG_ID` | Preflight `::error::`, job red |
| Org not allowlisted, or not reachable with the key | `t.Fatalf` before any write. `TestMain` exits 1 |
| `gh api` failure in dedupe | **Fail open:** `skip=false` with a `::warning::`, so an API error can't silently cancel a night |
| Secret scan hit / missing file / too few secrets | 1 / 2 / 2. The job is red |
| Partial janitor failure | Continues across clusters and projects, `errors.Join`, exit 1 |
| Watchdog or signal | Run-prefix sweep with a bounded 10–25 min context, then the post-step `always()` sweep |

**Edge cases**
- A schedule and a dispatch on the same SHA, both queued: the single group serializes them, and the second run's `dedupe` sees the first `in_progress` and skips. GitHub may replace a pending scheduled run with a newer dispatch (F-14). That's accepted under D3; the gate's per-day dedupe in Phase 7 absorbs it, and the guide documents it.
- A re-run of a failed nightly (attempt 2) is not a duplicate.
- A local developer: no `GITHUB_ACTIONS`, so the allowlist is optional and only warns. `CBCTL_TEST_PREFIX` may be custom, but must match `^cbctl-ci-([a-z0-9]+-)*$`.
- Concurrent `t.Cleanup` and a sweep on the same project: 404 counts as success.

**Acceptance criteria**
- All of v1 Phase 1, 2 and 4 acceptance criteria, with the D4 substitution (the variable, not a secret, for the org ID).
- `go test ./internal/ci/dedupe/` table cases:
  - Same SHA in progress → skip.
  - Dispatch after a success within 20h → skip.
  - Schedule after yesterday's success → run.
  - Re-run attempt → run.
  - Lister error → run, with a warning.
- `workflows_test.go` asserts:
  - The `dedupe` job exists and every other job `needs` it.
  - The concurrency group is the constant `nightly-sandbox` with `cancel-in-progress: false`.
  - `CBCTL_IT_ALLOWED_ORG_IDS` and `CAPELLA_ORG_ID` reference `vars.CAPELLA_SANDBOX_ORG_ID`.
  - `CAPELLA_API_KEY` references `secrets.CAPELLA_SANDBOX_API_KEY`.
- A manual dispatch of `janitor.yml` reaches `ListProjects` (no exit 3), and a nightly with credentials deleted is red.

## Phase 6: Test parity to 100% and tiered coverage gates

Purpose: meet D5. Every capellactl test is COVERED or N/A with a reason, and coverage meets the tier targets. Phases 1–5 each add the tests for their own changes. This phase adds the remaining scenario ports and turns on the gates.

**File/module structure**
- New scenario files in `internal/cli/`, from `parity-tests.md` P-TST-01..05 and 09..11:
  - `scenario_apply_failure_test.go`
  - `scenario_confirm_test.go`
  - `capella_managed_restore_test.go`
  - `scenario_sync_edge_test.go`
  - `scenario_ci_test.go`
  - `scenario_multi_project_test.go`
  - `scenario_init_test.go` (extended)
  - `help_contract_test.go` (P-TST-17)
- `internal/resources/cluster_test.go`, `kinds_test.go` (changed): `TestScaleInGuards`, `TestBucketPreflight` (P-TST-03). Delete `cover_helpers_test.go` (F-23).
- `internal/capellafake/` (changed):
  - Strict routes on by default (P-TST-07).
  - Duplicate project and cluster names allowed (P-TST-08).
  - `FailNext` on stats.
  - `RenameProject`.
  - A context-cancel hook.
- `internal/clitest/schemas/` and `AssertJSONSchema` (new; P-TST-12).
- `internal/cli/capella_output.go`, prompts (changed): `confirm` and the prompts use `Deps.Prompter` when it's set (F-17).
- `internal/cli/hermetic_test.go` (changed):
  - Add `internal/capella`, `capellaapi`, `rest` and `redact`.
  - The allow-list becomes file + needle pairs with reasons.
  - Violations fail hard.
- `internal/capella/cluster.go`, `connectivity.go` (changed): take an `engine.Clock`.
- `internal/integration/clone_test.go`, `backups_test.go`, `credentials_test.go`, `power_test.go` (changed): implemented on the fake, with no unconditional SKIP (P-TST-15, F-22). `server_test.go` moves behind the `integration && server` tag and runs in its own CI job.
- `cmd/cbctl/main_test.go` (new): `TestBinarySmoke` on the 3-OS CI matrix (P-TST-16).
- `internal/docgen/porttable/` (new), `cmd/cbdev/docs_check.go` (changed): `ac-coverage --port-table --source-list --min-ported` (F-19). Per-test checking: every Python test name maps to a Go test tagged `CAPxxx_ACn` or to an `N/A:` reason (P-TST-06).
- `internal/cli/testdata/scenario-port.md` (rewritten): all 151 files, plus per-test C/P/M/N/A counts that are computed rather than hand-written. The unit, contract, integration and binary tiers are included.
- `internal/cli/testdata/capellactl-tests.txt` (new): every `file::test` id from capellactl @ `6389d5a`, generated by `pytest --collect-only -q` and checked in.
- `docs/backlog/release-1.0.0/stories.yaml` (changed): real statuses. `Makefile` `ac-coverage` targets 1.0.0 with `--min-acs 1` (F-20).
- `internal/docs/workflows_test.go`, `doccmds_test.go`, `examples_test.go` (F-21).
- `Makefile` `cover-gate` (new; replaces `cover-engine`): tiered thresholds with `-coverpkg=./internal/...` (P-TST-18).

**Interfaces and function signatures**
```go
// porttable
type Status string // "covered" | "partial" | "missing" | "n/a"
type TestRow struct{ PyID, Status, GoTest, Reason string }
func ParseSourceList(r io.Reader) ([]string, error)            // pytest ids
func CollectGoTags(root string) (map[string][]string, error)    // CAPxxx_ACn → []GoTestName
func Check(py []string, rows []TestRow, tags map[string][]string, minCovered float64) []Problem
// clitest
func AssertJSONSchema(t testing.TB, schema string, doc []byte)
// capellafake
func WithStrictRoutes(tb testing.TB) Option
func (f *Fake) RenameProject(id, name string)
func (f *Fake) OnPoll(path string, fn func(n int)) // cancel/timeout injection
// cover gate
type Tier struct{ Name string; Pkgs []string; Min float64 }
func runCoverGate(profile string, tiers []Tier) int // cmd/cbdev cover-gate
```

**Data models**
- `TestRow`: `PyID` is the pytest node id. `Status` is one of the 4 values, normalized to lowercase; unknown values are an error. A `covered` row needs `GoTest` to resolve to an existing `func Test…` (or subtest path). An `n/a` row needs a non-empty `Reason`. `partial` and `missing` are allowed only while `--min-covered` < 100.
- Coverage tiers, from `parity-tests.md` §7:

| Tier | Packages | Min |
|---|---|---|
| core | `plan`, `engine`, `resources`, `config/capellafile`, `capellaapi`, `presets` | 90 |
| security | `redact`, `secrets`, `settings` | 90 |
| cli | `cli` | 85 |
| importer | `importer` | 80 |
| testinfra | `capellafake`, `clitest` | 85 |
| tooling | `ci/...`, `docgen/...` | 70 |
| server-legacy | `rest`, `server`, `capella` | 60 |

- The thresholds ratchet. While the work is in progress the Makefile holds the current floor. The **merge-to-main bar** is the table above and `--min-covered 100`.

**Error handling**
- `cbdev docs ac-coverage` exits 1 and lists each problem (`py id → reason`), and exits 2 on usage errors.
- `cover-gate` exits 1 with a per-tier table. A package that disappears is reported missing, not skipped.
- Strict fake routes: `tb.Errorf("capellafake: unhandled GET /…")`. The test fails, but the response is still 404, so the code under test sees realistic behavior.
- Hermetic violations: `t.Errorf` naming the file and needle.
- Prompter EOF means "no", the same as TTY EOF.

**Edge cases**
- Parametrized pytest ids (`test_x[aws]`): each parameter is its own row, or one row with a `*` suffix when one Go table test covers all of them.
- A Python test that asserts Python-only behavior (click internals, PyInstaller bundling, Python version): `n/a` with a reason such as "packaging: covered by `TestBinarySmoke` equivalents" or "click-specific".
- One Go test covering several Python tests: allowed. Tags list every `CAPxxx_ACn` it satisfies.
- Fake duplicate names now allowed: re-check `TestExecute_ConflictOnCreateAdopts`, which applies only to child resources.
- Strict routes turned on may break currently green tests. Fix each route or add the missing handler; never globally disable strict mode.
- Coverage of code reached only through `cli`: `-coverpkg` counts it once.

**Acceptance criteria**
- `make ac-coverage` passes with `--min-covered 100`: 878 of 878 Python test ids are `covered` or `n/a`, and every `n/a` has a reason. Deleting any Go test that a row cites makes it fail.
- `make cover-gate` passes at every tier minimum in the table.
- The P-TST-01..05 acceptance criteria all hold, including the exit codes:
  - 130 on interrupt, 5 on timeout, "Failed: …"/"Completed before the failure" text.
  - Decline leaves zero writes and an unchanged file.
  - Guards block with exit 4.
  - Restore on 422.
  - Sync edge goldens.
- `make test-integration-fake` reports zero `SKIP` for clone, backups, credentials (fake path) and power.
- `TestHermeticGrep` fails on a planted `time.Sleep(` in `internal/capella`.
- `TestBinarySmoke` is green on linux, macos and windows.
- `scenario-port.md` footer counts are generated and equal the checker's output.

## Phase 7: Release gate that blocks

This is v1 Phase 5, minus the concurrency change (D3) and the janitor split (moved to Phase 5).

**File/module structure**
- `internal/ci/releasegate/gate.go` (changed):
  - One run per UTC day (the newest attempt).
  - A run counts only when the `integration` job actually ran.
  - Runs skipped by `dedupe` don't count.
  - `sort.SliceStable`.
- `cmd/cbdev/ci_gate.go` (changed): `defaultRepo` comes from `git remote get-url origin`, falling back to `mminichino/cbctl` (F-32).
- `.github/workflows/release.yml` (changed):
  - Remove `--report-only`.
  - Workflow-level `permissions: contents: read`.
  - `goreleaser` job: `contents: write` and `timeout-minutes: 30`.
- `internal/docs/workflows_test.go` (changed): assertions for the above.

**Interfaces and function signatures**
```go
type Run struct{ ID int64; HeadSHA, Event, Conclusion string; StartedAt time.Time; IntegrationRan, Deduped bool }
func Evaluate(runs []Run, commit string, nights int) (ok bool, lines []string)
func FetchRuns(ctx context.Context, repo, workflow, branch string) ([]Run, error)
func fetchJobs(ctx context.Context, repo string, runID int64) (integrationRan, deduped bool, err error)
func defaultRepo(ctx context.Context) string
```

**Data models:** the day key is `StartedAt` truncated to a UTC date. `IntegrationRan` is true when the `integration` job's conclusion is one of `success|failure|cancelled|timed_out`. `Deduped` is true when the `dedupe` job output is `skip=true`.

**Error handling**

| Failure | Exit |
|---|---|
| `gh api` error or rate limit | 1, which blocks the release |
| Fewer than 3 consecutive green days | 1, with a per-day table |
| Newest green run isn't the tag commit | 1 |
| goreleaser runs over 30 minutes | Cancelled; `latest` isn't moved |

**Edge cases**
- A day with only a deduped run: that day doesn't count and breaks the streak. The guide documents re-dispatching.
- A schedule evicted by a dispatch (D3): the dispatch doesn't count, because only `event == schedule` runs count. The streak resets, and that's documented.
- A re-run attempt on the same day: the newest attempt wins.

**Acceptance criteria**
- `go test ./internal/ci/releasegate/` cases:
  - Two runs on one date count as one day.
  - A skipped integration job doesn't count.
  - A deduped run doesn't count.
  - The `defaultRepo` fallback works.
- `workflows_test.go` asserts:
  - No `--report-only` in `release.yml`.
  - SHA pins.
  - `contents: read` at workflow level.
  - A goreleaser timeout is set.
- Fixture test: `cbdev ci gate --runs-json testdata/not-green.json` exits 1, and `testdata/three-green.json` exits 0.

## 3. Exit criteria for merging `mm--capellactl-port` to `main`

1. **Functional parity:** every row in `parity-cli.md` §1–4 and `parity-engine.md` §3 is MATCH, or RENAMED/DIFFERENT with a migration-guide entry and a passing `TestCapellactlCompat` case. Zero MISSING rows. INTENTIONALLY-DROPPED is allowed only for features capellactl never shipped.
2. **Migration:** `configure migrate` and the legacy shims pass their acceptance tests, and `capella doctor` reports clean on a migrated fixture.
3. **Test parity:** `make ac-coverage` passes with `--min-covered 100`.
4. **Coverage:** `make cover-gate` meets every tier in Phase 6.
5. **Stability:** `rg 'os\.Exit\(' internal/ --glob '!*_test.go'` is empty, `go test -race ./...` is green, `make lint` is green, and the hermetic gate fails hard.
6. **Docs:** `make docs-check` passes. The migration guide, the 5 ported operator docs and the full config reference exist. README and cbctl.mdc are current.
7. **Nightly:** one green `workflow_dispatch` run of `nightly.yml` on the branch tip before the merge. GitHub fires `schedule` only on the default branch, so the scheduled streak cannot exist before `nightly.yml` is on `main`. The 3-consecutive-green-scheduled-nightlies rule is enforced afterwards by the release workflow's blocking gate (`cbdev ci release-gate`), which must pass before a release tag publishes.

## 4. Workflow (D7 and user rules)

- All commits go on `mm--capellactl-port`. Each phase ends with `go build ./... && go vet ./... && make lint && go test -race ./... && make docs-check`, then a commit (or a small series) and `git push origin mm--capellactl-port`. Never push to `main`.
- Phase commits use the existing style, for example `fix(cli): …`, `feat(settings): …` and `test(cli): …`.
- `.cursor/rules/cbctl.mdc` and `README.md` are updated in the same phase as the behavior change, per `general.mdc`. Phase 4 adds the migration guide on top.
- Phases 0 → 1 → 2 → 3 → 4 run in order, because each builds on the previous. Phase 5 can start after Phase 1. Phase 6 runs alongside the others and finishes last. Phase 7 comes after Phase 5.

## 5. Decisions on Q7–Q10 (2026-11-16)

All four plan defaults were accepted:
- **Q7:** restore the `cluster create` wizard on a TTY.
- **Q8:** `bucket add` defaults to 1024 MB.
- **Q9:** add `--output-format legacy` to `configure list/show`.
- **Q10:** honor `BEARER_TOKEN`, `ORG_ID` and `API_BASE_URL` for one release, with a warning.

- **Q7:** capellactl's `cluster create` wizard. Restore it fully (the default in Phase 2), or document that cbctl needs flags or `capella init`?
- **Q8:** `bucket add` default memory. Use 1024 MB to match capellactl (the default in Phase 2), or keep 100 MB and document it?
- **Q9:** `configure list/show -o json` shape. Add `--output-format legacy` (the default in Phase 2), or document the change only?
- **Q10:** Legacy env vars `BEARER_TOKEN`, `ORG_ID` and `API_BASE_URL` are generic names. Honor them for one release with a warning (the default in Phase 3), or ignore them and only document?

*End of plan v2.*
