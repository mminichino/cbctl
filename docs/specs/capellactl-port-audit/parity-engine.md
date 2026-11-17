# Parity audit: capellactl -> cbctl (behavior, config, engine)

Scope: declarative file, engine semantics, API v4 coverage, feature areas, cbctl error-handling robustness.
Baseline: capellactl HEAD `6389d5a` (Python) vs cbctl `mm--capellactl-port` @ `b05642b` (Go). No critical gaps: every capellactl capability has a cbctl path. Read-only; no live Capella calls.
Method: side-by-side source read, schema diff, offline `cbctl capella validate` of capellactl example files, `go build ./cmd/cbctl` + `--help` inspection.

## 0. Re-verification before the merge to main (2026-11-17, release 0.6.0)

The tables below are the snapshot at b05642b, kept as the record. Every row that was not MATCH was
checked again on the branch tip. **No MISSING row remains.**

| Row (snapshot status) | Now | Evidence |
|---|---|---|
| C3 `credentialsOutput` default (DIFFERENT) | `./.capellactl/credentials` used while `./.cbctl/credentials` is absent; `doctor` reports it; git-tracked dirs refused | `TestResolveCredentialsDir`, `TestMigrationEndToEndDoctorClean` |
| C28 discovery env (DIFFERENT) | `CAPELLACTL_CONFIG` honoured with a warning | `TestCapellactlConfigEnvVar` |
| C31 generated reference (PARTIAL, 13 rows) | MATCH: full field tree, regenerated and checked | `TestDocs_CAP033_AC3_ReferenceUpToDate`, `make docs-check` |
| S6 / S7 / S8 profile env, home, keychain service (DIFFERENT) | legacy names, `profiles.yaml` and keychain service `capellactl` read as fallbacks with a warning; `configure migrate` copies them | `TestLegacyProfileAndKeychainFallback`, `TestConfigureMigrateCommand` |
| S12 credential files (PARTIAL: errors swallowed) | MATCH: chmod failures warn once; a failed restore after a rejected rotation is reported with both errors | `secrets/files.go` (`chmodOrWarn`, `RestoreRotatedCredential`); `TestRotation_CAP028_*` |
| E22 sync `apiVersion` (DIFFERENT) | MATCH: `sync` keeps `capellactl/v1`; `--api-version` chooses | `TestSyncKeepsCapellactlAPIVersion` |
| E29 response size (PARTIAL) | MATCH: bounded read, typed error | `TestResponseTooLarge` |
| E32 hidden `import` (DIFFERENT) | `capella import` with sync flags exits 3 naming `sync`; documented | `TestImportSyncHint` |
| F3, F5–F8 operator docs (PARTIAL / MISSING) | MATCH: `docs/capella/{backup-restore,custom-clusters,terraform,migrating-from-scale-script,runbook}.md` | `make docs-check` |
| F9 migration guide (MISSING) | MATCH: `docs/capella/migrating-from-capellactl.md`, `guide migrate`, `capella doctor` | `TestMigrationEndToEndDoctorClean` |
| R1 legacy pre-port commands (DIFFERENT, unsafe) | hidden, deprecated, delegate to the v4 commands (`cluster destroy` → `cluster delete`, protection and `-y` honoured) | `TestLegacyDestroyDelegatesToDelete`, `TestDeprecateLegacyWarnsOnStderrOnly` |
| R2 `panic` (PARTIAL) | INTENTIONALLY kept: embedded-data parse and command-tree wiring only (programmer errors at startup, allowed by the project rules) | `presets.go`, `capella.go`, `capella_legacy.go`, `capella_prompts.go` |

Found while raising Server coverage and fixed in the same release: IPv6 literals in `--host` /
node specs (`ParseHostPort`, URL bracketing, `clusterInit` hostname); a node spec's non-default
management port dropped on multi-node `addNode`; `--ext-api` ignored when setting alternate
addresses; a query service that never became ready after provisioner `create` was hidden (now a
warning). Tests: `internal/rest/hostport_test.go`, `TestWarnIfQueryNotReady`,
`TestCreateClusterMultiNodeRebalanceFailure`.

## 1. Executive summary

- The declarative engine is a faithful port: plan/apply/drift/sync/destroy/prune, kind ordering, blocked actions, id pinning + `.bak`, 409 adoption, 412 retry, ambiguous-write recovery, full-jitter backoff, Retry-After, and the waiter poll curve all match.
- The schema is a superset: cbctl accepts `apiVersion: capellactl/v1` and `cbctl/v1`; existing `capella.yaml` files validate unchanged. No converter is needed or provided.
- All 57 Capella Management API v4 endpoints that capellactl calls exist in `internal/capellaapi/inventory.go`, with the same methods, paths, pagination and error decoding.
- The gaps are around the file, not in it: env var names, profile/keyring/home locations and the default credentials directory were renamed with no fallback or migration. Five operator docs were not ported. The generated config reference drops every field under `clusters`. A legacy pre-port `capella cluster create|destroy|exists|test` path bypasses the engine's safety (no `-y`, no Ctrl-C, `os.Exit` in `RunE`).

## 2. Counts

Status counts are over the mapping rows in section 3. Severity counts are over the findings in section 4.

| Status | Count |
|---|---|
| MATCH | 79 |
| PARTIAL | 5 |
| DIFFERENT | 8 |
| MISSING | 5 |
| INTENTIONALLY-DROPPED (justified) | 3 |
| INTENTIONALLY-DROPPED (not justified: silent, no shim) | 3 |
| **Total rows** | **103** (incl. 11 endpoint-family rows covering 57 endpoints) |

| Severity | Count |
|---|---|
| critical | 0 |
| high | 5 (P-ENG-01, 02, 03, 08, 09) |
| medium | 5 (P-ENG-04, 05, 06, 07, 11) |
| low | 7 (P-ENG-10, 12, 13, 14, 15, 16, 17) |
| **Total findings** | **17** |

