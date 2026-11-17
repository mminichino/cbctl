# Spec: Fold capellactl into cbctl — Phase 4 (test infrastructure, live suite, CI, docs, backlog port)

Status: ready for implementation. Builds on [`capella-fold-in-phases-0-2.md`](capella-fold-in-phases-0-2.md) and [`capella-fold-in-phase-3.md`](capella-fold-in-phase-3.md); Phases 0–3 should be merged (or at least their interfaces present) before this phase, because Phase 4 completes and verifies them.

Scope of Phase 4:
1. **Hermetic test entry point** (`cli.NewRootCmd` / `cli.Run` / `cli.Deps`) so scenarios and integration tests run in-process without global state (§2).
2. **Complete `capellafake`**: every operation in the `capellaapi.Inventory`, with deterministic time, fault injection and a parity test against the published API response schemas (§3).
3. **Scenario-suite infrastructure** and the traceability check between stories and tests (§4).
4. **Integration suite** that runs unchanged against the fake (every push) and a real Capella sandbox (nightly), harness, cleanup, **janitor**, **log secret scanner**, **release gate** (§5).
5. **`cbdev`** developer tool (docs generators/checkers, API snapshot refresh, CI utilities) (§6).
6. **CI/CD**: `ci.yml`, new `nightly.yml`, `release.yml` gate, Makefile, coverage gates (§7).
7. **Documentation**: README, guides, examples, CHANGELOG, NOTICE, generated reference, doc-command linter (§8).
8. **Port of the capellactl backlog documentation** (release 1.5.0 and 2.0.0 stories, API coverage reports, architecture reference, story/coverage generators) into `docs/backlog/` (§9).

Out of scope: implementing any 1.5.0/2.0.0 feature (those stay backlog); Windows-specific install docs; building distributables beyond the existing GoReleaser setup; third-party-notices generator (static `NOTICE` only); a full port of the 63 release-1.0.0 story files (only an index, §9.4).

Sources to port from (capellactl repo at `/Users/michael/IdeaProjects/capellactl/`): `tests/fakes/capella.py`, `tests/integration/{harness,conftest,test_*}.py`, `tests/scenario/helpers.py`, `tests/unit/fakes/test_fake_parity.py`, `scripts/{ci-janitor,scan-log-secrets,release-gate,update-story-index,update-api-coverage,update-api-responses,update-reference,check-ac-coverage}.py`, `Makefile`, `.github/workflows/{ci,nightly}.yml`, `docs/*.md`, `docs/release-1.0.0/00-architecture.md`, `docs/release-1.5.0/**`, `docs/release-2.0.0/**`, `docs/api/*.json`. Record the capellactl commit used (`git -C <capellactl> rev-parse HEAD`) in `docs/backlog/SOURCE.md` (§9.2).

---------------------------------------------------------------------------------------------------

## 0. Conventions and deltas from earlier specs

Same conventions as the Phase 0–2 spec §0 (stdlib `testing`, table tests, `context` first, `%w`, doc comments, offline unit tests, no secrets in output, `-race`).

