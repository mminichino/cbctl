# Test parity audit: capellactl → cbctl

- Source: `capellactl` HEAD `6389d5a` (151 `test_*.py`, 878 test functions).
- Target: `cbctl` branch `mm--capellactl-port` @ `b05642b` (112 `*_test.go`, 569 `Test*` funcs, ~367 named subtests).
- Read-only. Neither repo was modified. No live Capella calls and no Docker were used.

## Method

1. I parsed every Python test with `ast`: names, docstrings, parametrize decorators and `assert` lines.
2. I matched behaviours to Go tests by test and subtest names and test bodies. I also checked 1,189 asserted string literals from the Python tests against Go test bodies: 303 appear verbatim in a Go test, 105 exist only in Go source (implemented but never asserted), and the rest were reworded.
3. I confirmed gaps with a cross-package coverage profile (`go test -coverpkg=./internal/... ./internal/...`). A behaviour counts as MISSING when the Go code path for it has zero hits.
4. Counts are per Python test function, and they are a judgement call. A test is COVERED when a Go test asserts the same exit code, observable output or API effect. PARTIAL means the logic is tested at unit level but not at the CLI or end-to-end level, or only some assertions are mirrored.

## 1. Totals

| Tier | files | tests | COVERED | PARTIAL | MISSING | N/A | covered-or-N/A |
|---|---|---|---|---|---|---|---|
| unit | 69 | 252 | 150 | 30 | 25 | 47 | 78% |
| contract | 9 | 26 | 25 | 1 | 0 | 0 | 96% |
| scenario | 63 | 586 | 301 | 164 | 121 | 0 | 51% |
| integration | 9 | 10 | 8 | 0 | 2 | 0 | 80% |
| binary | 1 | 4 | 0 | 1 | 1 | 2 | 50% |
| **all** | **151** | **878** | **484 (55%)** | **196 (22%)** | **149 (17%)** | **49 (6%)** | **61%** |

The port target is not met. 345 Python tests (39%) lack full Go coverage. The gap sits in the scenario tier: interactive and confirmation flows, failure, interrupt and timeout reporting at the CLI, sync and multi-project, preflight guards, and guide walkthroughs.

### Test runs

- `go test ./... -count=1`: 27 packages pass. The only failure is `internal/docgen/port` `TestPort_RefusesDirtySourceUnlessForce`, which is the sandbox-only `.git/hooks` problem and was ignored.
- `CBCTL_IT_TARGET=fake go test -tags integration ./internal/integration/`: this needs no credentials or Docker and passes. 8 tests pass. `TestBackups_CAP056_CAP058`, `TestClone_CAP049_AC9` and `TestCredentials_CAP027_AC8` call `t.Skip` on both the fake and sandbox targets.

## 3. `scenario-port.md` corrections

**Verdict: the file is inaccurate in both directions.** It covers all 63 scenario files, but:

- Four rows say "ported" when they are only partial.
- Five rows say "planned" or name a different file when tests already exist.
- One row cites a file that has no tests.
- The summary counts don't match the table.
- It doesn't map the unit, contract or integration tiers at all (98 files).