## 3. Mapping table

### 3.1 Config file (capella.yaml)

| # | Item | capellactl | cbctl | Status | Finding |
|---|---|---|---|---|---|
| C1 | `apiVersion` | `capellactl/v1` only (`capellactl/config/schema.json`) | `cbctl/v1` or `capellactl/v1` (`internal/config/capellafile/schema.json`) | MATCH (superset) | — |
| C2 | `organizationId` (`${CAPELLA_ORG_ID}` or profile) | yes | yes | MATCH | — |
| C3 | `credentialsOutput` default | `./.capellactl/credentials` | `./.cbctl/credentials` (`schema.json:28-32`) | DIFFERENT | P-ENG-04 |
| C4 | Single-project form (`project:` + `clusters:`) | yes | yes | MATCH | — |
| C5 | Multi-project form (`projects: [...]`) | yes | yes | MATCH | — |
| C6 | `id` pinning on project/cluster/credential | yes | yes | MATCH | — |
| C7 | Cluster: provider/region/cidr/supportPlan/timezone/serverVersion/description | yes | yes | MATCH | — |
| C8 | Cluster `size` presets (dev/small/medium/large/custom) | `capellactl/resources/presets.yaml` | `internal/presets/presets.yaml` (embedded) | MATCH | — |
| C9 | `serviceGroups[]` (services, nodes, cpu, ram, disk type/size/iops/autoExpansion) | yes | yes | MATCH | — |
| C10 | Disk rules per provider (AWS iops required, etc.) | `crossref.py` | `crossref.go` + testdata `disk_iops_missing_aws.yaml` | MATCH | — |
| C11 | Single-node rules (`data,index,query,search` only) | `crossref.py:24,414` | cross-ref + `single_node_rules.yaml` | MATCH | — |
| C12 | Duplicate service-group detection | yes | yes (`dup_service_groups.yaml`) | MATCH | — |
| C13 | Free tier (`freeTier: true`) field restrictions | yes | yes | MATCH | — |
| C14 | `deletionProtection` | yes | yes | MATCH | — |
| C15 | `schedule` (on/off: timezone, per-day windows) | yes | yes | MATCH | — |
| C16 | `backupSchedule` (cluster) | yes | yes | MATCH | — |
| C17 | `allowedCidrs[]` (cidr, comment, expiresAt) | yes | yes | MATCH | — |
| C18 | `buckets[]` (type, storageBackend, vbuckets, memory, replicas, durability, ttl, flush, conflictResolution, eviction, priority) | yes | yes | MATCH | — |
| C19 | Bucket `backupSchedule` | yes | yes | MATCH | — |
| C20 | `scopes[]` / `collections[]` (`maxTTL`) | yes | yes | MATCH | — |
| C21 | `credentials[]` (access: privileges + resources; password ref) | yes | yes | MATCH | — |
| C22 | Password literal rejected; only `${VAR}` / `${file:PATH}` | `loader.py` | `secrets.go` | MATCH | — |
| C23 | `${file:PATH}` resolved relative to the config file dir | yes | yes | MATCH | — |
| C24 | Unset `${VAR}` -> exit 3 | yes | yes | MATCH | — |
| C25 | Draft 2020-12 schema validation, `additionalProperties: false` | yes | yes | MATCH | — |
| C26 | Defaults applied after validation | yes | yes | MATCH | — |
| C27 | Multi-document YAML / includes | not supported | not supported | MATCH (neither) | — |
| C28 | Discovery `--config` -> env -> profile `configFile` -> `./capella.yaml` -> `./capella.yml`; both present = error | `settings.py:149-160` (`CAPELLACTL_CONFIG`) | `discover.go:14-22,47-84` (`CBCTL_CONFIG` only) | DIFFERENT | P-ENG-01 |
| C29 | Comment-preserving in-place edit + `.bak` | ruamel.yaml | `editor.go` (yaml.Node, atomic tmp + rename) | MATCH | — |
| C30 | Converter from capellactl format | n/a | none needed (C1) | MATCH | P-ENG-16 |
| C31 | Generated field reference (`docs/reference.md`) | full tree (103 field rows) | `docs/capella/reference.md` has 13 rows and stops at `projects[].clusters` | PARTIAL | P-ENG-03 |

### 3.2 Settings, auth, local state

| # | Item | capellactl | cbctl | Status | Finding |
|---|---|---|---|---|---|
| S1 | `CAPELLA_API_KEY` | yes | yes (`settings.go:30`) | MATCH | — |
| S2 | `CAPELLA_TOKEN` alias | — | yes (`settings.go:31`) | MATCH (superset) | — |
| S3 | `BEARER_TOKEN` (deprecated, with warning) | `settings.py:185-187` | ignored silently | INTENTIONALLY-DROPPED (not justified: no warning) | P-ENG-01 |
| S4 | `ORG_ID` (deprecated) | `settings.py:196-197` | ignored silently | INTENTIONALLY-DROPPED (not justified) | P-ENG-01 |
| S5 | `API_BASE_URL` (deprecated) | `settings.py:210-212` | ignored silently | INTENTIONALLY-DROPPED (not justified) | P-ENG-01 |
| S6 | Profile env `CAPELLACTL_PROFILE` | `settings.py:174` | `CBCTL_PROFILE` only (`settings.go:34`) | DIFFERENT | P-ENG-01 |
| S7 | Home dir `CAPELLACTL_HOME` / `~/.config/capellactl/profiles.yaml` | `settings.py:27-37` | `CBCTL_HOME` / `~/.config/cbctl` (`profiles.go:30-50`) | DIFFERENT | P-ENG-02 |
| S8 | Keyring service name | `"capellactl"` (`settings.py:22`) | `"cbctl"` (`keychain.go:13`) | DIFFERENT | P-ENG-02 |
| S9 | `.env` loading | yes | yes (`root.go:69`, `dotenv.go`) | MATCH | — |
| S10 | Org auto-discovery when no org set | yes | yes (`capella_globals.go:246-251`) | MATCH | — |
| S11 | Secret registration + masking in logs/errors | `output/secrets_out.py` + redactor | `internal/redact`, `client.go:149,260,342-375` | MATCH | — |
| S12 | Credential files: 0700 dir, 0600 file, rotation `.bak`, restore on failed send | yes | `secrets/files.go:55-130` | PARTIAL (errors swallowed) | P-ENG-11, P-ENG-12 |
| S13 | Git-ignored check for secrets dir | yes | `secrets/files.go:133-` | MATCH | P-ENG-04 |