Deltas (all land in this phase's PR unless already done):
1. **`cli.Deps` and `cli.NewRootCmd`** replace the package-level `rootCmd` + `init()` (§2). Existing `newXxxCmd()` constructors stay; only the root assembly changes. Phase 2/3 tests that used `rootCmd.SetArgs` migrate to `cli.Run`.
2. **Integration environment variables** are unified and supersede the Phase 2/3 notes (`CBCTL_LIVE_CLUSTER`, `CBCTL_TEST_PREFIX`): see §5.1. `CBCTL_LIVE_CLUSTER=1` remains accepted as an alias of `CBCTL_IT_CLUSTERS=1` with a deprecation `t.Log`.
3. **Test naming/traceability**: Go tests that verify a story acceptance criterion are named `Test<Subject>_CAP<nnn>_AC<n>_<Behaviour>` (Phase 3 §13.3). `cbdev docs ac-coverage` enforces it (§4.3).
4. **Dev tool**: `cmd/cbdev` is added; it must **not** be shipped: verify `.goreleaser.yaml` builds only `./cmd/cbctl` (it does: `builds[0].main: ./cmd/cbctl`) and that `make build` stays `go build -o bin/cbctl ./cmd/cbctl`.
5. New dependency: none required beyond Phases 0–3 (`jsonschema/v6` and `yaml.v3` are already present). `cbdev` may use `golang.org/x/net/html` **only** if the link checker needs it — prefer a regexp-based Markdown link scan (no new dependency).

---------------------------------------------------------------------------------------------------

## 1. File and module structure (new/changed)

```
cmd/cbdev/main.go                        // dev tool entry (not shipped)
internal/cli/
  root.go                                // NewRootCmd(deps), Execute(), Run(...) (changed)
  deps.go                                // Deps, DefaultDeps, Prompter, test hooks (new)
  run.go                                 // Run(ctx, args, deps) int (new)
internal/capellafake/                    // completed fake (Phase 2 created the stub)
  fake.go state.go ids.go clock.go faults.go calls.go seed.go
  routes_org.go routes_project.go routes_cluster.go routes_freetier.go routes_schedule.go routes_cidr.go
  routes_bucket.go routes_scope.go routes_collection.go routes_credential.go routes_snapshot.go routes_backup.go
  parity_test.go inventory_test.go fake_test.go
  testdata/management-api-v4.0-responses.json    // copied from docs/backlog/api (single source: see §3.7)
internal/clitest/                        // scenario helpers (non-test package so other test packages can import)
  scenario.go result.go prompts.go assert.go
internal/integration/                    // //go:build integration
  harness.go target.go cleanup.go report.go
  lifecycle_test.go cluster_lifecycle_test.go data_model_test.go credentials_test.go power_test.go
  schedule_test.go scaling_test.go clone_test.go backups_test.go redaction_test.go main_test.go
internal/ci/                             // CI utilities as libraries (used by cbdev and the hidden janitor command)
  janitor/janitor.go (+_test)            // DeleteProjectTree, DeleteProjectsNamed, Stale
  secretscan/scan.go (+_test)
  releasegate/gate.go (+_test)
internal/docgen/                         // docs tooling libraries
  port/ (rewrite engine, rules, idmap)   // §9.5
  stories/ (parse story headers, index generator)           // §9.6
  coverage/ (API coverage generators + data)                // §9.7
  data/*.yaml                            // extracted coverage data (§9.7)
  accoverage/  reference/  links/  apisnap/
  testdata/…                             // golden files
internal/docs/                           // tests that lint repo docs
  doccmds_test.go examples_test.go links_test.go
docs/
  capella/                               // ported user guides + generated reference + examples (§8)
  backlog/                               // ported backlog documentation (§9)
  specs/                                 // existing specs
.github/workflows/ci.yml nightly.yml release.yml
Makefile
CHANGELOG.md NOTICE README.md
```
Import rules (test with an import-graph test): `capellafake` imports `engine`, `capellaapi` (for `BucketID`, `Inventory`) and stdlib only — **never** `cli`; `clitest` imports `cli`, `capellafake`, `settings`, `engine`; `integration` imports `clitest`, `capellafake`, `ci/janitor`; `ci/*` import `capellaapi`, `engine`; `docgen/*` import stdlib + `yaml.v3` only (no cbctl internals except `capellaapi.Inventory` for `apisnap`).

---------------------------------------------------------------------------------------------------

## 2. Hermetic CLI entry (`internal/cli`)

### 2.1 Why
Package-level `rootCmd` and global state (cobra flag values, `logging` writers, `redact` registry, `os.Getenv`, `os.Stdin`) make in-process tests leak state between runs. Scenario and integration suites need hundreds of independent invocations.

### 2.2 Interfaces
```go
// deps.go
type Deps struct {
	Env      func(string) string          // default os.Getenv
	LookupEnv func(string) (string, bool)
	Stdin    io.Reader                    // default os.Stdin
	Stdout, Stderr io.Writer              // default os.Stdout/os.Stderr
	IsTTY    func(io.Reader) bool         // stdin TTY detection; default term.IsTerminal on *os.File
	Cwd      func() (string, error)       // default os.Getwd
	Home     string                       // settings home dir; default settings.HomeDir(Env)
	Keyring  settings.Keyring             // default settings.OSKeyring{}
	Clock    engine.Clock                 // default engine.RealClock{}
	HTTPClient *http.Client               // default nil (capellaapi builds its own)
	Prompter Prompter                     // default terminal prompter on Stdin/Stderr
	Version  string
}
func DefaultDeps() Deps
type Prompter interface {
	Confirm(question string, def bool) (bool, error)
	Typed(prompt, expected string) (bool, error)         // typed-name confirmation
	Ask(question, def string) (string, error)
	Select(question string, options []string) (int, error)
	Secret(question string) (string, error)
}

// root.go
func NewRootCmd(d Deps) *cobra.Command      // builds a fresh tree every call; no package-level command vars
func Execute() error                         // = NewRootCmd(DefaultDeps()).ExecuteContext(signalCtx)
// run.go
// Run executes one CLI invocation in-process and returns the exit code (what HandleError would return).
// It never calls os.Exit. SIGINT handling is the caller's responsibility (pass a cancellable ctx).
func Run(ctx context.Context, args []string, d Deps) int
```
Rules:
- `cmd/cbctl/main.go`: `os.Exit(int(cli.HandleError(cli.Execute())))` (Phase 0–2 spec §2.8) — unchanged semantics.
- **No direct use of** `os.Getenv`, `os.LookupEnv`, `os.Setenv`, `os.Stdin/Stdout/Stderr`, `os.Getwd`, `time.Now`, `time.Sleep` inside `internal/cli/capella*`, `internal/config/capellafile`, `internal/settings`, `internal/plan`, `internal/resources`, `internal/importer`, `internal/secrets` — they use `Deps`/`Clock`. `settings.LoadDotEnv` takes the env via `Deps` and returns values to merge into a **copy** (`mergeEnv`), not `os.Setenv` (only `Execute()` — the real process entry — calls `os.Setenv` for the loaded `.env` values, to stay compatible with legacy commands that read `os.Getenv`). Enforce with `internal/cli/hermetic_test.go`: grep the listed packages' non-test sources for the forbidden identifiers (allow-list: `deps.go`, `Execute`, files named in the test).
- `logging` writers become instance state: `logging.New(out, err io.Writer, level Level) *Logger`; the package-level functions delegate to a default logger that `NewRootCmd` replaces for the command's lifetime via `cmd.SetContext` (store the logger in context; helpers `logging.From(ctx)`). Keep the existing package-level API working for legacy commands (they write to the default logger which `Run` **temporarily** redirects under a mutex `logging.WithOutputs(out, err, func())` — document that legacy-command tests are not `t.Parallel()`).
- `redact`: `Run` calls `redact.Reset()` at start (tests only; `Deps` field `ResetRedaction bool`, set by `clitest`).
- `engine.Clock` injection replaces the Phase 2 `clockFactory` package var.

### 2.3 Acceptance (this section)
1. `go test ./internal/cli -run TestRunIsHermetic -race -count=3`: two consecutive `Run` calls with different flags/env produce independent results (no flag leakage: run `capella project list --output json` then `capella project list` and assert text output the second time); concurrent `Run` calls in `t.Parallel()` subtests with separate `Deps` pass under `-race` (for non-legacy commands).
2. The hermetic grep test passes.
3. `cbctl --version`, all existing Server commands and the Phase 0–3 commands behave identically (existing tests green).

---------------------------------------------------------------------------------------------------

## 3. `internal/capellafake` — complete in-memory Capella

Goal: behavioural parity with capellactl's `tests/fakes/capella.py` (1244 lines) and the OpenAPI spec for every operation in `capellaapi.Inventory` (80 rows). Read the Python file section by section; this spec lists the contract.

### 3.1 API
```go
type Fake struct { /* unexported; guarded by one mutex */ }
type Option func(*Fake)
func WithOrgID(id string) Option            // default "org-1"
func WithAPIKey(key string) Option          // default "fake-api-key-0123456789" (≥8 chars so redaction/scan work)
func WithClock(c *engine.FakeClock) Option  // default: a new FakeClock; state progression derives from it
func New(t testing.TB, opts ...Option) *Fake // starts httptest.Server (TLS off, 127.0.0.1); t.Cleanup(Close)
func (f *Fake) URL() string                 // pass as CAPELLA_API_BASE_URL (http://127.0.0.1:port; allowed by NormalizeBaseURL)
func (f *Fake) OrgID() string; func (f *Fake) APIKey() string; func (f *Fake) Clock() *engine.FakeClock
func (f *Fake) Handler() http.Handler       // for tests that want their own server
```
Authentication: every request must carry `Authorization: Bearer <APIKey>` else **401** with Capella's error body (`{"httpStatusCode":401,"code":1001,"message":"Unauthorized","hint":"…"}`); wrong org id in the path → **404** `code 1003`-style (copy the Python `_org` behaviour). Unknown route → 404 JSON error, and the request is still logged.

### 3.2 Error and response helpers
`errBody(status, message string, code int, hint string)` → `{"httpStatusCode","code","message","hint"}`; codes: 1000 malformed/validation, 1001 unauthorized, 1002 forbidden (`Access Denied.`), 1003 not found, **1010 conflict "already exists"**, 1006 precondition failed (copy from Python where they differ). Create responses return `{"id":…}` (201/202 per endpoint doc in `endpoints.py` docstrings); updates 204; deletes 204/202; lists are paginated: `{"data":[…], "cursor":{"pages":{"page","next","previous","last","perPage","totalItems"},"hrefs":{…}}}` honouring `page`/`perPage`/`sortBy`/`sortDirection` (default perPage 10, max 100). Lists whose spec response is not paginated (`freeTier` buckets, bucket backup lists, cycles) return `{"data":[…]}`.

### 3.3 State model (`state.go`)
In-memory maps keyed by ID with insertion order retained: `projects`, `clusters` (per project; `free` flag), `buckets` (per cluster; ID = `capellaapi.BucketID(name)`), `scopes/collections` (nested), `credentials` (`users`), `allowedCIDRs`, `onOffSchedule` (per cluster), `snapshotSchedule` (per cluster), `snapshots`, `snapshotRestores`, `bucketBackupSchedules`, `backups` (grouped in cycles), `backupExports`, `deletionProtection`, per-resource ETag version counters (`Version: N`), `audit` blocks `{createdBy,createdAt(RFC3339 from the fake clock),modifiedBy,modifiedAt,version}`.
Concurrency: all handlers lock the same mutex; `Calls()` reads under lock.

### 3.4 Behaviours (each must have a test in `fake_test.go`)
- **Clusters**: `POST …/clusters` → 202 `{id}`, initial `currentState` per transition path (default path `[deploying, healthy]`, `step` 30 s); validation: provider/region present, ≥1 service group with `data`, Azure non-Ultra disks reject `storage`/`iops` (400 `Cannot specify disk size or IOPS for disk type P6`, see Python `_azure_disk_check`), duplicate name in project → **409 code 1010**. `PUT` requires `If-Match` equal to current version when sent (stale → **412**); body replaces `name`, `description`, `support`, `serviceGroups`; sets state path `[scaling, healthy]` (or `[rebalancing,healthy]` when only nodes changed — copy Python); `DELETE` → 202 with state `destroying` then 404 after the step (or `destroyFailed` via `FailDestroy`); deletion-protected delete → **409** `code 1041`-style message copied from Python; `PUT …/deletionProtection`; `POST/DELETE …/activationState` → 202, states `[turningOff, turnedOff]` / `[turningOn, healthy]`; `GET …/stats` → `{totalMemoryInMb, freeMemoryInMb, maxReplicas}` computed from the data service memory minus bucket allocations (copy `_cluster_stats`); `connectionString` = `couchbases://cb.<id>.fake.cloud.couchbase.com`.
- **State progression is lazy and clock-driven**: `SetTransition(clusterID string, path []string, step time.Duration)` records `(path, startedAt=clock.Now(), step)`; every read computes `idx = min(int(clock.Now().Sub(startedAt)/step), len(path)-1)`. `FakeClock.Sleep` advances time, so a waiter polling with `FakeClock` converges deterministically. `Advance(d)` is a convenience for `Clock().Advance(d)`.
- **Free tier**: one free cluster per org (second → 409/400 per Python), only `name`,`description`,`cloudProvider` accepted, `support.plan: "free"`; free buckets accept only `name`,`memoryAllocationInMb` (+ `enableCrossClusterVersioning` via PUT); `activationState` free variants.
- **On/Off schedule**: `GET` → 404 when none; `POST` 204 (409 if exists); `PUT` requires `timezone` and all 7 `days`; `activationState` pause/resume sets `activationStatus` `paused|active`.
- **Allowed CIDRs**: create 201 (409 duplicate CIDR), list, get, delete 204; `type: permanent|temporary` from `expiresAt`; max 75 (400/422 beyond).
- **Buckets/scopes/collections**: IDs base64url of name; defaults applied on create (copy Python: `durabilityLevel none`, `replicas 1`, `storageBackend couchstore`/`magma` default per Python, `vbuckets` rules, ephemeral rules); immutable fields rejected on PUT (400); bucket `stats.itemCount` etc. present; `_default` scope/collection exist and cannot be deleted (400); `maxTTL` update requires server ≥ 7.6.0 (cluster `couchbaseServer.version` default `7.6.2`) else 400; memory allocation exceeding free memory → 400/422 message copied from Python.
- **Credentials (`users`)**: create validates the password policy (`secrets.MeetsRules`) → 400 `code 1000` with the rules; returns `{id, password}` **only on create**; GET/list never include the password; `PUT` may carry `password`; `access` model with `privileges`, `resources.buckets[].scopes[].collections`; list returns `*` wildcards as Capella does (a whole-bucket grant comes back as `scopes:[{name:"*"}]` — see `credential.py` `without_wildcards`); duplicate name → 409.
- **Cloud snapshot backups**: schedule `GET` → **204 with empty body** when none; `PUT` validated against options (`intervals [1,2,4,6,8,12,24]` hours — copy the Python options by plan; min/max retention) else 400 with the allowed values; snapshots complete after `step` (`queued → pending → complete`), appear in lists only once `pending`/`complete` (queued ones are not listed), retention `PUT`, delete 202, restore 202 `{restoreId}` (replaces all data; restore record progresses `queued→inProgress→complete`), regions list (`AWS`/`Azure` only; GCP → 400/empty per Python), project-level summary list.
- **Bucket backups**: schedule `GET` 404 when none, `POST` 202, `PUT` 204, `DELETE` 202 (validate `dayOfWeek/startAt/incrementalEvery/retentionTime/costOptimizedRetention` enums); on-demand `POST …/backups` → 202 **no body**; backups grouped in cycles (`cycleID`), listing latest per bucket, `GET` by id, `DELETE` removes the whole cycle, restore requires `targetClusterID, sourceClusterID, backupID, services`, export `POST` 202 `{exportId}` (409 when the cycle already has one) and `GET` returns `status` progressing to `complete` with `backupDownloadURL` (valid 1 h).
- **ETag**: responses to single-resource GETs carry `ETag: "Version: N"`; `If-Match` on PUT accepts `N` (bare) and `Version: N`; mismatch → 412; resources without ETag support (allowed CIDRs) ignore it.
- **Rate limiting**: `RateLimit(n int, retryAfter time.Duration)` makes the next *n* requests return 429 with `Retry-After`.

### 3.5 Seeding and inspection (`seed.go`, `calls.go`, `faults.go`)
```go
func (f *Fake) SeedProject(name string, fields ...Field) string                       // returns id
func (f *Fake) SeedCluster(projectID, name string, state string, fields ...Field) string
func (f *Fake) SeedFreeCluster(projectID, name, state string, fields ...Field) string
func (f *Fake) SeedBucket(clusterID, name string, fields ...Field) string
func (f *Fake) SeedScope(clusterID, bucket, scope string, collections map[string]int)
func (f *Fake) SeedCredential(clusterID, name string, access []capellaapi.AccessEntry, advanced bool) string
func (f *Fake) SeedSnapshot(clusterID string, createdAt string, retentionHours int, status string) string
func (f *Fake) SeedBucketBackup(clusterID, bucket, method, source string) string
type Field func(map[string]any)               // e.g. WithField("deletionProtection", true)
func (f *Fake) SetTransition(clusterID string, path []string, step time.Duration)
func (f *Fake) ClusterState(clusterID string) string
func (f *Fake) FailDestroy(clusterID string)  // delete ends in destroyFailed
func (f *Fake) Advance(d time.Duration)

type Call struct{ Method, Path string; Query url.Values; Header http.Header; Body any /* decoded JSON */; Status int; Time time.Time }
func (f *Fake) Calls(method, pathGlob string) []Call   // "" = any; glob via path.Match with {id} segments allowed as *
func (f *Fake) Writes() []Call                          // POST/PUT/PATCH/DELETE
func (f *Fake) ResetCalls()
func (c Call) String() string                           // redacts bodies via redact.Object (no secrets in failure output)

func (f *Fake) FailNext(method, pathGlob string, o FaultOptions)
type FaultOptions struct{ Status, Code int; Message string; Times int /*default 1*/; Headers http.Header;
	DropAfterRead bool /*read the request then close the connection: lost-response POST*/; Delay time.Duration; Before func() }
```
`FailNext` faults apply in registration order; `Times` counts down; `DropAfterRead` uses `http.Hijacker` to close the connection after the handler has **applied nothing** unless `ApplyThenDrop: true` (the resource is created, then the connection is dropped — the ambiguous-write case; required by Phase 3 scenario 6).

### 3.6 Inventory parity test (`inventory_test.go`)
For every `capellaapi.Inventory` row, assert the fake registers a route with that method and path template (no row without a route; no extra routes). Table-driven from `capellaapi.Inventory`.

### 3.7 API response parity (`parity_test.go`)
`testdata/management-api-v4.0-responses.json` (single source of truth: `docs/backlog/api/management-api-v4.0-responses.json`, copied by `make docs`; a test asserts the two files are byte-identical) holds, per inventory operation, the documented 2xx status codes and the response JSON schema with `$ref`s resolved (format identical to capellactl's `docs/api/management-api-v4.0-responses.json`). The test drives each operation against the fake with a canonical request (table in the test), then asserts: status ∈ documented 2xx; body validates against the schema via `jsonschema/v6` with `additionalProperties` ignored for fields the fake adds (the Python test's rules — read `tests/unit/fakes/test_fake_parity.py` and port its tolerance rules exactly); required fields present. Operations the fake serves with 204 must have a documented 204.

### 3.8 Fake acceptance criteria
1. `go test -race ./internal/capellafake/...` green; inventory and parity tests green.
2. Each behaviour bullet in §3.4 has a named test (`TestFake_<Area>_<Behaviour>`) using plain `net/http` against `f.URL()`.
3. Determinism: running the full fake suite 20× (`-count=20`) produces identical results; no `time.Now()`/`time.Sleep` in the package (grep test).
4. All Phase 2 §3.11 and Phase 3 §13.2 scenario tests run against this fake without special-casing.

---------------------------------------------------------------------------------------------------

## 4. Scenario-suite infrastructure (`internal/clitest`)

### 4.1 Helpers
```go
type Scenario struct {
	T       testing.TB
	Fake    *capellafake.Fake
	Clock   *engine.FakeClock
	Env     map[string]string            // CAPELLA_API_BASE_URL, CAPELLA_API_KEY, CAPELLA_ORG_ID, CBCTL_HOME preset
	Dir     string                       // t.TempDir(): cwd for the CLI and home of capella.yaml
	Keyring *settings.MemKeyring
	Prompts *ScriptedPrompter
}
func NewScenario(t testing.TB, opts ...ScenarioOption) *Scenario    // wires fake + deps; registers cleanup
func (s *Scenario) Run(args ...string) Result                       // cli.Run with hermetic Deps; stdin empty, non-TTY unless s.TTY(true)
func (s *Scenario) RunWith(stdin string, args ...string) Result
func (s *Scenario) WriteFile(rel, text string) string                // under s.Dir; returns absolute path
func (s *Scenario) ReadFile(rel string) string
func (s *Scenario) Answers(answers ...string)                        // script Prompter: confirm "y"/"n", typed text, selections
func (s *Scenario) TTY(on bool)
type Result struct{ Code int; Stdout, Stderr string }
func (r Result) JSON(t testing.TB) any                               // json.Unmarshal(Stdout) must succeed; fails the test otherwise
func (r Result) Lines() []string
type ScriptedPrompter struct{ /* queue of answers; unexpected prompt → t.Fatalf with the question text */ }
func AssertNoSecret(t testing.TB, r Result, secrets ...string)       // none of secrets (also JSON-escaped, URL-encoded) in Stdout/Stderr
func AssertWrites(t testing.TB, f *capellafake.Fake, want ...string) // "METHOD glob" list, exact multiset match
func AssertNoWrites(t testing.TB, f *capellafake.Fake)
func GoldenText(t testing.TB, name, got string)                      // testdata/<name>.golden; -update flag rewrites
```
`Scenario` uses a `FakeClock` shared by the fake and the CLI so waits are instantaneous. Default timeouts in scenarios: `--timeout 60m` (fake time).

### 4.2 Scenario organization
`internal/cli/scenario_<topic>_test.go` (package `cli_test`), one file per area, named like capellactl's `tests/scenario/test_<topic>.py` (see §9.5 test-location rule). Tests that verify a story AC use the tag-bearing name (§0.3). **Port plan**: the 63 capellactl scenario files (`tests/scenario/test_*.py`) and 9 integration files cover stories CAP-001…CAP-063; Phase 2/3 specs already require specific scenarios; Phase 4 requires that **every scenario file in capellactl whose story is in scope of Phases 0–3 has a Go counterpart** unless listed in `internal/cli/testdata/scenario-port.md` as intentionally not ported with a reason (Windows, wheel/binary packaging, rulesets, Python-only). Produce that file (table: capellactl file → Go file | N/A reason) as part of this phase; reviewers use it as the checklist.

### 4.3 Traceability check
`cbdev docs ac-coverage` (§6) scans Go tests for `func Test\w*_CAP(\d{3})_AC(\d+)\b` and compares with each story's `### AC<n>` headings (stories with status `Implemented`/`In progress`; `Descoped`/`Not started` ignored; `--strict`/`--all` flags as capellactl). Applies to `docs/backlog/release-1.5.0` (and any future story dirs). For `release-1.0.0` there are no story files, so no AC check (status flips there need reviewer sign-off; `stories.yaml` is the record, §9.4).

### 4.4 Acceptance
1. Scenarios from Phase 2 §3.11 and Phase 3 §13.2 are migrated to `Scenario`; `go test -race ./internal/cli/...` green, offline, < 60 s.
2. `scenario-port.md` exists and accounts for every capellactl scenario file (script check in `cbdev docs ac-coverage --port-table`: every `tests/scenario/*.py` name in a capellactl checkout appears in the table; run manually when a capellactl checkout is present, skipped otherwise).
3. `AssertNoSecret`, `GoldenText`, `ScriptedPrompter` have unit tests (unexpected prompt fails, unused answers fail at cleanup).

---------------------------------------------------------------------------------------------------

## 5. Integration suite (`internal/integration`, `//go:build integration`)

### 5.1 Targets and environment
```
CBCTL_IT_TARGET   fake | sandbox | (unset)
  fake     in-memory capellafake + FakeClock: proves the suite's own logic on every push (CI job `integration-fake`)
  sandbox  real Capella: needs CAPELLA_SANDBOX_API_KEY and CAPELLA_SANDBOX_ORG_ID (nightly job)
  unset    sandbox if both sandbox variables are set; else every test calls t.Skip with the message
           "needs CAPELLA_SANDBOX_API_KEY and CAPELLA_SANDBOX_ORG_ID, or CBCTL_IT_TARGET=fake"
CBCTL_IT_CLUSTERS 1|0   cluster-creating scenarios on sandbox; default 1 when GITHUB_ACTIONS=true, else 0 (local safety/cost)
CBCTL_IT_LOG      path of the run log (default <t.TempDir()>/integration.log)
CBCTL_IT_REPORT   path of the JSON report (default none)
CAPELLA_SANDBOX_CLIENT_CIDR  runner's public CIDR allowed on the shared cluster (for the SDK connect test)
```
The plain `CAPELLA_API_KEY`/`CAPELLA_ORG_ID` from a developer's `.env` are **not** used by the suite unless `CBCTL_IT_TARGET=sandbox` is set explicitly **and** the sandbox variables are absent (then they are accepted, with `t.Log("using CAPELLA_API_KEY as sandbox credentials")`); `main_test.go` loads `.env` via `settings.LoadDotEnv` first so local `.env` works. Test names/projects use prefix `cbctl-ci-` (override `CBCTL_TEST_PREFIX`, must end with `-`); never touch projects without the prefix.

### 5.2 Harness (`harness.go`, `target.go`, `cleanup.go`, `report.go`)
```go
const Prefix = "cbctl-ci-"
type Target struct {
	Kind     string               // "fake" | "sandbox"
	APIKey, OrgID string
	Fake     *capellafake.Fake    // nil on sandbox
	Clock    engine.Clock         // FakeClock (fake) or RealClock
	LogPath  string
	Run      string               // run id: GITHUB_RUN_ID-GITHUB_RUN_ATTEMPT, else "local<unix>"
	Timeout  time.Duration        // 60m (fake: instantaneous)
	report   []ReportEntry
}
func TargetFromEnv(t *testing.T) *Target                         // skips or returns; one per test binary (TestMain)
func (t *Target) ProjectName(purpose string) string               // "cbctl-ci-<run>-<purpose>"
func (t *Target) CLI(tb testing.TB, args ...string) Result        // appends "--log-level debug" unless Debug(false); adds --yes unless NoApprove; writes "$ cbctl capella <args>\n<output>[exit N]\n\n" to LogPath (redacted)
type CLIOption func(*cliOpts)  // WithConfig(path), NoApprove(), Debug(false), WithEnv(k,v)
type Result struct{ Code int; Stdout, Stderr string }
func (r Result) Requests() []RequestLine                           // parse debug lines "METHOD /v4/... status Nms" (format defined by capellaapi debug log; add a golden test)
func (r Result) Writes() []RequestLine
func (t *Target) Client() *capellaapi.Client
func (t *Target) Cleanup(tb testing.TB, project string) *ReportEntry
//   registers tb.Cleanup that runs janitor.DeleteProjectsNamed(project) whatever happened (pass/fail/panic via defer in tests);
//   records {project, failure, teardown}; a failed teardown FAILS a test that had passed; after a failure the original error stays
//   the reported failure and the teardown result is still recorded.
func (t *Target) WriteReport(path string) error                    // {"target","run","projects":[{"project","failure","teardown"}]}
func (t *Target) Summary() []string
type Shared struct{ Cfg, Project, Cluster, Bucket string }
func SharedProject(t *testing.T, tg *Target) *Shared               // one 3-node AWS cluster + bucket, applied once per test binary, deleted in TestMain teardown
func SingleNodeConfig(project, cluster, cidr, extra string) string // cheapest layout (AC1)
func MultiNodeConfig(project, cluster, bucket, clientCIDR string) string
```
The debug request-line format is a **contract** between `capellaapi` (Phase 1 §2.2: `METHOD path -> status (Nms)`) and `Result.Requests()`; define the regexp `^\[debug\] - (GET|POST|PUT|PATCH|DELETE) (/v4/\S+) -> (\d{3}) \((\d+)ms\)$` here and add a test in `capellaapi` that its debug output matches it.
`TestMain`: parse target; on fake build `capellafake.New` with a shared FakeClock; run; on exit `WriteReport` if `CBCTL_IT_REPORT`; print `Summary()`; exit with the suite's code (fail if any teardown failed).

### 5.3 Scenarios (port of `capellactl/tests/integration/*`; one Go file each)
Every scenario: unique project per purpose via `Cleanup`, **zero-writes re-run** (second `apply` has `Writes()==[]`), **teardown always**. Fake and sandbox run the same code; sandbox-only waits use real time (`--timeout 60m`).
| Test | Story/AC (capellactl name) | Behaviour |
|---|---|---|
| `TestLifecycle_CAP032_AC1` | `test_integration_ac1_lifecycle` | write a single-node AWS config, `validate`, `plan`, `apply`, second `apply` → no writes, `drift` clean, `destroy`, project gone |
| `TestClusterLifecycle_CAP014_AC7` | `test_cluster_delete_ac7_integration` | protect → `delete` blocked (exit 4) → `protect --off` → `delete` waits → 404 |
| `TestDataModel_CAP023_AC8` / `…_CAP025_AC8` | `test_bucket_ac8…`, `test_scopes_ac8…` | bucket + scope + collection create/update/delete idempotency on the shared cluster |
| `TestCredentials_CAP027_AC8` | `test_credential_ac8_integration_connect` | create credential with generated password (saved 0600, never printed), connect with the **gocb SDK** (`cbctl capella cluster test --token … --database … -u … -p …` or direct `gocb` in the test) and KV put/get; needs `CAPELLA_SANDBOX_CLIENT_CIDR`; on fake the connect step is skipped with a logged reason |
| `TestPower_CAP019_AC2` | `test_integration_ac2_power` | `cluster power --off`, wait `turnedOff`, `plan` shows children blocked, `--on`, healthy |
| `TestSchedule_CAP020_AC2` | `test_integration_ac2_schedule` | hibernation schedule create/pause/resume/delete via file and via commands |
| `TestScaling_CAP015_AC2` | `test_integration_ac2_scaling` | 3→4 data nodes (scale out), back to 3 (scale in with checks) on the shared cluster |
| `TestClone_CAP049_AC9` | `test_cluster_clone_ac9_integration` | clone to a second region, assert plan clean, delete clone |
| `TestBackups_CAP056_CAP058` | (new; backups) | cluster snapshot schedule set/show/delete, on-demand snapshot create + wait + delete (no restore on sandbox: cost), bucket backup schedule set/delete; restore exercised on **fake** only |
| `TestRedaction_CAP032_AC4` | `test_integration_ac4_logs_have_no_secrets` | after the run, `secretscan` over the log, report and secret dir: exit 0 (see §5.5) |
Scenarios needing clusters are `t.Skip("CBCTL_IT_CLUSTERS=0")` on sandbox when disabled; on fake always run.

### 5.4 Janitor (`internal/ci/janitor`)
```go
const Prefix = "cbctl-ci-"
const RetryEvery = 60 * time.Second
func Stale(projects []capellaapi.Project, prefix string, olderThan time.Duration, now time.Time) []capellaapi.Project
//   name has prefix AND audit.createdAt parses (RFC3339, any fractional digits) AND now-created > olderThan; a project without a readable createdAt is KEPT (may belong to a run in progress) and a warning is logged
func DeleteProjectTree(ctx context.Context, c *capellaapi.Client, p capellaapi.Project, timeout time.Duration, clk engine.Clock) ([]string, error)
//   list clusters; for each: turn deletion protection off if on (step "deletion protection off for X"), delete (free-tier via DeleteFreeCluster; others DeleteCluster(retainSnapshotBackups=false)) retrying every RetryEvery on API errors (Capella refuses deletion of a deploying/scaling cluster) until timeout, treating ErrNotFound as done; then WaitForCluster(OpDelete) for each; then DeleteProject with the same retry. Returns the ordered step descriptions.
func DeleteProjectsNamed(ctx, c, name string, timeout, clk) ([]string, error)   // every project with exactly that name
```
CLI: hidden command `cbctl capella janitor [--prefix P] [--older-than-hours 24] [--dry-run] [--fail-on-leftovers] [--timeout-minutes 60] [--yes]` (Phase 2 §3.9.4 introduced the name): refuses a prefix that does not end in `-` or is shorter than 4 chars (exit 3, protects real projects); without `--yes` and without `--dry-run` on a TTY it lists and asks; non-TTY without `--yes` → exit 4. Output as capellactl (`leftover project X (created T)` + step lines, final `N leftover project(s) deleted, M failed`); exit 1 when any failed or `--fail-on-leftovers` and any found. Credentials: `CAPELLA_SANDBOX_API_KEY`/`CAPELLA_SANDBOX_ORG_ID` preferred, else `CAPELLA_API_KEY`/`CAPELLA_ORG_ID` (via settings precedence). Makefile `janitor` target runs it with `--older-than-hours 24`.

### 5.5 Log secret scanner (`internal/ci/secretscan`)
```go
const MinLength = 8
func Variants(v string) []string                  // raw, JSON-escaped (no quotes), url.QueryEscape, url.PathEscape
func SecretsInDir(dir string) map[string]string   // "password in <file>" → value, from every *.json with a string "password" field
func Scan(texts map[string]string, secrets map[string]string) []Hit   // Hit{File string; Line int; Source string}; never includes the value
```
`cbdev ci scan-secrets FILE… [--env NAME]… [--secrets-dir DIR]… [--value-file FILE]…`: exit 0 clean, 1 found (prints `file:line: contains <source>`), 2 nothing to check (missing env var, no secrets, missing file) — **a missing variable must not turn the check into a silent pass**. Values shorter than `MinLength` are skipped with a stderr note. Exact behaviours per `scripts/scan-log-secrets.py`.

### 5.6 Release gate (`internal/ci/releasegate`)
> **Superseded (2026-11-17):** the nightly is a health signal, not a gate. `release.yml` runs this command with `--report-only` in an informational `nightly-health` job and goreleaser does not need it; nothing blocks a merge, push or tag on the nightly.

```go
type Run struct{ Status, Conclusion, Event, HeadSHA, HTMLURL string; CreatedAt, RunStartedAt time.Time }
func Evaluate(runs []Run, commit string, nights int) (ok bool, lines []string)
//   completed scheduled runs only; newest first; the last `nights` (default 3) must all be "success", on consecutive UTC dates, and the newest's HeadSHA must equal commit; same explanation strings as release-gate.py
func FetchRuns(ctx context.Context, repo, workflow, branch string) ([]Run, error)   // exec `gh api repos/<repo>/actions/workflows/<wf>/runs?branch=<b>&event=schedule&per_page=30`
```
`cbdev ci release-gate --commit SHA [--repo OWNER/NAME] [--branch main] [--workflow nightly.yml] [--nights 3] [--runs-json FILE] [--report-only]` — prints `release gate: last N nightly runs of …`, one line per run, then `PASSED`/`FAILED`; `--report-only` always exits 0.

### 5.7 Acceptance
1. `CBCTL_IT_TARGET=fake go test -tags=integration -race ./internal/integration/...` passes in CI on every push; takes < 120 s.
2. Teardown tests: a scenario that fails mid-way still deletes its project (fake: assert project absent); a forced teardown failure (fault injection `FailNext DELETE …/projects/*`) fails an otherwise green test and is recorded in the report.
3. Janitor unit tests (fake + FakeClock): stale filter (prefix, age, unreadable createdAt kept), protection turned off first, retry on 409 then success, not-found tolerated, `--dry-run` deletes nothing, prefix safety refusals, exit codes.
4. Secret scanner unit tests ported from `tests/unit/scripts/test_scan_log_secrets.py`; release gate tests ported from `test_release_gate.py` (including `--runs-json` fixtures under `internal/ci/releasegate/testdata`).
5. Sandbox (manual, once, with real credentials): `CBCTL_IT_TARGET=sandbox CBCTL_IT_CLUSTERS=1 go test -tags=integration -timeout 300m ./internal/integration/...` passes and `cbctl capella janitor --older-than-hours 0 --dry-run` afterwards reports no leftovers.

---------------------------------------------------------------------------------------------------

## 6. `cbdev` (developer tool, `cmd/cbdev`, libraries in `internal/docgen`, `internal/ci`)

```
cbdev docs port        --src DIR --dst DIR [--release R]… [--sha SHA] [--force]          (§9.5)
cbdev docs extract-data --capellactl DIR --out internal/docgen/data                      (§9.7, one-shot; needs python3)
cbdev docs index       [--check] [--release R]…                                           (§9.6)
cbdev docs coverage    [--check] [--refresh-spec URL|FILE]                                (§9.7)
cbdev docs ac-coverage [--docs DIR]… [--tests DIR] [--strict] [--all] [--port-table CAPELLACTL_DIR]
cbdev docs reference   [--check]                                                          (§8.4)
cbdev docs links       [--check] [ROOT…]                                                  (§8.6)
cbdev api responses    [--spec FILE|URL] [--out PATH]                                      (§3.7)
cbdev ci release-gate  …                                                                   (§5.6)
cbdev ci scan-secrets  …                                                                   (§5.5)
```
Conventions: every `--check` mode regenerates in memory and exits **1** with a unified diff header (`stale: <path>; run 'make docs'`) when the committed file differs; exit 0 otherwise; exit 2 on usage/IO errors. All generators are deterministic (sorted maps, `\n` newlines, UTF-8, no timestamps except the explicit `retrieved` field copied from the API snapshot). Output paths are relative to the repo root (found by walking up to `go.mod`).

---------------------------------------------------------------------------------------------------

## 7. CI/CD

### 7.1 `Makefile` (replace/extend)
```make
.PHONY: build test test-unit test-race test-integration test-integration-fake janitor vet fmt-check tidy-check
.PHONY: cover-engine docs docs-check index index-check coverage coverage-check ac-coverage links-check check release-snapshot

test-unit:           go test -race ./internal/...
test-integration-fake:  CBCTL_IT_TARGET=fake go test -tags=integration -race -timeout 20m ./internal/integration/...
test-integration:    # sandbox: needs CAPELLA_SANDBOX_API_KEY / CAPELLA_SANDBOX_ORG_ID
	CBCTL_IT_TARGET=sandbox go test -tags=integration -timeout 300m ./internal/integration/... -v
janitor:             go run ./cmd/cbctl capella janitor --older-than-hours 24
fmt-check:           test -z "$$(gofmt -l .)"        # fails listing files
tidy-check:          go mod tidy && git diff --exit-code go.mod go.sum
cover-engine:        go run ./cmd/cbdev ci cover-gate --min 85 ./internal/plan ./internal/resources ./internal/config/capellafile
docs:                go run ./cmd/cbdev docs index && go run ./cmd/cbdev docs coverage && go run ./cmd/cbdev docs reference
docs-check:          go run ./cmd/cbdev docs index --check && … coverage --check && … reference --check && … links --check
ac-coverage:         go run ./cmd/cbdev docs ac-coverage --docs docs/backlog/release-1.5.0
check:               vet fmt-check test-unit test-integration-fake cover-engine docs-check ac-coverage tidy-check
```
`cbdev ci cover-gate --min N PKG…`: runs `go test -cover -coverprofile` per package pattern (via `os/exec`), parses `coverage: X% of statements`, fails (exit 1) if any package is below N; prints a table. (Replaces the Phase 3 `cover-engine` stub.) The existing `make test` keeps aliasing `test-unit`.

### 7.2 `.github/workflows/ci.yml`
Keep the existing trigger/permissions/concurrency (`group: ci-${{ github.ref }}`, cancel in progress). Jobs:
- `test` (matrix `ubuntu-latest`, `macos-latest`, `windows-latest`; `fail-fast: false`; `timeout-minutes: 25`): checkout, `setup-go` (`go-version-file: go.mod`, cache), `go vet ./...`, `gofmt` check (ubuntu only), `go test ./internal/...` with `-race` on ubuntu and macOS only (Windows runs without `-race`), `make cover-engine` (ubuntu only).
- `integration-fake` (ubuntu): `make test-integration-fake`.
- `docs` (ubuntu): `make docs-check ac-coverage` and `go test ./internal/docs/...`.
- `tidy` (ubuntu): `make tidy-check`.
- `release-smoke` (ubuntu): `goreleaser check` and `goreleaser release --snapshot --clean --skip=publish` (action `goreleaser/goreleaser-action@v6`, `version: "~> v2"`), asserting `dist/` has no `cbdev` binary.
Pin third-party actions to the major versions already used in the repo (`actions/checkout@v4`, `actions/setup-go@v5`). Live tests never run here.

### 7.3 `.github/workflows/nightly.yml` (new; port of capellactl's)
Triggers: `schedule` cron `"0 7 * * *"` (integration, 07:00 UTC) and `"0 9 * * 0"` (janitor, Sundays 09:00 UTC); `workflow_dispatch` with input `job: choice [integration, janitor]` (a manual run does **not** count for the release gate). `permissions: contents: read`. `concurrency: {group: nightly-sandbox, cancel-in-progress: false}`.
- `integration`: `if: github.event.schedule == '0 7 * * *' || inputs.job == 'integration'`, `timeout-minutes: 300`, env `CAPELLA_SANDBOX_API_KEY/ORG_ID` from secrets, `CBCTL_IT_TARGET: sandbox`, `CBCTL_IT_CLUSTERS: "1"`, `CBCTL_IT_LOG: integration.log`, `CBCTL_IT_REPORT: integration-report.json`. Steps: checkout, setup-go, `echo "CAPELLA_SANDBOX_CLIENT_CIDR=$(curl -fsS https://api.ipify.org)/32" >> "$GITHUB_ENV"`, `go test -tags=integration -timeout 290m -v ./internal/integration/...` (teardown runs whatever happens), then (`if: always()`) `go run ./cmd/cbdev ci scan-secrets integration.log integration-report.json --env CAPELLA_SANDBOX_API_KEY --secrets-dir <tmpdir exported by the suite as CBCTL_IT_SECRETS_DIR>`, then upload artifacts `integration.log`, `integration-report.json` (`if: always()`, `if-no-files-found: warn`). The suite exports its secrets directory path by writing it into `$GITHUB_ENV`-compatible `integration.env` file or by `CBCTL_IT_SECRETS_DIR` fixed to `${{ runner.temp }}/cbctl-it-secrets` (choose the fixed path; the suite honours `CBCTL_IT_SECRETS_DIR` for `credentialsOutput`).
- `janitor`: `if: github.event.schedule == '0 9 * * 0' || inputs.job == 'janitor'`, `timeout-minutes: 120`: `go run ./cmd/cbctl capella janitor --older-than-hours 24 --fail-on-leftovers --yes`.

### 7.4 `.github/workflows/release.yml`
> **Superseded (2026-11-17):** the job is `nightly-health`, report-only and `continue-on-error`; goreleaser has no `needs` on it. The nightly is a health signal, not a release gate.

Add a first job `gate` (only on `refs/tags/v*`; the workflow already triggers only on `v*`): `go run ./cmd/cbdev ci release-gate --commit "$GITHUB_SHA" --repo "$GITHUB_REPOSITORY"` with `GH_TOKEN: ${{ github.token }}` and `permissions: {contents: read, actions: read}`; the existing `goreleaser` job gets `needs: gate`. Add `workflow_dispatch` (dry run) running the gate with `--report-only`. The floating `latest` handling elsewhere in the file is **not** gated. Document in `docs/capella/release.md`: tag the commit the last green nightly ran on.

### 7.5 Acceptance
1. `make check` green locally with no network and no credentials.
2. A workflow-lint unit test (`internal/docs/workflows_test.go`) parses the three workflow YAML files and asserts: `release.yml` goreleaser job does not need the nightly report (originally `needs: gate`; superseded); `nightly.yml` has both crons and `concurrency.cancel-in-progress == false`; `ci.yml` runs `make test-integration-fake` and `make docs-check`; no workflow references `secrets.CAPELLA_SANDBOX_*` except `nightly.yml`.
3. `goreleaser release --snapshot --clean --skip=publish` locally yields only the `cbctl` archives.

---------------------------------------------------------------------------------------------------

## 8. Documentation deliverables (non-backlog)

### 8.1 Files
- `README.md`: new **Capella** section set (replace the old short Capella section): authentication (flag > env > `.env` > profile; `cbctl configure`), command reference tables (Phase 2 §3.6, Phase 3 §9) with one example each, **Declarative workflow** (`sync → validate → plan → apply`, `drift`, `destroy`, file-managed mode, `--no-apply`/`--direct`), JSON output & exit codes, secrets handling, testing (`make check`, integration suite, janitor, nightly), and a "Roadmap" link to `docs/backlog/README.md`. Remove/replace statements that contradict reality (token only via flag).
- `CHANGELOG.md` (Keep a Changelog): `## [Unreleased]` listing Added (all Phase 0–4 features by area), Changed (error printing/exit codes, `capella` flags), Deprecated (`CBCTL_LIVE_CLUSTER`), Security (secret redaction, 0600 files). The release workflow's GoReleaser changelog config is untouched.
- `NOTICE`: states that Capella command semantics, presets and size tables, endpoint inventory, JSON schema, guide topics, engine design and documentation are derived from **capellactl** (Apache-2.0, © Couchbase, Inc.), with the upstream URL `https://github.com/couchbaselabs/capellactl` and the commit from `docs/backlog/SOURCE.md`; add to GoReleaser `archives[].files` so it ships. `LICENSE` unchanged. A test asserts `NOTICE` mentions `capellactl` and Apache-2.0.
- `.cursor/rules/cbctl.mdc` (+ `general.mdc` reminder): command map updated for Phases 0–3 and the doc-update rule (`.cursor/` is gitignored in this repo: update the local files and mention in the PR description that they are local-only; do **not** change `.gitignore`).
- `docs/capella/` guides (ported with the §9.5 engine in `--profile guide`, then hand-edited): `getting-started.md`, `custom-clusters.md`, `backup-restore.md`, `runbook.md`, `terraform.md` (comparison with the Capella Terraform provider), `release.md` (new: release gate + tagging), `testing.md` (new: fake, scenarios, integration suite, janitor, secret scanner), `reference.md` (**generated**, §8.4), `examples/*.yaml` (ported from capellactl `docs/*/examples` if present, plus one per feature: single-node, multi-node MDS, free tier, hibernation, backups, credentials, multi-project). `migrating-from-scale-script.md` is **not** ported (Nova-specific).
- Manual edits required after the automatic rewrite of the guides: install sections (replace `pipx`/wheel/PyInstaller instructions with: GitHub release binary, `go install github.com/mminichino/cbctl/cmd/cbctl@latest`, `make build`), Windows PowerShell sections (drop), any Python/pytest wording, links to stories (point to `docs/backlog/…`).

### 8.2 Doc-command linter (`internal/docs/doccmds_test.go`)
Parses every fenced block with info string `bash`, `sh`, `console`, `shell` in `README.md` and `docs/capella/*.md` (**not** `docs/backlog/**`, `docs/specs/**`): for each logical command line (join `\`-continuations, strip a leading `$ `) whose first token is `cbctl`: split with a shell-words splitter, `root := cli.NewRootCmd(cli.DefaultDeps())`, `cmd, rest, err := root.Find(args)`; error → test failure `file:line: unknown command`; then `cmd.ParseFlags(rest)` must succeed (unknown flag → failure); required positional counts are **not** checked (placeholders are fine). Lines inside a block preceded by the HTML comment `<!-- doccheck:skip -->` are skipped; blocks tagged ```` ```bash nolint ```` are skipped. Pipelines/`&&`: each `cbctl` segment is checked.
`examples_test.go`: every ```` ```yaml ```` block starting with `apiVersion:` in the guides, and every `docs/capella/examples/*.yaml`, must pass `capellafile.LoadText` with env stubs (`CAPELLA_ORG_ID`, any `${VAR}` → `x`+ValidPassword value) and have **no** crossref issues.

### 8.3 Guide walkthrough test
`internal/cli/scenario_guides_test.go`: executes `docs/capella/getting-started.md`'s command sequence against `Scenario` (extract blocks tagged ```` ```bash run ````; commands with `NAME`-style placeholders are substituted from a small map in the test; non-`cbctl` lines skipped) and asserts exit 0 for each — the port of capellactl's CAP-033 AC5 "guides run against the fake".

### 8.4 Generated reference (`cbdev docs reference`)
Port `scripts/update-reference.py` + `capellactl/cli/help.py` `schema_fields`/`MUTABILITY`: generates `docs/capella/reference.md` from the embedded `schema.json` (`x-mutability`, `x-api`, `description`, `type`, `enum`, `default` keys; columns Field | Type | Values/default | Change (`updatable`, `**immutable**`, `key`, `re-created`, `rotate only`, `cbctl only`) | Description). Long enums (> 12 values, e.g. timezones) show three examples + "see `cbctl capella guide config FIELD`". The Phase 3 `guide config FIELD` command and this generator share `internal/config/capellafile.SchemaFields()` (add it). `--check` per §6.

### 8.5 Acceptance (docs)
1. `go test ./internal/docs/...` passes: doc-command linter, examples, links (§8.6), workflow lint, NOTICE.
2. `cbdev docs reference --check` clean; reference lists every schema property (test: count equals schema leaf count).
3. README contains no occurrence of `capellactl` outside the attribution/Roadmap/NOTICE paragraph (test).

### 8.6 Link checker (`cbdev docs links`)
Scans `*.md` under `docs/` and root; for relative links `](path#anchor)` verifies the file exists and (for `.md` targets) the anchor matches a GitHub-style heading slug (lowercase, spaces→`-`, strip punctuation except `-`); external `http(s)` links are **not** fetched. Ignores fenced code blocks. Used by `docs-check` and `internal/docs/links_test.go`.

---------------------------------------------------------------------------------------------------

## 9. Port of the capellactl backlog documentation

### 9.1 What is "the backlog"
| Source (capellactl) | Size | Content | Target (cbctl) |
|---|---|---|---|
| `docs/release-1.5.0/CAP-064…CAP-089-*.md` | 26 stories, 113 pts | operational-database features 1.0.0 left out (db roles, sample buckets, flush, query indexes, Eventing, Data API, XDCR, clone-from-backup, bucket storage migration, CA certificate, network peering, private endpoints, audit logging, CMEK, org settings/users, API keys, alert integrations, events, billing, Operational Insights ×5, release gate) | `docs/backlog/release-1.5.0/CAP-*.md` |
| `docs/release-1.5.0/README.md` | generated | story index, phases, checklist | generated by `cbdev docs index` |
| `docs/release-1.5.0/API-COVERAGE.md` | generated | 139 planned operations | generated by `cbdev docs coverage` |
| `docs/release-2.0.0/API-COVERAGE-APP-SERVICES.md` | generated | 73 App Services/App Endpoints operations | generated |
| `docs/release-1.0.0/API-COVERAGE.md` | generated | 80 implemented operations | generated (tracks cbctl Phases 0–3) |
| `docs/release-1.0.0/00-architecture.md` | hand-written | shared reference for all stories | ported + adapted (§9.8) |
| `docs/release-1.0.0/CAP-001…063` | 63 stories | **implemented in capellactl = the acceptance criteria of cbctl Phases 0–3** | **not ported**; replaced by an index + status map (§9.4) |
| `docs/api/management-api-v4.0-{operations,responses}.json` | snapshots | spec operation list (343 ops) and 2xx response schemas | copied to `docs/backlog/api/` |
| 4 generators (`update-story-index.py`, `update-api-coverage.py` 1200 lines, `update-api-responses.py`, `check-ac-coverage.py`) | scripts | | Go ports in `internal/docgen` (§9.6, §9.7) |
Non-goals: porting the 63 implemented story files (an optional later `--release 1.0.0 --full` mode of `cbdev docs port` using the same engine is allowed but not required), porting `docs/release-1.0.0/API-COVERAGE.md` by hand (generated), AI-operations docs (the 50 AI operations are "not planned" in both repos).

### 9.2 Target layout
```
docs/backlog/
  README.md                       hand-written: what the backlog is, the CAP-ID scheme, status values, how to start a story
  SOURCE.md                       hand-written+tool-filled: capellactl repo URL, commit SHA, port date, rewrite-rule version
  CAP-ID-MAP.md                   generated from stories.yaml: 1.0.0 IDs → title/epic/phase/spec/status (resolves "Depends on CAP-0xx")
  release-1.0.0/
    00-architecture.md            ported + adapted (§9.8)
    stories.yaml                  status map (§9.4) — the source for the 1.0.0 index and coverage
    README.md                     generated index (stub rows, no story files)
    API-COVERAGE.md               generated
  release-1.5.0/
    README.md  API-COVERAGE.md    generated
    CAP-064-custom-database-roles.md … CAP-089-release-1-5-docs-and-gate.md   (26 ported files, same file names)
  release-2.0.0/
    API-COVERAGE-APP-SERVICES.md  generated
  api/
    management-api-v4.0-operations.json    byte copy
    management-api-v4.0-responses.json     byte copy; regenerated by `cbdev api responses`
```
Every ported story ends with the provenance line (HTML comment, so the title stays on line 1 for the parsers):
`<!-- ported-from: capellactl@<sha7> docs/release-1.5.0/<file> · rules v1 -->`
`SOURCE.md` records the full SHA, `git -C <capellactl> describe --always --dirty` (must be clean: the tool refuses a dirty source tree unless `--force`), and the rules version (`const RulesVersion = 1` in `internal/docgen/port`).

### 9.3 Identifiers
Story IDs are **kept** (`CAP-064` … `CAP-089`; 1.0.0 `CAP-001` … `CAP-063`): they are cross-referenced by dependencies, AC test names and coverage commands, and cbctl continues the same numbering for new stories (`CAP-090+`). Epics (E1…E17), priorities (P0–P2), points and phases are kept verbatim. Status of every ported 1.5.0/2.0.0 story is `Not started` (as in source).

### 9.4 Release 1.0.0 status map (`docs/backlog/release-1.0.0/stories.yaml`)
Because the 63 implemented stories are not ported, their state in **cbctl** is tracked in one file; the generators read it instead of story files for release 1.0.0. Schema:
```yaml
release: 1.0.0
architecture: 00-architecture.md
stories:
  - id: CAP-002
    title: "…"        # filled by `cbdev docs extract-data` from the capellactl story header (line 1 after "# CAP-NNN — ")
    epic: "E1 Foundation"; priority: P0; points: 5; depends: "CAP-001"     # same, from the header table
    status: Not started          # Not started | In progress | Implemented | Descoped
    phase: "Phase 3"             # cbctl phase that delivers it (0–4) — hand-maintained (table below)
    ref: "phase-3 §3–§4"         # spec section (hand-maintained)
    note: ""                     # e.g. descoped reason
```
Initial hand-maintained columns (apply to the extracted rows; **status** `Not started` unless shown; update `status` to `Implemented` in the PR of the phase that merges it, with reviewer sign-off — no automated AC check exists for these):
```
CAP-001 Implemented  phase 0  ref existing scaffold+CI (cbctl already has cobra CLI, Makefile, CI, GoReleaser)
CAP-002 phase 3 §3–§4 · CAP-003 phase 1 §2 · CAP-004 phase 4 §3 · CAP-005 phase 3 §6 · CAP-006 phase 3 §6.4,§9.3 · CAP-007 phase 1 §2.10
CAP-008 phase 0 §1.6 + phase 3 §8 · CAP-009 phase 3 §7.1 · CAP-010 phase 2 project delete + phase 3 §9.4 · CAP-011 phase 3 §7.2 + phase 2 cluster create
CAP-012 phase 3 §7.2 · CAP-013 phase 3 §7.3 · CAP-014 phase 2 cluster delete · CAP-015/016/017/018 phase 2 cluster scale/update + phase 3 §7.2.1
CAP-019 phase 2 cluster power · CAP-020/021 phase 3 §7.4 + phase 2 cluster schedule · CAP-022 phase 3 §7.6 + phase 2 cidr
CAP-023/024/025/026 phase 3 §7.7–§7.8 + phase 2 bucket/scope/collection · CAP-027/028/029 phase 3 §7.9 · CAP-046 phase 2 credential
CAP-030/031 phase 3 §9.3–§9.5 · CAP-032 phase 4 §5 · CAP-033 phase 4 §8 · CAP-034 phase 3 §9.6 (guide) · CAP-035 phase 3 §11.3
CAP-036 phase 3 §11.1 · CAP-037 phase 0 §1.7–§1.8 · CAP-038 phase 3 §10 · CAP-039 phase 2 project create · CAP-040 phase 3 (multi-project)
CAP-041 phase 2 cluster options/free tier · CAP-042 phase 2 §3.2 (-y, --quiet, --no-wait) · CAP-044 phase 4 §8.1 (NOTICE)
CAP-047 phase 0 configure + phase 3 §11.2 · CAP-048 phase 3 §7.1–§7.2 (ids) · CAP-049 phase 2 cluster clone + phase 3 §10
CAP-051/052/053/060 phase 2 cluster create/group/disk-types · CAP-054 phase 3 §11.2 · CAP-056/057/058/059 phase 2 backups (+ phase 3 §7.5)
CAP-061 phase 4 §8 (backup & disk guides) · CAP-062 phase 3 §4.1 · CAP-063 phase 0–2 §2.8 + phase 3 §12
Descoped: CAP-043 (distributables: GoReleaser replaces wheel/sdist), CAP-045 (standalone executables: GoReleaser builds native binaries),
          CAP-050 (repository rulesets: out of scope here), CAP-055 (Windows PowerShell install steps: Python-specific)
```
Descoped stories keep their ID and appear only in a "Descoped" section (per the index generator). `CAP-ID-MAP.md` is generated (§9.6) as a table `ID | Title | Epic | Priority | Points | Status | Delivered by`.

### 9.5 `cbdev docs port` — the rewrite engine (`internal/docgen/port`)
Deterministic, idempotent (running twice on the same input yields identical bytes; running on already-ported output is a no-op: the provenance comment makes the tool skip files that already carry it unless `--force`). Input: `--src <capellactl>/docs`, output `--dst docs/backlog`, `--release 1.5.0` (repeatable; `1.0.0-architecture` selects only `00-architecture.md`), `--sha` (default: read from `git -C src/.. rev-parse HEAD`). Profiles: `story` (default for CAP-*.md), `architecture`, `guide` (for §8.1 guides). The engine processes a file **line by line, tracking fenced-block state**, applying the ordered rules below; every rule has golden tests; every substitution that the engine could not decide is **flagged**, not guessed: it appends `<!-- port-review: <rule-id> line N: <original text> -->` at the end of the file and the tool prints `REVIEW <file>:<line> <rule-id>`; the PR must resolve (edit and delete) every flag, and a test (`TestNoPortReviewMarkers`) fails while any `port-review` comment remains in `docs/backlog/` or `docs/capella/`.

**Rules (ordered; `R#` ids are stable and used in flags):**

| Id | Applies to | Rewrite |
|---|---|---|
| R1 | code blocks (any fence) and inline code | Command invocations: a line (after optional `$ `) or inline-code span starting with `capellactl` followed by a space → `cbctl capella ` + remainder. Global flags between (`capellactl -o json plan`, `capellactl --auto-approve apply`) are preserved after the new prefix: `cbctl capella -o json plan`. Inline code that is exactly `capellactl` → `cbctl`. |
| R2 | prose | Bare word `capellactl` (not part of a longer identifier, not followed by `/`, `-ci`, `_`) → `cbctl`. Occurrences immediately followed by a lowercase command word inside prose (e.g. "run capellactl sync") → flagged `R2` for review **unless** the word is in the known-command set (extracted from `internal/docgen/data/commands.yaml` first tokens ∪ `{sync, validate, plan, apply, destroy, drift, init, configure, guide}`), in which case → `cbctl capella <word>`. |
| R3 | all | `capellactl help` → `cbctl capella guide`; `help topics` → `guide topics`; `help config` → `guide config`; the phrase "`help` topics" → "`guide` topics". |
| R4 | all | `capellactl import` / `` `import` `` (the old alias of `sync`) → `cbctl capella sync` and flagged `R4` (cbctl's own `capella import` is the JSONL importer). `completion` mentions → "cobra's built-in `cbctl completion`" and flagged `R4`. |
| R5 | all | Environment variables: `CAPELLACTL_IT_TARGET/LOG/REPORT` → `CBCTL_IT_*`; `CAPELLACTL_PROFILE/CONFIG/HOME/QUIET` → `CBCTL_*`; `CAPELLA_SANDBOX_*`, `CAPELLA_API_KEY`, `CAPELLA_ORG_ID`, `CAPELLA_API_BASE_URL` unchanged; `BEARER_TOKEN`/`ORG_ID`/`API_BASE_URL` → flagged `R5` (not supported in cbctl). |
| R6 | all | Names/paths: `capellactl-ci-` → `cbctl-ci-`; `.capellactl/` → `.cbctl/`; `~/.config/capellactl` → `~/.config/cbctl`; `apiVersion: capellactl/v1` → `apiVersion: cbctl/v1` (cbctl also reads the old value); Python module/CLI names `capellactl.` → flagged `R6`. |
| R7 | story profile | **Code locations** (`- Code:` lines and anywhere a path appears): `cli/<x>_cmds.py` → `internal/cli/capella_<x>.go`; `cli/<x>.py` → `internal/cli/capella_<x>.go`; `resources/<x>.py` → `internal/resources/<x>.go`; `api/endpoints.py` → `internal/capellaapi/endpoints_*.go`; `api/client.py` → `internal/capellaapi/client.go`; `config/schema.json` → `internal/config/capellafile/schema.json`; `config/crossref.py` → `internal/config/capellafile/crossref.go`; `config/loader.py` → `internal/config/capellafile/load.go`; `importer/` → `internal/importer/`; `engine/<x>.py` → `internal/plan/<x>.go` (planner/executor/normalize) or `internal/engine/<x>.go` (waiter/clock); `output/<x>.py` → `internal/secrets/` or `internal/plan/render.go`; `help/*.md` → `internal/guide/topics/*.md`; `scripts/<x>.py` → `cmd/cbdev` + `internal/docgen`/`internal/ci` (named in the rule table `data/path-rules.yaml`); "the fake"/"fake Capella" → "`internal/capellafake`"; unmapped `*.py` path → flagged `R7`. A `<x>` containing `_` maps to `_`→`_` for Go file names (Go allows underscores; keep). |
| R8 | story profile | **Test locations**: `tests/scenario/test_<x>.py` → `internal/cli/scenario_<x>_test.go`; `tests/unit/config/test_<x>.py` → `internal/config/capellafile/<x>_test.go`; `tests/unit/docs/test_<x>.py` → `internal/docs/<x>_test.go`; `tests/unit/scripts/test_<x>.py` → `internal/docgen/<x>/…_test.go` or `internal/ci/<x>/…_test.go` (data/path-rules.yaml); `tests/unit/fakes/test_<x>.py` → `internal/capellafake/<x>_test.go`; `tests/contract/test_<x>.py` → `internal/capellaapi/<x>_test.go`; `tests/integration/test_<x>.py` → `internal/integration/<x>_test.go`; others flagged `R8`. |
| R9 | story profile, test tables | **Test names**: Python `test_<subject>_ac<n>_<behaviour>` in a story `CAP-<nnn>` → `Test<Subject>_CAP<nnn>_AC<n>_<Behaviour>` where `Subject`/`Behaviour` = PascalCase of the `_`-separated words (`bucket_flush`→`BucketFlush`, `confirm_and_flush`→`ConfirmAndFlush`; digits kept). Names without an `_ac<n>_` infix (e.g. `test_fake_parity`) → `Test<Pascal>` and flagged `R9`. The story id comes from the file's title line. |
| R10 | story profile | Python/tooling vocabulary: `pytest.skip("…")` → `t.Skip("…")`; `@pytest.mark.story` / `pytest` / `click` / `ruamel` / `requests` / `responses` / `freezegun` / `mypy` / `ruff` / `pip` / `pipx` → flagged `R10` (a human rewrites the sentence; the engine never guesses). `python3 scripts/…` → flagged `R10`. |
| R11 | all | Links: `../release-1.0.0/00-architecture.md` stays (target exists, §9.8); `../release-1.0.0/API-COVERAGE.md` stays (generated); `../release-1.0.0/CAP-<nnn>-….md` (story files not ported) → `../release-1.0.0/README.md#cap-<nnn>`-style anchor to the index row and the link text unchanged; `../release-2.0.0/…` stays; other relative links must resolve after the port (checked by `cbdev docs links`); `docs/release-1.5.0/…` → `docs/backlog/release-1.5.0/…`; `docs/<guide>.md` mentions of guides that become `docs/capella/<guide>.md` (getting-started, custom-clusters, backup-restore, runbook, terraform, reference) are rewritten; `docs/networking.md`, `docs/security.md`, `docs/operational-insights.md` (planned by CAP-089) → `docs/capella/…` equivalents (future files). |
| R12 | story profile | `--auto-approve` stays (cbctl keeps it as an alias of `-y/--yes`). `capella.yaml` stays. Exit codes, symbols, plan text unchanged. `apiVersion` rewritten by R6. |
| R13 | story profile | Footer: append `\n<!-- ported-from: capellactl@<sha7> docs/release-<r>/<file> · rules v1 -->\n` (once). |
| R14 | architecture profile | Whole-section replacements listed in §9.8 (applied by heading match before other rules). |
| R15 | guide profile | R1–R6, R10–R12 apply; R7–R9 do not; no footer, but a leading HTML comment `<!-- ported from capellactl docs/<file>; edited for cbctl -->`. |

Rule engine requirements: never rewrite inside URLs (`http(s)://…`) or inside markdown link targets except per R11; do not touch YAML/JSON bodies in fenced blocks except R5/R6 tokens; preserve every other byte (line endings, tables, trailing spaces); fenced-block detection must handle ``` and ~~~ and indented blocks inside lists; unit tests for each rule with positive, negative and boundary cases (e.g. `capellactl-ci-` vs `capellactl`, `capellactl/v1`, the word inside a URL, `capellactl` at line start in a table cell).

### 9.6 Story index generator (`internal/docgen/stories`) — port of `update-story-index.py`
```go
type Story struct{ ID, Title, File, Epic, Priority string; Points int; Depends, Status string }
func ParseStory(path string) (Story, error)       // line 1 `# CAP-NNN — Title`; header table row within lines 2–8 (| Epic | Priority | Points | Depends on | Status |); status ∈ Not started|In progress|Implemented|Descoped else error
func LoadRelease(dir string) ([]Story, error)      // story files, or stories.yaml when dir has no CAP-*.md (release-1.0.0)
func RenderIndex(release string, stories []Story, cfg ReleaseConfig) string
type ReleaseConfig struct{ Architecture, Scope string; Phases []Phase; Checklist []string }
type Phase struct{ Name, Outcome string; IDs []string }
```
Port `RELEASES` (phases, outcomes, checklists) for **1.0.0** and **1.5.0** into `internal/docgen/data/phases.yaml` (extracted, §9.7) and rewrite the checklist text with the §9.5 rules (e.g. "CAP-032 nightly integration suite green three nights…", "`cbdev docs ac-coverage --docs docs/backlog/release-1.5.0`"). Output format = `update-story-index.py` byte-for-byte except: the header banner says ``Generated by `cbdev docs index` … re-run `make docs` ``; links to story files are relative; for release 1.0.0 the story column links to `../CAP-ID-MAP.md#cap-nnn` instead of a file. Same semantics: validation (IDs contiguous per release; every story in exactly one phase; unknown status error), Snapshot/priority/epic tables, strikethrough for Implemented in priority tables and phase overview, Descoped section, phase badges (`✅ Done`/`🚧 Current`/`⬜ Planned` — first not-fully-done phase is Current), release checklist as `- [ ]`. `--check`: byte comparison with the committed file. Also generates `docs/backlog/CAP-ID-MAP.md`.

### 9.7 API coverage generators (`internal/docgen/coverage`, `internal/docgen/data`)
**Data extraction (one-shot).** `cbdev docs extract-data --capellactl DIR --out internal/docgen/data` runs a throwaway Python snippet (written to a temp file by the tool; requires `python3`) that imports `DIR/scripts/update-api-coverage.py` with `importlib` (adding `DIR` to `sys.path`) and `DIR/scripts/update-story-index.py`, and dumps as YAML (via `json` then converted by Go): `commands.yaml` (`COMMANDS`: name → {stories, left}), `workflow.yaml` (`WORKFLOW`), `ops.yaml` (`OPS`: key `METHOD path` → {commands, note, left, status, stories, requires, partial_use}), `areas.yaml` (`AREAS`), `gaps.yaml` (`GAPS`), `as_ops.yaml` (`AS_OPS`), `as_areas.yaml` (`AS_AREAS`), `phases.yaml` (`RELEASES`). **Never retype the data** (194 commands, 220 operations, 73 App Services operations, 12 workflow commands). After extraction the tool applies the §9.5 rules R1/R6/R7/R11 to every free-text field (notes, `left`, gap text, checklists) and writes `data/REVIEW.txt` listing flagged fields; commands stay relative to `cbctl capella` (the report header says so). The committed YAML is then maintained by hand (it is cbctl's data now).
**Generator.** Port `update-api-coverage.py` (`load_stories`, `in_release`, `status_of`, `open_stories`, `collect_ops`, `op_status`, `breakdown`, `build_db`, `build_15`, `build_as`, `refresh`) to Go:
```go
type Status string // "✅ Done" "🟡 Partial" "⬜ Not built" "➖ Not needed" "⏭️ Not in 1.0"
type Snapshot struct{ Source, APIVersion, Retrieved string; Operations []SpecOp }   // docs/backlog/api/management-api-v4.0-operations.json
type SpecOp struct{ Tag, Method, Path, Summary string }
func LoadSnapshot(path string) (Snapshot, error)
func Build1_0(snap Snapshot, known Stories, d Data) string       // docs/backlog/release-1.0.0/API-COVERAGE.md
func Build1_5(snap Snapshot, known Stories, d Data) string       // docs/backlog/release-1.5.0/API-COVERAGE.md
func BuildAppServices(snap Snapshot, d Data) string              // docs/backlog/release-2.0.0/API-COVERAGE-APP-SERVICES.md
func Refresh(urlOrFile string, snapshotPath string) error        // download OpenAPI YAML, rewrite the snapshot (tags, method, path, summary); fail on unmapped operations
```
Semantics are exactly the Python's: a command's status derives from its stories (all `Implemented`/`Descoped` → Done, some → Partial, none → Not built); an operation's status from its commands (explicit `status` for internal/not-needed; `partial_use` upgrades Not built → Partial); stories of later releases do not count in an earlier release's report (`⏭️ Not in 1.0`); the generator **fails** (exit 2, message like capellactl's) on: unmapped spec operation, mapped operation missing from the snapshot, unknown story id, command in `OPS` missing from `COMMANDS`, spec tag set changed vs `areas.yaml`. Story statuses come from `LoadRelease` for 1.0.0 (stories.yaml), 1.5.0 (story files). Counts expected on the initial port: spec 343 ops = 80 (1.0.0, done count depends on `stories.yaml`) + 1 not needed + 139 (1.5.0) + 73 (App Services) + 50 (AI, not planned); command counts 194 total / 127 planned in 1.5.0. Output formats byte-compatible with the Python reports apart from the §9.5-rewritten text and the generator banner (``Generated by `cbdev docs coverage` … `make docs` ``).
**Parity oracle (one-shot, local).** `cbdev docs coverage --compare-with <capellactl>/docs` regenerates all three reports with `stories.yaml` statuses forced to capellactl's real statuses (all `Implemented` except the two `In progress` stories) and diffs against capellactl's committed reports **after applying the §9.5 rules to the capellactl output**; the diff must be empty (this proves the Go port and the data extraction). Not run in CI (needs a capellactl checkout); the acceptance criterion below requires it once and commits the golden outputs used by the unit tests.

### 9.8 `00-architecture.md` adaptation (profile `architecture`)
Port the document, then apply these **replacements by heading** (R14) — the text of §1, §3, §5, §6, §7, §8, §10, §11, §13 stays (with R1–R6 rewrites; §5 CLI listing is rewritten by R1 and then hand-checked against Phase 2/3 command names: `help`→`guide`, `import`→`sync`, plus a note that `completion` is cobra's):
- **§2 Technology decision** → replaced with: *Language: Go 1.26 (existing cbctl codebase); CLI: cobra; HTTP: `net/http` with the retry client in `internal/capellaapi` (Phase 1); config: `gopkg.in/yaml.v3` + JSON Schema 2020-12 via `santhosh-tekuri/jsonschema/v6`; keychain: `zalando/go-keyring`; tests: stdlib `testing` + `internal/capellafake` + `httptest`; packaging: GoReleaser (existing).* Keep the comparison table's rows, replacing the Python column by Go and dropping the pipx/wheel notes.
- **§4 Architecture tree** → replaced with the cbctl package layout from Phase 3 §1 and Phase 4 §1 (the Reconciler contract in §4.1 is replaced by the Go interface of Phase 3 §6.1).
- **§9 API client behaviour** → table values kept; User-Agent row → `cbctl/<version> (go/<runtime.Version()>)`; "Retried" text refers to Phase 1 §2.3.
- **§12 Testing conventions** → replaced with: layers (unit, contract = `httptest` in `internal/capellaapi`, scenario = `internal/cli/scenario_*_test.go` on `capellafake`, integration = `internal/integration`), naming (`Test<Subject>_CAP<nnn>_AC<n>_<Behaviour>`), standard assertions (`Fake.Writes()`, `AssertNoWrites`, `AssertNoSecret`), the re-run and round-trip rules (kept verbatim).
- **§13 Definition of Done** → item 5 becomes "gofmt, `go vet`, `make cover-engine` (≥ 85% on `internal/plan`, `internal/resources`, `internal/config/capellafile`) pass"; item 7 becomes "re-run `make docs`".
- **§14 Release scope** → add a sentence: "In cbctl, 1.0.0 maps to Phases 0–4 of `docs/specs/`; 1.5.0 and 2.0.0 are the backlog in `docs/backlog/`."
Each replaced section carries `<!-- adapted for cbctl -->` after its heading. Any remaining mention of Python/pytest after the rules is flagged R10 and must be edited.

### 9.9 Worked example (golden test input/output): CAP-066
Input (excerpt, capellactl) → expected output (cbctl) after R1–R13:
```
- capellactl bucket flush CLUSTER NAME [--project NAME]             → cbctl capella bucket flush CLUSTER NAME [--project NAME]
- Code: `cli/bucket_cmds.py` gains `flush`; one operation in `api/endpoints.py`; the fake empties `stats.itemCount`.
  → - Code: `internal/cli/capella_bucket.go` gains `flush`; one operation in `internal/capellaapi/endpoints_*.go`; `internal/capellafake` empties `stats.itemCount`.
| `test_bucket_flush_ac1_confirm_and_flush` | `tests/scenario/test_bucket_flush.py` | Also wrong name cancels |
  → | `TestBucketFlush_CAP066_AC1_ConfirmAndFlush` | `internal/cli/scenario_bucket_flush_test.go` | Also wrong name cancels |
`capellactl bucket flush nova-dev-01 test-orders` (example block) → `cbctl capella bucket flush nova-dev-01 test-orders`
"`--auto-approve` skips it; refused without a terminal, as CAP-024"  → unchanged
footer: <!-- ported-from: capellactl@<sha7> docs/release-1.5.0/CAP-066-bucket-flush.md · rules v1 -->
```
`internal/docgen/port/testdata/CAP-066.in.md` / `.out.md` hold the full pair; the golden test runs the engine and compares bytes. Add synthetic fixtures `rules_*.in.md/.out.md` per rule id including flag-generating cases (expected `port-review` comment text).

### 9.10 Story-specific manual edits after the automatic port (checklist; each resolved in the same PR)
- **CAP-089** (release gate & docs; the only story about tooling): replace its **Design** and **AC** blocks with the Go equivalents: integration scenarios in `internal/integration/` with the §5.1 env vars and `t.Skip("needs a cloud-side endpoint")`; guides `docs/capella/networking.md`, `security.md`, `operational-insights.md` and new sections in `getting-started.md`/`runbook.md`; examples `docs/backlog/release-1.5.0/examples/*.yaml` validated by `internal/docs/examples_test.go` (extend the test glob when stories are implemented); CI checks are `make docs-check ac-coverage` (`cbdev docs ac-coverage --docs docs/backlog/release-1.5.0` and `cbdev docs index --check`). AC test names: `TestRelease15_CAP089_AC1_EveryAreaHasAScenario` in `internal/ci/releasegate/…` or `internal/docs/…` as appropriate; update the table locations accordingly.
- **Stories that mention `capellactl help topics`** (CAP-089 AC4): `cbctl capella guide topics`.
- **Stories with `### capella.yaml` sections** (11 stories): YAML bodies are unchanged; add one sentence at the end of each: "Schema additions go in `internal/config/capellafile/schema.json`; the loader rejects unknown keys." only where the story adds keys (CAP-064, 067, 068, 070, 074, 075, 076, 077, 081, 084, 085, 086, 087 — check each).
- **Stories whose commands could clash with existing cbctl names**: verify with a script (`internal/docgen/port/clash_test.go`): the first token of every 1.5.0 command group (`db-role sample-bucket index eventing data-api replication peer private-endpoint audit-log cmek org user api-key alert event billing insights` + `bucket flush`, `cluster certificate`, `cluster migrate-buckets`, `cluster backup clone`) must not equal an existing `cbctl capella` subcommand registered by Phases 0–3 (`project cluster bucket scope collection cidr credential configure import sync validate plan apply destroy drift init guide janitor`) except where the story **extends** an existing group (`bucket`, `cluster`); the test fails with the clash list otherwise. (`cbctl capella import` is the JSONL importer; no 1.5.0 story uses `import`.)
- **`README.md` of 1.5.0** is generated; its architecture link points to `../release-1.0.0/00-architecture.md` (exists after the port).
- **`docs/backlog/README.md`** (hand-written): explains the three releases, ID scheme, status flow (`Not started → In progress → Implemented`; flip the header row, run `make docs`), how to implement a story (write AC tests with the §0.3 naming, update `schema.json`/guides, set status, `make check`), and that Descoped stories keep their IDs.

### 9.11 Backlog acceptance criteria
1. `cbdev docs port --src <capellactl>/docs --dst docs/backlog --release 1.5.0 --release 1.0.0-architecture` produces the files in §9.2 (26 stories + architecture); a second run is a no-op; `--force` regenerates identically; `TestNoPortReviewMarkers` passes (all flags resolved by hand edits committed in the same PR).
2. Rule tests: for each of R1–R15 there is ≥ 1 positive and ≥ 1 negative golden test; the CAP-066 pair matches byte-for-byte.
3. **No Python leakage** in ported docs: a test greps `docs/backlog/**` and `docs/capella/**` for `\.py\b`, `pytest`, `capellactl` (outside `<!-- ported-from … -->` comments and `SOURCE.md`/`NOTICE`), `CAPELLACTL_`, `pipx`; zero hits.
4. `cbdev docs links --check` passes over `docs/backlog` (every relative link and anchor resolves).
5. `cbdev docs index --check` and `cbdev docs coverage --check` pass; the generated indexes show: release 1.5.0 → 26 stories / 113 points / P0 7 (38 pts), P1 12 (55 pts), P2 7 (20 pts) / 7 phases / completion 0%; API coverage 1.5.0 → 139 operations, 127 commands, all `⬜ Not built`; App Services → 73 operations; 1.0.0 coverage → 80 operations with statuses derived from `stories.yaml` (all `Not started` except CAP-001, until the earlier phases flip them).
6. **Parity oracle** (`--compare-with`) run once against the capellactl checkout used for the port: empty diff for all three coverage reports and both indexes (with statuses forced as described in §9.7); golden outputs committed under `internal/docgen/testdata/parity/`.
7. `cbdev docs ac-coverage --docs docs/backlog/release-1.5.0` exits 0 (no story is Implemented/In progress) and a unit test with a synthetic `Implemented` story lacking a tagged test exits 1 and names the AC; with a tagged test exits 0.
8. The command-clash test (§9.10) passes.
9. `docs/backlog/SOURCE.md` records the capellactl SHA; the dirty-tree refusal is tested.

---------------------------------------------------------------------------------------------------

## 10. Edge cases (each needs a test or explicit handling)

- **Hermetic runs**: parallel `Run` calls with distinct `Deps`; legacy command output interleaving (serialized by the logging mutex); a command that panics → `Run` recovers, prints `internal error: …` (redacted), returns 1.
- **Fake**: `FakeClock` advance while a handler computes state (single lock); pagination with `perPage` larger than max and `page` beyond last (empty `data`, `next` absent); `If-Match` formats; `Authorization` header variants (`bearer` lowercase is accepted by Capella? **no** — accept only `Bearer`); request bodies not JSON → 400 `code 1000`; HEAD/OPTIONS → 405; deleting a project with clusters → 409; creating a bucket on a non-healthy cluster → 409 with the cluster state in the message (port Python); `DropAfterRead` + `ApplyThenDrop` with `http.Hijacker` unsupported on HTTP/2 (fake uses HTTP/1.1 only).
- **Integration harness**: run-id collisions (GITHUB_RUN_ATTEMPT), a project with the prefix but created by a concurrent run (janitor's age filter protects it), cluster still `deploying` at teardown (retry loop), API 429 during teardown (client retries), teardown after a `t.FailNow()`/panic (use `t.Cleanup`, not `defer` in subtests), sandbox org already containing the free-tier cluster (free-tier scenarios skip with reason), `CBCTL_IT_CLUSTERS=0`.
- **Secret scan**: secrets that appear URL-encoded or JSON-escaped; values < 8 chars skipped; `--env` unset → exit 2; binary files (read with replacement chars); very long lines.
- **Release gate**: fewer than N runs; non-consecutive days (UTC midnight boundary); a rerun of the same day; the newest run on a different SHA; `gh` missing → exit 1 with message; `workflow_dispatch` runs excluded (`event != schedule`).
- **Docs tools**: Windows line endings in source docs (normalize to `\n` on output); tables with `|` inside code spans; headings with emoji for anchors; status cell variants (`Not started`); a story without the header table → exit 2 naming the file; story ids not contiguous; two stories claiming the same id; `stories.yaml` unknown fields (strict decode → error).
- **Rewrite engine**: `capellactl` inside link text but not target; inside a table cell at line start; inside inline code with backtick-escaped backticks; fenced block inside a list item; Windows paths; mixed-case `Capellactl` at sentence start → `Cbctl`? **No**: rewrite to `cbctl` (lowercase; it is a command name) and flag `R2` once per file to review the sentence.
- **CI**: Windows runner without `-race`; macOS `gofmt` identical; `goreleaser` snapshot with no tag.

---------------------------------------------------------------------------------------------------

## 11. Acceptance criteria (Phase 4 overall)

1. `make check` passes offline in a clean checkout: vet, gofmt, `-race` unit tests, integration suite on the fake, coverage gate (≥ 85% on `internal/plan`, `internal/resources`, `internal/config/capellafile`), docs checks (index, coverage, reference, links), AC coverage, tidy.
2. §2.3, §3.8, §4.4, §5.7, §7.5, §8.5 and §9.11 acceptance criteria all hold.
3. **Phase 0–3 regression**: every acceptance criterion listed in the Phase 0–2 and Phase 3 specs still passes on the completed fake via `clitest.Scenario` (no skipped tests other than documented platform skips).
4. **Traceability**: `scenario-port.md` accounts for every capellactl scenario/integration test; every `Implemented` story in `stories.yaml` points at a spec section (`ref`) that exists (test: parse refs of the form `phase-N §X` and verify headings exist in `docs/specs/*.md`).
5. **Nightly dry run**: `workflow_dispatch` of `nightly.yml` (job `integration`) against the sandbox completes green once; `release.yml` dry-run gate prints a report; `janitor` job reports zero leftovers. (Manual; record run URLs in the PR.)
6. **Docs**: README, CHANGELOG, NOTICE, `docs/capella/*`, `docs/backlog/*` merged; `docs/backlog/README.md` explains how to start a backlog story; a new engineer can follow `docs/capella/getting-started.md` against the fake (scenario test) end to end.
7. No secret in any new log/report/artifact: nightly artifacts scanned by `scan-secrets`; unit tests prove redaction in `Call.String()`, janitor output and release-gate output.

---------------------------------------------------------------------------------------------------

## 12. Non-goals / deferred

Implementing any 1.5.0/2.0.0 story; porting the 63 release-1.0.0 story files (index only); AI operations; third-party notices generator; PyInstaller/wheel/Docker image distribution; GitHub rulesets automation (CAP-050); Windows install walkthrough (CAP-055); shell-completion docs beyond a pointer to cobra's `cbctl completion`; running the live suite on pull requests; code signing of release binaries.
