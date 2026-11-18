# cbctl Manifest Spec — `cbctl/v1`

> **Amended for 0.7.0.** This copy of `cbctl-apply-manifest-spec.md` is the checked-in contract for `cbctl apply -f`. It differs from the original in the places listed under [Amendments](#amendments-for-070) at the end of the file (decision D3 and the conflict resolutions C1–C22 of [02-manifest-apply.md](02-manifest-apply.md)). Where the body and an amendment disagree, the amendment wins; the body text below already reflects the amendments that change a field or a default.

## Overview

`cbctl apply -f FILE` reconciles a cluster to a declarative manifest in a single invocation. The manifest follows the Kubernetes resource shape (`apiVersion`, `kind`, `metadata`, `spec`). Two kinds are defined in v1:

| Kind | Target | Replaces |
|---|---|---|
| `CouchbaseServer` | Couchbase Server on already-running instances | `cluster create` → `cluster join` (per node) → `cluster rebalance` → `keyspace create` |
| `CouchbaseCapella` | A Capella provisioned cluster | `capella project create` → `capella cluster create` → `capella cidr create` → `capella bucket/scope/collection add` → `capella credential create` |

```bash
cbctl apply -f couchbase.yaml
cbctl apply -f -              # read the manifest from stdin
```

`-f` is the only argument that selects or shapes what is applied. Everything else comes from the manifest, the environment, or (for Capella auth) the selected profile. The global output and confirmation flags (`-o`, `-y`, `--color`, `--log-level`, C14) and `--dry-run` (C17) are accepted.

## Common rules

### Envelope

```yaml
apiVersion: cbctl/v1          # required; exact match
kind: CouchbaseServer         # required; CouchbaseServer | CouchbaseCapella
metadata:
  name: airline-ops           # required; cluster name
  labels:                     # optional; informational only, not sent to the cluster
    team: field-eng
spec: {}                      # required; shape depends on kind
```

| Field | Rule |
|---|---|
| `apiVersion` | `cbctl/v1`. Any other value exits 3. `capella.yaml` uses the same value, so the two are told apart by `kind` (C1): a file with no `kind` exits 3 with a hint to use `cbctl capella apply -c FILE`. Later versions are new values (`cbctl/v2`); v1 manifests keep working unchanged. |
| `kind` | `CouchbaseServer` or `CouchbaseCapella`. Case-sensitive. |
| `metadata.name` | Kebab case: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 1–63 characters. Used as the cluster name. |
| `metadata.labels` | Map of string to string. Not applied anywhere; kept for tooling. |

### Parsing

- Unknown fields anywhere in the document exit 3 (strict decoding), so typos fail before any change is made.
- A file may hold several documents separated by `---`; they are validated together, then applied in file order. Any validation failure applies nothing. Kinds may be mixed; two documents with the same `kind` and `metadata.name` exit 3 (C12). A failure while applying document N stops later documents (exit 1) and the report lists them as skipped.
- `apply` is idempotent: a second run against an unchanged manifest makes no changes and exits 0.

### Secrets

Passwords are never literal. Every password field takes a reference resolved at apply time:

| Form | Resolves to |
|---|---|
| `${VAR}` | Environment variable `VAR`; unset or empty exits 3 |
| `${file:PATH}` | Contents of `PATH` (relative paths resolve from the manifest's directory), trailing newline stripped |
| `${base64:VALUE}` | `VALUE` decoded as standard base64 (RFC 4648, padding required); invalid base64 or an empty result exits 3 |

A literal value in a password field exits 3. Output redacts all resolved secrets, including the encoded `VALUE`. `${VAR}` and `${file:PATH}` match `capella.yaml`; `${base64:VALUE}` is new to manifests and is not accepted in `capella.yaml` in 0.7.0 (C16). A password reference that is not a string, or a missing required password, also exits 3.

Base64 is an encoding, not encryption: a manifest that contains `${base64:…}` holds the password and must be handled as a secret (mode 0600, not committed). Its purpose is to carry passwords containing YAML- or shell-significant characters through generated files without quoting problems.

### Bucket data import

Both kinds accept an optional `import` list on each bucket. Each entry loads documents into one collection of that bucket, from a JSON Lines file or from documents written inline in the manifest.

```yaml
buckets:
  - name: flights
    scopes:
      - name: ops
        collections: [schedules, gates]
    import:
      - scope: ops
        collection: schedules
        file: data/schedules.jsonl
        keyField: flightId
      - scope: ops
        collection: gates
        keyField: gateId
        data:
          - gateId: DAL-E10
            terminal: E
            status: open
          - gateId: DAL-E12
            terminal: E
            status: maintenance
```

| Field | Type | Default | Notes |
|---|---|---|---|
| `import[].scope` | string | `_default` | Must exist in the bucket's `scopes` or already exist in the cluster. |
| `import[].collection` | string | `_default` | Same rule as `scope`. |
| `import[].file` | path | — | JSON Lines file: one JSON object per line; blank lines are skipped. Relative paths resolve from the manifest's directory (from the working directory with `-f -`). The path is read on the host running cbctl. |
| `import[].data` | list | — | YAML sequence; each item is a mapping that becomes one document. |
| `import[].keyField` | string | none | Top-level field whose value is the document key. Omitted: keys are assigned as `cbctl import` assigns them. |

Rules:

- Each entry sets exactly one of `file` or `data`.
- Each `scope`/`collection` pair appears at most once across a bucket's `import` list.
- Every `data` item is a mapping. Values convert to JSON types: YAML timestamps become RFC 3339 strings; binary (`!!binary`) and non-string mapping keys exit 3. Anchors and aliases are expanded.
- `${…}` references are not resolved inside `data` or in imported files; they are stored as literal text.
- With `keyField`, every document must have that field as a non-empty string or integer, and keys must be unique within the entry; a violation exits 3 before anything is written. For `file`, this check reads the whole file during validation.
- An import runs only when its target collection is empty, matching `cbctl import`. A non-empty collection is reported and skipped, so a second `apply` loads nothing twice. To reload, empty the collection first.
- Imports run after the bucket, scope and collection exist. A failure partway through exits 1; the documents already written stay, and the next `apply` skips that collection because it is no longer empty.

### Service names

Both kinds use one service vocabulary: `data`, `index`, `query`, `search`, `analytics`, `eventing`. `CouchbaseServer` also accepts `backup` and treats `fts` as an alias of `search`. A service list is a set: order and duplicates do not matter.

### Exit codes

Same as the rest of cbctl: `0` success or no-op, `1` API/transport/wait failure, `3` invalid manifest or usage, `4` blocked (protection, confirmation without a terminal), `5` wait timed out, `130` interrupted.

---

## Kind: `CouchbaseServer`

Configures Couchbase Server on instances that are installed and running but not necessarily initialized. `apply` calls the management REST API on each node's `ipAddress`, so it can run on a cluster node or on any host that reaches port 8091 (18091 with TLS).

### Example

```yaml
apiVersion: cbctl/v1
kind: CouchbaseServer
metadata:
  name: airline-ops
spec:
  credentials:
    username: Administrator
    password: ${CB_PASSWORD}
  tls:
    enabled: false
  nodeDefaults:
    services: [data, index, query, search]
    dataPath: /data/couchbase
  nodes:
    - ipAddress: 10.0.1.10
      externalIpAddress: 203.0.113.10
      serverGroup: us-east-1a
      primary: true
    - ipAddress: 10.0.2.10
      externalIpAddress: 203.0.113.11
      serverGroup: us-east-1b
    - ipAddress: 10.0.3.10
      externalIpAddress: 203.0.113.12
      serverGroup: us-east-1c
      services: [index, query]
      ramGiB: 32
    - ipAddress: 10.0.3.11
      serverGroup: us-east-1c
      arbiter: true
  rebalance: true
  buckets:
    - name: flights
      quotaMiB: 1024
      replicas: 1
      scopes:
        - name: ops
          collections: [schedules, gates]
      import:
        - scope: ops
          collection: schedules
          file: data/schedules.jsonl
          keyField: flightId
```

### Field reference

| Field | Type | Default | Notes |
|---|---|---|---|
| `spec.credentials.username` | string | `Administrator` | Full admin user set at initialization. |
| `spec.credentials.password` | secret ref | required | See Secrets. |
| `spec.tls.enabled` | bool | `false` | `true` = `--ssl`, `false` = `--no-ssl`. |
| `spec.tls.caFile` | path | system trust | Optional CA bundle for `tls.enabled: true`; exits 3 when set with `enabled: false` (C20). Relative paths resolve from the manifest's directory. |
| `spec.nodeDefaults.services` | list | `[data, index, query, search]` | Applied to every node that sets neither `services` nor `arbiter`. |
| `spec.nodeDefaults.dataPath` | string | Couchbase default | Shared path for data, index, analytics and eventing. |
| `spec.nodeDefaults.ramGiB` | int | node's `memoryTotal` | Overrides the RAM used for quota calculation (containers). |
| `spec.nodes` | list | required | 1 or more. Order is join order, with the primary skipped (C10). |
| `spec.nodes[].ipAddress` | IP/host | required | Management address of the node. Unique across `nodes`. |
| `spec.nodes[].externalIpAddress` | IP/host | none | Alternate (external) address. |
| `spec.nodes[].serverGroup` | string | `Group 1` | Created if missing. Needs Enterprise Edition: on Community a non-default group exits 1 (C8). |
| `spec.nodes[].services` | list | `nodeDefaults.services` | Overrides the default for this node. |
| `spec.nodes[].dataPath` | string | `nodeDefaults.dataPath` | |
| `spec.nodes[].ramGiB` | int | `nodeDefaults.ramGiB` | |
| `spec.nodes[].arbiter` | bool | `false` | Manager-only node (Server 7.6+). Cannot be combined with `services`. |
| `spec.nodes[].primary` | bool | `false` | The node that initializes the cluster and serves as the rally address. At most one; if none is set, the first node is primary. |
| `spec.rebalance` | bool | `true` | `false` adds nodes without rebalancing them in. |
| `spec.buckets` | list | none | Optional data model, created after rebalance. |
| `spec.buckets[].name` | string | required | |
| `spec.buckets[].quotaMiB` | int | `128` | |
| `spec.buckets[].replicas` | int | `0` | 0–3. |
| `spec.buckets[].scopes[].name` | string | required | `_default` is a no-op. |
| `spec.buckets[].scopes[].collections` | list of string | none | `_default` is a no-op. |
| `spec.buckets[].import` | list | none | See Bucket data import. Written with the admin credentials. |

Units mirror each product's own API: Server uses `ramGiB` (GiB) and `quotaMiB` (MiB); Capella uses `ramGb`, `sizeGb` and `memoryMb` (C22).

### Validation

- At least one node; exactly one primary after defaulting.
- The primary is not an arbiter.
- At least one node runs `data`.
- `ipAddress` values are unique.
- `arbiter: true` with `services` exits 3.
- `ramGiB` > 0; `quotaMiB` ≥ 100; `replicas` 0–3.
- `backup` (Server only) has no memory quota and is passed to REST as `backup` (C11).
- `metadata.name` becomes the cluster name (C9), so Terraform's `var.cluster_name` must satisfy the name rule.

### Apply sequence

| Step | Equivalent command | Notes |
|---|---|---|
| 1. Load and validate | — | Offline. Resolves secrets. Nothing is contacted on failure. |
| 2. Initialize primary | `cluster create --name <metadata.name> --ip-address <primary.ipAddress> --external-ip-address … --services … --server-group … --data-path … --ram …` | Already initialized: reconciles quotas, external address and server group. |
| 3. Join each other node | `cluster join --rally-ip-address <primary.ipAddress> --ip-address <node.ipAddress> …` (`--arbiter` for arbiters) | In `nodes` order. Already a member: reconciles external address and server group. |
| 4. Rebalance | `cluster rebalance --rally-ip-address <primary.ipAddress>` | Once, after all joins. Skipped when `rebalance: false` or no node is pending. |
| 5. Data model | `keyspace create <bucket>.<scope>.<collection>` per collection | Existing buckets, scopes and collections are left as they are. |
| 6. Import | `import <file> <bucket>.<scope>.<collection>` per entry | `data` entries are written the same way. Skipped for non-empty collections. |

`apply` never removes nodes, buckets, scopes or collections. A node in the cluster but not in the manifest is reported and left in place.

### Terraform usage

The three `remote-exec` command lists collapse into one manifest and one command:

```hcl
locals {
  couchbase_manifest = yamlencode({
    apiVersion = "cbctl/v1"
    kind       = "CouchbaseServer"
    metadata   = { name = var.cluster_name }
    spec = {
      credentials  = { username = "Administrator", password = "$${base64:${base64encode(local.password)}}" }
      tls          = { enabled = false }
      nodeDefaults = { dataPath = var.data_path }
      nodes = [
        for node in local.nodes : {
          primary           = node.key == local.primary_node_key
          ipAddress         = aws_instance.node_group[node.key].private_ip
          externalIpAddress = aws_instance.node_group[node.key].public_ip
          services          = node.services
          serverGroup       = aws_instance.node_group[node.key].availability_zone
        }
      ]
    }
  })
}

provisioner "file" {
  content     = local.couchbase_manifest
  destination = "/tmp/couchbase.yaml"
}

provisioner "remote-exec" {
  inline = [
    "chmod 600 /tmp/couchbase.yaml",
    "sudo /usr/local/bin/cbctl apply -f /tmp/couchbase.yaml",
    "rm -f /tmp/couchbase.yaml",
  ]
}
```

`$${base64:…}` escapes Terraform interpolation for the reference itself while `${base64encode(local.password)}` is still evaluated, so the manifest contains `${base64:<encoded password>}`. The password travels in the file rather than on the command line, so the manifest is restricted to 0600 and removed after apply. To keep the password out of the file, use `password = "$${CB_PASSWORD}"` and run `sudo CB_PASSWORD='${local.password}' /usr/local/bin/cbctl apply -f /tmp/couchbase.yaml`. `var.cluster_name` must satisfy the `metadata.name` rule.

---

## Kind: `CouchbaseCapella`

Deploys or reconciles one provisioned Capella cluster through the Capella Management API v4.

### Authentication

Not part of the manifest. Resolved as for every `cbctl capella` command: API key from `CAPELLA_API_KEY` → `CAPELLA_TOKEN` → keychain for the profile; profile from `CBCTL_PROFILE` → `default`; API host from `CAPELLA_API_BASE_URL` → profile → `https://cloudapi.cloud.couchbase.com`. Organization from `spec.organizationId` → `CAPELLA_ORG_ID` → profile → auto-discovery.

### Relationship to `capella.yaml`

A `CouchbaseCapella` manifest is a self-contained, single-cluster declaration. `cbctl apply -f` does not read or write `capella.yaml`, does not pin ids into the manifest, and does not trigger file-managed mode. The cluster is identified by `spec.project` + `metadata.name`. Use `capella.yaml` with `cbctl capella apply` for multi-cluster estates, schedules and backups.

### Example

```yaml
apiVersion: cbctl/v1
kind: CouchbaseCapella
metadata:
  name: airline-ops-prod
spec:
  project: airline-demo
  provider: aws
  region: us-east-1
  cidr: 10.0.10.0/24
  availability: multi
  description: Airline ops demo
  supportPlan:
    plan: developer pro
    timezone: PT
  deletionProtection: false
  serviceGroups:
    - services: [data]
      nodes: 3
      compute: { cpu: 8, ramGb: 32 }
      disk: { type: gp3, sizeGb: 100, iops: 3000, autoExpansion: true }
    - services: [index, query]
      nodes: 2
      compute: { cpu: 8, ramGb: 32 }
    - services: [search]
      nodes: 2
      compute: { cpu: 4, ramGb: 16 }
  allowedCidrs:
    - cidr: 203.0.113.0/24
      comment: office
  buckets:
    - name: flights
      memoryMb: 1024
      replicas: 1
      storageBackend: magma
      scopes:
        - name: ops
          collections:
            - name: schedules
            - name: gates
              maxTtl: 86400
      import:
        - scope: ops
          collection: gates
          credential: app
          keyField: gateId
          data:
            - gateId: DAL-E10
              terminal: E
              status: open
  credentials:
    - name: app
      password: ${APP_DB_PASSWORD}
      access:
        privileges: read+write
        bucket: flights
        scope: ops
  wait:
    timeout: 45m
```

Single-group alternative, using a preset in place of `serviceGroups`:

```yaml
spec:
  project: airline-demo
  provider: gcp
  region: us-east1
  size: small
```

### Field reference

| Field | Type | Default | Notes |
|---|---|---|---|
| `spec.organizationId` | string | from env/profile | |
| `spec.project` | string | required | Created if it does not exist. |
| `spec.provider` | enum | `aws` | `aws`, `gcp`, `azure`. Immutable. |
| `spec.region` | string | `us-east-1` / `us-east1` / `eastus` | Provider default. Immutable. |
| `spec.cidr` | CIDR | first free `/24` | Cluster network. Overlap with another cluster in the project exits 3. Immutable. |
| `spec.availability` | enum | `multi`; `single` for `size: dev` | `multi` or `single`. Immutable. `size: dev` with `availability: multi` exits 3 (C2). |
| `spec.serverVersion` | string | Capella default | Immutable. |
| `spec.description` | string | none | Applied on create; absent on an existing cluster leaves Capella's value (C18). |
| `spec.supportPlan.plan` | enum | `developer pro` | `basic`, `developer pro`, `enterprise`. |
| `spec.supportPlan.timezone` | enum | `PT` | `ET`, `GMT`, `IST`, `PT`. |
| `spec.deletionProtection` | bool | `false` on create | An absent field leaves an existing cluster's value (C18). Turning protection off needs `-y` without a terminal (C13). |
| `spec.size` | enum | — | `dev`, `small`, `medium`, `large`. Mutually exclusive with `serviceGroups`. |
| `spec.serviceGroups` | list | — | Required unless `size` is set. |
| `spec.serviceGroups[].services` | list | required | The set identifies the group; each group's set is unique. |
| `spec.serviceGroups[].nodes` | int | required | First group 3–27, others 2–24, cluster total ≤ 27. These limits apply to `serviceGroups` only; a `size` preset supplies its own node count (C2). |
| `spec.serviceGroups[].compute.cpu` | int | required | vCPU; must pair with `ramGb` as a size listed by `capella cluster options`. |
| `spec.serviceGroups[].compute.ramGb` | int | required | |
| `spec.serviceGroups[].disk.type` | string | provider default | `gp3`/`io2` (AWS), `pd-ssd` (GCP), `P6`–`P60`/`Ultra` (Azure). |
| `spec.serviceGroups[].disk.sizeGb` | int | provider default | Not allowed for fixed-size Azure types. |
| `spec.serviceGroups[].disk.iops` | int | provider default | Not allowed for GCP or fixed-size Azure types. |
| `spec.serviceGroups[].disk.autoExpansion` | bool | `true` on create (AWS and GCP) | Sent on create only; existing clusters are left alone (C21). |
| `spec.allowedCidrs[].cidr` | CIDR/IP | required | Bare IP becomes `/32`. `0.0.0.0/0` and `::/0` require `allowAnyIp: true`. |
| `spec.allowedCidrs[].comment` | string | none | |
| `spec.allowedCidrs[].expiresAt` | RFC 3339 | none | |
| `spec.allowedCidrs[].allowAnyIp` | bool | `false` | |
| `spec.buckets[].name` | string | required | |
| `spec.buckets[].type` | enum | `couchbase` | `couchbase`, `ephemeral`. |
| `spec.buckets[].memoryMb` | int | `1024` | |
| `spec.buckets[].replicas` | int | `1`; `0` on a single-node cluster | 0–3. A value above 0 on a single-node cluster exits 3 (C3). |
| `spec.buckets[].storageBackend` | enum | Capella default | `couchstore`, `magma`. Immutable. |
| `spec.buckets[].durability` | enum | `none` | `none`, `majority`, `majorityAndPersistActive`, `persistToMajority`. |
| `spec.buckets[].ttl` | int | `0` | Seconds. |
| `spec.buckets[].flush` | bool | `false` | |
| `spec.buckets[].scopes[].name` | string | required | |
| `spec.buckets[].scopes[].collections[].name` | string | required | |
| `spec.buckets[].scopes[].collections[].maxTtl` | int | `0` | Seconds. |
| `spec.buckets[].import` | list | none | See Bucket data import. |
| `spec.buckets[].import[].credential` | string | required with `import` | Name of a `spec.credentials` entry with `write` or `read+write` access to the target. Its password must be a reference, not generated, so apply can use it. The host running cbctl must be inside `allowedCidrs`; this is not checked offline and no entry is added implicitly (C4). |
| `spec.credentials[].name` | string | required | |
| `spec.credentials[].password` | secret ref | generated | Omitted: a 32-character password is generated, printed once, and saved under `.cbctl/credentials` (mode 0600), relative to the manifest's directory (C5). `cbctl apply` never changes the password of an existing credential (D3, C6); rotate with `cbctl capella credential update --rotate-password`. |
| `spec.credentials[].access.privileges` | enum | required | `read`, `write`, `read+write`. |
| `spec.credentials[].access.bucket` | string | — | Required unless `allBuckets: true`. |
| `spec.credentials[].access.scope` | string | all scopes | |
| `spec.credentials[].access.collections` | list | all collections | Requires `scope`. |
| `spec.credentials[].access.allBuckets` | bool | `false` | Mutually exclusive with `bucket`. |
| `spec.wait.timeout` | duration | `60m` | Max `24h`. Exceeding it exits 5; the Capella operation continues. |
| `spec.wait.enabled` | bool | `true` | `false` returns once Capella accepts each request. |

### Validation

Offline, before any API call:

- `size` xor `serviceGroups`.
- At least one service group runs `data`; it is ordered first.
- Node-count limits per group and per cluster.
- `compute` pair and `disk` fields valid for `provider`.
- Bucket memory total fits the data group's RAM.
- Every `import[].credential` names a `credentials` entry that grants write access to the import target and has a `password` reference.
- Every `credentials[].access.bucket`/`scope`/`collections` reference exists in `buckets` or already exists in the cluster (checked online).

### Apply sequence

| Step | Equivalent command | Notes |
|---|---|---|
| 1. Load and validate | `capella validate` checks | Resolves secrets and auth. |
| 2. Ensure project | `capella project create` | No-op when present. |
| 3. Create or reconcile cluster | `capella cluster create` / `cluster update` / `cluster scale` / `cluster group add` | Waits until healthy. |
| 4. Deletion protection | `capella cluster protect --on\|--off` | |
| 5. Allowed CIDRs | `capella cidr create` | Present entries are a no-op. |
| 6. Data model | `capella bucket add`, `scope add`, `collection add` | Existing buckets get `bucket update` for changed mutable fields. |
| 7. Credentials | `capella credential create` / `update` | After the data model, so access references resolve. A credential that exists keeps its password: `apply` prints `credential X exists; password not changed` and never rotates it in v1 (D3). Access changes are applied; passwords are not. |
| 8. Import | `cbctl import` against the connection string with `--ssl` | Uses each entry's `credential`. Skipped for non-empty collections. |
| 9. Report | — | Prints the connection string. |

### Change rules on an existing cluster

| Change | Result |
|---|---|
| `nodes`, `compute`, `disk` of an existing group | Applied via `cluster scale` / `cluster update`; capacity checks apply. |
| New service group | `cluster group add`. |
| Group removed from the manifest | Refused, exit 4. |
| `provider`, `region`, `cidr`, `availability`, `serverVersion`, `storageBackend` | Refused, exit 3, before anything is planned (C7). |
| Bucket, scope, collection, CIDR or credential removed from the manifest | Left in place and reported. |

`apply -f` never deletes resources in v1.

---

## Amendments for 0.7.0

These are the decisions that differ from, or complete, the original text. The numbering follows [02-manifest-apply.md](02-manifest-apply.md) §5.

### D3 — credential passwords are never rotated by `apply`

`cbctl apply` never rotates the password of an existing Capella credential in v1. Capella never returns a password and `apply` keeps no state, so a changed `${VAR}` value cannot be detected. Step 7 and `spec.credentials[].password` say so above. To rotate, run `cbctl capella credential update <cluster> <name> --rotate-password` (or `--password-env` / `--password-file`). Server cluster passwords are only used to initialize and connect; `apply` does not change the password of an initialized cluster either.

### Conflict resolutions

| # | Resolution |
|---|---|
| C1 | A document with no `kind` exits 3 with a hint to use `cbctl capella apply -c FILE`; capella commands given a file with `kind` exit 3 with the opposite hint. |
| C2 | `size: dev` is single-node with `availability: single`. `size: dev` with `availability: multi` exits 3. The node-count limits apply to `serviceGroups` only. |
| C3 | Capella bucket `replicas` defaults to 1, or 0 on a single-node cluster; a value above 0 there exits 3. |
| C4 | Whether the host running cbctl is in `allowedCidrs` is not checked offline. An import that cannot connect exits 1 with the hint "is this host in spec.allowedCidrs?". No allow-list entry is added implicitly. |
| C5 | Generated credential passwords are saved under `.cbctl/credentials` next to the manifest. The git-ignore check applies (exit 4 unless the file is ignored). An existing file for a new credential exits 4. There is no overwrite flag in v1. |
| C6 | See D3. |
| C7 | Immutable changes are checked against the observed cluster before planning and exit 3. A removed service group is refused with exit 4. |
| C8 | A non-default `serverGroup` on Community Edition exits 1 ("server groups need Enterprise Edition"). |
| C9 | `metadata.name` is the Server cluster name. |
| C10 | Join order is `nodes` order with the primary skipped. |
| C11 | `backup` has no quota; it is passed to REST as `backup`. |
| C12 | Kinds may be mixed in one stream and are applied in order. Capella auth is resolved only if a Capella document exists. Two documents with the same kind and name exit 3. A failure in document N stops later documents (exit 1), which are reported as skipped. |
| C13 | Server never prompts. Capella shows the plan on a terminal and asks for confirmation; without a terminal it proceeds for create, update and scale-out. `-y` is required (exit 4 otherwise) to turn `deletionProtection` from `true` to `false` and for any scale-in. |
| C14 | The global output and confirmation flags are accepted next to `-f`. |
| C15 | `spec.wait.timeout` is the only timeout; `apply` has no `--timeout`. |
| C16 | `${base64:…}` is accepted in manifests only, not in `capella.yaml`. |
| C17 | `--dry-run` validates, and for Capella plans (exit 2 when there are changes), if the flag is adopted for 0.7.0. |
| C18 | Presence semantics: a field that is absent from the manifest leaves the value of an existing cluster alone. Defaults (including `deletionProtection: false` and an empty `description`) apply on create only. This deliberately differs from the original defaults column. |
| C19 | Imports without `keyField` use generated keys, as `cbctl import` does; after a partial failure, empty the collection to reload. |
| C20 | `spec.tls.caFile` is an optional additive field; without it the system trust store is used. |
| C21 | `disk.autoExpansion` is sent on create only, and only for providers that support it. |
| C22 | Units are kept as in the original, each mirroring its product's API (`ramGiB`, `quotaMiB`; `ramGb`, `sizeGb`, `memoryMb`). |
