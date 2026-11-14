# Scenario port table (capellactl → cbctl)

Phase 4 §4.2 checklist. Source: `tests/scenario/test_*.py` in the capellactl checkout
(`/Users/michael/IdeaProjects/capellactl`, HEAD recorded in `docs/backlog/SOURCE.md` when present).
`cbdev docs ac-coverage --port-table` checks that every `test_*.py` basename appears below.

Status legend:

| Status | Meaning |
|---|---|
| **ported** | Covered by `internal/cli/scenario_*_test.go` on `clitest.Scenario` |
| **partial** | Behaviour exists under ad-hoc `runResource` / `newFixture` helpers; not yet on `Scenario` |
| **planned** | In scope for Phases 0–3; Scenario file not written yet |
| **N/A** | Intentionally not ported (reason in Notes) |

Helpers (not a `test_*.py`):

| capellactl | Go counterpart | Notes |
|---|---|---|
| `tests/scenario/helpers.py` | `internal/clitest` | `Scenario`, `Assert*`, `ScriptedPrompter`, `GoldenText` |

## Every `tests/scenario/test_*.py` (63 files)

| # | capellactl file | Primary CAP | Go counterpart | Status | Notes |
|---|---|---|---|---|---|
| 1 | `test_allowed_cidrs.py` | CAP-022 | `internal/cli/capella_cidr_test.go` | partial | Imperative CIDR verbs; declarative allowlist on Scenario planned |
| 2 | `test_apply.py` | CAP-006 | `internal/cli/scenario_apply_test.go` | ported | validate/plan/apply; zero-writes re-run; `--yes` / blocked |
| 3 | `test_auth.py` | CAP-003 | `internal/cli/configure_test.go`, `capella_globals_test.go` | partial | Key/profile resolution; Scenario auth cases planned |
| 4 | `test_backup_restore_guide.py` | CAP-061 | `internal/cli/scenario_guides_test.go` | planned | Getting-started runner landed (CAP-033); backup guide walkthrough still open |
| 5 | `test_bucket.py` | CAP-023 | `internal/cli/capella_bucket_test.go` | partial | |
| 6 | `test_bucket_backup.py` | CAP-059 | `internal/cli/capella_bucket_backup_test.go` | partial | |
| 7 | `test_bucket_backup_schedule.py` | CAP-058 | `internal/cli/capella_bucket_backup_test.go` | partial | |
| 8 | `test_bucket_delete.py` | CAP-024 | `internal/cli/capella_bucket_test.go` | partial | |
| 9 | `test_bucket_free_tier.py` | CAP-023/024 | `internal/cli/capella_bucket_test.go` | partial | Free-tier bucket endpoints |
| 10 | `test_ci_mode.py` | CAP-042 | — | planned | Quiet / non-interactive CI mode on Scenario |
| 11 | `test_cluster_backup.py` | CAP-057 | `internal/cli/capella_cluster_backup_test.go` | partial | |
| 12 | `test_cluster_backup_schedule.py` | CAP-056 | `internal/cli/capella_cluster_backup_test.go` | partial | |
| 13 | `test_cluster_cli.py` | CAP-038 | `internal/cli/capella_managed_ops_test.go` | partial | File-managed cluster verbs |
| 14 | `test_cluster_clone.py` | CAP-049 | `internal/cli/capella_cluster_clone_test.go` | partial | |
| 15 | `test_cluster_create.py` | CAP-011 | `internal/cli/capella_managed_ops_test.go`, `capella_cluster_test.go` | partial | |
| 16 | `test_cluster_create_advanced.py` | CAP-053 | `internal/cli/capella_cluster_test.go` | partial | |
| 17 | `test_cluster_create_mds.py` | CAP-051 | `internal/cli/capella_cluster_clone_test.go` (groups) | partial | MDS create ACs still expanding |
| 18 | `test_cluster_delete.py` | CAP-014 | `internal/cli/capella_cluster_lifecycle_test.go` | partial | |
| 19 | `test_cluster_disk.py` | CAP-060 | `internal/cli/capella_cluster_test.go` | partial | Disk types / options |
| 20 | `test_cluster_group.py` | CAP-052 | `internal/cli/capella_cluster_clone_test.go` | partial | |
| 21 | `test_cluster_update.py` | CAP-012 | `internal/cli/capella_cluster_scale_test.go` | partial | |
| 22 | `test_configure.py` | CAP-037 | `internal/cli/configure_test.go` | partial | Profiles/keyring; Windows install bits → see CAP-055 N/A |
| 23 | `test_configure_check.py` | CAP-047 | `internal/cli/configure_test.go` | partial | Org mismatch checks |
| 24 | `test_credential.py` | CAP-027 | `internal/cli/scenario_credentials_test.go` | ported | Declarative credential apply + AssertNoSecret; access-edge cases still partial elsewhere |
| 25 | `test_credential_access_kept.py` | CAP-027/028/036 | `internal/cli/capella_credential_test.go` | partial | Access-preservation edge cases |
| 26 | `test_credential_cli.py` | CAP-046 | `internal/cli/capella_credential_test.go` | partial | |
| 27 | `test_credential_delete.py` | CAP-029 | `internal/cli/capella_credential_test.go` | partial | |
| 28 | `test_custom_clusters_guide.py` | CAP-051/061 | — | planned | Guide runner later |
| 29 | `test_deletion_protection.py` | CAP-013 | `internal/cli/capella_cluster_lifecycle_test.go` | partial | |
| 30 | `test_drift.py` | CAP-031 | `internal/cli/scenario_drift_test.go` | ported | Clean/unmanaged drift + destroy/protection on Scenario |
| 31 | `test_environment.py` | CAP-030 | `scenario_apply_test.go` + `capella_env_test.go` | partial | Apply/destroy lifecycle; Scenario covers create + zero-writes |
| 32 | `test_error_messages.py` | CAP-062 | `internal/cli/errors_test.go`, `capella_env_test.go` | partial | |
| 33 | `test_errors.py` | CAP-003 | `internal/cli/errors_test.go` | partial | Message + ACTION hints |
| 34 | `test_first_environment.py` | CAP-033 | — | planned | `scenario_guides_test.go` (getting-started against fake) |
| 35 | `test_free_tier.py` | CAP-041 | `internal/cli/capella_cluster_test.go` | partial | |
| 36 | `test_help_hints.py` | CAP-034 | — | planned | Help topic pointers |
| 37 | `test_import.py` | CAP-036 | `internal/cli/capella_env_test.go` (`TestSync*`, init import) | partial | Full import ACs on Scenario planned |
| 38 | `test_init.py` | CAP-035 | `internal/cli/scenario_init_test.go`, `capella_env_test.go` | partial | Non-interactive on Scenario; interactive still ad-hoc |
| 39 | `test_messages.py` | CAP-063 | `internal/cli/errors_test.go` | partial | `[error]`/`[warning]`/`[ACTION]` form |
| 40 | `test_multi_project.py` | CAP-040 | — | planned | Multi-project file form |
| 41 | `test_plan.py` | CAP-005 | `internal/cli/scenario_plan_test.go`, `scenario_apply_test.go` | ported | Read-only plan + detailed exitcode; richer symbols planned |
| 42 | `test_power.py` | CAP-019 | `internal/cli/capella_cluster_lifecycle_test.go` | partial | Integration power AC → `internal/integration` |
| 43 | `test_project.py` | CAP-009 | `internal/cli/scenario_project_test.go`, `capella_project_test.go` | partial | List/get on Scenario; declarative ensure still partial |
| 44 | `test_project_create.py` | CAP-039 | `internal/cli/capella_project_test.go` | partial | |
| 45 | `test_project_delete.py` | CAP-010 | `internal/cli/capella_project_test.go` | partial | |
| 46 | `test_readme_walkthrough.py` | CAP-038/054 | — | planned | README / sync walkthrough against fake |
| 47 | `test_redaction.py` | CAP-008 | `internal/cli/scenario_redaction_test.go` | ported | `AssertNoSecret` on validate/plan/apply |
| 48 | `test_resource_verbs.py` | CAP-009 | `internal/cli/capella_resource_test.go`, `capella_project_test.go` | partial | Verb naming / wrong-kind hints |
| 49 | `test_rotation.py` | CAP-028 | `internal/cli/capella_credential_test.go` | partial | |
| 50 | `test_scale_in.py` | CAP-016 | `internal/cli/capella_cluster_scale_test.go` | partial | |
| 51 | `test_scale_out.py` | CAP-015 | `internal/cli/capella_cluster_scale_test.go` | partial | |
| 52 | `test_schedule.py` | CAP-020 | `internal/cli/capella_cluster_schedule_test.go` | partial | |
| 53 | `test_schedule_cli.py` | CAP-020 | `internal/cli/capella_cluster_schedule_test.go` | partial | |
| 54 | `test_schedule_pause.py` | CAP-021 | `internal/cli/capella_cluster_schedule_test.go` | partial | |
| 55 | `test_scope_collection_delete.py` | CAP-026 | `internal/cli/capella_scope_test.go`, `capella_collection_test.go` | partial | |
| 56 | `test_scopes_collections.py` | CAP-025 | `internal/cli/capella_scope_test.go`, `capella_collection_test.go` | partial | |
| 57 | `test_step_scaling.py` | CAP-018 | `internal/cli/capella_cluster_scale_test.go` | partial | |
| 58 | `test_sync.py` | CAP-038 | `internal/cli/scenario_sync_test.go` | ported | stdout/dry-run/write/org-mismatch on Scenario |
| 59 | `test_sync_all.py` | CAP-054 | — | planned | `sync` for every org project |
| 60 | `test_track_by_id.py` | CAP-048 | — | planned | Pin-by-id rename behaviour |
| 61 | `test_validate.py` | CAP-002 | `scenario_apply_test.go`, `capella_env_test.go` | partial | Offline validate on Scenario; schema ACs still ad-hoc |
| 62 | `test_vertical_scaling.py` | CAP-017 | `internal/cli/capella_cluster_scale_test.go` | partial | |
| 63 | `test_waiter_cli.py` | CAP-007 | — | planned | `--timeout` / waiter CLI flags on Scenario |

