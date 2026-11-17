# 0.7.0 Feature 2b: manifest execution, errors, edge cases, acceptance

Model and conflicts: [02-manifest-apply.md](02-manifest-apply.md).

## 1. `cbctl apply -f` command flow (`internal/cli/apply.go`)

1. Parse flags. A missing `-f`, or `-f` given twice, exits 3 with the usual hint. `-f -` reads stdin (max 10 MiB). A TTY stdin with `-f -` exits 3 ("pipe a manifest").
2. `manifest.Load`. Any issue in any document exits 3 and nothing is contacted (spec: "Any validation failure applies nothing").
3. For each document, in order: `Resolve` secrets (exit 3 on failure, before any network call for **every** document, so a bad ref in doc 2 stops doc 1 too). Then dispatch to `serverapply.Apply` or `capellaapply.Apply`.
4. Render the `manifest.Report` list: text lines as they happen, or one JSON document at the end (`{"apiVersion":"cbctl/v1","documents":[Report...]}`, schema `internal/manifest/testdata/apply.manifest.schema.json`).
5. Map the first error to an exit code through `HandleError`.

## 2. `CouchbaseServer` execution (`serverapply.Apply`)

| Step | Calls (reused unless marked new) | Output (P2 levels) |
|---|---|---|
| 0. Defaulting | `primary` = the flagged node or `nodes[0]`. Services from `nodeDefaults` unless the node sets `services` or `arbiter`. Canonical names go to `rest` names (`search`→`fts`). | — |
| 1. Primary | `rest.Client.BootstrapPrimary(ProvisionOptions{ClusterName: metadata.name, IPAddress, ExternalIPAddress, Services, ServerGroup, DataPath, RAMGiB, Username, Password, SSL})` | `[ok] - Cluster created on IP`, or `[info] - Cluster already configured` |
| 2. Joins | for each non-primary node in order: `rest.Client.JoinNode(opts with RallyIPAddress=primary, Arbiter)` | `[ok] - Node IP added to cluster at RALLY` / `[ok] - Arbiter node …` / `[info] - Node IP already configured` |
| 3. Membership report | **new** `rest.Client.ClusterNodes(rally, u, p) ([]NodeInfo, error)` (GET `/pools/default`: hostname, `clusterMembership`, services) | `[warning] - node X is in the cluster but not in the manifest; left in place` |
| 4. Rebalance | skipped if `rebalance: false`, or if **new** `PendingNodes` (`clusterMembership == inactiveAdded`) is empty. Otherwise `RebalanceCluster`. | `[ok] - Cluster rebalanced` / `[info] - Rebalance not needed` |
| 5. Data model | SDK connect to the primary (`server.Server.Connect` with `config.Config{Host: primary, SSL, creds}`). Per bucket: `BucketExists` → `CreateBucket(name, quota, replicas)`. Per scope: `ScopeExists` → `CreateScope` (`_default` no-op). Same for collections. Existing resources are left as they are, even if the quota differs: a `[warning]` notes the difference. | `[ok] - Bucket "b" created`, `[info] - Bucket "b" exists` … |
| 6. Import | per entry: `CollectionIsEmpty` false → `[warning] - Collection b.s.c not empty; import skipped`. Otherwise **new** `server.ImportDocuments(ctx, src, b, s, c, keyField)`. | `[ok] - Imported N documents into b.s.c` |
| 7. Summary | — | `[ok] - CouchbaseServer/airline-ops applied (3 nodes, 1 bucket, 2 collections, 1 import)` |

New code in `internal/server/import.go`:

```go
// DocSource yields documents in order. Next returns io.EOF at the end.
type DocSource interface{ Next() (doc map[string]any, where string, err error); Close() error }
func FileSource(path string) (DocSource, error) // JSONL, blank lines skipped, 10 MiB line cap (as today)
func InlineSource(docs []json.RawMessage, where string) DocSource
// ImportDocuments upserts every document; key = doc[keyField] (string or integer, formatted base 10) or a UUID.
func (s *Server) ImportDocuments(ctx context.Context, src DocSource, bucket, scope, collection, keyField string) (int, error)
```

`PopulateCollection` becomes `ImportDocuments(ctx, FileSource(path), …, "")`, so `cbctl import` behaviour is unchanged (a test asserts it).

Context: `rest.Client` calls take no `ctx` today, which breaks the project rule. P4 does **not** rewrite `rest`. `serverapply` checks `ctx.Err()` between steps and between documents, and Ctrl-C exits 130 with the completed steps listed. A follow-up issue is filed to thread `ctx` through `rest` (backlog, not 0.7.0).

