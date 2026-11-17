# 0.7.0 Feature 2: `cbctl apply -f` manifests (P3 model, P4 Server, P5 Capella)

Source: `cbctl-apply-manifest-spec.md` (`cbctl/v1`, kinds `CouchbaseServer` and `CouchbaseCapella`). This file covers the shared model and the spec conflicts. Execution per kind is in [02b-manifest-execution.md](02b-manifest-execution.md).

## 1. What is reused and what is new

| Spec piece | Reuses | New |
|---|---|---|
| Parsing, strict decoding, multi-document | `internal/yamlio` (P1) | the `manifest` types and loader |
| `${VAR}`, `${file:PATH}` | `capellafile/secrets.go` `expandRefs` and the file rules. These move into `internal/secrets/refs.go` so both loaders share one resolver. | `${base64:VALUE}` (manifests only, see C19) |
| Redaction | `internal/redact` (`redact.Register`) | register both the decoded value and the encoded `VALUE` |
| Server init/join/rebalance | `rest.Client.BootstrapPrimary`, `JoinNode` (incl. `Arbiter`), `RebalanceCluster`, `ReconcileServiceQuotas`, server-group reconcile, `rest.NormalizeServerService` | `rest.Client.PendingNodes`, `rest.Client.ClusterNodes` (membership report). Orchestrator `internal/manifest/serverapply`. |
| Server data model | `server.Server.CreateBucket`, `CreateScope`, `CreateCollection`, `*Exists`, `EnsureCollection` | none |
| Import | `server.PopulateCollection`, `CollectionIsEmpty`, `forEachJSONLDocument`, `DecodeJSONLLine` | `server.ImportDocuments(ctx, src, keyField)` with a `DocSource` (file or inline) and `keyField` keys |
| Capella cluster, CIDRs, buckets, scopes, collections, credentials, protection, waits | The whole `plan` + `resources` engine (`BuildPlan`, `Execute`, reconcilers, `engine.Waiter`), `capellaapi`, `presets`, `capellafile` crossref checks | `internal/manifest/capellaapply`: translates a manifest into an in-memory `capellafile.Config`, plus an engine "ephemeral" mode with no id pinning, no file writes and no prune |
| Capella import | `capella_legacy_sdk.go` SDK connect over TLS to the connection string; `server.ImportDocuments` | connection by credential, plus an allow-list reachability hint |
| Exit codes, `CLIError`, `HandleError` | `internal/cli/errors.go` | `manifest.ValidationError` → exit 3 |
| Output | `internal/output` (P2) | the `apply` JSON document, `apply.manifest.schema.json` |

Not reused, on purpose:
- `capella.yaml` discovery, `Editor` and file-managed mode. The spec says `apply -f` never reads or writes `capella.yaml`.
- The `--prune` / `destroy` paths. v1 never deletes.

## 2. File and module structure

```
internal/manifest/                       (new; no network; imports yamlio, secrets, redact)
  doc.go
  types.go          Envelope, Metadata, ServerSpec, CapellaSpec, Bucket*, Import, Credential, Wait
  load.go           Load(ctx, src) -> []Document ; strict decode, kind dispatch
  validate.go       offline rules per kind (spec "Validation"), collects all issues
  secrets.go        SecretRef type (UnmarshalJSON keeps the ref; Resolve at apply)
  services.go       one vocabulary; Server: +backup, fts->search; set semantics
  data.go           inline data conversion (timestamps RFC3339, binary/non-string key rejection)
  keys.go           keyField checks (file: streamed read during validation)
  issues.go         Issue{Doc, Pointer, Line, Col, Msg, Fix}; ValidationError
internal/manifest/serverapply/           (new) Apply(ctx, Env, *ServerDoc) (*Report, error)
internal/manifest/capellaapply/          (new) Translate(*CapellaDoc) (*capellafile.Config, error); Apply(...)
internal/server/import.go                (changed) DocSource, ImportDocuments; PopulateCollection delegates
internal/rest/nodes.go / init.go         (changed) PendingNodes, ClusterNodes
internal/secrets/refs.go                 (new) shared ${...} resolver (capellafile/secrets.go calls it)
internal/plan/context.go                 (changed) RunContext.Ephemeral bool (skip pin hooks / file notes)
internal/cli/apply.go                    (new) `cbctl apply -f FILE|-`
internal/cli/root.go                     (changed) root.AddCommand(newApplyCmd())
internal/manifest/testdata/{valid,invalid}/*.yaml + *.golden (issues)
docs/manifests.md                        (new) user guide; examples tested by the docs runner
```

