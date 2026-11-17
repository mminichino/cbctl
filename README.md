# cbctl

Couchbase Server CLI for provisioning and inspecting clusters and keyspaces.

Capella cloud operations are available under `cbctl capella` (see [Capella](#capella)).

## Install

**Release binaries** (recommended):

```bash
# Example: macOS Apple Silicon (floating latest release)
curl -sL "https://github.com/mminichino/cbctl/releases/download/latest/cbctl_Darwin_arm64.tar.gz" \
  | tar xz
sudo mv cbctl /usr/local/bin/
cbctl --version

# Pin a version (replace v0.2.0)
# curl -sL "https://github.com/mminichino/cbctl/releases/download/v0.2.0/cbctl_Darwin_arm64.tar.gz" | tar xz
```

See [Releases](https://github.com/mminichino/cbctl/releases) for Linux, Windows, and other architectures. Asset URLs use `/releases/download/<tag>/...` (tag before the filename).

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
| `cluster create` | Initialize a Server cluster. Success: `Cluster created on <hosts>`. Already initialized: reconciles memory quotas, external address, and server group, then `Cluster already configured` (exit 0). |
| `cluster join` / `cluster add` | Add one node to an existing cluster (no rebalance). Success: `Node <ip> added to cluster at <rally>`. Already a member: reconciles external address and server group, then `Node already configured` (exit 0). |
| `cluster rebalance` | Rebalance all known nodes. Success: `Cluster rebalanced`. |
| `cluster exists` | Prints `true` or `false` (lowercase). |
| `cluster map` | Prints the cluster host map from the management REST API. |
| `cluster test` | Connect and KV put/get check. Without `--bucket`, creates/deletes a temporary `__test` bucket. With `--bucket`, uses that existing bucket only. |

`cluster test` options:

- `--external` / `--internal` — mutually exclusive; force SDK network mode (`external` vs internal/`default`)
- `--timeout` — SDK connect timeout in seconds (default `5`; must be > 0)

#### Provisioner mode (create / join / rebalance)

Use these flags when bootstrapping from Terraform/`remote-exec` or a controller host. All calls use the management REST API against the given IPs, so the CLI may run on the node itself or on a remote machine that can reach port 8091 (or 18091 with `--ssl`).

Service memory quotas use 80% of the node's reported `memoryTotal` when `--ram` is omitted. Set `--ram` to a GiB value to override that total — for example when Couchbase runs in a container and the process sees the host or cgroup limit instead of the RAM you want to budget. Enabled services other than Query split the available RAM evenly. Services that are not enabled stay at the API minimum (Data, Index, Search, and Eventing 256 MiB; Analytics 1024 MiB) so they do not keep Couchbase's larger default. Re-running `create` applies that layout again for services on this node, without lowering a quota for a service already running on another node. If `join` or `add` introduces a service the cluster does not already run, that service's quota is raised to an equal share of the available RAM on the node that adds it (`--ram` overrides that node's reported total the same way).

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

# Arbiter / manager-only node (Couchbase Server 7.6+; no services)
cbctl cluster join \
  -p "$PASSWORD" --rally-ip-address 10.0.0.1 \
  --ip-address 10.0.0.3 --arbiter --server-group us-east-1c --no-ssl

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
| `--arbiter` | join | Join with no services (7.6+ arbiter). Cannot combine with `--services` |
| `--ram` | create, join | Node RAM in GiB. Overrides reported `memoryTotal` (containers). Omitted: use `memoryTotal` |

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
- `RAM` — GiB integer. `@RAM` on a spec, or `--ram` when the spec omits it, overrides the node's reported `memoryTotal`. When both are omitted, quotas use `memoryTotal`
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

### Auth

API key precedence (first non-empty wins): `--token` → `CAPELLA_API_KEY` → `CAPELLA_TOKEN` → OS keychain for the selected profile (only if that profile exists in `profiles.yaml`).

Organization ID: `--org-id` → `CAPELLA_ORG_ID` → profile `organizationId` → auto-discover when the key sees exactly one org.

API base URL: `--api-host` (only when explicitly set) → `CAPELLA_API_BASE_URL` → profile `apiBaseUrl` → `https://cloudapi.cloud.couchbase.com`.

Profile name: `--profile` → `CBCTL_PROFILE` → `default`.

Copy `.env.example` to `.env` for local keys (never committed). `cbctl` loads the nearest `.env` without overriding variables already set in the shell.

```bash
cbctl configure --api-key-stdin --org-id <id>   # validate key, save profile + keychain
cbctl configure show
cbctl configure list
cbctl configure remove --profile default -y
cbctl configure migrate         # copy capellactl profiles + keychain API keys (--dry-run, --force, --remove-legacy)
cbctl capella doctor            # offline: list what is left from capellactl (--detailed-exitcode exits 4)
```

**Coming from capellactl.** Its settings keep working, each with a one-time `[warning]` on stderr:

| capellactl | cbctl | Rule |
|---|---|---|
| `BEARER_TOKEN`, `ORG_ID`, `API_BASE_URL` | `CAPELLA_API_KEY`, `CAPELLA_ORG_ID`, `CAPELLA_API_BASE_URL` | Used when the cbctl variable is unset; when both are set to different values the cbctl one wins. |
| `CAPELLACTL_CONFIG`, `CAPELLACTL_PROFILE`, `CAPELLACTL_QUIET`, `CAPELLACTL_HOME` | `CBCTL_CONFIG`, `CBCTL_PROFILE`, `CBCTL_QUIET`, `CBCTL_HOME` | Same. |
| `~/.config/capellactl/profiles.yaml` (`$XDG_CONFIG_HOME`, `%APPDATA%`, `CAPELLACTL_HOME`) | `~/.config/cbctl/profiles.yaml` | A profile cbctl does not have is read from capellactl's file. |
| Keychain service `capellactl` | Keychain service `cbctl` | A key cbctl's keychain lacks is read from capellactl's entry for the same profile. |
| `./.capellactl/credentials` | `./.cbctl/credentials` | Without `credentialsOutput`, capellactl's directory is used while `.cbctl/credentials` does not exist. |
| `apiVersion: capellactl/v1` | `apiVersion: cbctl/v1` | Both are accepted; `sync` keeps the file's version (`--api-version` changes it). |

`cbctl configure migrate` copies the profiles and keys for good (a profile that differs in cbctl is a conflict, exit 4, unless `--force`); keys are never printed. capellactl settings are only consulted when cbctl uses its default home (no `CBCTL_HOME`) or `CAPELLACTL_HOME` is set.

**Saved passwords and git.** Generated passwords are written under the credentials directory (mode 0600). When that directory is inside a git work tree and not ignored, generating a password exits 4 before anything is created; add `.cbctl/` to `.gitignore` or pass `--force-overwrite-secret`. `init` and `sync` add `.cbctl/` to the `.gitignore` next to `capella.yaml` when it is in a git work tree.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Success or intentional no-op |
| 1 | API / transport / wait failure |
| 2 | Changes found (`plan`/`drift --detailed-exitcode`, `sync --dry-run`) |
| 3 | Invalid usage or config (missing key, bad flags, wrong argument count, unknown/ambiguous subcommand or name) |
| 4 | Blocked (deletion protection, free-tier restriction, confirmation without TTY) |
| 5 | Async wait timed out (operation continues in Capella) |
| 130 | Interrupted (Ctrl-C); Capella keeps running the operation |

A group with no arguments prints its help and exits 0. An unknown subcommand (`cbctl capella cluster bogus`) exits 3 with a "did you mean …?" hint; an ambiguous prefix (`cbctl capella c`) lists the candidates. These codes apply to the Server commands too: their runtime failures exit 1, usage mistakes exit 3.

### Declarative workflow (`capella.yaml`)

Describe projects, clusters, buckets, scopes, collections, credentials, CIDRs, schedules and backups in one file and let `cbctl` reconcile Capella to it. The file is found in this order: `-c/--config FILE` → `CBCTL_CONFIG` (or capellactl's `CAPELLACTL_CONFIG`, with a warning) → `configFile` of the profile (`cbctl configure --config-file`) → `./capella.yaml` → `./capella.yml` (both present is an error). The organization comes from `--org-id`, `CAPELLA_ORG_ID`, the profile, then `organizationId` in the file.

```bash
cbctl capella init --non-interactive --project demo --provider aws --region us-east-1 --preset small   # or: init (interview), sync (import what exists)
cbctl capella validate          # offline: no API key needed; exit 3 on problems
cbctl capella plan              # what apply would do; --detailed-exitcode exits 2 on changes, --target KIND/NAME
cbctl capella apply             # asks first (--yes skips); a second apply changes nothing
cbctl capella drift             # Capella versus the file: changed, missing, unmanaged; --detailed-exitcode
cbctl capella sync              # (re)write the file from Capella, keeping comments; --dry-run, --stdout, --project, --cluster, --full
cbctl capella destroy           # deletes what the file declares (typed confirmation; protected clusters block it)
cbctl capella guide [TOPIC]     # built-in docs: getting-started, config, scaling, safety, ci, ...
```

| Command | Notes |
|---|---|
| `apply` | `--target`, `--prune` (delete what the file no longer lists), `--rotate-passwords`, `--power-on`, `--allow-service-removal`, `--allow-service-group-removal`, `--skip-capacity-check`, `--force-overwrite-secret`. Creating a bucket, scope or collection delete asks you to type its name. Created ids are pinned into the file (previous version kept as `capella.yaml.bak`). |
| `sync` | Writes a new file, or refreshes an existing one: comments, `${VAR}` references and keys the importer cannot read are kept; renamed projects are followed by pinned `id`. `--dry-run` prints a diff and exits 2 when the file would change. A `capellactl/v1` file stays `capellactl/v1`; `--api-version cbctl/v1\|capellactl/v1` sets it. |
| `destroy` | Removes clusters and projects declared in the file; the file keeps its description but loses the ids. |
| `init` | Interactive interview, or `--non-interactive` with flags (`--provider`, `--region`, `--preset`, `--nodes`, `--bucket`, `--credential`, ...). |

Passwords never appear in the file: write `${VAR}` or `${file:PATH}` and `cbctl` reads the value at apply time; all output is redacted.

**File-managed mode.** When a capella.yaml exists, every mutating Capella command edits the file first — `project create|update|delete`; `cluster create|update|scale|protect|delete|clone`, `cluster group add|remove`, `cluster schedule set|delete|pause|resume`, `cluster backup schedule set|delete`; `bucket add|update|delete`, `bucket backup schedule set|delete`; `scope add|delete`; `collection add|update|delete`; `cidr create|delete`; `credential create|update|delete` (passwords are written as `${VAR}` / `${file:PATH}` references from `--password-env` / `--password-file`, never literally). `cluster create` takes `--name`, `--size`, `--group SERVICES:NODESxCPU/RAM[:DISK]`, `--free-tier` and more in this mode. Deletes run the Capella-side delete (with its confirmations and protection checks) and then remove the entry from the file; declining leaves both untouched. Each command validates the result (reporting only the problems this change causes), shows the plan, asks, saves, then applies it. `--no-apply` only records the change (deploy later with `apply`); `--direct` acts on Capella only and leaves the file alone. If Capella refuses the change, the file is restored.

Extra exit codes: 2 = changes found (`plan`/`drift --detailed-exitcode`, `sync --dry-run`), 3 = invalid config, 4 = blocked plan or confirmation without a terminal.

### Commands

Capella commands live under `cbctl capella`:

```bash
# Create a cluster (no capella.yaml: directly in Capella; with one: edits the file and applies)
cbctl capella cluster create dev-01 --size dev --provider aws --region us-east-1
cbctl capella cluster create prod-01 --group data:3x8/32 --group index,query:2x8/32
cbctl capella cluster create            # on a terminal: a short wizard asks for name, provider, region, size, CIDR

# capellactl spellings that keep working
cbctl capella bucket create my-cluster orders          # = bucket add (stderr note)
cbctl capella scope create my-cluster orders sales     # = scope add
cbctl capella collection create my-cluster orders sales eu   # = collection add
cbctl capella cluster schedule apply my-cluster -y     # = apply --target schedule/my-cluster
cbctl capella help getting-started                     # = guide getting-started

cbctl capella --help
```

`--timeout` takes a duration (`90m`, `1h30m`, units in any case) or a bare number of seconds (`90`); it must be positive and at most 24h. `CBCTL_QUIET` accepts `1/true/yes/on`. New buckets default to 1024 MB and Capella's default storage backend.

#### Deprecated commands

These pre-port commands are hidden from `--help`, still run, and print a warning on stderr naming the replacement (stdout is unchanged, so `exists` still prints only `true`/`false`):

| Deprecated | Use instead |
|---|---|
| `capella cluster exists --database N` | `capella cluster get N` (exit 3 when not found) |
| `capella cluster destroy --database N` | `capella cluster delete N -y` (destroy runs the same code: needs `-y` without a terminal, refused under deletion protection) |
| `capella cluster test --database N` | `capella credential create N USER ...`, then `cbctl cluster test --host <connection string> --ssl --bucket B -u USER -p ...` |
| `capella bucket create NAME --database N` | `capella bucket add N NAME [--memory MB] [--replicas N]` |
| `capella bucket exists NAME --database N` | `capella bucket get N NAME` |
| `capella cluster create --database N ...` (legacy flags) | `capella cluster create N [--size ...]`, then `capella cidr create` / `capella credential create` |
| `capella cluster apply N`, `capella project apply N` | `capella apply --target cluster/N` / `--target project/N` |

The legacy flags (`--database`, `--database-id`, `--org`, `--project-id`, `--user-email`, `--user-id`, `--allow-cidr`, `-u`) are hidden. They are safe by default: `--allow-cidr` has no default and `0.0.0.0/0` / `::/0` need `--allow-any-ip`; no database user is created unless `--username` and `--password-env VAR` or `--password-file PATH` are given; a literal `-p/--password` exits 3. Mixing legacy flags with `--size`/`--group` exits 3. `capella import` is the JSONL import; called without arguments or with sync flags (`--full`, `--stdout`, `--cluster`, ...) it exits 3 and points to `capella sync` (capellactl's `import`).

### Capella resources

These commands use the Capella Management API v4 (`--token`/`CAPELLA_API_KEY`, `--org-id`/`CAPELLA_ORG_ID`, `--api-host`). Clusters are addressed by name (`CLUSTER`); pass `--project NAME` only when several projects hold a cluster of that name. `-o json` prints one JSON document on stdout; `--yes` / `-y` skips the confirmation of destructive commands (without a terminal they exit 4 unless `--yes` is given).

| Command | What it does |
|---|---|
| `project list\|get\|create\|update\|delete` | Projects. `delete` refuses a non-empty project unless `--cascade` deletes its clusters first (`--discard-snapshots` also drops their backups, `--no-wait` returns once the deletes are accepted). |
| `cidr list\|create\|delete CLUSTER CIDR` | Allowed CIDRs. A bare IP becomes `/32` (`/128`); `create` takes `--comment`, `--expires-at` (RFC 3339) and `--allow-any-ip` for `0.0.0.0/0`; at most 75 entries. Creating a present CIDR and deleting a missing one both succeed. |
| `credential list\|get\|create\|update\|delete CLUSTER NAME` | Database credentials. Access is `--privileges read\|write\|read+write` on `--bucket` (with optional `--scope` and repeatable `--collection`) or `--all-buckets`. A generated 32-character password is printed once; supply your own with `--password-env VAR` or `--password-file PATH`. `update` replaces the access and/or sets a new password (`--rotate-password`). |
| `bucket list\|get\|add\|update\|delete CLUSTER [NAME]` | Buckets (`bucket create\|exists` remain for the legacy flow). `add` takes `--type`, `--storage-backend`, `--vbuckets`, `--memory`, `--replicas`, `--durability`, `--ttl`, `--flush`, `--conflict-resolution`, `--eviction-policy`, `--priority` and is a no-op when the bucket exists. `update` changes memory, replicas, durability, TTL, flush, priority and cross-cluster versioning. Free-tier clusters only take `--memory` (and `--cross-cluster-versioning` on update). |
| `scope list\|add\|delete CLUSTER BUCKET [NAME]` | Scopes. `add` is idempotent; `_default` and `_system` cannot be deleted. |
| `collection list\|add\|update\|delete CLUSTER BUCKET SCOPE [NAME]` | Collections. `add` and `update` take `--max-ttl` (`update` needs Couchbase Server 7.6.0 or later); `_default` and system collections cannot be deleted. |
| `cluster list\|get\|options\|disk-types` | Read-only. `list` shows every cluster of every project (`--project` limits it); `get CLUSTER` shows nodes, service groups, disks, support plan and connection string; `options` shows what you can deploy (free tier, providers, sizes, plans); `disk-types [--provider P]` lists the disk types with size and IOPS rules. |
| `cluster scale CLUSTER` | Change a service group's node count: `--nodes N` or `--step +1\|-1` (mutually exclusive), `--group SERVICES` / `--all-groups`, `--min` / `--max`. Group limits are checked (first group 3-27, extra groups 2-24, at most 27 nodes); scaling in is refused when bucket memory or replicas would not fit (`--skip-capacity-check` overrides). Free tier is blocked (exit 4); a step that is already at its limit is a no-op. Waits until the cluster is healthy unless `--no-wait`. |
| `cluster update CLUSTER` | Change `--description`, `--support-plan`, `--support-timezone`, and per group `--cpu`, `--ram`, `--disk-type`, `--disk-size`, `--disk-iops`, `--disk-auto-expansion` (`--group` picks the group). A free-tier cluster only takes `--description`. |
| `cluster power CLUSTER --on\|--off` | Turn a cluster off (storage and data are kept) or on again (`--with-app-service` also starts the linked App Service). Turning off asks for confirmation. |
| `cluster protect CLUSTER --on\|--off` | Deletion protection. Turning it off asks for confirmation. |
| `cluster delete CLUSTER` | Delete a cluster with its buckets and schedule; you confirm by typing the cluster name (or `--yes`). Snapshot backups are kept unless `--discard-snapshots`. Refused (exit 4) while deletion protection is on; a missing cluster is a no-op. |
| `cluster clone SOURCE NEW_NAME --region R` | Deploy a copy of a provisioned cluster in another region or project (`--to-project`): same size, services and availability, plus buckets, scopes and collections (`--no-data-model` skips them). The network `--cidr` defaults to the first free `10.x.y.0/24` that does not overlap another cluster. `--with-cidrs` copies allowed CIDRs; `--with-credentials` recreates credentials with new generated passwords that are printed once. Documents, schedule, CMEK and private DNS are never copied. |
| `cluster group list\|add\|remove` | Service groups (multi-dimensional scaling). `add CLUSTER --services S [--nodes 2-24] [--cpu --ram --disk-*]` copies unset options from the data group; `remove CLUSTER SERVICES` is a guarded scale-in (`--allow-service-removal`, `--skip-capacity-check`). |
| `cluster schedule show\|set\|pause\|resume\|delete CLUSTER` | Weekly on/off schedule. `set --timezone TZ --weekdays HH:MM-HH:MM --weekend off` (also `--monday` ... `--sunday`; windows use `on`, `off` or `HH:MM-HH:MM` in 30-minute steps). `show` prints a weekly grid and the next transition; `pause --power-on` also turns a cluster on that the schedule had turned off. Provisioned clusters only. |
| `cluster backup schedule show\|options\|set\|delete CLUSTER` | Cloud snapshot backup schedule: `set --every HOURS --retain 30d --start HH:MM --copy-to REGION` (or `--no-copies`); values are validated against `options` for the support plan. |
| `cluster backup list\|create\|retain\|delete\|restore\|restores\|regions` | Snapshot backups. `create` waits until the backup is complete (`--no-wait`, `--retain`, `--copy-to`); `retain CLUSTER ID --retain 90d`; `restore CLUSTER ID` replaces all data (asks for confirmation; `--from-region` falls back to a copy); `restores` lists restores; `regions` lists copy regions. |
| `bucket backup schedule show\|set\|delete CLUSTER BUCKET` | Weekly full plus incremental bucket backups: `set --day D --start-at H --incremental-every 1\|6\|12\|24 --retention 30days --cost-optimized`. |
| `bucket backup list\|get\|create\|delete\|restore\|export`, `bucket backup cycle list\|get` | Bucket backups and their cycles. `delete` removes the whole cycle; `restore` takes `--to-cluster`, `--services`, `--include-data`, `--exclude-data`, `--map-data`, `--replace-ttl`, `--force-updates` and asks you to confirm; `export` prints a one-hour download link. |

Deleting a bucket, scope or collection is refused (exit 4) while the cluster has deletion protection on.

Commands that start a cluster operation (`scale`, `update`, `power`, `delete`, `clone`, `group add|remove`, backups) wait until it finishes, print progress dots on stderr, and take `--no-wait` to return as soon as Capella accepts the request. Ctrl-C stops waiting (exit 130) but does not cancel the operation in Capella. A hidden `cbctl capella janitor --prefix P [--yes]` deletes every project whose name starts with `P` together with its clusters; it exists to clean up after live tests.

```bash
cbctl capella project create demo
cbctl capella cidr create my-cluster 203.0.113.0/24 --comment office
cbctl capella credential create my-cluster app --privileges read+write --bucket orders --scope inv
cbctl capella bucket add my-cluster orders --memory 1024 --storage-backend magma
cbctl capella collection add my-cluster orders inv items --max-ttl 3600
cbctl capella cluster scale my-cluster --step +1
cbctl capella cluster clone my-cluster my-cluster-dr --region us-west-2 --with-cidrs
cbctl capella cluster schedule set my-cluster --timezone US/Pacific --weekdays 08:00-18:00 --weekend off
cbctl capella cluster backup schedule set my-cluster --every 24 --retain 30d
```

## Development

```bash
make build              # bin/cbctl
make test-unit          # with -race (./internal/...)
make test-integration   # Docker required; Couchbase enterprise image
make test-capella-live  # needs CAPELLA_API_KEY and CAPELLA_ORG_ID (CBCTL_LIVE_CLUSTER=1 adds billable clusters)
make janitor PREFIX=cbctl-ci-   # delete leftover live-test projects
make vet
make lint               # golangci-lint v2.15.0 (config: .golangci.yml)
make check              # everything CI runs, including lint
```

CI (`.github/workflows/ci.yml`) runs on pushes and pull requests to `main` and `mm--capellactl-port`. Workflow actions are pinned by commit SHA; Dependabot (`.github/dependabot.yml`) proposes weekly bumps.

Offline Capella scenario tests use `internal/capellafake`. Live Capella tests (`//go:build integration`) skip unless sandbox credentials are set; with `CBCTL_LIVE_CLUSTER=1` they also run the declarative §13.4 validate/plan/apply/drift/prune/destroy flow. See `docs/specs/capella-fold-in-phase-4.md` for the Phase 4 harness (`CBCTL_IT_TARGET=fake|sandbox`), janitor, secret scanner, and release gate. Roadmap / backlog stories live under `docs/backlog/` (ported from [capellactl](https://github.com/couchbaselabs/capellactl)).