## 3. `CouchbaseCapella` execution (`capellaapply.Apply`)

**Translation** (`Translate`) builds an in-memory `capellafile` document in the `projects:` form and runs it through `capellafile.LoadText` (schema + defaults + crossref), so every existing check applies. Mapping:

| Manifest | capella.yaml (engine) |
|---|---|
| `spec.organizationId` | `organizationId` |
| `spec.project` | `projects[0].name` |
| `metadata.name` | `projects[0].clusters[0].name` |
| `provider`, `region`, `cidr` | `cloudProvider.{type,region,cidr}`. Missing cidr: the free-/24 picker already used by managed `cluster create`, run online before planning. |
| `availability` | `availability.type` |
| `serverVersion` | `couchbaseServer.version` |
| `description` | `description` |
| `supportPlan.{plan,timezone}` | `support.{plan,timezone}` |
| `deletionProtection` | `deletionProtection` (only when set, C18) |
| `size` | `presets.Lookup(size)` → `configurationType`, `availability`, one `serviceGroups` entry |
| `serviceGroups[].{services,nodes,compute{cpu,ramGb},disk{type,sizeGb,iops,autoExpansion}}` | `serviceGroups[].{services,numOfNodes,node.compute{cpu,ram},node.disk{type,storage,iops,autoExpansion}}` |
| `allowedCidrs[].{cidr,comment,expiresAt,allowAnyIp}` | `allowedCidrs[]` same, plus the cluster `allowAnyIp` when any entry needs it |
| `buckets[].{name,type,memoryMb,replicas,storageBackend,durability,ttl,flush}` | `buckets[].{name,type,memoryAllocationInMb,replicas,storageBackend,durabilityLevel,timeToLiveInSeconds,flushEnabled}` |
| `scopes[].collections[].{name,maxTtl}` | `scopes[].collections[].{name,maxTTL}` |
| `credentials[]` `privileges read\|write\|read+write`, `bucket/scope/collections/allBuckets` | `databaseCredentials[].access[]` with `privileges [data_reader]` / `[data_writer]` / both, plus `resources.buckets[…]`. The mapping already exists in `capella_credential.go` and moves into `resources` or `capellafile` as an exported helper. |
| `wait.timeout`, `wait.enabled` | `RunContext.Timeout`, `RunContext.NoWait = !enabled` |
| `import` | not translated: run after `Execute` |

Path mapping: every translated value records its manifest pointer (`map[capellafile.Path]string`). `capellafile` issues and plan notes are therefore re-pointed to manifest lines, so a user never sees capella.yaml paths.

**Engine run**:
1. `plan.BuildPlan(ctx, rc, resources.BuildTreeWith(opts), plan.Options{})` with `rc.Ephemeral = true`.
2. Immutable pre-check (C7) → exit 3.
3. Blocked → exit 4. Confirmation (C13).
4. `plan.Execute`. `pinCreatedIDs` and every file hook are skipped under `Ephemeral`. `Unmanaged` resources are reported as `[info]` "left in place" (spec: removed items are reported). Prune never runs.

Then the imports. Each runs through `capella_legacy_sdk` connect with `couchbases://` and the entry's credential. The steps and rules are the same as Server step 6. Finally, print the connection string (`e.connectionStrings`).

Engine change (small, P5): `plan.RunContext.Ephemeral bool`. The executor and the `credential` reconciler consult it. Generated passwords still go to the credentials dir and are still printed once (`printSecretsOnce`).

## 4. Error handling

| Failure mode | Detected | Surfaced | Recovery | Exit |
|---|---|---|---|---|
| Unknown field, bad type, invalid YAML | `yamlio.UnmarshalStrict` | `[error]` + issue list with lines | fix the file | 3 |
| Rule violation (any kind) | `validate.go` | all issues at once, sorted by document and line | fix | 3 |
| `apiVersion` ≠ `cbctl/v1`, unknown `kind`, no `kind` | `Load` | issue with the C1 hint | — | 3 |
| Secret unset or empty, file missing, bad base64, literal | `Resolve` | issue naming the field, never the value | set the env/file | 3 |
| `keyField` missing, duplicate, wrong type | validation (streams the file) | `data/schedules.jsonl:812: flightId duplicates line 17` | fix the data | 3 |
| Node REST unreachable (8091/18091) | `WaitForNodeAPI` | `[error] - node 10.0.2.10: management API not reachable` + action | rerun (idempotent) | 1 |
| Wrong admin password on an initialized node | REST 401 | `[error]` "credentials rejected by 10.0.1.10" | — | 1 |
| Rebalance failed | `WaitForRebalanceComplete` | `[error]` with the REST reason | rerun | 1 |
| Server group on Community | REST 4xx | C8 message | — | 1 |
| Capella API / transport | `capellaapi` typed errors | existing `HandleError` cases | rerun | 1 |
| Capella blocked change, protection-off or scale-in without `-y` and no TTY | plan / C13 | existing blocked rendering | add `-y` or fix | 4 |
| Capella immutable change | pre-check | issue at the manifest line | — | 3 |
| Wait timeout | `engine.WaitTimeout` | existing message; the operation continues in Capella | rerun | 5 |
| Import failure partway | `ImportDocuments` error | `[error] - import into b.s.c failed after N documents: …` + "the next apply skips this collection; empty it to reload" | as the spec says | 1 |
| Capella import cannot connect | SDK timeout | C4 hint | add the CIDR | 1 |
| Ctrl-C | ctx | completed steps listed, `interrupted` status in JSON | rerun | 130 |
| Credential secret file not git-ignored | `secrets.NotIgnoredError` | existing message | ignore `.cbctl/` | 4 |