### 3.3 Engine semantics

| # | Item | capellactl | cbctl | Status | Finding |
|---|---|---|---|---|---|
| E1 | Load pipeline (parse -> apiVersion -> secrets -> schema -> defaults -> crossref) | `config/loader.py` | `capellafile/load.go` | MATCH | — |
| E2 | Kind order project -> cluster -> deletion-protection -> schedule -> backup-schedule -> allowed-cidr -> bucket -> bucket-backup-schedule -> scope -> collection -> credential | `engine/planner.py` | `plan/planner.go:269-290` (`kindRank`, `byKind`) | MATCH | — |
| E3 | Destroy in reverse order | yes | yes | MATCH | — |
| E4 | `--target KIND/NAME` (self + descendants) | yes | `planner.go:33-103` | MATCH | — |
| E5 | Children of a blocked/created ancestor blocked or deferred | yes | `planner.go:252-268` (`blockByAncestor`) | MATCH | — |
| E6 | Multiple observed matches for one desired -> blocked | yes | `planner.go:236-250` | MATCH | — |
| E7 | `--prune` of unmanaged resources (never projects/clusters without flag set) | yes | yes | MATCH | — |
| E8 | Blocked changes (`!`) -> exit 4; `--detailed-exitcode` 2 = changes | yes | yes | MATCH | — |
| E9 | Service-removal / service-group-removal gates | yes | yes (`--allow-service-*`) | MATCH | — |
| E10 | Scale-in capacity check (bucket memory, replicas) | yes | yes (`--skip-capacity-check`) | MATCH | — |
| E11 | Password rotation only on `--rotate-passwords`; secret overwrite gate | yes | yes (`--force-overwrite-secret`) | MATCH | — |
| E12 | Powered-off clusters: `--power-on` | yes | yes | MATCH | — |
| E13 | Sequential execute, stop on first error, report remaining | `engine/executor.py` | `plan/executor.go:164-260` | MATCH | — |
| E14 | 409 on create -> adopt existing | yes | `executor.go:86-98` | MATCH | — |
| E15 | 412 precondition -> re-read + retry once, else `ConcurrentChange` | yes | `executor.go:98-160` | MATCH | — |
| E16 | Ambiguous POST (request written, no response) -> re-observe before retry | `api/client.py` `AmbiguousWriteError` | `executor.go:106`, `client.go:356-361` (`httptrace` `WroteRequest`) | MATCH | — |
| E17 | Ctrl-C -> stop, report, exit 130; operation continues in Capella | yes | `root.go:80`, `executor.go:84`, `ApplyResult.Interrupted` | MATCH | — |
| E18 | ID pinning after apply, previous file in `.bak` | yes | `editor.go:160-215` | MATCH | — |
| E19 | Second apply of an unchanged file makes no writes | yes | yes | MATCH | — |
| E20 | Drift (read-only), `--detailed-exitcode` | yes | yes | MATCH | — |
| E21 | Sync / import from Capella (`--full`, `--dry-run`, `--stdout`, merge) | `capellactl/importer/` | `internal/importer/{importer,exporters,render,merge}.go` | MATCH | — |
| E22 | Sync writes `apiVersion` | `capellactl/v1` | `cbctl/v1` on full render | DIFFERENT (harmless) | P-ENG-15 |
| E23 | Waiter poll curve 10s x1.5 -> 60s cap; dots every 30s | `engine/waiter.py` | `engine/waiter.go:127-300` | MATCH | — |
| E24 | Healthy-flicker grace for update/scale | partial | `waiter.go` (`OpUpdate` grace) | MATCH (improved) | — |
| E25 | Global `--timeout` (default 1h) -> exit 5 | yes | yes | MATCH | — |
| E26 | HTTP retry: 429/500/502/503/504 + transport drops; full jitter 1,2,4,8,16s; Retry-After (seconds or HTTP-date) | `api/client.py` | `capellaapi/retry.go:26-110` | MATCH | — |
| E27 | POST retried only on 429 or before the request is written | yes | `retry.go:37-45` (`idempotent`) | MATCH | — |
| E28 | Transport timeouts (dial 10s, TLS 10s, response header 60s) | requests timeouts | `client.go:156-163` | MATCH | — |
| E29 | Response body size bound | n/a | `io.ReadAll` unbounded (`client.go:359`) | PARTIAL | P-ENG-13 |
| E30 | Error envelope decode (`code`, `message`, `hint`, `httpStatusCode`) -> two-line `[error]` / `→ action` | `cli/main.py` | `capellaapi/errors.go:110-130`, `cli/errors.go` | MATCH | — |
| E31 | Exit codes 0/1/2/3/4/5/130 | yes | yes (`cli/errors.go`) | MATCH | — |
| E32 | Hidden `import` alias for `sync` | `cli/env.py:644` | `capella import` is now JSONL data import | DIFFERENT | P-ENG-07 |

### 3.4 API v4 endpoints (capellactl `api/endpoints.py` vs cbctl `capellaapi/inventory.go`)

