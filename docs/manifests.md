# Manifests: `cbctl apply -f`

A manifest describes one cluster in a single YAML document. `cbctl apply -f` makes the cluster match it, in one
command, and a second run of the same manifest changes nothing.

```bash
cbctl apply -f couchbase.yaml            # one file
cbctl apply -f manifests/                # every *.yaml / *.yml in the directory, in name order
cat couchbase.yaml | cbctl apply -f -    # stdin (up to 10 MiB, not a terminal)
cbctl apply -f couchbase.yaml --dry-run  # validate and show the plan, change nothing
cbctl apply -f couchbase.yaml -o json    # one JSON report on stdout
```

`-f` can be repeated. The flags `-o`, `-y`, `--color` and `--log-level` work as they do everywhere else.
`--dry-run` is the only flag that is specific to `apply`.

The field-by-field contract is [release-0.7.0/manifest-spec-cbctl-v1.md](specs/release-0.7.0/manifest-spec-cbctl-v1.md).
This page is the user guide.

## Which tool for which file

| File | Command |
|---|---|
| Has `kind:` (`CouchbaseServer` or `CouchbaseCapella`), one cluster per document | `cbctl apply -f` |
| Has no `kind:`, a whole Capella estate (projects, schedules, backups, drift, prune) | `cbctl capella plan / apply / drift -c capella.yaml` |

The two formats are told apart by `kind`. `cbctl apply` given a `capella.yaml` exits 3 and points to
`cbctl capella apply -c`; the `capella` commands given a manifest exit 3 and point to `cbctl apply -f`.
A manifest never reads or writes `capella.yaml`.

## Envelope

```yaml
apiVersion: cbctl/v1            # required
kind: CouchbaseServer           # or CouchbaseCapella
metadata:
  name: airline-ops             # the cluster name: lower case, digits and '-', 1-63 characters
  labels: { team: field-eng }   # optional, informational
spec: {}
```

A file can hold several documents separated by `---`, and the kinds can be mixed. Every document is validated
before the first one is applied. Unknown fields are errors, reported with the file, line and column and, when
it is a typo, a suggestion.

## Kind `CouchbaseServer`

For Couchbase Server instances that are installed and running. `apply` talks to the management REST API of each
node, so it can run on a node or on any host that reaches port 8091 (18091 with TLS).

```yaml
apiVersion: cbctl/v1
kind: CouchbaseServer
metadata:
  name: airline-ops
spec:
  credentials:
    username: Administrator
    password: ${CB_PASSWORD}
  nodeDefaults:
    services: [data, index, query, search]
    dataPath: /data/couchbase
  nodes:
    - ipAddress: 10.0.1.10
      primary: true
      serverGroup: us-east-1a
    - ipAddress: 10.0.2.10
      serverGroup: us-east-1b
    - ipAddress: 10.0.3.10
      serverGroup: us-east-1c
      services: [index, query]
      ramGiB: 32
    - ipAddress: 10.0.3.11
      arbiter: true               # manager-only node, Server 7.6+
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
          file: data/schedules.jsonl     # JSON Lines, relative to the manifest
          keyField: flightId
        - scope: ops
          collection: gates
          keyField: gateId
          data:                          # documents written inline
            - { gateId: E10, status: open }
            - { gateId: E12, status: maintenance }
```

What `apply` does, in order: initialize the primary node (or reconcile it if it is already initialized), join
the other nodes one at a time, rebalance once after the last join (skipped with `rebalance: false` or when
nothing is waiting), create buckets, scopes and collections, then import documents.

- It never removes nodes, buckets, scopes or collections. A node in the cluster that the manifest does not list
  is reported with a `[warning]` and left in place.
- A bucket that exists keeps its settings; a different `quotaMiB` is a `[warning]`.
- A primary that already belongs to a cluster with a different name exits 1 and changes nothing.
- Server groups need Enterprise Edition (a non-default group on Community exits 1).
- The admin password is used to initialize and to connect. `apply` does not change the password of an
  initialized cluster.

