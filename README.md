# cbctl

Couchbase Server CLI for provisioning and inspecting clusters and keyspaces.

Capella cloud operations are available under `cbctl capella` (see [Capella](#capella)).

## Install

**Release binaries** (recommended):

```bash
# Example: macOS Apple Silicon
curl -sL "https://github.com/mminichino/cbctl/releases/latest/download/cbctl_Darwin_arm64.tar.gz" \
  | tar xz
sudo mv cbctl /usr/local/bin/
cbctl --version
```

See [Releases](https://github.com/mminichino/cbctl/releases) for Linux, Windows, and other architectures.

**From source:**

```bash
go install github.com/mminichino/cbctl/cmd/cbctl@latest
# or
git clone https://github.com/mminichino/cbctl.git
cd cbctl && make build   # → bin/cbctl
```

## Quick start

```bash
# Initialize a local single-node cluster
cbctl cluster create --host 127.0.0.1 -u Administrator -p password --no-ssl

# Create a keyspace (bucket + scope + collection)
cbctl keyspace create demo.app.orders --host 127.0.0.1 -u Administrator -p password --no-ssl

# Verify connectivity
cbctl cluster test --host 127.0.0.1 -u Administrator -p password --no-ssl

# Import JSON Lines into an empty collection
cbctl import data.jsonl demo.app.orders --host 127.0.0.1 -u Administrator -p password --no-ssl
```

Running a command with no subcommand (or a subgroup with no args) prints help.

## Connection flags

Most commands accept:

| Flag | Default | Notes |
|------|---------|--------|
| `--host` | `127.0.0.1` | Hostname or IP |
| `-u` / `--username` | `Administrator` | Admin user |
| `-p` / `--password` | `password` | Admin password |
| `--ssl` / `--no-ssl` | `--no-ssl` | TLS for management/SDK |

Pass explicit `--host`, credentials, and `--ssl` / `--no-ssl` in scripts unless local defaults are intentional.

## Commands

### Cluster

```bash
cbctl cluster create --host 127.0.0.1 -u Administrator -p password --no-ssl
cbctl cluster join --rally-ip-address 10.0.0.1 --ip-address 10.0.0.2 -p password --no-ssl
cbctl cluster rebalance --rally-ip-address 10.0.0.1 -p password --no-ssl
cbctl cluster exists --host 127.0.0.1 -u Administrator -p password --no-ssl
cbctl cluster map --host 127.0.0.1 -u Administrator -p password --no-ssl
cbctl cluster test --host 127.0.0.1 -u Administrator -p password --no-ssl
```

| Command | Behavior |
|---------|----------|
| `cluster create` | Initialize a Server cluster. Success: `Cluster created on <hosts>`. Already initialized: `Cluster already configured` (exit 0). |
| `cluster join` / `cluster add` | Add one node to an existing cluster (no rebalance). Success: `Node <ip> added to cluster at <rally>`. Already a member: `Node already configured` (exit 0). |
| `cluster rebalance` | Rebalance all known nodes. Success: `Cluster rebalanced`. |
| `cluster exists` | Prints `true` or `false` (lowercase). |
| `cluster map` | Prints the cluster host map from the management REST API. |
| `cluster test` | Connect and KV put/get check. Without `--bucket`, creates/deletes a temporary `__test` bucket. With `--bucket`, uses that existing bucket only. |

`cluster test` options:

- `--external` / `--internal` — mutually exclusive; force SDK network mode (`external` vs internal/`default`)
- `--timeout` — SDK connect timeout in seconds (default `5`; must be > 0)

#### Provisioner mode (create / join / rebalance)

Use these flags when bootstrapping from Terraform/`remote-exec` or a controller host. All calls use the management REST API against the given IPs, so the CLI may run on the node itself or on a remote machine that can reach port 8091 (or 18091 with `--ssl`).

```bash
# Primary node
cbctl cluster create \
  --name my-cluster -p "$PASSWORD" \
  --ip-address 10.0.0.1 --external-ip-address 203.0.113.10 \
  --services data,index,query,fts --server-group us-east-1a \
  --data-path /data/couchbase --no-ssl

# Additional node (alias: cluster add)
cbctl cluster join \
  -p "$PASSWORD" --rally-ip-address 10.0.0.1 \
  --ip-address 10.0.0.2 --external-ip-address 203.0.113.11 \
  --services data,index,query,fts --server-group us-east-1b \
  --data-path /data/couchbase --no-ssl

# After all joins
cbctl cluster rebalance -p "$PASSWORD" --rally-ip-address 10.0.0.1 --no-ssl
```

| Flag | Commands | Notes |
|------|----------|--------|
| `--ip-address` | create, join | Management IP of **this** node (also accepted as `--host` when `--ip-address` is omitted on create) |
| `--external-ip-address` | create, join | External alternate address (alias of `--alternate-address`) |
| `--rally-ip-address` | join, rebalance | Primary/rally management IP |
| `--name` | create | Cluster display name |
| `--server-group` | create, join | Server group / AZ name (Enterprise) |
| `--data-path` | create, join | Shared path for data/index/analytics/eventing |
| `--services` / `-s` | create, join | Comma-separated services (`search` → `fts`) |

Provisioner `create` initializes **only** the primary node (no automatic join/rebalance). Use `join`/`add` for each extra node, then `rebalance` once.

### Bucket / scope / collection

```bash
cbctl bucket create <name> --host 127.0.0.1 -u Administrator -p password --no-ssl
cbctl bucket exists <name> --host 127.0.0.1 -u Administrator -p password --no-ssl
cbctl scope create <name> --bucket <bucket> --host 127.0.0.1 -u Administrator -p password --no-ssl
cbctl scope exists <name> --bucket <bucket> --host 127.0.0.1 -u Administrator -p password --no-ssl
cbctl collection create <name> --bucket <bucket> --scope <scope> --host 127.0.0.1 -u Administrator -p password --no-ssl
cbctl collection exists <name> --bucket <bucket> --scope <scope> --host 127.0.0.1 -u Administrator -p password --no-ssl
```

| Command | Behavior |
|---------|----------|
| `bucket create` | Defaults: `--quota 128` (MiB), `--replicas 0`. |
| `bucket\|scope\|collection exists` | Prints `true` or `false`. |
| `scope create` | Scope `_default` is a no-op. |
| `collection create` | Collection `_default` is a no-op. |

### Keyspace

```bash
cbctl keyspace create <bucket.scope.collection> --host 127.0.0.1 -u Administrator -p password --no-ssl
```

Creates the bucket (defaults `--quota 128`, `--replicas 0`), then the scope and collection.

- Keyspace must be exactly `bucket.scope.collection` (three non-empty parts).
- Scope or collection named `_default` is skipped.
- Prefer `keyspace create` when provisioning bucket + scope + collection together.

### Import

```bash
cbctl import <jsonl-file> <bucket.scope.collection> --host 127.0.0.1 -u Administrator -p password --no-ssl
```

- Ensures the collection exists.
- Refuses non-empty collections: prints `Collection not empty` and exits 0 without importing.
- Success: `Imported N documents`.

## Multi-node / MDS (`cluster create`)

Repeat `--node` / `-n` with:

```text
HOST[=SERVICES][@RAM][#ALTERNATE[;PORTMAP]]
```

- `SERVICES` — comma-separated; default `data,index,query,fts` (from `--services` / `-s` when omitted)
- `RAM` — GiB integer; default `4` (from `--ram` when omitted)
- `ALTERNATE` — external hostname/IP
- `PORTMAP` — `service:port,...` (e.g. `kv:9000,n1ql:9050`)

```bash
cbctl cluster create --node 10.0.0.1 --node 10.0.0.2=data,index@8 \
  -u Administrator -p password --no-ssl

cbctl cluster create --node '10.0.0.1=data,query@4#ext.example.com;kv:11210' \
  -u Administrator -p password --no-ssl

cbctl cluster create --host 127.0.0.1 --services data,index,query,fts --ram 4 \
  -u Administrator -p password --no-ssl
```

Rules:

- Single-node without `--node`: use `--host` and optional `--alternate-address` / `-a` (`HOST` or `HOST;PORTMAP`).
- With `--node`, do not use `--alternate-address`; put `#ALTERNATE` in each node spec.
- `--ext-api`: management REST via alternate address; requires an alternate on at least one node. Primary connect host becomes the first node's alternate when set.

## Capella

Capella commands live under `cbctl capella`:

```bash
cbctl capella cluster create --token <token> --project <project> --database <name> \
  --user-email <email> -u <dbuser> -p <dbpass>

cbctl capella cluster exists --token <token> --project <project> --database <name> \
  --user-email <email> -u <dbuser> -p <dbpass>

cbctl capella cluster destroy --token <token> --project <project> --database <name> \
  --user-email <email> -u <dbuser> -p <dbpass>

cbctl capella bucket create <name> --token <token> --project <project> --database <name> \
  --user-email <email> -u <dbuser> -p <dbpass>

cbctl capella --help
```

Common Capella flags: `--token`, `--api-host`, `--org` / `--org-id`, `--project` / `--project-id`, `--database` / `--database-id`, `--user-email` / `--user-id`, `--allow-cidr`.
## Development

```bash
make build              # bin/cbctl
make test-unit
make test-integration   # Docker required; Couchbase enterprise image
make vet
```