| # | Resource family | Endpoints | Status |
|---|---|---|---|
| A1 | organizations (list/get) | 2 | MATCH |
| A2 | projects (CRUD) | 5 | MATCH |
| A3 | clusters (CRUD, power `activationState`, deletion protection) | 7 | MATCH |
| A4 | freeTier clusters (+ on/off) | 5 | MATCH |
| A5 | onOffSchedule (get/put/post/delete) | 4 | MATCH |
| A6 | allowedcidrs (list/create/delete) | 3 | MATCH |
| A7 | buckets + freeTier buckets (CRUD) | 10 | MATCH |
| A8 | scopes / collections (list/create/update/delete) | 7 | MATCH |
| A9 | users = database credentials (CRUD) | 5 | MATCH |
| A10 | cloudsnapshotbackups (cluster backups, schedule, restore, regions) | 4 | MATCH |
| A11 | bucket backups, cycles, schedule, restores, exports | 5 | MATCH |
| | **Total** | **57** | **57/57 MATCH** |

Pagination (`page`/`perPage` with `cursor.pages`) and ETag / `If-Match` handling match. Request and response field usage was spot-checked for cluster, bucket, credential and schedule bodies; no field that capellactl reads or sends is missing.

### 3.5 Feature areas

| # | Area | capellactl | cbctl | Status | Finding |
|---|---|---|---|---|---|
| F1 | Importer / sync | `capellactl/importer/` | `internal/importer/` | MATCH | — |
| F2 | Backup/restore commands | `docs/backup-restore.md` | commands present | MATCH (code) | — |
| F3 | Backup/restore doc | `docs/backup-restore.md` | guide topic `backup.md` only | PARTIAL | P-ENG-05 |
| F4 | Custom / multi-group clusters (code) | yes | yes | MATCH | — |
| F5 | `docs/custom-clusters.md` | yes | not ported | MISSING | P-ENG-05 |
| F6 | `docs/terraform.md` (interop guidance) | doc only, no code | not ported | MISSING | P-ENG-05 |
| F7 | `docs/migrating-from-scale-script.md` | doc | not ported | MISSING | P-ENG-05 |
| F8 | `docs/runbook.md` (day-2 + command reference) | doc | not ported | MISSING | P-ENG-05 |
| F9 | capellactl -> cbctl migration guide | n/a | none | MISSING | P-ENG-06 |
| F10 | Built-in help topics (11) | `capellactl/help/*.md` | `internal/guide/topics/*.md` (same 11) | MATCH | — |
| F11 | On/off scheduling (hibernation) | yes | yes | MATCH | — |
| F12 | App Services, audit logs, private endpoints, network peering, org users/API keys, alerts | not implemented ("Not started", release 1.5.0/2.0.0 backlog) | not implemented (backlog ported) | INTENTIONALLY-DROPPED (justified: never shipped) | — |
| F13 | Bucket storage migration (CAP-072) | backlog only | backlog only | INTENTIONALLY-DROPPED (justified) | — |
| F14 | CMEK / private DNS on clone | not copied | not copied | INTENTIONALLY-DROPPED (justified, documented) | — |

### 3.6 cbctl-only robustness items

| # | Item | Evidence | Status | Finding |
|---|---|---|---|---|
| R1 | Legacy pre-port `capella cluster create/destroy/exists/test`, `capella bucket create/exists` (old `internal/capella` backend) | `cli/capella.go:44-303`, `capella/orchestrate.go:68-200` | DIFFERENT (unsafe path next to v4 commands) | P-ENG-08, P-ENG-09 |
| R2 | `panic` in library code | `presets.go:149,165`, `capella_managed_ops.go:66`, `secrets/password.go:90` | PARTIAL (startup/programmer errors only) | P-ENG-14 |

## 4. Findings

### P-ENG-01 — Legacy env vars silently ignored (`CAPELLACTL_CONFIG`, `CAPELLACTL_PROFILE`, `BEARER_TOKEN`, `ORG_ID`, `API_BASE_URL`) — **high**

- capellactl: `capellactl/config/settings.py:149-153` (`CAPELLACTL_CONFIG`), `:174` (`CAPELLACTL_PROFILE`), `:185-187` (`BEARER_TOKEN`, deprecated with a warning), `:196-197` (`ORG_ID`), `:210-212` (`API_BASE_URL`).
- cbctl: `internal/settings/settings.go:28-35` reads only `CAPELLA_API_KEY`, `CAPELLA_TOKEN`, `CAPELLA_ORG_ID`, `CAPELLA_API_BASE_URL`, `CBCTL_PROFILE`, `CBCTL_CONFIG`. `internal/config/capellafile/discover.go:14,22` reads only `CBCTL_CONFIG`.
- Impact: CI jobs that set `CAPELLACTL_CONFIG=envs/prod.yaml` fall back to `./capella.yaml` with no warning. In a repo with several files, `plan` or `apply` then runs against the **wrong file**, and `apply --prune` or `destroy` could act on the wrong environment. `CAPELLACTL_PROFILE=prod` falls back to `default`, which can point at another org. `BEARER_TOKEN` users get "API key not set", which is safe but confusing.
- Fix:
  - Add a deprecation shim in `internal/settings/settings.go`:
    ```go
    // legacyEnv maps capellactl variable names to cbctl names.
    var legacyEnv = map[string]string{
        "CAPELLACTL_CONFIG":  envConfig,
        "CAPELLACTL_PROFILE": envProfile,
        "CAPELLACTL_HOME":    "CBCTL_HOME",
        "BEARER_TOKEN":       envAPIKey,
        "ORG_ID":             envOrgID,
        "API_BASE_URL":       envAPIBaseURL,
    }
    // lookupWithLegacy returns the cbctl value, else the legacy value plus a Notice to rename it.
    func lookupWithLegacy(env func(string) string, name string) (val, source string, notice *Notice)
    ```
  - Use it in `Resolve` and in `capellafile.Discover`. Put each `Notice` into `Resolved.Warnings`, printed as `[warning] - CAPELLACTL_CONFIG is deprecated` / `  → Set CBCTL_CONFIG instead`.
  - Precedence: the new name always wins. If both are set with different values, warn and use the new one.
  - Exit codes: unchanged (still 3 when the key is missing).