## Kind `CouchbaseCapella`

Creates or updates one Capella project, cluster, buckets, scopes, collections, allowed CIDRs and database
credentials, then imports documents. It uses the same plan and apply engine as `cbctl capella apply`, in
memory, and needs a Capella API key like every `capella` command (`cbctl configure` or `CAPELLA_API_KEY`;
organization from `spec.organizationId`, `CAPELLA_ORG_ID` or the profile).

```yaml
apiVersion: cbctl/v1
kind: CouchbaseCapella
metadata:
  name: airline-ops-prod
spec:
  project: airline-demo
  provider: aws
  region: us-east-1
  cidr: 10.0.10.0/24            # leave out to get a 10.x.y.0/24 no cluster in the organization uses
  availability: multi
  deletionProtection: false
  serviceGroups:
    - services: [data]
      nodes: 3
      compute: { cpu: 8, ramGb: 32 }
      disk: { type: gp3, sizeGb: 100, iops: 3000 }
    - services: [index, query]
      nodes: 2
      compute: { cpu: 8, ramGb: 32 }
  allowedCidrs:
    - cidr: 203.0.113.7
      comment: build runner
  buckets:
    - name: flights
      memoryMb: 1024
      replicas: 1
      scopes:
        - name: ops
          collections: [{ name: schedules }]
      import:
        - credential: loader
          scope: ops
          collection: schedules
          file: data/schedules.jsonl
          keyField: flightId
  credentials:
    - name: loader
      password: ${LOADER_PASSWORD}
      access:
        privileges: read+write
        bucket: flights
  wait:
    timeout: 60m
```

A small cluster for development uses a preset instead of `serviceGroups`:

```yaml
apiVersion: cbctl/v1
kind: CouchbaseCapella
metadata:
  name: dev-cluster
spec:
  project: airline-demo
  provider: gcp
  region: us-east1
  size: dev              # dev | small | medium | large; excludes serviceGroups
  buckets:
    - name: flights
      replicas: 0
```

How `apply` treats an existing cluster:

- A field you leave out keeps the value Capella has (`description`, `deletionProtection`, disk auto-expansion).
  Defaults apply only when the cluster is created.
- `provider`, `region`, `cidr`, `availability`, the `serverVersion` release line and a bucket's `storageBackend`
  cannot change. A manifest that differs exits 3 and says which field. Removing a service group exits 4.
- Nodes, compute and disk changes are applied as scale and update operations, with the usual capacity checks.
- `apply` never deletes. A bucket, scope, collection, CIDR or credential that was removed from the manifest is
  left in place.
- On a terminal `apply` shows the plan and asks before it changes anything. Without a terminal, creating,
  updating and scaling out run without `-y`; scaling in, or turning `deletionProtection` from `true` to
  `false`, needs `-y` (exit 4 otherwise).
- `spec.wait.timeout` (default `60m`, maximum `24h`) bounds the wait; a timeout exits 5 and the operation
  continues in Capella. `spec.wait.enabled: false` returns once Capella accepts each request.
- An import connects with the SDK over TLS as the credential named in `import[].credential`. That credential
  needs a password reference (not a generated password) and write access to the target, and the host running
  cbctl must be inside `allowedCidrs`; nothing adds that entry for you. A connect failure hints at
  `spec.allowedCidrs`. Imports run last, so the cluster resources are already applied if one fails.

## Credential passwords

`cbctl apply` never changes the password of an existing database credential (decision D3 of the 0.7.0 plan).
Capella never returns a password and `apply` keeps no state, so it cannot tell that a `${VAR}` changed. When a
credential exists, `apply` still applies access changes and prints `credential X exists; password not changed`.
To rotate a password, run:

```bash
cbctl capella credential update <cluster> <name> --rotate-password
cbctl capella credential update <cluster> <name> --password-env NEW_PASSWORD
```

