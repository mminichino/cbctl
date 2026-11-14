# Scenario port table (capellactl → cbctl)

Checklist for Phase 4 §4.2. Rows are filled as scenarios are ported. `cbdev docs ac-coverage --port-table` checks completeness when a capellactl checkout is present.

| capellactl file | Go counterpart | Notes |
|---|---|---|
| `tests/scenario/test_init.py` | `internal/cli/capella_env_test.go` (`TestInit*`) | Partial; expand CAP-035 ACs |
| `tests/scenario/helpers.py` | `internal/clitest` | Scenario / Assert* helpers |
| `tests/integration/test_lifecycle.py` | `internal/integration/capella_live_test.go` (§13.4) | Live declarative flow; fake harness TBD |
| `tests/integration/harness.py` | `internal/integration/harness.go` | Not landed yet |
| `tests/unit/scripts/test_scan_log_secrets.py` | `internal/ci/secretscan` | Ported |
| `tests/unit/scripts/test_release_gate.py` | `internal/ci/releasegate` | Ported |

Windows / wheel / rulesets / Python-only scenarios → N/A (see Phase 4 §4.2).