- AC:
  1. With only `CAPELLACTL_CONFIG=x.yaml` set, `cbctl capella plan` loads `x.yaml` and prints one deprecation warning on stderr.
  2. With both `CBCTL_CONFIG=a` and `CAPELLACTL_CONFIG=b` set, cbctl uses `a` and warns about the conflict.
  3. A table-driven test in `settings_test.go` covers all six names.
  4. `-o json` keeps stdout valid JSON (warnings go to stderr).

### P-ENG-02 — Profiles and keyring are not found after migration — **high**

- capellactl: `settings.py:22` (`KEYRING_SERVICE = "capellactl"`), `:27-37` (home `CAPELLACTL_HOME`, `%APPDATA%\capellactl`, `$XDG_CONFIG_HOME/capellactl`, `~/.config/capellactl/profiles.yaml`).
- cbctl: `internal/settings/keychain.go:13` (`"cbctl"`), `internal/settings/profiles.go:30-50` (`~/.config/cbctl`).
- Impact: every `capellactl configure` user must re-enter API keys. Without the shim from P-ENG-01, `CAPELLACTL_HOME` is ignored too. Commands fail with exit 3 "API key not set", which is safe but is a functionality break on day 1. Profile `configFile` entries are lost as well, so discovery changes (see P-ENG-01).
- Fix:
  - Add `cbctl configure migrate [--from-dir DIR] [--dry-run] [--remove-legacy]` in `internal/cli/configure_migrate.go`, backed by:
    ```go
    // internal/settings/migrate.go
    type MigrationReport struct{ Profiles []string; Keys []string; Skipped map[string]string }
    func MigrateLegacy(ctx context.Context, env func(string) string, kr Keyring, opts MigrateOptions) (*MigrationReport, error)
    ```
  - It reads `<legacyHome>/profiles.yaml` and copies the entries the profile schema allows. It copies keyring secrets from service `capellactl` to `cbctl` for each profile. It never prints a key, and `redact.Register` runs on every value it reads.
  - Also add a lazy fallback: when the `cbctl` keyring has no entry for a profile, try `capellactl` and warn once.
  - Edge cases: a profile exists in both stores with different values, so skip it and report it (exit 0 with warnings, or exit 4 without `--force`). The keyring may be locked or unavailable: return exit 1 with an action line. A malformed legacy YAML file exits 3. `--dry-run` writes nothing.
- AC:
  1. With a legacy `profiles.yaml` and keyring entry fixture, `migrate` creates an equal cbctl profile, and `configure show` shows the masked key.
  2. A second run is a no-op (idempotent).
  3. The lazy fallback is covered by a test with a fake keyring.

### P-ENG-03 — Generated config reference omits every field under `clusters` — **high** (docs; operators lose the field reference)

- capellactl: `docs/reference.md` has 103 field rows covering cluster, serviceGroups, buckets, scopes, collections, credentials, schedules and CIDRs.
- cbctl: `docs/capella/reference.md` has 13 rows. The root cause is `internal/docgen/reference/reference.go:113-127`: `isSection` checks `items["type"] == "object"` but does not dereference `items: {"$ref": "#/$defs/cluster"}` (`schema.json`, `projects.items.properties.clusters`). So `projects.clusters` and top-level `clusters` are treated as leaves ("list of values") and the walk stops.
- Impact: the full field reference users relied on is gone. `cbctl capella guide config FIELD` still works offline, which partly mitigates this.
- Fix: in `reference.go`, resolve `$ref` before classifying: `items = deref(items, root)` inside `isSection`, and do the same where rows are emitted. Then add a golden test asserting that the row count is at least capellactl's 103 and that it contains `projects.clusters.serviceGroups.services`, `projects.clusters.buckets.scopes.collections.maxTTL` and `projects.clusters.credentials.access`. Make the docs-check CI step (`Write` in check mode, `reference.go:336`) fail on drift.
- AC: regenerated `docs/capella/reference.md` documents every leaf in `schema.json`. A unit test walks the schema and asserts that every leaf path appears in the output.

### P-ENG-04 — Default `credentialsOutput` moved from `./.capellactl/credentials` to `./.cbctl/credentials` — **medium** (secret-leak risk)

- capellactl: default in `capellactl/config/schema.json` (`credentialsOutput`).
- cbctl: `internal/config/capellafile/schema.json:28-32`; writer in `internal/secrets/files.go:55-100`.
- Impact:
  - Existing repos gitignore `.capellactl/`, not `.cbctl/`. The first `credential create` or `apply --rotate-passwords` writes a plaintext password file into a directory that is **not ignored**. `GitWarning` (`files.go:133-`) only warns.
  - Rotations no longer find the old file, so no `.bak` is kept and the previous password is lost locally. Files in `.capellactl/credentials` become orphaned.
- Fix:
  - In `secrets.Preflight` / `WriteRotatedCredential`: when the configured dir is the default and `./.capellactl/credentials` exists, keep using the legacy dir and warn `[warning] - using legacy ./.capellactl/credentials` / `  → set credentialsOutput or move the directory`. Alternatively, refuse with exit 4 until the user sets `credentialsOutput` (choose one and document it).
  - `cbctl capella init` and `sync` should append `.cbctl/` to `.gitignore` when the file is in a git work tree. Make `GitWarning` block (exit 4) when the dir is tracked or not ignored, unless `--force-overwrite-secret` is passed.
