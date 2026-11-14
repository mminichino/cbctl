# Backlog source

| Field | Value |
|---|---|
| Upstream | https://github.com/couchbaselabs/capellactl |
| Commit | `6389d5a318f2bd3cd8d4cd9590877319e992e7fe` |
| Describe | `6389d5a` |
| Port date | 2026-11-14 |
| Rewrite rules | v1 (`internal/docgen/port.RulesVersion`) |

Ported with `cbdev docs port` (MVP rules R1, R3, R5, R6, R13). Release 1.5.0 stories and
`release-1.0.0/00-architecture.md` come from that commit; API JSON under `docs/backlog/api/`
is a byte copy of `docs/api/` from the same SHA.