Dependency direction: `cli/apply.go` → `manifest`, `manifest/serverapply`, `manifest/capellaapply`. `serverapply` → `rest`, `server`. `capellaapply` → `capellafile`, `plan`, `resources`, `capellaapi`. `manifest` imports none of them.

## 3. Interfaces and signatures

```go
package manifest

const APIVersion = "cbctl/v1"
const (KindServer = "CouchbaseServer"; KindCapella = "CouchbaseCapella")

// Source is where the manifest comes from: a path, or "-" for stdin.
type Source struct{ Path string; Stdin io.Reader; Cwd string; Env func(string) string }

// Load reads, strictly decodes and validates every document offline. Nothing is resolved
// against the network. A non-nil error is *ValidationError (exit 3) or an I/O error.
func Load(ctx context.Context, src Source) (*Set, error)

type Set struct{ Docs []Document; BaseDir string; Name string /* display */ }
type Document struct {
	Index    int
	Envelope Envelope
	Server   *ServerSpec  // set iff Kind == KindServer
	Capella  *CapellaSpec // set iff Kind == KindCapella
	Lines    yamlio.LineIndex
}

// Resolve resolves every SecretRef of d (env, file, base64) and registers the values with redact.
func (d *Document) Resolve(env func(string) string, baseDir string) error

type SecretRef struct{ Raw string; value string; resolved bool }
func (s SecretRef) Value() string     // panics if not resolved: programmer error
func (s *SecretRef) UnmarshalJSON([]byte) error // rejects literals: "must be ${VAR}, ${file:PATH} or ${base64:VALUE}"

type ValidationError struct{ Source string; Issues []Issue }

package serverapply
type Env struct{ Rest *rest.Client; Out Reporter; Clock engine.Clock; SDK func(*config.Config) ServerSDK }
type Reporter interface{ OK(f string, a ...any); Info(f string, a ...any); Warn(what, action string) }
func Apply(ctx context.Context, env Env, doc *manifest.Document, baseDir string) (*Report, error)

package capellaapply
func Translate(doc *manifest.Document) (*capellafile.Config, error)
func Apply(ctx context.Context, rc *plan.RunContext, doc *manifest.Document, opts Options) (*Report, error)
type Options struct{ Yes, Interactive bool; Import ImportFunc }

// Report is shared (manifest.Report) and is what -o json prints.
type Report struct {
	Kind, Name string
	Steps   []Step          // ordered; Status ok|unchanged|skipped|failed|interrupted
	Notices []string        // e.g. "node 10.0.9.9 is in the cluster but not in the manifest; left in place"
	ConnectionString string `json:",omitempty"` // Capella
}
type Step struct{ Name, Target, Status, Detail string; Duration time.Duration; Count int }
```

`cbctl apply` flags. The spec says `-f` is the only *input*. The plan also allows these, recommended in C16: `-f/--filename` (required), `-o text|json`, `-y/--yes`, `-q`, `--log-level`, and the root `--color`. No resource flags.

## 4. Data models (Go types, `json` tags only)