For a new credential with no `password`, `apply` generates a 32-character one, prints it once on stdout (`-o json`:
`credentialPasswords`) and saves it under `.cbctl/credentials` next to the manifest with mode 0600. If that
directory is inside a git work tree and not ignored, `apply` exits 4 before writing anything.

## Secrets

Passwords are never literal. A password field holds one of:

| Form | Value |
|---|---|
| `${VAR}` | The environment variable `VAR`. Unset or empty exits 3. |
| `${file:PATH}` | The file's contents, trailing newline removed. A relative `PATH` is resolved from the manifest's directory. |
| `${base64:VALUE}` | `VALUE` decoded as standard base64 (padding required). |

All references are resolved before any node or API is contacted, and every resolved value is redacted in
output. `${base64:...}` is accepted in manifests only, not in `capella.yaml`. It carries passwords that contain
YAML or shell characters through generated files; base64 is not encryption, so a manifest that holds one is
itself a secret (mode 0600, not committed). References inside `import` data or in imported files are not
resolved: they are stored as text.

## Importing documents

Each bucket takes an `import` list. An entry names a `scope` and `collection` (default `_default`) and sets
exactly one of `file` (JSON Lines, relative to the manifest, or to the working directory with `-f -`) or `data`
(a YAML list of mappings). With `keyField`, that top-level string or integer field becomes the document key,
and every document must have it and be unique; otherwise keys are generated as `cbctl import` generates them.

An import runs only into an empty collection. A collection that already has documents is skipped with a
`[warning]`, so a second `apply` loads nothing twice. After a failure partway through, empty the collection to
load it again.

## Dry run

`--dry-run` validates the whole manifest and resolves its secrets. For `CouchbaseServer` it lists the steps it
would take and contacts no node. For `CouchbaseCapella` it plans against Capella (reads only) and prints the
plan. Both exit 0 for a valid manifest whether or not there are changes (`apply` does not use the exit code 2 of
`capella plan --detailed-exitcode`); an invalid manifest exits 3 and a blocked plan exits 4. Use it as a check
that a manifest is valid:

```bash
cbctl apply -f couchbase.yaml --dry-run -o json
```

## Output and exit codes

Text mode prints `[ok]`, `[info]` and `[warning]` lines per step. With `-o json`, stdout holds exactly one
document (`apiVersion`, `dryRun`, `status`, `documents[]` with `steps[]`); status lines go to stderr. The schema
is `internal/manifest/testdata/apply.manifest.schema.json`.

| Exit | Meaning |
|---|---|
| 0 | Applied, or nothing to do |
| 1 | An API, transport or wait failure; later documents are reported as `not-run` |
| 3 | Invalid manifest or usage, or an immutable field changed. Nothing was applied |
| 4 | Blocked: confirmation needed without a terminal, a removed service group, an unsafe credentials directory |
| 5 | The wait timed out |
| 130 | Interrupted between steps; run the command again to finish |

Documents are applied in order. The first failure stops the run; earlier documents stay applied.

## Terraform

A `remote-exec` provisioner can write the manifest and apply it, replacing a list of `cluster create`,
`cluster join` and `cluster rebalance` commands. Pass the password as `${base64:...}` so characters in it
cannot break the YAML:

```hcl
locals {
  couchbase_manifest = yamlencode({
    apiVersion = "cbctl/v1"
    kind       = "CouchbaseServer"
    metadata   = { name = var.cluster_name }
    spec = {
      credentials = { username = "Administrator", password = "$${base64:${base64encode(local.password)}}" }
      nodes = [
        for node in local.nodes : {
          primary   = node.key == local.primary_node_key
          ipAddress = aws_instance.node_group[node.key].private_ip
          services  = node.services
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

For Capella, see [capella/terraform.md](capella/terraform.md) on when to use the Terraform provider instead.
