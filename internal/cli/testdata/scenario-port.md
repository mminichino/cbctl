# Scenario port table (capellactl → cbctl)

Checklist for Phase 4 §4.2. Rows are filled as scenarios are ported. Full inventory of
all ~63 `tests/scenario/test_*.py` files comes later; `cbdev docs ac-coverage --port-table`
checks completeness when a capellactl checkout is present.

| capellactl file | Go counterpart | Notes |
|---|---|---|
| `tests/scenario/helpers.py` | `internal/clitest` | `Scenario`, `Assert*`, `ScriptedPrompter`, `GoldenText` |
| `tests/scenario/test_init.py` | `internal/cli/capella_env_test.go` (`TestInit*`) | Partial; expand CAP-035 ACs onto `clitest.Scenario` |
| `tests/scenario/test_project.py` | `internal/cli/capella_project_test.go` (partial) | Migrate onto `clitest.Scenario` |
| `tests/scenario/test_apply.py` | `scenario_apply_test.go` (planned) | Declarative apply / zero-writes re-run |
| `tests/scenario/test_plan.py` | `scenario_plan_test.go` (planned) | Phase 3 plan |
| `tests/scenario/test_redaction.py` | `scenario_redaction_test.go` (planned) | Uses `AssertNoSecret` |
| `tests/integration/test_lifecycle.py` | `internal/integration/capella_live_test.go` (§13.4) | Live declarative flow; fake harness TBD |
| `tests/integration/harness.py` | `internal/integration/harness.go` | Not landed yet |
| `tests/unit/scripts/test_scan_log_secrets.py` | `internal/ci/secretscan` | Ported |
| `tests/unit/scripts/test_release_gate.py` | `internal/ci/releasegate` | Ported |
| CAP-043 packaging / wheel scenarios | N/A | Descoped: GoReleaser replaces wheel/sdist |
| CAP-045 standalone executables | N/A | Descoped: GoReleaser native binaries |
| CAP-050 repository rulesets | N/A | Out of scope for cbctl |
| CAP-055 Windows PowerShell install | N/A | Python-specific; not ported |