```go
type Envelope struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   Metadata          `json:"metadata"`
	Spec       json.RawMessage   `json:"spec"` // decoded strictly per kind
}
type Metadata struct{ Name string `json:"name"`; Labels map[string]string `json:"labels,omitempty"` }

type ServerSpec struct {
	Credentials  ServerCredentials `json:"credentials"`
	TLS          *TLS              `json:"tls,omitempty"`
	NodeDefaults NodeDefaults      `json:"nodeDefaults"`
	Nodes        []ServerNode      `json:"nodes"`
	Rebalance    *bool             `json:"rebalance,omitempty"`  // nil = true
	Buckets      []ServerBucket    `json:"buckets,omitempty"`
}
type ServerCredentials struct{ Username string `json:"username,omitempty"`; Password SecretRef `json:"password"` }
type NodeDefaults struct{ Services ServiceSet `json:"services,omitempty"`; DataPath string `json:"dataPath,omitempty"`; RAMGiB *int `json:"ramGiB,omitempty"` }
type ServerNode struct {
	IPAddress, ExternalIPAddress, ServerGroup, DataPath string // json: ipAddress, externalIpAddress, serverGroup, dataPath
	Services ServiceSet `json:"services,omitempty"`; RAMGiB *int `json:"ramGiB,omitempty"`
	Arbiter bool `json:"arbiter,omitempty"`; Primary bool `json:"primary,omitempty"`
}
type ServerBucket struct {
	Name string `json:"name"`; QuotaMiB *int `json:"quotaMiB,omitempty"`; Replicas *int `json:"replicas,omitempty"`
	Scopes []ServerScope `json:"scopes,omitempty"`; Import []Import `json:"import,omitempty"`
}
type ServerScope struct{ Name string `json:"name"`; Collections []string `json:"collections,omitempty"` }

type Import struct {
	Scope      string            `json:"scope,omitempty"`      // default _default
	Collection string            `json:"collection,omitempty"` // default _default
	File       string            `json:"file,omitempty"`
	Data       []json.RawMessage `json:"data,omitempty"`       // each must be a JSON object
	KeyField   string            `json:"keyField,omitempty"`
	Credential string            `json:"credential,omitempty"` // Capella only (validated per kind)
}

type CapellaSpec struct {
	OrganizationID string `json:"organizationId,omitempty"`; Project string `json:"project"`
	Provider, Region, CIDR, Availability, ServerVersion, Description, Size string // omitempty
	SupportPlan *SupportPlan `json:"supportPlan,omitempty"`
	DeletionProtection *bool `json:"deletionProtection,omitempty"` // nil = leave as is (C24)
	ServiceGroups []ServiceGroup `json:"serviceGroups,omitempty"`
	AllowedCIDRs []AllowedCIDR `json:"allowedCidrs,omitempty"`
	Buckets []CapellaBucket `json:"buckets,omitempty"`
	Credentials []Credential `json:"credentials,omitempty"`
	Wait *Wait `json:"wait,omitempty"`
}
type ServiceGroup struct{ Services ServiceSet; Nodes int; Compute Compute; Disk *Disk }  // compute{cpu,ramGb} disk{type,sizeGb,iops,autoExpansion}
type AllowedCIDR struct{ CIDR, Comment string; ExpiresAt *time.Time; AllowAnyIP bool }    // json: cidr, comment, expiresAt, allowAnyIp
type CapellaBucket struct{ Name, Type, StorageBackend, Durability string; MemoryMB, Replicas, TTL *int; Flush *bool; Scopes []CapellaScope; Import []Import }
type CapellaScope struct{ Name string; Collections []CapellaCollection }                // collection {name, maxTtl}
type Credential struct{ Name string; Password *SecretRef; Access Access }                 // nil password = generate
type Access struct{ Privileges string; Bucket, Scope string; Collections []string; AllBuckets bool }
type Wait struct{ Timeout Duration `json:"timeout,omitempty"`; Enabled *bool `json:"enabled,omitempty"` }
type ServiceSet []string // UnmarshalJSON: lowercases, maps aliases, dedupes, sorts; Server-only names checked in validate
```

Pointers mark fields the spec defaults. Absence must be visible for change rules (C24) and for the "applies to every node that sets neither" rule.

Validation constraints implement the spec's lists word for word, with one table test row per rule:
- `metadata.name` must match `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` and be 1–63 characters.
- Exactly one primary after defaulting, and it is not an arbiter. At least one `data` node. Unique `ipAddress`. Arbiter ⟂ services. `ramGiB > 0`, `quotaMiB ≥ 100`, `replicas` 0–3.
- Capella: `size` xor `serviceGroups`. The data group comes first. 3–27 / 2–24 / ≤ 27 nodes. compute and disk per provider, from `presets` (`compute.yaml`, `disks.yaml`). Bucket memory must fit the data group's RAM. Import credentials must have write access and a ref. Unique group service sets.
- Imports: exactly one of `file`/`data`. Unique (scope, collection) per bucket. `keyField` values must be unique and be non-empty strings or integers.

Issues are rendered like `capellafile` issues: `couchbase.yaml:14:7 (document 1, CouchbaseServer/airline-ops): spec.nodes[2]: arbiter cannot be combined with services`.

## 5. Conflicts, ambiguities and open questions in the spec

Each item has a recommended default. None are decided silently: the user confirms them (summarised in 04 §Open questions).