- AC:
  1. In a fixture repo with `.capellactl/credentials/c1/app.json` and no `credentialsOutput`, `credential update --rotate-password` writes into the legacy dir and creates a `.bak` there.
  2. In a git repo where `.cbctl/` is not ignored, generating a credential exits 4 with an action line and writes no file.

### P-ENG-05 — Five operator docs not ported (runbook, custom clusters, terraform, scale-script migration, backup/restore) — **medium**

- capellactl: `docs/runbook.md` (day-2 tasks lines 21-460: scale out/in/up, groups, power, schedules, buckets, credentials, rotation, CIDRs, disk type, cluster/bucket backup and restore, download backup, deploymentFailed recovery, teardown; command reference lines 461+), `docs/custom-clusters.md`, `docs/terraform.md`, `docs/migrating-from-scale-script.md`, `docs/backup-restore.md`.
- cbctl: `docs/capella/` has only `getting-started.md`, `reference.md` and `testing.md`. `internal/guide/topics/` has the 11 short help topics. The backlog docs were ported, but the user docs were not.
- Impact: there is no code loss, but the procedures operators follow (for example "Recover from deploymentFailed" and "Restore a bucket from a backup") are missing for cbctl. `migrating-from-scale-script.md` maps `scale_capella_cluster.sh` flags to `capellactl cluster scale`, and its cbctl equivalent is `cbctl capella cluster scale`.
- Fix: port the five files to `docs/capella/` using the existing `internal/docgen/port` rewriter (`port.go:166-270` already maps `capellactl X` to `cbctl capella X`, `.capellactl/` to `.cbctl/`, `apiVersion`, and so on). Add a coverage test (`internal/docgen/coverage/compare.go` already compares against capellactl) that lists each capellactl `docs/*.md` and requires either a ported file or an explicit `dropped:` entry with a reason.
- AC: `go test ./internal/docgen/...` fails if a capellactl doc has no counterpart. Every `cbctl` command in the ported docs parses (`cbctl <cmd> --help` exits 0), which a doc-lint test can enforce.

### P-ENG-06 — No capellactl -> cbctl migration guide or `doctor` check — **medium**

- capellactl: n/a (it is the source).
- cbctl: `README.md:341` only links the backlog. No guide covers env var renames, profile/keyring moves, the credentials dir, `capellactl import` vs `cbctl capella import`, or `apiVersion`.
- Impact: P-ENG-01, 02, 04 and 07 are each survivable, but they are silent. Users will discover them in production.
- Fix:
  - Add `docs/capella/migrating-from-capellactl.md` with a mapping table: commands (`capellactl X` = `cbctl capella X`), env vars, files, keyring, exit codes (identical) and the `apiVersion` note.
  - Optionally add `cbctl capella doctor` (`internal/cli/capella_doctor.go`, `func runDoctor(ctx context.Context, d *Deps) (*DoctorReport, error)`). It is read-only and offline by default and reports legacy env vars, legacy home or keyring entries, `.capellactl/credentials`, and gitignore status. It exits 0 when clean and 4 when it finds a migration item (with `--detailed-exitcode`).
- AC: the doc exists and is linked from `README.md` and `cbctl capella guide topics`. `doctor` on a fixture with the legacy setup lists every item and exits 4 with `--detailed-exitcode`.

### P-ENG-07 — `capella import` changed meaning (capellactl: hidden alias for `sync`; cbctl: JSONL data import) — **medium**

- capellactl: `capellactl/cli/env.py:644` (`@click.command("import", hidden=True, ...)`, "'import' is now 'sync'").
- cbctl: `cbctl capella import` = "Import JSON Lines into an empty Capella collection".
- Impact: old scripts running `capellactl import` become `cbctl capella import` after the documented `s/capellactl/cbctl capella/` rewrite (`port.go:166`). They now fail with a usage error (positional args missing, exit 3), which is safe. Worse, if someone passes positional args, the command imports data instead of writing `capella.yaml`.
- Fix: when `capella import` is called with zero positional args, or with sync-only flags (`--project`, `--cluster`, `--file`, `--full`, `--stdout`, `--dry-run`), exit 3 with `[error] - 'import' imports JSON Lines; capellactl's 'import' is now 'sync'` / `  → cbctl capella sync ...`. Document this in the migration guide.
- AC: `cbctl capella import --full` exits 3 with the hint. `cbctl capella import file.jsonl b.s.c` still imports.

### P-ENG-08 — Legacy `capella cluster create|destroy|exists|test` and `capella bucket create|exists` bypass engine safety — **high** (stability / data-loss risk)

- cbctl: `internal/cli/capella.go:44-303` registers the pre-port backend commands next to the v4 commands (`cbctl capella cluster --help` lists both `delete` and `destroy`, and both `create` paths).
  - `destroy` (`capella.go:105-130`, `internal/capella/orchestrate.go:138-168`) deletes the cluster with **no `-y` / TTY confirmation and no deletion-protection pre-check**. This differs from `cluster delete` and from cbctl.mdc ("`cluster delete` ... need `-y` without a terminal (exit 4)").
  - Errors call `exitErr()` (`root.go:90-92`, which runs `os.Exit(1)`) from inside `RunE` at `capella.go:68,120,148,163,208,213,237,245,303`. That skips the exit-code mapping (always 1, never 3/4/5), deferred cleanup (`signal.NotifyContext` stop at `root.go:81`) and JSON output.
