# cbctl 1.0.0 — Architecture & Technical Decisions

This document is the shared reference for every story in `docs/backlog/release-1.0.0`. Stories link back here instead of repeating the language decision, the idempotency model, conventions and the test strategy. The story list and delivery phases are in the release [README.md](README.md). How to use the tool is in the repository [README.md](../../../README.md).

API: Capella Management API v4.0, `https://cloudapi.cloud.couchbase.com` ([reference](https://docs.couchbase.com/cloud/management-api-reference/index.html)). Every endpoint, status code, field and enum named in the stories was taken from the published OpenAPI spec (`openapi.external.generated.yaml`, v4.0).

---

## 1. Product summary

`cbctl` sets up and manages a Couchbase Capella environment from one YAML file:

```
organization (existing)
└── project
    └── cluster(s)
        ├── deletion protection
        ├── hibernation schedule (on/off)
        ├── allowed CIDRs
        ├── buckets
        │   └── scopes
        │       └── collections
        └── database credentials
```

0. **Set up access** once: `cbctl capella configure` saves the organization and stores the API key in the OS keychain (or use `CAPELLA_API_KEY`).
1. **Describe** the organization in one `capella.yaml` (or `capella.yml`), written by the tool: `cbctl capella sync` discovers every existing project and cluster (CAP-054), `cbctl capella project create` and `cbctl capella cluster create` add new ones. The file holds a `projects:` list, each project with its `clusters:` (CAP-040); the older single-project form is still accepted.
2. **Validate** it offline (`cbctl capella validate`).
3. **Plan**: compare the file with Capella and show every create, update, replace, delete or blocked change (`cbctl capella plan`).
4. **Apply**: make only those changes, in dependency order, waiting for long cluster operations (`cbctl capella apply`).
5. **Operate**: scale out, in or up; power off or on; pause the hibernation schedule; rotate passwords.
6. **Re-run safely**: an unchanged file makes zero write calls.

---

## 2. Technology decision
<!-- adapted for cbctl -->

*Language: Go 1.26 (existing cbctl codebase); CLI: cobra; HTTP: `net/http` with the retry client in `internal/capellaapi` (Phase 1); config: `gopkg.in/yaml.v3` + JSON Schema 2020-12 via `santhosh-tekuri/jsonschema/v6`; keychain: `zalando/go-keyring`; tests: stdlib `testing` + `internal/capellafake` + `httptest`; packaging: GoReleaser (existing).*

| Criterion | Bash + curl + jq | Python | **Go (cbctl)** | Terraform provider |
|---|---|---|---|---|
| Desired-state diff of nested JSON | Hard in `jq` | Native dicts | Typed structs | Built in |
| Retries, 429 backoff, pagination, ETag | Hand-rolled | One client module | One client module (`internal/capellaapi`) | Built in |
| Unit and scenario testing | Hard (bats) | pytest | `go test` + fake | Provider acceptance tests |
| Ops-team readability | Yes | Yes | Single binary, Go source | Needs Terraform skills |
| Install | None | pipx / wheel | GitHub release binary / `go install` | Terraform + state |
| Idempotency | Must be built | Must be built | Must be built (this design) | Native |

**Decision: Go**, shipped as native binaries via GoReleaser (existing cbctl packaging). Capella management is folded into `cbctl capella`.

Customers who already run Terraform should use Couchbase's official Capella Terraform provider (CAP-033). `cbctl` is for teams that want a readable, self-contained tool from runbooks, cron and CI.

---
## 3. Design principles

1. **Declarative, tool-owned file.** `capella.yaml` is the desired state and the record of the whole organization (all projects), but users never edit it: `sync` writes and refreshes it from Capella (`import` is its alias, CAP-054), and `cluster create|update|scale` change it (validate → plan → confirm → write → apply). Hand-editing remains possible, never required (CAP-038).
2. **Idempotent.** A second `apply` with an unchanged file makes zero `POST`, `PUT` or `DELETE` calls and exits 0 with `No changes`.
3. **Stateless.** No local state file. Capella is the source of truth; resources are found by natural key (section 6.1).
4. **Plan before apply.** Every write is first shown as a plan line. `apply` asks for confirmation unless `--auto-approve`.
5. **Safe by default.** Nothing is deleted unless asked (`delete`, `destroy`, `--prune`). Immutable-field changes are BLOCKED, never fixed by destroy-and-recreate. Scale-in is guarded by capacity checks.
6. **Secrets never leak.** API keys and passwords never appear in output, logs or exceptions.
7. **One cohesive interface.** Same flags, plan symbols, output formats, exit codes and error format for every resource.

---

## 4. Architecture
<!-- adapted for cbctl -->

```
cmd/cbctl/                         # shipped CLI
cmd/cbdev/                         # developer tool (docs/CI helpers; not shipped)
internal/
├── cli/                           # cobra commands; NewRootCmd / Run / Deps
├── capellaapi/                    # Management API client, inventory, endpoints
├── capellafake/                   # in-memory Capella for tests
├── clitest/                       # scenario helpers
├── config/capellafile/            # schema, load, crossref, discovery
├── plan/                          # planner / executor / normalize
├── engine/                        # waiter, clock
├── resources/                     # reconcilers (project, cluster, …)
├── importer/                      # observed → capella.yaml
├── secrets/                       # credential files + password generation
├── guide/                         # guide topics (markdown)
├── presets/                       # size / disk tables
├── settings/                      # profiles, keyring, env precedence
├── redact/                        # secret redaction
├── integration/                   # //go:build integration
├── ci/                            # janitor, secretscan, releasegate
└── docgen/                        # backlog port + generators
docs/
├── capella/                       # user guides
├── backlog/                       # ported stories + coverage
└── specs/                         # fold-in phase specs
```

### 4.1 Reconciler contract
<!-- adapted for cbctl -->

```go
type Reconciler interface {
    Kind() string
    Key(desired any) string
    Observe(parent ParentIDs) (map[string]Observed, error)
    Diff(desired any, observed *Observed) Action
    Apply(action Action, parent ParentIDs) (Result, error)
    Wait(result Result, parent ParentIDs) error
}
```

`Action.Type` is one of `NOOP`, `CREATE`, `UPDATE`, `REPLACE`, `DELETE`, `BLOCKED`, `DEFERRED`. Each action carries `Changes []Change{Path, Before, After}` and, when `BLOCKED` or `DEFERRED`, a `Reason`. Plan text, JSON output and tests all read the same structure (Phase 3 §6.1).

### 4.2 Dependency order

```
project → cluster ─┬→ deletion protection
                   ├→ hibernation schedule
                   ├→ allowed CIDRs
                   └→ buckets → scopes → collections → database credentials
```

Deletes run in reverse. Children of a cluster are reconciled only when the cluster is `healthy` (otherwise `DEFERRED`, section 8).

---
## 5. Command-line interface

```
cbctl capella [GLOBAL FLAGS] <command>

Global flags
  -c, --config PATH        default: discovered (section 5.1)
      --profile NAME       saved profile (CAP-037)
  -o, --output text|json   default text
      --log-level LEVEL    error|warn|info|debug (default info)
      --auto-approve
      --no-wait
      --timeout DURATION   default 60m

Getting help and setting up
  help [COMMAND… | TOPIC | topics | config [FIELD]]
  completion bash|zsh|fish
  configure [list | show | remove] [--profile NAME]
  init [--file PATH] [--non-interactive …] [--dry-run] [--force]
  sync [--project NAME…] [--skip-project NAME…] [--cluster NAME…] [--dry-run] [--stdout] [--full] [--force]

Environment
  validate | plan [--detailed-exitcode] | apply [--target KIND/NAME…] [--prune] | destroy | drift

Resources
  project    list | get | create | update | delete [--cascade] [--discard-snapshots]
  cluster    options | list | get | create | update | delete [--cascade] [--discard-snapshots]
  cluster    protect NAME --on|--off
  cluster    create NAME [--group SERVICES:NODESxCPU/RAM[:DISK] …]   (multi-dimensional, CAP-051)
                     [--single-node] [--server-version V] [--zone Z] [--cmek-id ID] [--private-dns]   (CAP-053)
  cluster    scale NAME (--nodes N | --step +1|-1) [--group SERVICES] [--min N] [--max N]
  cluster    group list | add | remove NAME   (service groups, CAP-052)
  cluster    power NAME --on [--with-app-service] | --off
  cluster    schedule show|apply|pause|resume|delete NAME
  cluster    disk-types [--provider P]   (CAP-060)
  cluster    backup list|create|retain|delete|restore|restores|regions   (cloud snapshots, CAP-057)
  cluster    backup schedule show|options|set|delete NAME   (CAP-056)
  cidr       list | create | delete
  bucket     list | get | create | update | delete
  bucket     backup list|get|create|delete|restore|export   (CAP-059)
  bucket     backup schedule show|set|delete · backup cycle list|get   (CAP-058)
  scope      list | create | delete
  collection list | create | update | delete
  credential list | get | create | update [--rotate-password] | delete
```

**Verbs.** `apply` is the declarative, whole-file command (with `--target` to narrow it). Resource groups use
the API's verbs: `create` and `update` record the change in `capella.yaml` (validated like `validate`), show the
plan for that one resource, ask once and apply it (`--no-apply` only records it); `delete` removes the resource
from Capella and from the file. A group offers only the verbs its API supports (allowed CIDRs and scopes have no
update). `project apply` and `cluster apply` remain as hidden aliases of `apply --target` for one release.

### 5.1 Authentication

| Variable | Required | Meaning |
|---|---|---|
| `CAPELLA_API_KEY` | yes | Sent as `Authorization: Bearer <key>` |
| `CAPELLA_ORG_ID` | unless `organizationId` is in the file | Organization ID |
| `CAPELLA_API_BASE_URL` | no | Default `https://cloudapi.cloud.couchbase.com` |

`BEARER_TOKEN`, `ORG_ID` and `API_BASE_URL` (used by the Nova script) are accepted as fallbacks with a deprecation warning.

**Profiles (CAP-037).** `cbctl capella configure` saves `organizationId`, `apiBaseUrl` and a default `configFile` per profile in `~/.config/cbctl/profiles.yaml`, and the API key in the OS keychain. Precedence, highest first: command-line flags → environment variables → selected profile (`--profile`, `CBCTL_PROFILE`, else `default`) → `organizationId` in the config file.

**Config file discovery.** `--config` → `CBCTL_CONFIG` → profile `configFile` → `./capella.yaml` → `./capella.yml` (both present is an error).

Roles: project create needs Organization Owner or Project Creator; everything else needs Organization Owner, Project Owner or Project Manager on the target project (per endpoint, as listed in the API reference).

### 5.2 Plan symbols

| Symbol | Action | Meaning |
|---|---|---|
| `+` | CREATE | Will be created |
| `~` | UPDATE | Will be changed in place; field-level `before → after` follows |
| `±` | REPLACE | Will be re-created (only allowed CIDRs, which have no update API) |
| `-` | DELETE | Will be deleted |
| `=` | NOOP | Matches the file |
| `!` | BLOCKED | Cannot be applied; reason given; apply refuses to start |
| `…` | DEFERRED | Waits on its parent (for example cluster not yet healthy) |

### 5.3 Exit codes

| Code | Meaning |
|---|---|
| 0 | Success, or nothing to do |
| 1 | Runtime or API error |
| 2 | `plan` / `drift` with `--detailed-exitcode` found changes |
| 3 | Configuration invalid |
| 4 | Blocked (immutable change, deletion protection, guard failed, non-empty parent, ambiguous name, cluster turned off) |
| 5 | Timed out waiting for an async operation |
| 130 | Interrupted |

---

## 6. Idempotency model

### 6.1 Identity

| Resource | Natural key | Notes |
|---|---|---|
| Project | `name` in the organization | Names are not unique in Capella. 0 matches → create; 1 → reconcile; >1 → BLOCKED until `id:` is pinned |
| Cluster | `name` in the project | Same rule |
| Hibernation schedule | the cluster (one per cluster) | `GET …/onOffSchedule` 404 = none |
| Allowed CIDR | `cidr` in the cluster | |
| Bucket | `name` in the cluster | ID (base64 of name) always taken from the list response |
| Scope | `name` in the bucket | `_default`, `_system` never created or deleted |
| Collection | `name` in the scope | `_default` never created or deleted |
| Database credential | `name` in the cluster | |

### 6.2 Updatable vs immutable

| Resource | Create | Update in place | Immutable (→ BLOCKED) | Delete |
|---|---|---|---|---|
| Project | `POST /projects` 201 | `PUT` `name`, `description` | — | `DELETE` 204; 409 if not empty |
| Cluster | `POST …/clusters` 202 | `PUT` `name`, `description`, `support`, `serviceGroups` | `cloudProvider.*`, `availability`, `configurationType`, `couchbaseServer.version`, `zones`, `cmekId`, `enablePrivateDNSResolution` | `DELETE` 202 |
| Deletion protection | — | `PUT …/deletionProtection` 204 | — | — |
| Power state | — | `POST` / `DELETE …/activationState` 202 | — | — |
| Hibernation schedule | `POST …/onOffSchedule` 204 | `PUT` 204; pause/resume via `…/onOffSchedule/activationState` | — | `DELETE` 204 |
| Allowed CIDR | `POST` 201 | none → REPLACE | `cidr` | `DELETE` 204 |
| Bucket | `POST` 201 | `PUT` `memoryAllocationInMb`, `durabilityLevel`, `replicas`, `flushEnabled`, `timeToLiveInSeconds`, `priority`, `enableCrossClusterVersioning` | `type`, `storageBackend`, `vbuckets`, `bucketConflictResolution`, `evictionPolicy` | `DELETE` 204 |
| Scope | `POST` 201 | — | — | `DELETE` 204 |
| Collection | `POST` 201 | `PUT` `maxTTL` (server ≥ 7.6.0) | — | `DELETE` 204 |
| Database credential | `POST` 201 (returns password once) | `PUT` `access`; `password` only on rotation | `credentialType` | `DELETE` 204 |

### 6.3 Comparison rules (`internal/plan/normalize.go`)

- Only fields **present in the file** are compared; server-generated fields (`id`, `audit`, `stats`, `connectionString`, `currentState`, `expressScaling`) are ignored.
- **Defaults** are applied to desired state before comparing (bucket `type: couchbase`, `replicas: 1`, `durabilityLevel: none`, `timeToLiveInSeconds: 0`, `flushEnabled: false`, `bucketConflictResolution: seqno`; cluster `configurationType: multiNode`). `priority` is not defaulted because it depends on the server version.
- **Sets are sorted:** `services`, `privileges`, names inside credential `access`.
- **Aliases:** privilege `read` → `data_reader`, `write` → `data_writer`.
- **Deprecated:** `flushEnabled` is written; `flush` is read only if `flushEnabled` is absent.
- **Service groups** are matched by their sorted `services` set (the *group key*, for example `data,index,query`), not by list position.
- **Enums** compared case-insensitively (`support.plan: "Developer Pro"` = `developer pro`).
- **Schedules** are expanded to the API's 7-day Monday→Sunday form before comparing (CAP-020).

### 6.4 Full-replacement updates

The cluster `PUT` requires `name`, `description`, `support`, `serviceGroups`; the bucket `PUT` requires `memoryAllocationInMb`, `durabilityLevel`, `replicas`, `timeToLiveInSeconds`; the schedule `PUT` requires `timezone` and all 7 `days`. The body is always **observed state overlaid with desired state**, so unset fields keep their current values.

### 6.5 Concurrency and races

- `GET` returns an `ETag`; updates send it as `If-Match`. On `412`, the resource is re-read and re-planned once; a second `412` fails with exit 1.
- `409` on create (code 1010, "already exists") → re-read by natural key and continue as if it existed.
- Cluster updates are sent only when `currentState` is `healthy`.

---

## 7. Scaling model

Scaling is a change to `serviceGroups` sent with the cluster `PUT` (section 6.4). `internal/resources/scaling.go` classifies every service-group change before it is planned, so `apply` and the `cluster scale` command share the same rules.

| Change | Story | Classification |
|---|---|---|
| `numOfNodes` up, or a new group | CAP-015 | Scale out |
| `numOfNodes` down, or a group removed | CAP-016 | Scale in (guarded) |
| `node.compute` or `node.disk` changed | CAP-017 | Vertical scale |
| `services` of a group changed | CAP-015/016 | Treated as remove old group + add new group, so both rules apply |

### 7.1 Limits (from the API spec)

| Rule | Value |
|---|---|
| Nodes in the first service group of a multi-node cluster | 3–27 |
| Nodes in each additional service group | 2–24 |
| Total nodes in the cluster | ≤ 27 |
| `singleNode` cluster | exactly 1 node; cannot be scaled out or in |
| At least one group runs `data` | always |

### 7.2 Scale-in guards (`GET …/clusters/{id}/stats`)

`stats` returns `totalMemoryInMb`, `freeMemoryInMb` and `maxReplicas`. For a scale-in that reduces data-service nodes from `n` to `m`:

| Guard | Rule | Result if violated |
|---|---|---|
| Memory | `allocated = total − free`; `projectedTotal = total × m / n`; require `allocated ≤ projectedTotal` | BLOCKED |
| Replicas | every bucket's `replicas ≤ m − 1` | BLOCKED |
| Data service present | the last group running `data` cannot be removed | BLOCKED |
| Service still available | removing the last group running `index`, `query`, `search`, `analytics` or `eventing` | BLOCKED unless `--allow-service-removal` |
| Confirmation | every scale-in needs confirmation; `--auto-approve` is enough unless a whole group is removed, which also needs `--allow-service-group-removal` | — |

### 7.3 Imperative scaling and the file

`cluster scale` records the new node count in `capella.yaml` and applies it, so the file and Capella always agree (CAP-038). `--step` (CAP-018) will follow the same path.

Every row of the table above has a command, so no service-layout change needs a file edit: `cluster create --group` builds several groups at once (CAP-051), `cluster group add|remove` adds or removes one (CAP-052, the CAP-015/016 rules), `cluster scale --group` changes a group's node count and `cluster update --group` its compute and disk. A group's services set is changed by adding the new group and removing the old one.

---

## 8. Asynchronous operations and cluster states

`GET …/clusters/{id}` returns `currentState`, one of: `draft`, `deploying`, `scaling`, `upgrading`, `rebalancing`, `peering`, `destroying`, `healthy`, `degraded`, `turnedOff`, `turningOff`, `turningOn`, `deploymentFailed`, `scaleFailed`, `upgradeFailed`, `rebalanceFailed`, `peeringFailed`, `destroyFailed`, `offline`, `turningOffFailed`, `turningOnFailed`.

| Operation | Success | Failure | Transitional (keep waiting) |
|---|---|---|---|
| create | `healthy` | `deploymentFailed` | `draft`, `deploying` |
| update / scale | `healthy` | `scaleFailed`, `rebalanceFailed`, `upgradeFailed` | `scaling`, `rebalancing`, `upgrading` |
| power off | `turnedOff` | `turningOffFailed` | `turningOff` |
| power on | `healthy` | `turningOnFailed` | `turningOn` |
| delete | `GET` 404 | `destroyFailed` | `destroying` |

Polling starts at 10 s and backs off to 60 s. `--timeout` (default 60m) → exit 5; the operation continues in Capella and a later run resumes waiting.

| Cluster state found by `apply` | Cluster action | Children |
|---|---|---|
| `healthy` | reconcile | reconcile |
| transitional | wait, then reconcile | after wait |
| `turnedOff` | no changes possible | BLOCKED with reason *turned off* (exit 4); `apply --power-on` turns it on first (CAP-019) |
| `degraded`, `offline` | BLOCKED | BLOCKED |
| any `*Failed` | BLOCKED with recovery hint | BLOCKED |

---

## 9. API client behaviour

| Concern | Behaviour |
|---|---|
| Timeouts | 10 s connect, 60 s read |
| Retried | `429`, `500`, `502`, `503`, `504`, connection errors |
| Backoff | exponential with full jitter, 1 s base, 30 s cap, max 6 attempts; `Retry-After` honoured |
| `POST` | retried only on `429` or a connection error raised before the request was sent; after an ambiguous failure the caller re-observes by natural key first |
| Pagination | `perPage=100`, follow `cursor.pages.next` until absent; responses without `cursor` are a single page |
| Errors | body `{httpStatusCode, code, message, hint}` → `CapellaApiError`; CLI prints `message` and `hint` |
| Logging | `debug` logs method, path, status, duration; `Authorization` and any `password` field redacted |
| User agent | `cbctl/<version> (go/<runtime.Version()>)` | <!-- adapted for cbctl --> |

---

## 10. Secrets

- API key from `CAPELLA_API_KEY` or the OS keychain (profiles, CAP-037); never in `capella.yaml`, `profiles.yaml` or any other file.
- `init` and `sync` never write passwords or the API key into the generated file; credentials get `${VAR}` placeholders or a "password not exported" comment.
- Passwords only as `${VAR}` or `${file:…}`; literal passwords fail validation.
- Capella-generated passwords (201 response of `POST …/users`) are written once to `credentialsOutput` (default `./.cbctl/credentials/<cluster>.<name>.json`, mode `0600`) and never printed.
- Passwords are never read back by Capella, so they are not compared; they change only on explicit rotation (CAP-028).
- `internal/redact` masks the API key, every resolved secret value and every `password` field in all output (CAP-008).

---

## 11. Output

- **Text:** plan (section 5.2), live progress lines, final summary table.
- **JSON (`-o json`):** `{"plan": {"actions": [{"kind","key","action","changes":[{"path","before","after"}],"reason"}]}, "result": {"status","actions":[{"kind","key","status","id","durationSeconds"}]}}`. Stable contract; breaking changes need a major version.

---

## 12. Testing conventions
<!-- adapted for cbctl -->

### 12.1 Layers

| Layer | Location | What |
|---|---|---|
| Unit | `internal/*/…_test.go` | Pure functions: normalisation, diff, guards, config validation, redaction |
| Contract | `internal/capellaapi` + `httptest` | Endpoint methods vs mocked HTTP |
| Scenario | `internal/cli/scenario_*_test.go` on `capellafake` | Full command flows; asserts writes, output, exit code |
| Integration | `internal/integration` (`//go:build integration`) | Fake or sandbox org via `CBCTL_IT_TARGET` |

### 12.2 Naming and traceability

Every acceptance criterion `ACn` in story `CAP-NNN` has at least one test named

```
Test<Subject>_CAP<nnn>_AC<n>_<Behaviour>
```

A CI check (`cbdev docs ac-coverage`) fails if an AC of an `Implemented` / `In progress` story has no tagged test.

### 12.3 Standard assertions

| Helper | Asserts |
|---|---|
| `Fake.Writes()` | `POST`/`PUT`/`DELETE` calls for zero-write re-run checks |
| `Fake.Calls(method, path)` | Calls matching a pattern |
| `Fake.Advance(d)` | Moves async states on the fake clock |
| `AssertNoSecret(output, …)` | None of the secret values appear in stdout/stderr/logs |

### 12.4 The re-run and round-trip rules

Every resource story has an **idempotency AC**: run the command, run it again, and assert no writes on the second run and exit code 0.

Every exporter in `internal/importer` is covered by a **round-trip AC**: import a seeded Capella state, run `plan`, and assert `No changes`.

---
## 13. Global Definition of Done

1. Every AC in the story has a passing, tagged test (section 12.2).
2. The idempotency AC passes.
3. No secret appears in any output (`assert_no_secret` used where the story touches secrets).
4. Text and JSON output for the story's commands are documented in `docs/`.
5. Reviewed by one engineer; gofmt, `go vet`, `make cover-engine` (≥ 85% on `internal/plan`, `internal/resources`, `internal/config/capellafile`) pass. <!-- adapted for cbctl -->
6. Exercised once against the sandbox organization, and the story's **Open questions** answered or moved to 1.1.
7. Story **Status** updated and re-run `make docs`. <!-- adapted for cbctl -->

---

## 14. Release scope

**In 1.0.0:** `help` with topics and per-field config help; shell completion; profiles with keychain-stored API keys; `init` wizard; `import` of existing projects and clusters; projects; provisioned clusters (create, edit, protect, delete); scale out, in and vertically; power off/on; free-tier clusters; disk type choice; hibernation (on/off) schedules with pause/resume; cluster and bucket backup schedules, backups and restore; cluster allowed CIDRs; buckets, scopes, collections; basic database credentials; full-environment apply/destroy; drift.

In cbctl, 1.0.0 maps to Phases 0–4 of `docs/specs/`; 1.5.0 and 2.0.0 are the backlog in `docs/backlog/`. <!-- adapted for cbctl -->

**Not in 1.0.0:** advanced credentials (`userRoles`) and custom database roles; private endpoints and network peering; cloning a cluster from a backup; Operational Insights clusters; audit logging; CMEK key management; organization users and API keys; sample buckets; and every other operational-database operation 1.0.0 does not call. These are planned for [release 1.5.0](../release-1.5.0/README.md) ([API coverage](../release-1.5.0/API-COVERAGE.md)). App Services and App Endpoints are planned for [release 2.0.0](../release-2.0.0/API-COVERAGE-APP-SERVICES.md). Server version upgrades have no Management API operation and are not planned.

<!-- ported-from: capellactl@6389d5a docs/release-1.0.0/00-architecture.md · rules v1 -->