| # | Issue | Recommended default |
|---|---|---|
| C1 | `apiVersion: cbctl/v1` is also the `capella.yaml` version. | Tell them apart by `kind`. A document with no `kind` given to `cbctl apply -f` exits 3: "this looks like capella.yaml; use `cbctl capella apply -c FILE`". `cbctl capella validate/plan/apply -c` on a file that has `kind` exits 3 with the opposite hint. Add `kind` to the `capella.yaml` schema as forbidden, with that message. |
| C2 | `size: dev` is a 1-node `singleNode`, `availability: single` preset (`presets.yaml`), but the spec says the first group has 3–27 nodes and `availability` defaults to `multi`. | Presets supply `availability` and node count. `size: dev` plus explicit `availability: multi` exits 3. The node-count rule applies to `serviceGroups` only. |
| C3 | Capella `replicas` defaults to 1, but a single-node cluster needs 0 (`capellafile.BucketDefaults(singleNode)`). | Default 1, or 0 on single-node (`size: dev`). An explicit value > 0 on single-node exits 3. |
| C4 | Capella import needs the host inside `allowedCidrs`. That cannot be checked offline. | Offline: no check. Online: before import, compare the public egress IP (if known) with the CIDRs and warn. An SDK connect timeout exits 1 with the hint "is this host in spec.allowedCidrs?". No implicit allow-list entry is added. |
| C5 | Generated credential passwords are saved under `.cbctl/credentials` "but apply never writes files". | Saving is allowed: it is the secret store, not `capella.yaml`. The directory is relative to the manifest's directory (cwd with `-f -`). The existing `NotIgnoredError` git check applies (exit 4 unless the file is ignored). No `--force-overwrite-secret` in v1: an existing file for a new credential exits 4 with a hint. |
| C6 | "Passwords rotate only when the referenced value changes": Capella never returns passwords, and apply keeps no state. | v1 never rotates an existing credential's password (same as `capella apply` without `--rotate-passwords`). It prints `[info] - credential X exists; password not changed (cbctl apply cannot detect a changed value)`. The spec text needs to change. Alternative: a `rotate: true` field. Deferred to v2. |
| C7 | Immutable changes (`provider`, `region`, `cidr`, `availability`, `serverVersion`, `storageBackend`) must "exit 3", but the engine marks them `Blocked` (exit 4). A removed group must "exit 4". | `capellaapply` pre-checks immutables against the observed cluster and exits 3 before planning. Group removal stays engine `Blocked` → exit 4. |
| C8 | `serverGroup` is "created if missing (Enterprise)". | On Community, a non-default group exits 1 with "server groups need Enterprise Edition". The default `Group 1` is a no-op. |
| C9 | Should `metadata.name` (kebab) become the Server cluster name? `cluster create --name` accepts anything. | Yes, as the spec says. Document that Terraform `var.cluster_name` must satisfy the rule. |
| C10 | Join order "is `nodes` order", but the primary may not be first. | Join order is `nodes` order with the primary skipped. |
| C11 | Is `backup` (Server) a quota service? | It has no quota. It is passed to REST as `backup`. `ReconcileServiceQuotas` ignores it (add a test). |
| C12 | Mixing kinds in one stream. Two documents with the same kind and name. | Mixing is allowed, applied in order. Capella auth is resolved only if a Capella document exists. A duplicate kind and name exits 3. A failure in document N stops later documents (exit 1) and the report lists them as `skipped`. |
| C13 | Interaction prompts. | Server: never prompts (it never removes anything). Capella: on a TTY, show the engine plan and confirm (like `capella apply`). Without a TTY, proceed without `-y` for create, update and scale-out. **Require `-y`** (exit 4 otherwise) for `deletionProtection: true → false` and for any scale-in. Q4. |
| C14 | `-f` is "the only argument", but `-o json`, `-y`, `--color` and `--log-level` are still needed. | Allow the global output and confirmation flags only (§3). |
| C15 | `spec.wait.timeout` vs the global `--timeout`. | The manifest wins. `apply` has no `--timeout`. |
| C16 | `${base64:…}` in `capella.yaml`? | Manifests only in 0.7.0 (as the spec says). The shared resolver has a `AllowBase64` switch. |
| C17 | No dry run. | Add `cbctl apply -f F --dry-run`: validate, plus for Capella, plan with exit 2 when there are changes. It is cheap because the engine has it. The flag is added only if the user agrees (Q5). Otherwise defer it. |
| C18 | Absent `deletionProtection`/`description` on an existing cluster: the spec defaults would turn protection **off** or clear the description. | Presence semantics: an absent field leaves Capella's value. Defaults apply on create only. This is a deliberate deviation from the spec text, and the spec should be amended. |
| C19 | Imports with no `keyField` use UUID keys (as `cbctl import` does). A partial failure plus a manual clean is the documented reload path. | As the spec says. Documented. |
| C20 | Server `spec.tls.enabled: true`: which CA? The CLI has `--ssl` and a CA path in `config.Config`. | Add `spec.tls.caFile` (optional path) **or** keep it system-trust only. Q6. Default: system trust plus `caFile` optional. That is an additive field. |
| C21 | The spec has Capella `disk.autoExpansion` default `true`, but `capellafile` only sends it when it is set. | Send it on create only when the field is set or the provider supports it (AWS/GCP). Leave existing clusters alone (C18). |
| C22 | Units mix GiB (Server `ramGiB`), MiB (`quotaMiB`), `ramGb`/`sizeGb`/`memoryMb` (Capella API style). | Keep them as in the spec (they mirror each product's own units). The README table spells out each one. |