- capellactl: no equivalent. capellactl had one cluster create path, and its `cluster delete` required confirmation.
- Impact: a migrating user who types `cbctl capella cluster destroy` (a natural guess from Terraform or capellactl `destroy`) deletes a cluster without a prompt. Automation gets exit 1 for every failure.
- Fix (choose one):
  1. Remove the legacy Capella commands, or hide them behind `cbctl legacy capella ...`, if they only served the Server-style `cbctl capella cluster create` flow that the v4 `cluster create` now covers.
  2. Keep them, but route through `confirmDestructive(cmd, "delete cluster X")` (the same helper `cluster delete` uses), check `deletionProtection`, return `Errorf(ExitBlocked, ...)` instead of `exitErr()`, and pass `cmd.Context()` down (see P-ENG-09).
  - Also replace every `exitErr()` in `capella.go`, `cluster.go`, `keyspace.go` and `keyspace_cmd.go` with returned typed errors (`Errorf(ExitError, "", "...: %w", err)`), and delete `exitErr` so `os.Exit` lives only in `cmd/cbctl/main.go`.
- AC:
  1. Without a TTY, `cbctl capella cluster destroy` without `-y` exits 4 and makes no DELETE call (verified against `capellafake`).
  2. With protection on, it exits 4.
  3. `rg 'os\.Exit' internal/` returns nothing.

### P-ENG-09 — Legacy backend: no context, no HTTP timeouts, swallowed poll errors — **high** (stability)

- cbctl:
  - `internal/capella/client.go:50` builds `&http.Client{}` and passes it via `capellaapi.WithHTTPClient` (`client.go:96-97`). That **replaces** the tuned transport (`capellaapi/client.go:156-163`: dial, TLS and response-header timeouts) with Go's default, which has no response-header timeout.
  - `client.go:114` calls `api.Do(context.Background(), ...)`, so Ctrl-C and `--timeout` are ignored.
  - `internal/capella/cluster.go:264-276` `Wait` loops 600 times with a fixed `time.Sleep(time.Second)`, uses no ctx, `continue`s on every non-404 error (errors are swallowed, including 401/403), and has no backoff.
- capellactl: n/a. These commands do not exist in capellactl, but they are reachable in cbctl.
- Impact: a hung TCP connection blocks forever. Ctrl-C does not reach in-flight requests, although the process is still killed by the default signal handler once `NotifyContext` stops. Auth failures during a wait show up as a 10-minute "timeout".
- Fix: thread `ctx context.Context` through `Backend` methods (`CreateCluster(ctx, cfg)`, `DestroyCluster(ctx, cfg)`, `ClusterExists(ctx, cfg) (bool, error)`; the current `bool` swallows lookup errors at `orchestrate.go:171`). Drop the custom `http.Client` and use the capellaapi default. Replace `Cluster.Wait` with `engine.WaitForCluster`. Map errors to 1, 3, 4, 5 or 130.
- AC: a unit test with a stalled `httptest` server and a 2s ctx returns `context.DeadlineExceeded` and exit 5. A 403 during wait returns exit 1 immediately. `ClusterExists` on 500 returns an error, not `false`.

### P-ENG-10 — Server keyspace/import commands connect with `context.Background()` — **low**

- cbctl: `internal/cli/keyspace.go:38,65,113,143,195,228,271`, `internal/cli/keyspace_cmd.go:49` call `s.Connect(context.Background(), cfg)` instead of `cmd.Context()`. The SDK timeouts (`--timeout`, KV/connect/query timeouts) still bound each call.
- capellactl: n/a. These are Server commands, outside the capellactl surface.
- Impact: Ctrl-C during connect or import is not propagated, so it does not produce a clean 130 exit with best-effort cleanup.
- Fix: replace these calls with `cmd.Context()`. In `import`, check `ctx.Err()` between batches and return `Errorf(ExitInterrupted, ...)`.
- AC: a test with a cancelled ctx returns exit 130 and does not start new batches.

### P-ENG-11 — Failed password rotation: restoring the local secret file swallows errors — **medium**

- cbctl: `internal/secrets/files.go:111-123` `RestoreRotatedCredential(path, backup string)` has no return value. The `ReadFile` error returns silently, and `_ = os.WriteFile(path, raw, 0o600)` (`:122`) is ignored.
- capellactl: `capellactl/output/secrets_out.py` restore raises on I/O failure, so the user is told.
- Impact: if the PUT fails and the restore also fails (disk full, permissions), the local file holds a **new password that Capella never accepted**. The user is not told, and apps that read the file break. The `.bak` is the only good copy.
- Fix: change the signature to `func RestoreRotatedCredential(path, backup string) error` and wrap the errors (`fmt.Errorf("restore %s from %s: %w", path, backup, err)`). The caller (credential update / apply rotation) appends `[error] - could not restore previous password file` / `  → copy <backup> to <path>` and keeps the original API error as the primary error. Exit stays 1.
- AC: with a read-only target dir and a failed PUT (fake), the output names both paths and the exit code is 1. A unit test covers the restore error.

### P-ENG-12 — Secrets dir: `chmod` errors ignored; dead code in `GitWarning` — **low**

- cbctl: `internal/secrets/files.go:63` (`_ = os.Chmod(dir, 0o700)`), `:96` (`_ = os.Chmod(path, 0o600)`), `:138` (`_ = strings.TrimSpace(string(top))`: the repo top-level is computed and then thrown away, so `check-ignore` runs relative to the process cwd, not the repo).
- Impact: on a pre-existing dir with 0755, a chmod failure leaves passwords group/world-readable with no warning. `GitWarning` can misreport when cwd is outside the repo containing `dir`.
- Fix: return or log `chmod` failures as `[warning] - could not restrict permissions on <path>` / `  → chmod 600 <path>`. In `GitWarning`, run `git -C <dir> check-ignore -q .` (or use `top` as `cmd.Dir`) and drop the dead assignment.
- AC: unit test with an injected chmod failure produces a warning. A `GitWarning` test with cwd outside the repo detects that a non-ignored dir is not ignored.

### P-ENG-13 — Unbounded response body read — **low**