| Row | File | Says | Should be | Evidence |
|---|---|---|---|---|
| 2 | `test_apply.py` | ported | partial (9C/5P/1M) | `confirmApply` (`cli/capella_env_cmds.go:255`) is 9.5% covered, so declining at the prompt is untested. `applyFailure` (`:424`) is 16% covered: exit 130, exit 5 and "Completed before the failure" are untested at CLI level. |
| 24 | `test_credential.py` | ported | partial (6C/2P/1M) | One Scenario test (`scenario_credentials_test.go:11`) against 9 Python tests. The all-buckets warning and created-last ordering aren't asserted at CLI level. |
| 30 | `test_drift.py` | ported | partial (3C/2P) | The typed confirmation in `prune_ac4_protection_and_confirmation` is uncovered (`capella_env_cmds.go:610-623`, `sameNames` 0%). |
| 47 | `test_redaction.py` | ported | partial (1C/1M) | `redaction_ac5_uncaught_exception_redacted` has no Go test. The recover path in `cli/run.go:62-63` has 0 hits. |
| 58 | `test_sync.py` | ported | partial (5C/5P/5M) | ac13/ac14 are uncovered: deleted project or cluster removed, never-deployed kept, renamed project. `capella_sync.go:503-599` has 0 hits; `wasDeployed` and `renamedProject` are 0%. |
| 34 | `test_first_environment.py` | planned | partial (1C/1M) | `scenario_guides_test.go:25` `TestGuides_CAP033_AC5_GettingStartedRun` exists; only the "with init" variant is missing. |
| 60 | `test_track_by_id.py` | planned | partial (2C/2P/2M) | `capella_env_test.go:162` (ids pinned after apply) and `resources/apply_test.go` `TestProjectRenameHintAndPinnedID`. |
| 63 | `test_waiter_cli.py` | planned | partial (3C/4P/2M) | `engine/waiter_test.go` has 15 tests, plus "Apply complete in" in `capella_env_test.go`. |
| 10 | `test_ci_mode.py` | planned | partial (2C/4P/4M) | `TestLogLevelAndQuiet` and `TestProgress` (the CI env case). The uniform "refusing to … without approval" message is never asserted (the string doesn't appear in any Go test). |
| 40 | `test_multi_project.py` | planned | partial (1C/4P/8M) | Config and planner level only; no CLI test uses a two-project file. |
| 48 | `test_resource_verbs.py` | names `capella_resource_test.go` | wrong file | `capella_resource_test.go` holds helpers (`resEnv`, `resResult`) and no `Test` funcs. The real partial coverage is in `capella_managed_test.go:22`. |
| 17 | `test_cluster_create_mds.py` | names `capella_cluster_clone_test.go` (groups) | wrong file | That file has `TestClusterGroup` (group add/remove), not MDS create. MDS create is in `capella_managed_ops_test.go:110`, and `parseGroupSpec` is only 44% covered. |
| 59 | `test_sync_all.py` | planned | still planned, but note the code exists | `--skip-project` is implemented (`capella_sync.go:42`) with zero tests. |
| footer | counts | "ported 7 … partial ~46 … planned ~10" | table really says ported 6 / partial 47 / planned 10; after these fixes: ported 1 (`test_plan.py`), partial 56, planned/missing 6 | The footer counts "init/project Scenario rows" as ported, but the table lists them as partial. |
| related | scope | lists only `tests/integration/test_lifecycle.py` | add the other 8 integration files, 9 contract files and 69 unit files | The 3 always-skipped integration tests should be listed as open. |

Statuses that are correct: `test_plan.py` is effectively ported once `plan/planner_test.go` is counted. The `planned` rows for `test_backup_restore_guide.py`, `test_custom_clusters_guide.py`, `test_help_hints.py` and `test_readme_walkthrough.py` are right.

## 4. Contract tests, schemas and fake fidelity

- **capellactl `tests/contract`** (26 tests) mocks HTTP with `responses` and asserts methods, paths, bodies, ETag/If-Match and retry rules by hand. cbctl covers 25 of these 26 through `capellaapi/endpoints_contract_test.go` (`TestEndpointRequests`, `TestContractCoversInventory`), `client_retry_test.go` (17 tests), `paginate_test.go`, `etag_test.go` and `errors_test.go`. The partial one is the free-tier bucket list "not paginated" assertion, which is only implied.
- **capellactl `tests/schemas`** holds CLI output schemas (`apply`, `plan`, `scale`), not API schemas.
  - cbctl copies them to `plan/testdata/`. `apply` and `plan` are byte-identical; `scale` differs only in its description string.
  - `plan/render_test.go:346` `TestJSONOutputsMatchSchemas` validates `plan` and `apply` output from the renderer, not from the CLI.
  - `scale.schema.json` is compiled at `render_test.go:350` but never validated against `cluster scale --step -o json` output (P-TST-12).
- **OpenAPI validation in cbctl:**
  - Responses: yes, but only for the fake. `capellafake/parity_test.go` (`TestFakeResponseParity`) checks fake responses for the 81 operations in `docs/backlog/api/management-api-v4.0-responses.json` (status codes and body schemas via `jsonschema/v6`). `TestResponsesTestdataMatchesDocs` keeps the testdata copy in sync with the docs.
  - Requests: no. `management-api-v4.0-operations.json` (343 ops) only has method, path, tag and summary, with no request-body schemas. cbctl request bodies are checked only against hand-written expectations. capellactl had the same gap.
- **Fake fidelity, `capellafake` vs the real API and the Python fake:**
  1. **Unknown routes return 404** (`fake.go:591` "no such endpoint"). The Python fake failed loudly (`test_fake_ac1_unhandled_call_fails_loudly`). Many cbctl paths treat 404 as "absent", so a mistyped path would be silently read as "already gone" (P-TST-07).
  2. **Duplicate names are rejected with 409**: projects (`fake_cluster.go:121-127`) and clusters (`fake_test.go:163` expects `ErrConflict`). The Python fake encodes that Capella *allows* duplicates (`test_fake_ac3_duplicate_cluster_name_allowed`: two projects named "p", two clusters with the same body). The ambiguity code paths (`planner_ac5_*`, `project_ac4_ambiguous_blocked`, `--project-id` disambiguation in `capella_sync.go:207-211` and `capella_init.go:839-848`) can only be reached through seeding, and the "409 → adopt" create path is tested against behaviour the real API may not have (P-TST-08).
  3. Request validation is minimal: the fake returns 422 only when `name`, `cloudProvider.type`, service groups or `cidr` are missing. It doesn't enforce Capella's server-side rules such as replicas vs data nodes or disk/provider combinations, so preflight logic is never challenged by fake rejections.
  4. What matches the Python fake: ETag `"Version: N"`, 412 on a stale If-Match, deploying→healthy polling via `SettlePolls`, power state transitions, `FailNext` fault injection, a request log that excludes GETs, and redaction of `Call.String`.
  5. `api_test.go:157-160` defines `TestFake_*` aliases that rerun existing tests, which inflates test counts without adding coverage.

## 2. Per-file mapping (capellactl test file → behaviours → cbctl tests → status)

Counts are per Python test function (parametrized cases count once). C=COVERED, P=PARTIAL, M=MISSING, N/A. Go paths are relative to `internal/`. File status: **COVERED** if all tests are covered, **MISSING** if none are, **N/A** if all are N/A, otherwise **PARTIAL**.


### scenario

| capellactl file | tests | behaviours asserted | cbctl Go tests | C | P | M | N/A | status |
|---|---|---|---|---|---|---|---|---|
| `test_allowed_cidrs.py` | 13 | declarative CIDR create/replace (create-then-delete, 409 fallback), /32 normalisation, unmanaged not deleted+listed, any-IP warning/blocked, past-expiry no recreate, CLI create/delete, rerun 0 writes | cli/capella_cidr_test.go:TestCIDRCreate/TestCIDRDelete; resources/kinds_test.go:TestAllowedCIDRReplaceAndPrune,TestAllowedCIDRExpiredDesiredIsNoop; cli/capella_managed_ops_test.go:55 TestManagedCIDRAndCredential | 8 | 3 | 2 | 0 | **PARTIAL** |
| `test_apply.py` | 15 | no-change no prompt; decline=0 writes; --yes; no-tty blocked; write order; blocked/--target; failure skips rest + resume; 409 adopt; 412 replan once/twice; ambiguous POST adopt; JSON schema; Ctrl-C exit 130 | cli/scenario_apply_test.go:32,45; plan/executor_test.go (StopsAtFirstFailure, ConflictOnCreateAdopts, PreconditionFailed, AmbiguousWrite, Interrupted); plan/render_test.go TestJSONOutputsMatchSchemas | 9 | 5 | 1 | 0 | **PARTIAL** |
| `test_auth.py` | 4 | missing key exit 3 + no request; 401 message; 403 names roles on write and read | cli/capella_globals_test.go:409 TestNewRuntimeAuth; capellaapi/errors_test.go TestRequiredRoles | 2 | 2 | 0 | 0 | **PARTIAL** |
| `test_backup_restore_guide.py` | 1 | backup guide steps run end-to-end against fake | none (scenario_guides_test.go covers getting-started only) | 0 | 0 | 1 | 0 | **MISSING** |
| `test_bucket.py` | 17 | create/update full body, CLI records+applies, rejected create restores file, already-in-file refused, immutable blocked, memory/replica preflight (4 tests), replica note, rerun 0 writes, list/get | cli/capella_bucket_test.go (7 tests); resources/kinds_test.go TestBucketImmutableAndUpdate; cli/capella_managed_ops_test.go:23 (rejected-create file restore path capella_managed.go:117-126 has 0 hits) | 8 | 3 | 6 | 0 | **PARTIAL** |
| `test_bucket_backup.py` | 14 | list/get/create/delete cycle/restore (confirm, options body, refused, turned-off)/export/help | cli/capella_bucket_backup_test.go:TestBucketBackupLifecycle,TestBucketBackupFreeTier | 9 | 4 | 1 | 0 | **PARTIAL** |
| `test_bucket_backup_schedule.py` | 11 | set create/update/interactive, rerun 0 writes, invalid values, free tier, show/delete, cycles, apply/sync/drift, help | cli/capella_bucket_backup_test.go TestBucketBackupSchedule; resources/kinds_test.go TestBackupSchedulesCreateUpdateDelete,TestBucketBackupScheduleRemovedWithPrune | 7 | 3 | 1 | 0 | **PARTIAL** |
| `test_bucket_delete.py` | 9 | typed-name confirm + wrong name cancels, --yes needed, protection, lists dependent credentials, removed-from-file not deleted, --prune deletes/respects protection, rerun noop | cli/capella_bucket_test.go:388-434; resources/apply_test.go TestDeletionProtectionBlocksPrune,TestApplyUpdatesDriftAndPrunes | 6 | 2 | 1 | 0 | **PARTIAL** |
| `test_bucket_free_tier.py` | 7 | free-tier endpoint, other settings refused, interactive update asks memory only, update memory, delete/list, import round-trip, single-node 0 replicas | cli/capella_bucket_test.go:168,236-290; resources/units_test.go TestFreeTierClusterAndBucketFlow | 5 | 1 | 1 | 0 | **PARTIAL** |
| `test_ci_mode.py` | 10 | --yes/-y approves every write; env var is not approval; no stdin reads; missing answer exit 3; uniform refusal text; --quiet output/errors; tty still confirms; rerun 0 writes | cli/capella_globals_test.go:344 TestLogLevelAndQuiet; cli/capella_output_test.go:236 TestProgress,278 TestConfirm | 2 | 4 | 4 | 0 | **PARTIAL** |
| `test_cluster_backup.py` | 14 | list (project/every project), create waits/refused, retain + rerun noop, delete + missing noop, restore confirm/waits/turned off, restores+regions, help | cli/capella_cluster_backup_test.go TestClusterBackupLifecycle,TestClusterBackupNoWaitAndBlocked | 10 | 3 | 1 | 0 | **PARTIAL** |
| `test_cluster_backup_schedule.py` | 14 | set body, no drift, rerun, refused, free tier, show/options/delete, apply creates, sync round trip, sync keeps unreadable schedule, drift+prune, help | cli/capella_cluster_backup_test.go TestClusterBackupSchedule; resources/kinds_test.go TestBackupSchedulesCreateUpdateDelete | 9 | 4 | 1 | 0 | **PARTIAL** |
| `test_cluster_cli.py` | 26 | managed cluster create/update/scale: interactive deploy + re-ask, flags/defaults, invalid input writes nothing, decline/--no-apply keeps comments, rejected create restores file, undeployed entry replaced, Azure premium disk not resized, CIDR overlap refuse/re-ask/warn | cli/capella_managed_ops_test.go:110 TestManagedClusterCommands,138; cli/capella_managed_test.go (5 tests) | 10 | 9 | 7 | 0 | **PARTIAL** |
| `test_cluster_clone.py` | 13 | flags copy settings, interactive source pick, region-bound reset, --no-data-model, --with-cidrs/--with-credentials, --to-project, invalid input, rejected create restores file, idempotent, server-version latest, same region asks | cli/capella_cluster_clone_test.go:49 TestClusterClone (12 subtests) | 8 | 2 | 3 | 0 | **PARTIAL** |
| `test_cluster_create.py` | 10 | create body == fixture, plan line, children after healthy, connection string summary, existing noop, deploying waits, deploymentFailed blocked, validate rules, 422 hint, rerun 0 writes | resources/cluster_test.go; resources/apply_test.go TestApplyEverythingThenConverges; cli/capella_env_test.go:132 | 5 | 4 | 1 | 0 | **PARTIAL** |
| `test_cluster_create_advanced.py` | 11 | advanced options/auto-expansion in body, single-node custom/refused, rules, 422 restores file, version immutable, auto-expansion update, help/options, rerun, import round trip | cli/capella_cluster_scale_test.go:128 TestClusterUpdate; config/capellafile/load_test.go; resources/cluster_test.go TestClusterImmutableChangeBlocked | 5 | 4 | 2 | 0 | **PARTIAL** |
| `test_cluster_create_mds.py` | 20 | multi-group body fixture, plan line per group, flag conflicts, duplicate group, limits, data group first, disk per group, Azure-only auto-expansion, interactive two groups/re-ask/known sizes, unknown size warns, 422 restores, deploymentFailed not deleted | cli/capella_managed_ops_test.go:110 (parseGroupSpec 44% covered); config/capellafile/crossref_test.go | 6 | 7 | 7 | 0 | **PARTIAL** |
| `test_cluster_delete.py` | 12 | delete+wait, wrong name cancels, discard snapshots, protected/ambiguous blocked, lists buckets in confirmation, destroying waits, absent noop, destroyFailed, schedule mention, rerun, --no-wait then rerun waits | cli/capella_cluster_lifecycle_test.go:96 TestClusterDelete (8 subtests) | 8 | 3 | 1 | 0 | **PARTIAL** |
| `test_cluster_disk.py` | 7 | disk-types list, options points to list, create/group-add pickers, update type, rerun noop + wrong provider, help | cli/capella_cluster_test.go:199 TestClusterDiskTypes; cli/capella_cluster_scale_test.go:148 | 3 | 2 | 2 | 0 | **PARTIAL** |
| `test_cluster_group.py` | 13 | group list/add body/file updated, add rerun noop, add refused (single-node, total, free tier), remove body/guards/memory guard, remove missing noop, turned-off blocked, help | cli/capella_cluster_clone_test.go:185 TestClusterGroup | 5 | 3 | 5 | 0 | **PARTIAL** |
| `test_cluster_update.py` | 11 | full-body overlay, preserves unset fields, immutable blocked, waits before/after, 412 retry, rename with pinned id / hint, rerun, settings-only note, scale-in guard | cli/capella_cluster_scale_test.go:128; resources/cluster_test.go; resources/apply_test.go TestProjectRenameHintAndPinnedID | 7 | 3 | 1 | 0 | **PARTIAL** |
| `test_configure.py` | 13 | profile save/show/list/remove, keychain vs file, key never on disk, JSON redacts, defaults | cli/configure_test.go (3 tests); settings/profiles_test.go | 6 | 4 | 3 | 0 | **PARTIAL** |
| `test_configure_check.py` | 7 | key check against org, 403 hint (token vs id), org mismatch | cli/configure_test.go:17; cli/capella_globals_test.go:464 | 3 | 3 | 1 | 0 | **PARTIAL** |
| `test_credential.py` | 9 | supplied/generated password, existing secret file blocks, update access w/o password, all-buckets warning, advanced blocked, created last, rerun 0 writes | cli/scenario_credentials_test.go:11; resources/kinds_test.go TestCredential* | 6 | 2 | 1 | 0 | **PARTIAL** |
| `test_credential_access_kept.py` | 4 | access preserved across update/sync/import | cli/capella_credential_test.go TestAccessGrant,TestAccessSummary | 1 | 2 | 1 | 0 | **PARTIAL** |
| `test_credential_cli.py` | 8 | credential create/update/rotate imperative, password printed once | cli/capella_credential_test.go (7 tests) | 6 | 2 | 0 | 0 | **PARTIAL** |
| `test_credential_delete.py` | 4 | confirm, protection, unknown, prune | cli/capella_credential_test.go:390-417 | 3 | 1 | 0 | 0 | **PARTIAL** |
| `test_custom_clusters_guide.py` | 3 | custom clusters guide commands run against fake | none | 0 | 0 | 3 | 0 | **MISSING** |
| `test_deletion_protection.py` | 9 | protect on/off, delete refused, prune/destroy blocked, declarative toggle | cli/capella_cluster_lifecycle_test.go:68; resources/cluster_test.go TestDeletionProtectionToggle,TestDestroyBlockedBelowProtectedCluster; cli/scenario_drift_test.go:62 | 7 | 2 | 0 | 0 | **PARTIAL** |
| `test_drift.py` | 5 | three groups, exit code + JSON, prune children only, protection + confirmation, system objects excluded | cli/scenario_drift_test.go:14,62; cli/capella_env_test.go:186 | 3 | 2 | 0 | 0 | **PARTIAL** |
| `test_environment.py` | 8 | full build order, summary table, 2nd run 0 writes, resume after failure, destroy reverse order/blocked/keeps undeclared/nothing | cli/capella_env_test.go:132,186; plan/executor_test.go | 5 | 2 | 1 | 0 | **PARTIAL** |
| `test_error_messages.py` | 5 | [ERROR]/[ACTION] for API, 5xx-on-write hint, transport errors | cli/errors_test.go; capellaapi/errors_test.go TestAPIErrorHints | 2 | 2 | 1 | 0 | **PARTIAL** |
| `test_errors.py` | 1 | message + action hint | cli/errors_test.go:17 TestHandleError | 1 | 0 | 0 | 0 | **COVERED** |
| `test_first_environment.py` | 2 | first environment from scratch / with init | cli/scenario_guides_test.go:25 TestGuides_CAP033_AC5_GettingStartedRun | 1 | 0 | 1 | 0 | **PARTIAL** |
| `test_free_tier.py` | 8 | free-tier create/update description/scale refused/import round trip, one per org | cli/capella_cluster_test.go:185; resources/units_test.go TestFreeTierClusterAndBucketFlow; config/capellafile/crossref_test.go TestFreeTierIsLimitedPerOrganizationAcrossProjects | 5 | 2 | 1 | 0 | **PARTIAL** |
| `test_help_hints.py` | 2 | blocked reason / validation error names a guide topic | plan/render_test.go (Topic rendering) | 0 | 1 | 1 | 0 | **PARTIAL** |
| `test_import.py` | 17 | all kinds exported, expired CIDR skipped, any-IP flag, advanced credential commented, round trip no changes, only GETs, no secrets, cluster filter, ambiguous names pinned, minimal/full, schedule compaction, existing file/stdout, turned-off cluster, empty project | cli/capella_env_test.go:260,440; cli/scenario_sync_test.go:14; importer/importer_test.go (5 tests, 5% pkg coverage) | 7 | 6 | 4 | 0 | **PARTIAL** |
| `test_init.py` | 15 | interactive file, lists orgs+projects, typed org w/o key, never writes Capella, cluster name re-asked, no secrets, existing file prompt/--force/backup, invalid answers re-prompted, non-interactive parity, --dry-run, missing flags, delegates to import, plan after init | cli/capella_env_test.go:322-457; cli/scenario_init_test.go:14 | 5 | 4 | 6 | 0 | **PARTIAL** |
| `test_messages.py` | 4 | warning/config problem/usage error formatting | cli/errors_test.go; config/capellafile/issues_test.go | 2 | 2 | 0 | 0 | **PARTIAL** |
| `test_multi_project.py` | 13 | legacy single-project form plans + converted on write, plan/apply two projects, qualified --target, project create appends, import/sync all projects, cluster create project choice, same cluster name needs --project, cluster list/get infer project | config/capellafile/editor_test.go TestEditorEnsureProjectsConvertsSingleProjectForm; plan/planner_test.go TestBuildPlan_KindOrderAndProjectGrouping; cli/capella_resolve_test.go | 1 | 4 | 8 | 0 | **PARTIAL** |
| `test_plan.py` | 10 | symbols+summary, GET-only, ambiguous name/project blocked, children of new cluster, deferred kinds, --target, JSON schema, --detailed-exitcode | cli/scenario_plan_test.go (3); plan/planner_test.go (22); plan/render_test.go | 9 | 1 | 0 | 0 | **PARTIAL** |
| `test_power.py` | 12 | off+wait, on body flag, already-in-state noop, same/opposite direction waits, 409 rereads state, turning-on failed, apply turned-off blocked / --power-on, schedule next-on note, rerun, free tier | cli/capella_cluster_lifecycle_test.go:8 TestClusterPower; resources/cluster_test.go TestClusterPowerOn,TestClusterChangeWhileOffBlocked | 7 | 3 | 2 | 0 | **PARTIAL** |
| `test_project.py` | 11 | create when missing, description If-Match, omitted untouched, ambiguous blocked, pinned id rename / missing never creates, list/get, rerun, apply command targets project, seed noop | cli/scenario_project_test.go; cli/capella_project_test.go; resources/kinds_test.go TestProjectDescriptionUpdate; resources/units_test.go TestPlanErrorOnUnknownPinnedProject | 8 | 2 | 1 | 0 | **PARTIAL** |
| `test_project_create.py` | 7 | interactive, --yes, needs --yes, existing name, adds to file, then cluster create, help/readme | cli/capella_project_test.go:76; cli/capella_managed_test.go:22 | 4 | 2 | 1 | 0 | **PARTIAL** |
| `test_project_delete.py` | 10 | empty, absent noop, non-empty blocked, cascade order/protection, wrong name cancels, 409 message, rerun, removed from file, free-tier cascade | cli/capella_project_test.go:159,223 (16 subtests) | 7 | 2 | 1 | 0 | **PARTIAL** |
| `test_readme_walkthrough.py` | 3 | README walkthrough runs, no manual edit steps, docs use sync | docs/workflows_test.go (lint only) | 0 | 1 | 2 | 0 | **PARTIAL** |
| `test_redaction.py` | 2 | canary never printed; uncaught exception redacted | cli/scenario_redaction_test.go:28 (run.go:62 recover path uncovered) | 1 | 0 | 1 | 0 | **PARTIAL** |
| `test_resource_verbs.py` | 5 | CRUD verbs without apply, project update records+applies, hidden apply aliases, cluster-name-given-to-project hints | cli/capella_managed_test.go:22 (capella_resource_test.go has helpers only, no Test funcs) | 1 | 2 | 2 | 0 | **PARTIAL** |
| `test_rotation.py` | 6 | rotate from configured secret, generated saved with backup, failed send restores file, no implicit rotation, bulk with --target, missing credential created | resources/kinds_test.go TestCredentialRotateUsesAccessAndSavesSecret; secrets/files_test.go TestRotateRestore (no CLI --rotate-passwords test) | 1 | 3 | 2 | 0 | **PARTIAL** |
| `test_scale_in.py` | 10 | guards pass PUT sent, memory guard, replica guard, group removal flags, last data group, --yes not enough, stats unavailable blocks, --skip-capacity-check, rerun no stats call | resources/cluster_test.go TestClusterScaleInBlockedByReplicas/AllowedWithinReplicas; resources/units_test.go TestScalingCheck | 2 | 3 | 5 | 0 | **PARTIAL** |
| `test_scale_out.py` | 14 | add nodes/group body, classification, limits, nodes default data group, ambiguous group, file updated no drift, turned-off blocked, scale failed, rerun, same count noop, scale-in guards | cli/capella_cluster_scale_test.go:14 TestClusterScale; resources/cluster_test.go TestClusterScaleOutUpdatesAndWaits; resources/units_test.go TestScalingHelpers | 8 | 4 | 2 | 0 | **PARTIAL** |
| `test_schedule.py` | 8 | create/update minimal diff, friendly==API noop, omitted untouched, ensure absent, update while off, rerun | resources/kinds_test.go TestScheduleUpdateAndEnsureAbsent; resources/units_test.go TestScheduleExpansion | 7 | 1 | 0 | 0 | **PARTIAL** |
| `test_schedule_cli.py` | 3 | show grid + next, set records+creates, delete removes from Capella and file | cli/capella_cluster_schedule_test.go TestClusterSchedule; cli/capella_managed_ops_test.go:81 | 2 | 1 | 0 | 0 | **PARTIAL** |
| `test_schedule_pause.py` | 9 | pause/resume prints next, noop, no schedule, --power-on order, declarative paused/omitted/after create, recorded in file | cli/capella_cluster_schedule_test.go TestClusterSchedulePausePowerOn | 6 | 3 | 0 | 0 | **PARTIAL** |
| `test_scope_collection_delete.py` | 11 | collection delete + wrong name cancels, scope delete lists collections, protection, system refused, prune only/scope, prune protection, dependent credentials listed (2), rerun | cli/capella_scope_test.go,capella_collection_test.go delete subtests; resources/kinds_test.go TestSystemScopesAndCollections | 6 | 2 | 3 | 0 | **PARTIAL** |
| `test_scopes_collections.py` | 16 | create order/bodies, children after new bucket, maxTTL update + stale ETag, old server blocked/still creates, _default/_system handling, before credentials, rerun, CLI scope/collection add | cli/capella_scope_test.go; cli/capella_collection_test.go; resources/kinds_test.go TestCollectionMaxTTLUpdateAndVersionGate,TestDefaultScopeIsNotCreatedOrPruned | 12 | 3 | 1 | 0 | **PARTIAL** |
| `test_step_scaling.py` | 9 | step up/down + guards, at max/min noop, --all-groups, waits when busy, --no-wait busy exit 4, JSON | cli/capella_cluster_scale_test.go:14 (scale.schema.json compiled but never validated) | 7 | 2 | 0 | 0 | **PARTIAL** |
| `test_sync.py` | 15 | refresh with diff, keeps local-only values, 2nd run in sync, --dry-run exit codes, GET-only, cluster filter, removes deleted project/cluster, keeps never-deployed, wildcard handling, turned-off cluster keeps children, read error keeps values | cli/scenario_sync_test.go:14,65; cli/capella_env_test.go:260,309 (capella_sync.go:503-599 uncovered) | 5 | 5 | 5 | 0 | **PARTIAL** |
| `test_sync_all.py` | 11 | adds missing projects, dry run flags missing, creates file like import, stdout, --project limits, --skip-project remembered/first sync, turned-off not waited for, import alias, GET-only no rewrite | none at CLI level (selectProjects 54%, --skip-project untested) | 1 | 3 | 7 | 0 | **PARTIAL** |
| `test_track_by_id.py` | 6 | import pins ids, pinned rename shown as rename, sync adopts new name, unpinned rename blocks create, new project still created, sync adopts unpinned rename | cli/capella_env_test.go:162 (ids pinned); resources/apply_test.go TestProjectRenameHintAndPinnedID | 2 | 2 | 2 | 0 | **PARTIAL** |
| `test_validate.py` | 2 | invalid exit 3; example valid offline | cli/capella_env_test.go:58,76; cli/scenario_apply_test.go:12 | 2 | 0 | 0 | 0 | **COVERED** |
| `test_vertical_scaling.py` | 8 | compute body, classification, disk change per provider, disk shrink blocked, disk type/provider validation, combined single PUT, rerun, RAM decrease memory guard | cli/capella_cluster_scale_test.go:139-178; resources/units_test.go TestScalingHelpers; config/capellafile/crossref_test.go | 4 | 3 | 1 | 0 | **PARTIAL** |
| `test_waiter_cli.py` | 9 | progress lines, timeout exit 5 message, --no-wait defers children, later apply completes, transitional no 2nd write, update waits first, Ctrl-C message, dots + total time (create/delete) | engine/waiter_test.go (15); cli/capella_env_test.go ("Apply complete in"); plan/executor_test.go TestExecute_NoWait | 3 | 4 | 2 | 0 | **PARTIAL** |

### binary

| capellactl file | tests | behaviours asserted | cbctl Go tests | C | P | M | N/A | status |
|---|---|---|---|---|---|---|---|---|
| `test_smoke.py` | 4 | frozen binary: --version, help/schema/presets bundled, validate; keychain + exit-code parity binary vs installed; bundle allowlist; size <50MB, startup <2s | integration/capella_live_test.go:57 builds cmd/cbctl (sandbox-only); no Go binary smoke in default CI | 0 | 1 | 1 | 2 | **PARTIAL** |

### contract

| capellactl file | tests | behaviours asserted | cbctl Go tests | C | P | M | N/A | status |
|---|---|---|---|---|---|---|---|---|
| `test_client.py` | 2 | Authorization/Accept/User-Agent headers; debug log redacts password + key | capellaapi/client_test.go TestRequestHeadersQueryAndBody,TestDebugBodyIsRedacted | 2 | 0 | 0 | 0 | **COVERED** |
| `test_endpoints_bucket.py` | 2 | bucket POST/PUT/GET stats paths + bodies; free-tier list not paginated | capellaapi/endpoints_contract_test.go TestEndpointRequests | 1 | 1 | 0 | 0 | **PARTIAL** |
| `test_endpoints_cidr.py` | 1 | allowed-CIDR POST/GET/DELETE | capellaapi/endpoints_contract_test.go TestEndpointRequests | 1 | 0 | 0 | 0 | **COVERED** |
| `test_endpoints_cluster.py` | 7 | GET returns ETag; 'Version: 17' sent bare in If-Match; PUT sends If-Match; 412 raises; deletion protection; delete retain param; activation state | capellaapi/etag_test.go TestResponseETag; capellaapi/endpoints_contract_test.go; capellaapi/errors_test.go TestAPIErrorIsSentinels | 7 | 0 | 0 | 0 | **COVERED** |
| `test_endpoints_credential.py` | 2 | credential create/delete contract | capellaapi/endpoints_contract_test.go | 2 | 0 | 0 | 0 | **COVERED** |
| `test_endpoints_schedule.py` | 2 | on/off schedule + activation contract | capellaapi/endpoints_contract_test.go | 2 | 0 | 0 | 0 | **COVERED** |
| `test_endpoints_scope.py` | 1 | scope/collection contract | capellaapi/endpoints_contract_test.go | 1 | 0 | 0 | 0 | **COVERED** |
| `test_pagination.py` | 2 | follows pages; stops | capellaapi/paginate_test.go (8 tests) | 2 | 0 | 0 | 0 | **COVERED** |
| `test_retries.py` | 7 | GET retry w/ jitter, Retry-After, POST not retried on 5xx/read-timeout, retried on connect error/429, max attempts | capellaapi/client_retry_test.go (17 tests) | 7 | 0 | 0 | 0 | **COVERED** |

### integration

| capellactl file | tests | behaviours asserted | cbctl Go tests | C | P | M | N/A | status |
|---|---|---|---|---|---|---|---|---|
| `test_cluster_clone.py` | 1 | live clone + plan clean | integration/clone_test.go:12 (always t.Skip on fake and sandbox) | 0 | 0 | 1 | 0 | **MISSING** |
| `test_cluster_lifecycle.py` | 1 | create/update/delete cluster live | integration/cluster_lifecycle_test.go:16 (passes on fake) | 1 | 0 | 0 | 0 | **COVERED** |
| `test_credentials.py` | 1 | credential create + SDK KV connect | integration/credentials_test.go:16 (always t.Skip) | 0 | 0 | 1 | 0 | **MISSING** |
| `test_data_model.py` | 2 | buckets/scopes/collections live, rerun 0 writes | integration/data_model_test.go:13 | 2 | 0 | 0 | 0 | **COVERED** |
| `test_lifecycle.py` | 1 | full apply/drift/destroy | integration/lifecycle_test.go:37 | 1 | 0 | 0 | 0 | **COVERED** |
| `test_power.py` | 1 | power off/on | integration/power_test.go:12 | 1 | 0 | 0 | 0 | **COVERED** |
| `test_redaction_ci.py` | 1 | CI log never contains key | integration/redaction_test.go:18 | 1 | 0 | 0 | 0 | **COVERED** |
| `test_scaling.py` | 1 | scale out live | integration/scaling_test.go:12 | 1 | 0 | 0 | 0 | **COVERED** |
| `test_schedule.py` | 1 | on/off schedule live | integration/schedule_test.go:12 | 1 | 0 | 0 | 0 | **COVERED** |

### unit

| capellactl file | tests | behaviours asserted | cbctl Go tests | C | P | M | N/A | status |
|---|---|---|---|---|---|---|---|---|
| `api/test_auth_env.py` | 1 | key/org env precedence | settings/settings_test.go TestResolvePrecedence | 1 | 0 | 0 | 0 | **COVERED** |
| `api/test_backoff.py` | 1 | backoff ceilings | capellaapi/client_retry_test.go TestBackoffCeilings | 1 | 0 | 0 | 0 | **COVERED** |
| `api/test_endpoint_inventory.py` | 1 | every inventory endpoint implemented | capellaapi/endpoints_contract_test.go TestContractCoversInventory; capellafake/inventory_test.go | 1 | 0 | 0 | 0 | **COVERED** |
| `api/test_errors.py` | 2 | parse error body; non-JSON body | capellaapi/errors_test.go TestAPIErrorParsing,TestAPIErrorBodyTruncated | 2 | 0 | 0 | 0 | **COVERED** |
| `cli/test_backup_progress.py` | 3 | dots every 30s on one line; failed ends line; each state own line | engine/waiter_test.go TestOnTickFiresEvery30Seconds,TestWaitForClusterHooks | 0 | 2 | 1 | 0 | **PARTIAL** |
| `cli/test_cluster_defaults.py` | 2 | cluster defaults per provider | presets/presets_test.go; capella/cluster_config_test.go | 1 | 1 | 0 | 0 | **PARTIAL** |
| `cli/test_completion.py` | 3 | bash/zsh/fish scripts; command completion; cluster names from capella.yaml | none (no ValidArgsFunction anywhere) | 0 | 0 | 2 | 1 | **MISSING** |
| `cli/test_configure_key.py` | 2 | clean pasted key; --api-key-stdin CRLF/BOM | settings/settings_test.go TestSanitizeAPIKey; readAPIKey 33% covered | 1 | 1 | 0 | 0 | **PARTIAL** |
| `cli/test_console_spinner.py` | 3 | spinner between dots; none in CI/dumb tty; none when stderr not tty | cli/capella_output_test.go:236 TestProgress (Go prints dots, no spinner) | 1 | 0 | 0 | 2 | **COVERED** |
| `cli/test_exit_codes.py` | 2 | exit code set {0,1,2,3,4,5,130}; no numeric literals | cli/errors_test.go | 1 | 0 | 1 | 0 | **PARTIAL** |
| `cli/test_group_spec.py` | 5 | parse SERVICES:NxCPU/RAM[:DISK]; parse errors; disk defaults per provider; choice prefix/ambiguous | cli/capella_managed_ops_test.go (parseGroupSpec 44%) | 1 | 3 | 1 | 0 | **PARTIAL** |
| `cli/test_help.py` | 8 | root help sections; help X == X --help; unknown command 'Did you mean'; topics; plain when not tty; unknown topic; offline; ci topic | guide/guide_test.go; cli/capella_globals_test.go:198 TestLegacyHelpStillWorks | 2 | 2 | 4 | 0 | **PARTIAL** |
| `cli/test_help_config.py` | 4 | guide config FIELD lookup | guide/guide_test.go TestFieldLookup,TestFieldsTree | 3 | 1 | 0 | 0 | **PARTIAL** |
| `cli/test_init_presets.py` | 3 | presets load/sizes | presets/presets_test.go | 3 | 0 | 0 | 0 | **COVERED** |
| `cli/test_main.py` | 6 | --version string; root lists groups; every group has Examples; globals reach context; invalid -o exit 2; non-UTF8 pipe | cli/capella_globals_test.go TestReadGlobalsValues,TestReadGlobalsValidation | 2 | 2 | 1 | 1 | **PARTIAL** |
| `cli/test_make_disk.py` | 2 | IOPS defaults per provider/type | cli/capella_cluster_scale_test.go:148 (recommendedIOPS 67%) | 1 | 1 | 0 | 0 | **PARTIAL** |
| `cli/test_messages.py` | 5 | [ERROR]/[ACTION] format; every CLI exit has action; API error mapping; transport; no CAP- ids in help/messages/schema | cli/errors_test.go; capellaapi/errors_test.go TestAPIErrorHints | 2 | 1 | 2 | 0 | **PARTIAL** |
| `cli/test_prompt_helpers.py` | 4 | no direct prompts; write commands accept approval; ask uses default or names flag; refusal message | clitest/prompts_test.go; cli/capella_output_test.go TestConfirm | 1 | 2 | 1 | 0 | **PARTIAL** |
| `config/test_bucket_rules.py` | 3 | bucket schema rules | config/capellafile/crossref_test.go TestBucketTypeRules; load_test.go TestLoadInvalidGolden | 3 | 0 | 0 | 0 | **COVERED** |
| `config/test_cidr_rules.py` | 4 | CIDR rules/normalise/expiry | config/capellafile/crossref_test.go TestNormalizeCIDR,TestParseRFC3339 | 4 | 0 | 0 | 0 | **COVERED** |
| `config/test_compute_sizes.py` | 1 | compute size table | presets/presets_test.go TestComputeSizes | 1 | 0 | 0 | 0 | **COVERED** |
| `config/test_credential_rules.py` | 2 | credential rules | config/capellafile/load_test.go TestLoadInvalidGolden | 2 | 0 | 0 | 0 | **COVERED** |
| `config/test_crossref.py` | 8 | cross-reference rules | config/capellafile/crossref_test.go TestCrossRefRules | 8 | 0 | 0 | 0 | **COVERED** |
| `config/test_crossref_disk.py` | 3 | disk/provider cross-ref | config/capellafile/crossref_test.go | 3 | 0 | 0 | 0 | **COVERED** |
| `config/test_defaults.py` | 2 | defaults applied | config/capellafile/load_test.go TestDefaults,TestDefaultsSingleNodeReplicasAndFreeTier | 2 | 0 | 0 | 0 | **COVERED** |
| `config/test_discovery.py` | 4 | capella.yaml/yml discovery, both = error, explicit path | config/capellafile/discover_test.go (6) | 4 | 0 | 0 | 0 | **COVERED** |
| `config/test_error_messages.py` | 5 | issue text/did-you-mean/line numbers | config/capellafile/issues_test.go; load_test.go TestIssuesAreSortedAndHaveLines | 5 | 0 | 0 | 0 | **COVERED** |
| `config/test_free_tier_rules.py` | 1 | one free tier per org | config/capellafile/crossref_test.go TestFreeTierIsLimitedPerOrganizationAcrossProjects | 1 | 0 | 0 | 0 | **COVERED** |
| `config/test_loader.py` | 2 | load valid/invalid fixtures | config/capellafile/load_test.go TestLoadValidFixtures | 2 | 0 | 0 | 0 | **COVERED** |
| `config/test_multi_project.py` | 2 | projects: list form loads | config/capellafile/load_test.go; editor_test.go | 2 | 0 | 0 | 0 | **COVERED** |
| `config/test_password_rules.py` | 2 | password policy | secrets/password_test.go TestMeetsRules | 2 | 0 | 0 | 0 | **COVERED** |
| `config/test_precedence.py` | 1 | flag > env > profile | settings/settings_test.go TestResolvePrecedence | 1 | 0 | 0 | 0 | **COVERED** |
| `config/test_schedule_rules.py` | 2 | schedule window rules | config/capellafile/crossref_test.go TestDayValueAndGroupKey | 2 | 0 | 0 | 0 | **COVERED** |
| `config/test_scope_rules.py` | 4 | scope/collection name rules | config/capellafile/crossref_test.go TestKeyspaceNameIssue | 4 | 0 | 0 | 0 | **COVERED** |
| `config/test_secrets.py` | 6 | ${VAR}/${file:} resolved, unset/missing named, value never in errors, literal rejected | config/capellafile/load_test.go TestSecretReferences | 6 | 0 | 0 | 0 | **COVERED** |
| `docs/test_backup_guide.py` | 3 | backup guide content/commands valid | guide/guide_test.go TestTopicsAreListedAndRender | 1 | 1 | 1 | 0 | **PARTIAL** |
| `docs/test_doc_commands.py` | 2 | every command in docs parses | docgen/reference/reference_test.go; cli/hermetic_test.go | 0 | 2 | 0 | 0 | **PARTIAL** |
| `docs/test_examples.py` | 2 | example files validate | config/capellafile/load_test.go TestLoadValidFixtures | 1 | 1 | 0 | 0 | **PARTIAL** |
| `docs/test_reference.py` | 1 | reference doc up to date | docgen/reference/reference_test.go | 1 | 0 | 0 | 0 | **COVERED** |
| `docs/test_runbook.py` | 1 | runbook commands exist | docs/workflows_test.go | 0 | 1 | 0 | 0 | **PARTIAL** |
| `docs/test_runbook_ci.py` | 1 | CI runbook | docs/workflows_test.go TestWorkflowsLint | 0 | 1 | 0 | 0 | **PARTIAL** |
| `docs/test_windows_docs.py` | 7 | PowerShell install docs | N/A (CAP-055 descoped) | 0 | 0 | 0 | 7 | **N/A** |
| `engine/test_normalize.py` | 5 | undeclared ignored, defaults, equivalences, privilege aliases, flush | plan/normalize_test.go (11) | 5 | 0 | 0 | 0 | **COVERED** |
| `engine/test_waiter.py` | 6 | create->healthy, state changes only, backoff, failure states, delete 404 ok, 30s tick | engine/waiter_test.go; engine/waiter_client_test.go | 6 | 0 | 0 | 0 | **COVERED** |
| `fakes/test_fake_capella.py` | 11 | every inventory endpoint, unhandled call fails loudly, deploying->healthy, delete->404, power states, stale 412, dup child 409, dup cluster name ALLOWED, writes exclude GETs, call repr redacts, fail_next | capellafake/fake_test.go; capellafake/api_test.go (fake answers 404 for unknown routes; rejects duplicate cluster/project names with 409) | 9 | 0 | 2 | 0 | **PARTIAL** |
| `fakes/test_fake_parity.py` | 2 | response shape parity vs OpenAPI; checker reports each kind | capellafake/parity_test.go TestFakeResponseParity,TestParityCheckerReportsEachKind | 2 | 0 | 0 | 0 | **COVERED** |
| `output/test_gitcheck.py` | 3 | warn when secrets dir not git-ignored; silent when ignored / outside git | none (secrets.GitWarning untested) | 0 | 0 | 3 | 0 | **MISSING** |
| `output/test_password_gen.py` | 2 | generated password policy/unique | secrets/password_test.go | 2 | 0 | 0 | 0 | **COVERED** |
| `output/test_redact.py` | 2 | register + mask | redact/redact_test.go | 2 | 0 | 0 | 0 | **COVERED** |
| `output/test_render.py` | 1 | table/JSON render | cli/capella_output_test.go | 1 | 0 | 0 | 0 | **COVERED** |
| `output/test_secrets_out.py` | 3 | 0600 file + content; terminal shows path only; preflight refuses overwrite | secrets/files_test.go; resources/kinds_test.go TestCredentialCreateRefusesExistingSecretFile | 2 | 1 | 0 | 0 | **PARTIAL** |
| `resources/test_allowed_cidr.py` | 3 | CIDR reconciler | resources/units_test.go TestCIDRHelpers; kinds_test.go | 3 | 0 | 0 | 0 | **COVERED** |
| `resources/test_bucket_body.py` | 3 | bucket body build | resources/units_test.go TestBucketHelpers | 3 | 0 | 0 | 0 | **COVERED** |
| `resources/test_collection.py` | 6 | collection reconciler/maxTTL/version gate | resources/kinds_test.go TestCollectionMaxTTLUpdateAndVersionGate | 6 | 0 | 0 | 0 | **COVERED** |
| `resources/test_credential.py` | 4 | credential access body/canon | resources/kinds_test.go TestAccessHelpers; plan/normalize_test.go TestCanonPrivileges | 4 | 0 | 0 | 0 | **COVERED** |
| `resources/test_deletion_protection.py` | 3 | omitted unchanged, diff, new cluster | resources/cluster_test.go TestDeletionProtectionToggle | 3 | 0 | 0 | 0 | **COVERED** |
| `resources/test_scale_in_guards.py` | 3 | memory math, boundary equal allowed, no data left | resources/units_test.go TestScalingCheck (scaling.go:167,202 uncovered) | 1 | 2 | 0 | 0 | **PARTIAL** |
| `resources/test_schedule_expand.py` | 4 | friendly schedule expansion | resources/units_test.go TestScheduleExpansion | 4 | 0 | 0 | 0 | **COVERED** |
| `resources/test_schedule_next.py` | 2 | next transition | resources/units_test.go TestScheduleNextTransitions | 2 | 0 | 0 | 0 | **COVERED** |
| `scripts/test_api_coverage.py` | 4 | API coverage doc generation | none (docgen/coverage 0%, no tests); docgen/apisnap partially | 0 | 1 | 3 | 0 | **PARTIAL** |
| `scripts/test_check_ac_coverage.py` | 4 | AC coverage checker | docgen/accoverage/accoverage_test.go | 4 | 0 | 0 | 0 | **COVERED** |
| `scripts/test_ci_janitor.py` | 3 | janitor prefix/dry-run | ci/janitor/janitor_test.go; cli/capella_janitor_test.go | 3 | 0 | 0 | 0 | **COVERED** |
| `scripts/test_github_rulesets.py` | 9 | GitHub rulesets JSON | N/A (CAP-050 out of scope) | 0 | 0 | 0 | 9 | **N/A** |
| `scripts/test_release_gate.py` | 4 | release gate | ci/releasegate/gate_test.go | 4 | 0 | 0 | 0 | **COVERED** |
| `scripts/test_scan_log_secrets.py` | 3 | log secret scan | ci/secretscan/scan_test.go | 3 | 0 | 0 | 0 | **COVERED** |
| `test_binary.py` | 11 | PyInstaller spec/bundle/version | N/A (GoReleaser); version string partially | 0 | 2 | 0 | 9 | **PARTIAL** |
| `test_integration_suite.py` | 6 | teardown on failure / failed teardown fails test / both reported; zero writes from request log; nightly workflow; rehearsed on push | integration/harness_test.go:11 TestResultRequestsParsesDebugLines; docs/workflows_test.go | 1 | 2 | 3 | 0 | **PARTIAL** |
| `test_licence.py` | 7 | licence/NOTICE files | docs/workflows_test.go TestNOTICEMentionsCapellactl; rest N/A (CAP-044) | 1 | 0 | 0 | 6 | **COVERED** |
| `test_packaging.py` | 12 | wheel/sdist/pyproject | N/A (CAP-043, GoReleaser) | 0 | 0 | 0 | 12 | **N/A** |

## 5. Per-package coverage

"Own" is `go test -cover ./internal/...`, where each package is measured only by its own tests. "Cross" is `-coverpkg=./internal/...`, where statements are counted as hit by any package's tests. Total statement coverage: own 64.4%, cross 66.5%.

| Package | stmts | own % | cross % | tier | < 80%? |
|---|---|---|---|---|---|
| internal/plan | 777 | 97.9 | 97.9 | engine | |
| internal/engine | 143 | 95.1 | 95.8 | engine | |
| internal/presets | 68 | 94.1 | 94.1 | engine | |
| internal/ci/secretscan | 51 | 96.1 | 96.1 | tooling | |
| internal/config/capellafile | 1664 | 89.6 | 91.8 | engine | |
| internal/guide | 169 | 87.6 | 88.2 | product | |
| internal/capellafake | 1808 | 85.7 | 86.6 | test infra | |
| internal/resources | 2397 | 85.1 | 86.3 | engine | |
| internal/docgen/accoverage | 86 | 84.9 | 84.9 | tooling | |
| internal/capellaapi | 802 | 84.0 | 84.9 | engine | |
| internal/ci/janitor | 88 | 80.7 | 80.7 | tooling | |
| internal/redact | 54 | 77.8 | 81.5 | security | **yes (own)** |
| internal/ci/releasegate | 80 | 75.0 | 75.0 | tooling | **yes** |
| internal/cli | 9168 | 74.1 | 74.1 | product | **yes** (largest package; 2,375 uncovered statements) |
| internal/secrets | 91 | 72.5 | 82.4 | security | **yes (own)** |
| internal/clitest | 197 | 72.1 | 79.7 | test infra | **yes** |
| internal/settings | 276 | 67.0 | 75.4 | security | **yes** (`keychain.go` Get/Set/Delete 0%) |
| internal/docgen/apisnap | 141 | 60.3 | 60.3 | tooling | **yes** |
| internal/docgen/links | 138 | 58.7 | 58.7 | tooling | **yes** |
| internal/docgen/port | 209 | 56.5 | 56.5 | tooling | **yes** (one sandbox failure) |
| internal/logging | 45 | 42.2 | 75.6 | product | **yes** |
| internal/importer | 860 | 5.0 | 43.5 | product | **yes** (feeds `sync`/`init`) |
| internal/rest | 1099 | 31.0 | 31.0 | Server legacy | **yes** |
| internal/docgen/reference | 222 | 17.6 | 17.6 | tooling | **yes** |
| internal/server | 404 | 11.1 | 11.1 | Server legacy (SDK, Docker-only IT) | **yes** |
| internal/docgen/stories | 390 | 8.5 | 8.5 | tooling | **yes** |
| internal/capella | 1236 | 7.5 | 7.5 | legacy SDK-backed `capella cluster create/test/bucket create…` (`cli/capella.go`) | **yes** |
| internal/docgen/coverage | 953 | 0.0 | 0.0 | tooling | **yes** (no tests) |
| internal/config | 31 | 0.0 | 3.2 | product | **yes** (no tests) |
| internal/docgen/data | 23 | 0.0 | 65.2 | tooling | **yes** |
| internal/models | — | no tests | — | types | n/a |

CI gate: `make cover-engine` (`cbdev ci cover-gate --min 85`) covers only `plan`, `resources` and `config/capellafile`. Nothing gates `cli`, `capellaapi`, `secrets`, `redact`, `settings` or `importer`.

### 15 most important untested error branches

All have zero hits in the cross-package profile.

| # | file:line | branch | why it matters |
|---|---|---|---|
| 1 | `internal/cli/capella_env_cmds.go:425-430` | `applyFailure`: `result.Interrupted` → `ExitInterrupted` (130) with the "Run the command again" action | The Ctrl-C contract for apply (`apply_ac8`, `waiter_ac8`). Only the executor is tested; the CLI mapping isn't. |
| 2 | `internal/cli/capella_env_cmds.go:436-441` | `applyFailure`: `WaitTimeout`/`StatusTimeout` → `ExitTimeout` (5) | The exit-5 contract (`waiter_ac4_timeout_exit_5`). |
| 3 | `internal/cli/capella_env_cmds.go:442-457` | "Failed: KIND KEY", "Completed before the failure: …" plus `OnFailure` text | Partial-failure reporting (`apply_ac5`, `env_ac3`). |
| 4 | `internal/cli/capella_env_cmds.go:262-313` | `confirmApply`: destructive/rotating lists, typed name for data deletes, decline → "Apply cancelled" | The interactive safety net for `apply --prune` and `--rotate-passwords`. Only `--yes` and no-tty are tested. |
| 5 | `internal/cli/capella_env_cmds.go:610-643` | `runDestroy`: typed confirmation (`sameNames`), cancel message, interrupted/timeout exit codes | `destroy` without `-y`. Every branch is untested. |
| 6 | `internal/cli/capella_managed.go:117-126` | `ChangeAndApply` restore: Capella rejected the write, so capella.yaml is restored, with a message | `*_ac8_rejected_create_restores_file` (bucket, cluster, clone, MDS). There is a risk of a file that diverges from Capella. |
| 7 | `internal/cli/capella_managed.go:89-97` | managed edit: plan blocked → exit 4; decline → "Cancelled; … unchanged" | `cluster_cli_blocked_change_writes_nothing` and `ac8_decline_writes_nothing`. |
| 8 | `internal/cli/capella_sync.go:503-522` | sync: a renamed project in Capella is adopted, or blocked when ambiguous (exit 4); a deleted project is removed with a warning | `sync_ac13`, `track_by_id_ac5`. Silent data loss in capella.yaml is possible. |
| 9 | `internal/cli/capella_sync.go:134-147` | sync over an existing non-cbctl file: `--yes` or no tty → error; prompt → overwrite with backup or cancel | `sync_all_ac2`, `init_ac5`. File clobbering. |
| 10 | `internal/cli/capella_sync.go:408-434` | sync `--project` naming unknown projects → exit 3 | `sync_all_ac3`. |
| 11 | `internal/cli/capella_cluster_util.go:416-422` | `checkCapacity`: stats unreadable → exit 4 unless `--skip-capacity-check` | `scale_in_ac7`, imperative. The declarative twin, `internal/resources/scaling.go:186-196`, is also uncovered. |
| 12 | `internal/resources/scaling.go:167` and `:202` | scale-in blocked: the last nodes of a service removed; bucket memory exceeds the remaining data quota | `scale_in_ac2`, `ac4`, `ac5`: data-loss guards. |
| 13 | `internal/resources/bucket.go:321-330` and `:351-354` | bucket preflight: replicas > max supported; needs N MB but cluster has M free → blocked | `bucket_ac6_*` (4 tests). |
| 14 | `internal/cli/run.go:62-63` | `recover()` → "internal error" (redacted) | `redaction_ac5_uncaught_exception_redacted`. A panic could print secrets. |
| 15 | `internal/cli/configure.go:127-128` | `configure` key check: 403 → "use the key's token, not its ID…" | `configure_check`. The most common onboarding error. |

Honourable mentions, also 0 hits:

- `internal/cli/capella_cluster_util.go:104` `conflictAsBlocked` (409 mid-operation → exit 4), the imperative half of `power_ac5_409_rereads_state`.
- `internal/resources/cluster.go:627-674` (free-tier power-on wait error, free-tier update).
- `internal/secrets/files.go:130-143` `GitWarning` (`gitcheck_ac4`).
- `internal/settings/keychain.go:26-46` (OS keychain get/set/delete).
- `internal/capellaapi/client.go:252,342,361,409` (encode, build-request and transport failures; invalid base URL).
- `internal/capellaapi/paginate.go:44,68` (decode errors).

## 6. Findings

Severity: **High** means a user-visible safety or exit-code contract has no test. **Medium** means functionality is ported but unverified, or tracking docs are wrong. **Low** is hygiene.

### P-TST-01: Apply and destroy failure, interrupt and timeout exit codes are untested at CLI level (High)
- **Evidence:** `applyFailure` is 16% covered (`cli/capella_env_cmds.go:424-458`) and the `runDestroy` error branches (`:635-643`) have 0 hits. Python tests: `apply_ac5_failure_skips_rest`, `apply_ac5_rerun_resumes_without_duplicates`, `apply_ac8_keyboard_interrupt_exit_130`, `waiter_ac4_timeout_exit_5`, `waiter_ac8_interrupt_message`, `env_ac3_resume_after_failure`.
- **Fix:** add `internal/cli/scenario_apply_failure_test.go` with:
  - `TestApply_CAP006_AC5_FailureSkipsRestAndResumes`: `fake.FailNext("POST", ".*/buckets$", 500)`, apply, then apply again.
  - `TestApply_CAP006_AC8_InterruptExit130`: cancel the context from a `capellafake` hook on the first cluster poll.
  - `TestWaiter_CAP007_AC4_TimeoutExit5`: `SettlePolls(1000)` plus the fake clock with `--timeout 5m`.
  - `TestDestroy_CAP030_InterruptAndTimeout`.
- **Acceptance:**
  - Exit codes are 1, 130 and 5 respectively.
  - stderr contains `Failed: bucket …`, `Completed before the failure: project …, cluster …`, `stopped waiting; the operation continues in Capella` and `still … after 5m0s`.
  - The second apply sends no duplicate POST (`fake.Count("POST", "/clusters$") == 1`).
  - `applyFailure` reaches ≥90% coverage.

### P-TST-02: Interactive confirmations for apply, destroy and prune are untested (High)
- **Evidence:** `confirmApply` is 9.5% covered; `confirmDataDelete`, `dataDeleteLines`, `bucketItems`, `sameNames` and `truthyData` are all 0%. Python tests: `apply_ac2_decline_makes_no_writes`, `bucket_delete_ac1_wrong_name_cancels`, `prune_ac4_protection_and_confirmation`, `ci_ac5_terminal_still_confirms`, `project_delete_ac5_wrong_name_cancels`.
- **Fix:** add `internal/cli/scenario_confirm_test.go`. Use `clitest.Scenario` with a TTY stub (`Deps.IsTTY`) and `clitest.ScriptedPrompter`. Tests:
  - `TestApply_CAP006_AC2_DeclineNoWrites`
  - `TestApply_Prune_TypedNameWrongCancels`
  - `TestApply_Prune_ListsItemsStored`: seed a bucket with `stats.itemCount`.
  - `TestApply_RotatePasswords_ListsRotating`
  - `TestDestroy_TypedProjectNames`, with the names in a different order
- **Acceptance:**
  - Declining leaves `fake.Writes()` empty and capella.yaml byte-identical.
  - stderr shows `Destructive changes:`, `Passwords to rotate:`, `items stored: N` and `Apply cancelled; nothing was changed.`
  - `confirmApply` reaches ≥90% coverage.

### P-TST-03: Preflight guards for scale-in and bucket memory/replicas are untested on their block paths (High)
- **Evidence:**
  - `resources/scaling.go:167,186-202` and `resources/bucket.go:321-354` have 0 hits.
  - `cli/capella_cluster_util.go:416-434` has 0 hits.
  - `--skip-capacity-check` appears in no Go test.
  - Python tests: `scale_in_ac2…ac8` (10), `bucket_ac6_*` (4), `group_cmd_ac4_remove_memory_guard`, `vertical_ac6_ram_decrease_memory_guard`.
- **Fix:**
  - Extend `internal/resources/cluster_test.go` with table-driven `TestScaleInGuards` cases: memory exceeded, last data group removed, last service nodes without `AllowServiceRemoval`, stats returning 403/500 with and without `SkipCapacityCheck`, and boundary equal allowed.
  - Add `TestBucketPreflight` to `kinds_test.go`: replicas > maxReplicas, memory > freeMemoryInMb, sum of new buckets.
  - Add CLI tests `TestClusterScale/"stats unavailable blocks"` and `".../--skip-capacity-check proceeds"`.
  - Fake: add `FailNext` on `GET .*/stats`.
- **Acceptance:**
  - Each guard yields `plan.Blocked` with the expected reason text and exit 4 at CLI.
  - With the flag, a PUT is sent and the note says `memory not checked (--skip-capacity-check)`.
  - `scaling.go` and `bucket.go` reach ≥90% coverage.

### P-TST-04: Sync edge cases (rename, deletion, --skip-project, overwrite) are unported (High)
- **Evidence:** `capella_sync.go` `refresh` is 54% covered and `selectProjects` 54%; `wasDeployed`, `renamedProject`, `importError` and `report` are 0%. Python tests: `sync_ac13_*` (4), `sync_ac14_*` (3), `test_sync_all.py` (7 MISSING), `track_by_id_ac3`, `track_by_id_ac5`, `multi_ac6_sync_all_projects`.
- **Fix:** add `internal/cli/scenario_sync_edge_test.go` with:
  - `TestSync_CAP038_AC13_RemovesDeletedProject`, `…_KeepsNeverDeployed` and `…_RemovesDeletedCluster`
  - `TestSync_CAP048_AC5_AdoptsRenamedProject`: seed the project, sync, rename it in the fake, sync again.
  - `TestSync_CAP054_AC3_SkipProjectRemembered`
  - `TestSync_CAP054_AC3_UnknownProjectExit3`
  - `TestSync_CAP054_AC2_ExistingForeignFileNeedsConfirm`
  - `TestSync_CAP038_AC14_TurnedOffKeepsChildren`
  - `TestSync_CAP038_AC14_ReadErrorKeepsPrevious`: `FailNext` on the buckets GET.
- **Fixtures:** `capellafake.SeedProject`/`SeedCluster` with rename helpers (add `Fake.RenameProject(id, name)`).
- **Acceptance:**
  - Each test asserts the resulting YAML (golden via `clitest.GoldenText`), the exit code, and `fake.Writes()` empty (sync is GET-only).
  - `capella_sync.go` reaches ≥85% coverage.

### P-TST-05: Managed-edit file restore and blocked/decline paths are untested (High)
- **Evidence:** `capella_managed.go:89-126` has 0 hits and `validateCandidate` is 0%. Python tests: `bucket_cli_ac8_rejected_create_restores_file`, `cluster_cli_ac8_rejected_create_restores_file`, `cluster_clone_ac7_rejected_create_restores_file`, `cluster_mds_ac10_422_restores_file`, `cluster_adv_ac4_422_restores_file`, `rotation_ac2_failed_send_restores_file`, `cluster_cli_blocked_change_writes_nothing`.
- **Fix:** add `internal/cli/capella_managed_restore_test.go` with:
  - `TestManaged_RejectedCreateRestoresFile`: `FailNext("POST", "/buckets$", 422)`.
  - `TestManaged_TransportErrorKeepsFile`: `FailNextOpts{DropAfterRead:true}`.
  - `TestManaged_BlockedWritesNothing`
  - `TestManaged_DeclineKeepsFile`, which already exists for delete; extend it to `add`.
- **Acceptance:**
  - On 422 the file is byte-identical to before, and stderr contains `was restored: nothing was recorded`.
  - On a dropped connection the file keeps the edit.
  - A blocked plan exits 4 with zero writes.

### P-TST-06: `scenario-port.md` overstates and understates status (Medium)
- **Evidence:** see §3. Four "ported" rows are partial, five rows are mis-stated, two rows cite the wrong file, and the footer counts are inconsistent.
- **Fix:**
  - Apply the §3 table and add a per-test column (C/P/M counts from §2).
  - Extend `cbdev docs ac-coverage --port-table` to fail when a row says `ported` but a Python test name in that file has no Go test tagged with the same `CAPxxx_ACn` suffix.
  - Add unit, contract and integration sections, or a sibling `unit-port.md`.
- **Acceptance:** `make ac-coverage` fails on an overstated row, and the counts in the footer are computed rather than hand-written.

### P-TST-07: The fake silently answers 404 for unknown routes (Medium)
- **Evidence:** `capellafake/fake.go:591`. The Python counterpart is `test_fake_ac1_unhandled_call_fails_loudly`. Several reconcilers map 404 to "absent" (`ignoreNotFound` in `resources/cidr.go:294` and `bucket.go:451`), so a path typo passes tests.
- **Fix:** add a `WithStrictRoutes()` option, default on, that calls `f.t.Errorf("capellafake: unhandled %s %s")` and still returns 404. Add `TestFake_UnhandledRouteFailsTest` (using a `testing.TB` spy).
- **Acceptance:** an unrouted request fails the calling test; the full suite stays green.

### P-TST-08: The fake rejects duplicate project and cluster names, but Capella allows them (Medium)
- **Evidence:**
  - `fake_cluster.go:124` returns 409 for a duplicate project name.
  - `fake_test.go:163` expects `ErrConflict` for a duplicate cluster.
  - Python: `test_fake_ac3_duplicate_cluster_name_allowed`.
  - Ambiguity code that only real data can reach: `capella_sync.go:197-211` and `capella_init.go:839-848`, both 0 hits.
  - Planner ambiguity is covered only by unit tests (`TestBuildPlan_AmbiguousMatchBlocked`).
- **Fix:**
  - Make duplicates allowed: return 201 with a new ID.
  - Rewrite `fake_test.go:163` as `TestFake_DuplicateClusterNameAllowed`.
  - Add CLI tests: `TestPlan_CAP005_AC5_AmbiguousProjectBlocked`, `TestSync_AmbiguousProjectNeedsProjectID` and `TestInit_AmbiguousProjectNeedsProjectID`.
  - Re-verify `TestExecute_ConflictOnCreateAdopts` against the documented 409 semantics for child resources only.
- **Acceptance:** the fake matches the real API on duplicates, and each ambiguity path exits 4 (or 3) with an action naming `--project-id`.

### P-TST-09: CI mode and the uniform refusal message are not asserted (Medium)
- **Evidence:** "refusing to" appears in no Go test. Python `test_ci_mode.py` has 4 MISSING and 4 PARTIAL.
- **Fix:** add `internal/cli/scenario_ci_test.go`:
  - A table over every write command (from the command tree, not hand-listed): no TTY and no `-y` → exit per contract, the same `[ERROR]`/`[ACTION]` text, zero writes.
  - `-y`/`--yes` → succeeds.
  - `CBCTL_YES=1` (or any env) is not approval.
  - stdin is never read (a stdin that blocks forever makes the test time out).
- **Acceptance:** adding a new write command without approval wiring fails the test.

### P-TST-10: Interactive init, cluster create and MDS flows are mostly untested (Medium)
- **Evidence:** `initOrganization` is 22.9% covered, `initProject` 26.2% and `parseGroupSpec` 44%. Init error lines `:945-1022` have 0 hits. Python: `test_init.py` (6 M), `test_cluster_cli.py` (7 M), `test_cluster_create_mds.py` (7 M), `test_group_spec.py`.
- **Fix:**
  - Table-driven `TestParseGroupSpec` in `capella_managed_ops_test.go` covering every usage error.
  - In `scenario_init_test.go`: `TestInit_CAP035_AC2_ListsOrgsAndProjects` (seed 2 orgs), `…_AC6_InvalidAnswersReprompted` and `…_AC2_ExistingClusterNameReasked`, all using `ScriptedPrompter`.
  - `TestClusterCreate_CAP051_AC6_InteractiveTwoGroups`
- **Acceptance:** `capella_init.go` and `parseGroupSpec` reach ≥85% coverage, and the written file is a golden match.

### P-TST-11: Multi-project files have no CLI-level test (Medium)
- **Evidence:** `test_multi_project.py` is 1 C, 4 P, 8 M. No CLI test uses a `projects:` file.
- **Fix:** add `internal/cli/scenario_multi_project_test.go` with:
  - `TestMulti_CAP040_AC2_LegacyConvertedOnFirstWrite`
  - `…_AC3_PlanApplyTwoProjects`
  - `…_AC3_TargetQualified`, using `--target project/cluster`
  - `…_AC7_SameClusterNameNeedsProject`
  - `…_AC5_SyncAllProjects`
- **Acceptance:** plan output groups by project, apply creates both projects, and an ambiguous cluster name exits 3 naming `--project`.

### P-TST-12: The `scale.schema.json` output contract is never validated (Low)
- **Evidence:** `plan/render_test.go:350` loads the schema but doesn't validate against it. `capella_cluster_scale_test.go:29` decodes the JSON without schema checks.
- **Fix:** move the schemas to `internal/clitest/schemas/` and add `clitest.AssertJSONSchema(t, name, out)`. Use it in `TestClusterScale/"step up json"`, the all-groups case, and in `scenario_apply_test.go`/`scenario_plan_test.go` on real CLI output.
- **Acceptance:** all three schemas are validated against real command output.

### P-TST-13: Redaction of the panic path is untested (Medium)
- **Evidence:** `cli/run.go:62-63` has 0 hits. Python: `redaction_ac5_uncaught_exception_redacted`.
- **Fix:** add `TestRedaction_CAP008_AC5_PanicRedacted` to `scenario_redaction_test.go`. Inject a panic via a test-only hidden command or a `Deps` hook, with the API key in the panic value.
- **Acceptance:** exit 1, stderr contains `internal error` and not the key (`clitest.AssertNoSecret`).

### P-TST-14: Security helpers below 80% (Medium)
- **Evidence:**
  - `secrets.GitWarning` is 0%. Python: `test_gitcheck.py` (3 MISSING).
  - `settings/keychain.go` Get/Set/Delete are 0%.
  - `redact.HasPrintableSecret` and `isStdoutTTY` are 0%.
  - `capellaapi/client.go:388-393` `maskedError` is 0%, so redaction of transport errors is untested.
- **Fix:**
  - `secrets/files_test.go`: add `TestGitWarning` (temp git repo: not ignored → warn; ignored → silent; outside git → silent; skip if `git` is absent).
  - `settings`: add a `keyring.MockInit()` test.
  - `capellaapi/client_test.go`: add `TestTransportErrorMasksKey` (dial a closed port with the key in the URL).
- **Acceptance:** `secrets`, `settings` and `redact` reach ≥90% coverage, and these packages are added to the cover gate.

### P-TST-15: Integration tests that always skip (Medium)
- **Evidence:** `integration/clone_test.go:16,18`, `credentials_test.go:19,43` and `backups_test.go:26` skip on both targets. Python: `test_cluster_clone.py`, `test_credentials.py` and backup coverage inside `test_lifecycle.py`.
- **Fix:** implement the fake path for clone, including the cross-region create and plan-clean steps, and for backup create, wait, delete and restore. The fake already supports snapshots (`TestSnapshots`). Keep the SDK KV step sandbox-only and record it in `scenario-port.md`.
- **Acceptance:** `make test-integration-fake` runs these tests with no `SKIP` on fake.

### P-TST-16: No binary smoke test of `cmd/cbctl` in default CI (Low)
- **Evidence:** `test_smoke.py` checks that help, schema, presets and guides are bundled, plus exit codes and startup time. In cbctl a binary is built only in `capella_live_test.go:57` (sandbox). `cmd/cbctl` has no tests.
- **Fix:** add `cmd/cbctl/main_test.go` `TestBinarySmoke`. Build with `-ldflags -X version`, then run `--version`, `capella guide topics`, `capella validate` on an example (exit 0), an invalid file (exit 3), and `capella init --non-interactive --dry-run`. Assert startup < 2s.
- **Acceptance:** this runs in `ci.yml` on all three OSes.

### P-TST-17: Help, completion and messages contracts are unported (Low)
- **Evidence:**
  - "Did you mean" for unknown commands and topics isn't asserted.
  - No test checks that every group has `Example`.
  - No "no CAP- ids in user text" test.
  - No "every CLIError has an Action" test.
  - No cluster-name shell completion (`ValidArgsFunction` appears nowhere).
  - Python: `test_help.py` (4 M), `test_main.py`, `test_messages.py` (2 M), `test_completion.py`.
- **Fix:**
  - `internal/cli/help_contract_test.go`: walk `newRootCmd()` and assert a non-empty `Example` on every non-hidden capella command, plus no `CAP-\d+` in `Short`, `Long`, `Example`, guide topics or the schema.
  - Assert `unknown command "scael"… Did you mean` and exit 2.
  - Static check: every `Errorf(` call site has a non-empty action.
  - Decide whether completion is in scope. If it is, add `ValidArgsFunction` for cluster names plus `TestCompletion`; otherwise mark the row N/A in the port table.
- **Acceptance:** the help contract test fails on a missing example or a leaked story id.

### P-TST-18: Coverage gate is too narrow (Low)
- **Evidence:** the `Makefile:62` gate is 85% on `plan`, `resources` and `config/capellafile` only. `cli` is at 74%, and `importer` at 5% own (43% cross).
- **Fix:** gate by tier (see the targets below), using `-coverpkg=./internal/...` for packages exercised through `cli`. Add `importer` unit tests (`exporters.go`, `merge.go`: schedule compaction, minimal vs `--full`, expired CIDR skip, advanced credential commented).
- **Acceptance:** `make check` fails when a tier drops below its target.

## 7. Recommended coverage targets

| Tier | Packages | Target (statement, cross-package) |
|---|---|---|
| Engine / core | `plan`, `engine`, `resources`, `config/capellafile`, `capellaapi`, `presets` | ≥ 90% (currently 84–98%) |
| Security-sensitive | `redact`, `secrets`, `settings` | ≥ 90%, with every error branch tested |
| Product CLI | `cli`, `importer`, `guide`, `logging`, `config` | ≥ 85% for `cli`; ≥ 80% for `importer` |
| Test infrastructure | `capellafake`, `clitest` | ≥ 85% |
| Dev tooling | `ci/*`, `docgen/*` | ≥ 70%; `docgen/coverage` needs at least a smoke test |
| Server legacy (SDK/Docker) | `rest`, `server`, `capella` (SDK) | ≥ 60% unit coverage via `httptest` for `rest` provisioning/wait; the rest via the Docker IT (`integration/server_test.go`) run in nightly |

Suggested order of work: P-TST-01 through 05 (High), then P-TST-06 so tracking is accurate, then the Medium items. Closing P-TST-01..05, 09, 10, 11 and 13 would move about 190 scenario tests from P or M to C, raising the scenario tier to about 85% and the total to about 88%.