## 5. Edge cases

- One node, no buckets: steps 2, 4, 5 and 6 are `skipped`. A second run is all `unchanged`, exit 0, and makes no REST writes. Asserted with the `rest` fake by counting POSTs.
- A primary that is already a member of a *different* cluster: REST reports it initialized with a different cluster UUID. Exit 1 "node already belongs to another cluster", with no reconcile.
- A node listed but already in the cluster under its external address: compare internal and alternate hostnames before joining.
- `nodes` order changed between runs: no effect (joins are idempotent).
- `ipAddress` given as a hostname versus an IP in the REST host list: normalise with `rest.ParseHostPort` and compare case-insensitively.
- An empty `import.data: []`: exit 3 ("data must not be empty"). Empty JSONL file: imports 0, `[info]`.
- Huge inline `data` (10k items): streamed into the SDK and not held twice. The YAML size cap is 10 MiB.
- `keyField` integer `1e3` in YAML (a float): rejected unless integral. `-0` → `0`.
- `-f -` with relative `file:` imports resolves from cwd (spec). With `-f dir/m.yaml` it resolves from `dir/`.
- Multiple documents: Capella auth is resolved once and reused. Two Capella documents in different orgs both work (per-document `organizationId`).
- Concurrency: documents run sequentially. Within Server, joins are sequential (rebalance safety). Imports use the SDK sequentially, as today. A worker pool is a later optimisation and is not needed.
- Partial failure across documents: earlier documents stay applied. The report shows `ok` / `failed` / `skipped` for each.

## 6. Acceptance criteria

P3 (model):
1. Every Validation bullet in the spec has a table test row (valid and invalid fixture). Every invalid fixture has a golden issue text with line and column.
2. Unknown field anywhere → exit 3 with the field path and a suggestion. Multi-document: an error in doc 2 means no document is applied (asserted with fakes recording zero calls).
3. `${base64:…}`: valid, invalid padding, and empty cases. Neither the decoded nor the encoded value appears in any output (redaction canary test, as in `TestRedaction_CAP008_*`).
4. Inline `data` conversion: timestamp → RFC 3339, anchors expanded, `!!binary` and non-string keys exit 3.

P4 (Server):
5. Docker IT (`-tags=integration,server`): the 1-node manifest with bucket, scopes, collections, `file` and `data` imports with `keyField`. First run exit 0 with `[ok]` lines. The second run makes no changes, exit 0, imports nothing ("not empty"). Documents are readable by key.
6. Multi-node IT (2 containers on a docker network, `CBCTL_IT_SERVER_MULTI=1`, nightly only): primary + 1 join + rebalance. Arbiter covered by the `rest` fake.
7. `cbctl import` output and behaviour are byte-identical before and after the `ImportDocuments` refactor.

P5 (Capella):
8. Fake: create from scratch (`size: small` and `serviceGroups`). Re-apply with no writes. Scale-out applied. Group removal exit 4. Region change exit 3. Absent `deletionProtection` leaves protection on. Generated credential printed once and saved 0600. Import with credential. JSON matches the schema.
9. `capella.yaml` is never read or written: the test runs in a dir with a `capella.yaml` and asserts identical bytes and mtime. No `.bak` is created.
10. Sandbox: one live run with `CBCTL_IT_CLUSTERS=1`, project prefix `cbctl-ci-`, cleaned by the janitor.

Docs: README section "Manifests (`cbctl apply`)", `docs/manifests.md`, `cbctl.mdc` command map line `cbctl apply -f FILE|- [-o json] [-y]` plus behaviour bullets, and a guide topic `cbctl capella guide manifests` (optional).