- cbctl: `internal/capellaapi/client.go:359` `io.ReadAll(hr.Body)`.
- capellactl: `requests` also reads the whole body (same behavior).
- Impact: in theory, memory exhaustion from a misbehaving proxy. In practice, low risk.
- Fix: `io.ReadAll(io.LimitReader(hr.Body, maxBody+1))` with `const maxBody = 32 << 20`. If the body is longer, return `&APIError{Status: hr.StatusCode, Message: "response too large"}` (exit 1).
- AC: a test server streaming 64 MiB gets an error, not an OOM.

### P-ENG-14 — `panic` in non-main packages — **low**

- cbctl: `internal/presets/presets.go:149,165` (embedded YAML parse; tests cover it), `internal/cli/capella_managed_ops.go:66` (command-tree wiring), `internal/secrets/password.go:90` (`crypto/rand` failure), `internal/integration/target.go:316` (test harness).
- Impact: none at runtime in practice. These are programmer or startup errors, which the general rules allow.
- Fix: none required. Optionally make `password.Generate` return `(string, error)` so a `crypto/rand` failure (for example in a seccomp sandbox) surfaces as exit 1 instead of a stack trace.
- AC: no `panic` is reachable from user input. Add a fuzz/test note.

### P-ENG-15 — `sync` writes `apiVersion: cbctl/v1`, so the file is no longer readable by capellactl — **low**

- capellactl: rejects any `apiVersion` other than `capellactl/v1` (`capellactl/config/schema.json`).
- cbctl: full render in `internal/importer/render.go` emits `cbctl/v1`. Merge mode keeps the existing key.
- Impact: rolling back to capellactl during migration needs a one-line edit.
- Fix: preserve the input file's `apiVersion` on merge (already done), and document it in the migration guide (P-ENG-06). Optionally add `sync --api-version capellactl/v1`.
- AC: `sync` on a `capellactl/v1` file keeps `capellactl/v1`. This is covered by a test.

### P-ENG-16 — No converter (none required) — **low / informational**

- Evidence: `internal/config/capellafile/testdata/valid/capellactl-*.yaml` (copies of capellactl examples, including `capellactl-example.yaml`, `capellactl-gcp-prod.yaml`, `capellactl-azure-prod.yaml`, `capellactl-single-node-dev.yaml`, `capellactl-minimal-cluster.yaml`) validate with exit 0. The schema accepts `capellactl/v1` (`reference.go:151`).
- Impact: the file format needs no migration. Only the out-of-file items (P-ENG-01, 02, 04) need attention.
- Fix: add a test that loads **every** example under the capellactl repo's `examples/` and `docs/` code fences (snapshotted into testdata) to lock this in, and mention this in the migration guide.
- AC: `go test ./internal/config/capellafile -run TestCapellactlExamples` passes for all snapshotted files.

### P-ENG-17 — `requireCapella` falls back to `context.Background()` when `cmd.Context()` is nil — **low**

- cbctl: `internal/cli/capella_globals.go:242-244`.
- Impact: only reachable in tests or embedding that call `Execute()` instead of `ExecuteContext`. Org discovery would then ignore Ctrl-C.
- Fix: fall back to the deps context (`contextWithDeps`), or fail with `Errorf(ExitError, ...)` so a missing ctx is caught in tests.
- AC: a test that runs a command without ctx gets a deterministic error or uses the deps ctx.

## 5. Top 10 gaps

1. P-ENG-01 — `CAPELLACTL_CONFIG` / `CAPELLACTL_PROFILE` / `BEARER_TOKEN` / `ORG_ID` / `API_BASE_URL` are silently ignored, so cbctl can run against the wrong config file or profile.
2. P-ENG-08 — Legacy `cbctl capella cluster destroy` deletes with no `-y` and no deletion-protection check; `os.Exit(1)` in `RunE` bypasses exit codes.
3. P-ENG-02 — Profiles (`~/.config/capellactl`) and keyring service `capellactl` are not read and there is no migrate command, so every user must re-configure.
4. P-ENG-09 — Legacy `internal/capella` backend: no ctx, default `http.Client` without timeouts, a 600x1s poll loop that swallows errors.
5. P-ENG-03 — Generated `docs/capella/reference.md` has 13 rows vs 103 (a `$ref` under `items` is not dereferenced), so all cluster fields are undocumented.
6. P-ENG-04 — Default credentials dir is now `.cbctl/credentials`: not gitignored in existing repos, and rotation loses the local `.bak`.
7. P-ENG-05 — `runbook.md`, `custom-clusters.md`, `terraform.md`, `migrating-from-scale-script.md` and `backup-restore.md` were not ported.
8. P-ENG-06 — No capellactl -> cbctl migration guide or doctor check.
9. P-ENG-07 — `capella import` now means JSONL data import, not the old `sync` alias.
10. P-ENG-11 — A failed rotation restore swallows I/O errors, so the local secret file can hold an unapplied password.

## 6. Verdicts

**Config-file compatibility / converter:** compatible as-is. cbctl's schema is a superset that accepts `apiVersion: capellactl/v1`. Every field, type, default (except `credentialsOutput`), validation rule, `${VAR}` / `${file:PATH}` interpolation, the single-/multi-project forms, id pinning and `.bak` behave identically. capellactl example files validate with exit 0. No converter exists, and none is needed for the file itself. The breaking changes are all **outside** the file: the env-var names used for discovery and auth, profile/keyring location, and the default credentials dir. They need a shim plus a `configure migrate` (P-ENG-01, 02, 04).

**API endpoint coverage:** complete. All 57 Management API v4 endpoints capellactl calls are in `internal/capellaapi/inventory.go`, with matching method, path, pagination, ETag/If-Match, error-envelope decoding, retry/backoff (429/5xx, Retry-After, full jitter, POST-safety) and ambiguous-write handling. No used request or response field is missing. App Services, private endpoints, peering, audit logs, org users/API keys and alerts are absent from both tools, so their absence is not a regression.