**Coverage count:** 63/63 files accounted for · **ported** 7 (`test_apply`, `test_plan`, `test_redaction`, `test_drift`, `test_sync`, `test_credential`, plus init/project Scenario rows) · **partial** ~46 · **planned** ~10 · **N/A** 0 among `test_*.py`. Getting-started guide runner: `scenario_guides_test.go` (CAP-033).

## Related / descoped (not `tests/scenario/test_*.py`)

| Source | Go counterpart | Notes |
|---|---|---|
| `tests/integration/test_lifecycle.py` | `internal/integration/` (§5 / CAP-032) | Live/fake harness; other agents |
| `tests/integration/harness.py` | `internal/integration/harness.go` | Other agents |
| `tests/unit/scripts/test_scan_log_secrets.py` | `internal/ci/secretscan` | Ported |
| `tests/unit/scripts/test_release_gate.py` | `internal/ci/releasegate` | Ported |
| CAP-043 packaging / wheel / sdist | N/A | Descoped: GoReleaser replaces Python packaging |
| CAP-045 standalone executables | N/A | Descoped: GoReleaser native binaries |
| CAP-050 repository rulesets | N/A | Out of scope for cbctl (GitHub rulesets) |
| CAP-055 Windows PowerShell install | N/A | Python/Windows-specific install docs; not ported |
| CAP-044 license/notices | N/A (docs/CI) | NOTICE / license gate — not a scenario test |
