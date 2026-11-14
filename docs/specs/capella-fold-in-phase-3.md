# Spec: Fold capellactl into cbctl — Phase 3 (declarative engine)

Status: ready for implementation. Builds on [`capella-fold-in-phases-0-2.md`](capella-fold-in-phases-0-2.md) (Phases 0–2 **must be merged first**; this spec reuses their packages and names without redefining them).
Scope: `capella.yaml` format, loader/validator, comment-preserving editor, the plan/apply engine with one reconciler per resource kind, and the commands `validate`, `plan`, `apply`, `destroy`, `drift`, `sync`, `init`, `guide`; plus the "file-managed" mode of the Phase 2 resource commands (`--no-apply`).
Out of scope: anything in capellactl's 1.5.0/2.0.0 backlog (advanced credentials `userRoles`, custom roles, network peering, private endpoints, audit logs, CMEK management, org users, API keys, billing, App Services), shell completion (cobra's built-in `cbctl completion` already exists), the Phase 4 live-test expansion beyond §13.4.

Sources to port from — **authoritative for anything this spec summarizes**: `/Users/michael/IdeaProjects/capellactl/`
`docs/release-1.0.0/00-architecture.md` (read it first; §4.1, §5.2, §6, §7, §8, §11 are the contract), `capellactl/config/{schema.json,loader.py,crossref.py,editor.py,presets.yaml}`, `capellactl/engine/{planner,executor,normalize,context}.py`, `capellactl/resources/*.py`, `capellactl/importer/__init__.py`, `capellactl/output/{render,secrets_out,password_gen}.py`, `capellactl/cli/{env,sync,init,cluster_ops,resource_cmds}.py`, `capellactl/help/*.md`, `tests/fakes/capella.py`, `tests/schemas/*.schema.json`.

---------------------------------------------------------------------------------------------------

## 0. Prerequisites, conventions and deltas from the Phase 0–2 spec

Used as-is from Phases 0–2: `settings` (`Resolve`, `Settings`, `Profile`, `ProfileStore`, `Keyring`), `redact`, `capellaapi` (client, models, all 80 endpoint methods, `BucketID`, errors/sentinels, `Paginate`), `engine` (`Clock`, `FakeClock`, `WaitForCluster`, `WaitForStatus`, `Op*`, `Transitional`, `WaitFailed/Timeout`), `cli` (`CLIError`, `ExitCode`, `HandleError`, `runtime`, `Renderer`, `confirm`, persistent flags `-o -y/--auto-approve -q --no-wait --timeout --log-level --profile`), `logging` levels, `internal/capellafake`.

Deltas this phase introduces (apply them in the same PR):
1. **`settings.Profile` gains `ConfigFile string \`yaml:"configFile,omitempty"\``**; `cbctl configure` gets `--config-file PATH` to set it; `configure show` prints it with its source.
2. **`capella` persistent flag `-c, --config PATH`** (env `CBCTL_CONFIG`). No shorthand clashes (`-c` is unused on every existing leaf; verify with the flag-snapshot test from Phase 2 AC9, extending its expectations).
3. **`internal/secrets`** package (password generator + credential files, §8). The Phase 2 `--password-env/--password-file`/generator code is refactored to call it.
4. **`ExitChanges` (2)** from `cli/errors.go` becomes live (plan/drift `--detailed-exitcode`).
5. `apiVersion`: files written by cbctl say `cbctl/v1`; **files that say `capellactl/v1` are accepted unchanged** (so existing capellactl users can point cbctl at their file). Any other value → config error.
6. Defaults use the `.cbctl` namespace: `credentialsOutput` default `./.cbctl/credentials`. If an existing file sets `credentialsOutput` explicitly, it is honoured as-is.

Conventions: same as Phase 0–2 §0 (stdlib tests, `context.Context` first, `%w`, doc comments, no secrets in output, offline unit tests, `make test-unit` with `-race`). **Generic maps, not typed structs, for the desired state** (§3.2) — this is deliberate: the engine's rule is "compare only fields present in the file", which needs presence semantics.

New dependencies: `github.com/santhosh-tekuri/jsonschema/v6` (schema validation). `gopkg.in/yaml.v3` already added in Phase 0.

---------------------------------------------------------------------------------------------------

## 1. Package layout (new)

```
internal/config/capellafile/       // the file: load, validate, edit, discover
  schema.json                      // //go:embed — copy of capellactl schema.json, then edit apiVersion/credentialsOutput default (§3.1)
  model.go                         // Obj, Path, Config, ProjectEntry, Counts
  issues.go                        // Issue, ConfigError, Describe, FormatPath, ParsePath, RenderIssues
  load.go                          // Load, LoadText, DeclaredOrgID, pipeline
  yamlnodes.go                     // parse to yaml.Node + line index + JSON-compatible Obj
  secrets.go                       // ${VAR} / ${file:PATH} resolution
  defaults.go                      // ApplyDefaults, hibernation booleans
  crossref.go                      // CheckCrossReferences (+ helpers: NormalizeCIDR, KeyspaceNameIssue, ...)
  warnings.go                      // Warnings(cfg) []Notice
  discover.go                      // Discover
  editor.go                        // Editor (comment-preserving)
  render.go                        // Dump (canonical YAML writer used by sync/init/editor)
  *_test.go  testdata/
internal/plan/                     // engine core (imports engine, capellaapi; NOT resources)
  types.go                         // ActionType, Action, Change, Observed, Plan, Reconciler, Base
  context.go                       // RunContext
  normalize.go                     // FieldChanges, DeepMerge, GroupKey, Canon*
  planner.go                       // BuildPlan, targets
  executor.go                      // Execute, ApplyResult
  render.go                        // RenderPlanText, RenderPlanJSON, RenderSummaryTable, ApplyResult.JSON
  *_test.go
internal/resources/                // reconcilers (imports plan, capellaapi, engine, capellafile)
  tree.go                          // BuildTree
  project.go cluster.go scaling.go deletion_protection.go schedule.go backup_schedule.go
  cidr.go bucket.go scope.go credential.go guards.go
  *_test.go
internal/importer/                 // observed → file document (sync/init)
  importer.go exporters.go merge.go render.go *_test.go
internal/secrets/
  password.go                      // GeneratePassword, MeetsRules (shared with Phase 2)
  files.go                         // credential secret files
internal/guide/
  guide.go                         // //go:embed topics/*.md; Topics, Render
  topics/*.md                      // adapted copies of capellactl/help/*.md
internal/cli/
  capella_env.go                   // validate, plan, apply, destroy, drift
  capella_sync.go                  // sync (+ init in capella_init.go)
  capella_guide.go
  capella_managed.go               // file-managed mode: ChangeAndApply, mutations
```
Import rule (enforce with a test using `go list -deps` or an import-graph test): `plan` ↛ `resources`; `capellafile` ↛ `plan`/`resources`/`capellaapi`; `resources` → `plan`, `capellafile`, `capellaapi`, `engine`, `secrets`; `cli` → everything.

---------------------------------------------------------------------------------------------------

## 2. Behavioural contract (summary of 00-architecture.md; keep it as the single source of intent)

1. Declarative, tool-owned file. 2. **Idempotent**: a second `apply` with an unchanged file performs zero POST/PUT/DELETE and exits 0 with `No changes. Capella matches capella.yaml.` 3. Stateless (no state file; Capella is the source of truth; resources found by natural key). 4. Plan before apply; `apply` asks unless `--auto-approve`. 5. Safe by default: nothing deleted unless `delete`/`destroy`/`--prune`; immutable changes are BLOCKED, never recreate; scale-in guarded. 6. Secrets never leak (§8, Phase 0 §1.6). 7. One interface: same symbols/format/exit codes everywhere.

Exit codes (`cli.ExitCode`): 0 ok/nothing to do · 1 runtime/API error · 2 `plan`/`drift` `--detailed-exitcode` found changes · 3 config invalid · 4 blocked · 5 timeout · 130 interrupted.

---------------------------------------------------------------------------------------------------

## 3. `capella.yaml`

### 3.1 Schema
Copy `capellactl/config/schema.json` verbatim into `internal/config/capellafile/schema.json` (Draft 2020-12), then change only: `credentialsOutput.default` → `"./.cbctl/credentials"`. Structure (for orientation; the file is authoritative):

```
apiVersion*  organizationId  credentialsOutput  skipProjects[]
projects[]: {id, name*, description, clusters[]}          } exactly one of `projects` or the legacy single form:
project: {id, name*, description} + clusters[]            }   `project:` + `clusters:`
cluster*: id name* tier(provisioned|free, default provisioned) description configurationType(multiNode|singleNode)
  cloudProvider*{type*(aws|gcp|azure) region* cidr} couchbaseServer{version} availability{type*(single|multi)}
  support{plan*(basic|developer pro|enterprise, any case) timezone(ET|GMT|IST|PT)}
  serviceGroups[1..]{node*{compute*{cpu,ram} disk*{type,storage,iops,autoExpansion}} numOfNodes*(1..27) services*[data|index|query|search|analytics|eventing]}
  zones[≤1] cmekId enablePrivateDNSResolution deletionProtection allowAnyIp(bool,false)
  allowedCidrs[]{cidr* comment expiresAt}
  hibernation{timezone(enum of Capella zones) weekdays weekend monday…sunday (on|off|HH:MM-HH:MM|bool) paused ensure(present|absent)}
  backupSchedule{interval* retention* startTime(HH:MM) copyToRegions[≤2] ensure}
  buckets[]{name* type storageBackend vbuckets memoryAllocationInMb bucketConflictResolution durabilityLevel replicas flushEnabled
            timeToLiveInSeconds evictionPolicy priority enableCrossClusterVersioning scopes[]{name* collections[]} backupSchedule{…ensure}}
  databaseCredentials[]{name* password access*[]{privileges* resources{buckets[]{name scopes[]{name collections[]}}}}}
```

### 3.2 In-memory model (`model.go`)
```go
package capellafile

type Obj = map[string]any          // JSON-compatible: string, bool, float64→normalized (see below), []any, Obj, nil
type Path []any                    // elements are string (key) or int (index)

type ProjectEntry struct {
	Data          Obj     // the project mapping (without "clusters" for the projects: form's `clusters` key kept in Clusters)
	Clusters      []Obj
	Base          Path    // ("projects", i) or ("project")
	ClustersBase  Path    // ("projects", i, "clusters") or ("clusters")
}
func (p ProjectEntry) Name() string

type Config struct {
	Path     string            // absolute
	Source   string            // display name, e.g. "capella.yaml"
	Raw      Obj               // validated, defaults applied, secrets resolved
	Text     string            // original text
	OrgID    string            // organizationId from the file ("" if absent), after ${VAR} resolution
	lines    map[string]int    // FormatPath(Path) → 1-based line
}
func (c *Config) Projects() []ProjectEntry          // both forms
func (c *Config) ProjectNames() []string
func (c *Config) CredentialsOutput() string         // resolved relative to filepath.Dir(c.Path) when relative
func (c *Config) LineOf(p Path) int                 // 0 if unknown
func (c *Config) Counts() Counts                    // projects, clusters, buckets, scopes, collections, credentials, cidrs
type Counts struct{ Projects, Clusters, Buckets, Scopes, Collections, Credentials, CIDRs int }
func (c *Config) Summary() string                   // "capella.yaml is valid: 1 project, 2 clusters, ..." (singular/plural correct)
```
**Number normalization.** After YAML decode, convert every integral number to `int64` and every other number to `float64`; the schema validator receives a JSON-roundtripped copy (`json.Marshal` → `jsonschema.UnmarshalJSON`) so int/float typing is validator-safe. Booleans stay `bool`. Provide helpers in `plan/normalize.go`: `AsInt(any) (int64, bool)`, `AsString`, `AsBool`, `AsList`, `AsObj`.

### 3.3 Discovery
```go
// Discover returns the config file path and where the choice came from.
// Order: --config flag → CBCTL_CONFIG → profile.ConfigFile → ./capella.yaml → ./capella.yml.
// Both ./capella.yaml and ./capella.yml present → error (exit 3): "both capella.yaml and capella.yml found; pass --config".
// When nothing exists the returned path is "capella.yaml" (cwd) and Exists=false — callers decide whether that is an error.
func Discover(flag string, env func(string) string, profile settings.Profile, cwd string) (Discovery, error)
type Discovery struct{ Path, Source string; Exists bool }
```
Relative paths from flag/env/profile are resolved against `cwd`. **File-managed mode** (§10) is active iff `Discovery.Exists` **or** `--config`/`CBCTL_CONFIG`/profile explicitly named a path.

---------------------------------------------------------------------------------------------------

## 4. Loading and validation (`load.go`)

```go
func Load(path string, env func(string) string) (*Config, error)           // reads file, then LoadText
func LoadText(text, source, baseDir string, env func(string) string) (*Config, error)
func DeclaredOrgID(path string, env func(string) string) (string, error)   // cheap peek (no validation): organizationId with ${VAR} expanded; "" if absent
```
Pipeline — each stage returns `[]Issue`; stages 1–3 abort on first failing stage, 4–7 collect together; all failures end as one `*ConfigError`:
1. **YAML** → `yaml.Node` (record line numbers per path) + decoded `Obj`. Syntax error → one issue with the parser's line. Empty file or non-mapping root → issue `capella.yaml is empty` / `must be a mapping`. Tabs/duplicate keys: yaml.v3 errors surface as issues.
2. **apiVersion**: missing → `missing required field 'apiVersion'` with fix `add apiVersion: cbctl/v1`; not in `{cbctl/v1, capellactl/v1}` → issue listing supported values.
3. **Secret references** (`secrets.go`): for every string leaf matching `\$\{(file:)?([^}]+)\}` (whole-value or embedded): `${NAME}` → `env(NAME)`; `${file:PATH}` → file contents with one trailing `\n`/`\r\n` trimmed, PATH relative to `baseDir`. Unset/empty env var, unreadable file, or empty file → issue at that path (`environment variable NAME is not set`, fix `export NAME=…`). **Literal passwords fail**: any `…databaseCredentials[].password` that is a non-empty string *without* a reference → issue `password must be a ${VAR} or ${file:PATH} reference, not a literal`. Resolved password values are registered with `redact` immediately. References are resolved in **all** string fields (so `organizationId: ${CAPELLA_ORG_ID}` works), but only password values are secret.
4. **Schema** (embedded `schema.json`): each validation error → `Issue` using the same message-rewriting as capellactl `_schema_message` (type errors → `must be a string`; enum → `must be one of a, b, c`; required → `missing required field 'x'`; additionalProperties → did-you-mean via `difflib`-style closest key (implement a small Levenshtein ≤ 2)). Unknown keys are **errors** (verified: root, cluster, bucket, scope, collection all set `additionalProperties:false`; keep).
5. **Defaults** (`ApplyDefaults`): bucket defaults `type: couchbase, replicas: 1, durabilityLevel: none, timeToLiveInSeconds: 0, flushEnabled: false, bucketConflictResolution: seqno`; cluster `configurationType: multiNode`, `tier: provisioned`; hibernation boolean shorthands normalized (`weekdays: true` → `on`, `false` → `off`) and a hint collected for `Warnings`. `priority` is **not** defaulted. Defaults apply to the in-memory `Raw` only, never to the file.
6. **Cross-references** (`crossref.go`) — port `check_cross_references` exactly. Rules (messages abbreviated; copy the full text from `crossref.py`):
   - provider/region shape (`aws us-east-1`, `gcp us-east1`, `azure eastus`; "looks like another provider's region" hint); skip when the value contains `${`.
   - `tier: free`: forbidden fields `configurationType couchbaseServer serviceGroups availability support zones cmekId enablePrivateDNSResolution hibernation backupSchedule`; region must be in `FREE_TIER_REGIONS`; only one free cluster per organization (across all projects); buckets limited to `name memoryAllocationInMb enableCrossClusterVersioning scopes`.
   - `tier: provisioned`: required `availability support serviceGroups`; at least one group runs `data`; no two groups share a service set; first group 3–27 nodes (multiNode), others 2–24, total ≤ 27; `singleNode`: exactly one group, 1 node, services ⊆ {data,index,query,search}, `availability: single`; `zones` only with `availability: single`; disk rules per provider (`DISK_TYPES`, storage required for aws/gcp, iops required for aws, `autoExpansion` azure-only, ranges via `_check_disk_range`/`_low_iops`).
   - allowed CIDRs: parse (`netip.ParsePrefix`, bare IP → /32 with "did you mean" hint when host bits set), `0.0.0.0/0`/`::/0` need `allowAnyIp: true`, duplicates after normalization, `expiresAt` RFC 3339, overlap with another cluster's network CIDR in the same file.
   - buckets: duplicate names; ephemeral/couchbase rule table (`bucket_type_rules`: ephemeral → no `storageBackend`, eviction ∈ {noEviction,nruEviction}, durability ∈ {none,majority}); replicas vs cluster shape (`crossref.py` lines ~434–439, copy the full messages): on a single-node cluster any `replicas` other than unset/0 → `single-node clusters only support replicas: 0 (there is no …)`; on a multi-node cluster `replicas: 0` → `replicas: 0 is only for single-node clusters; use 1, 2 or 3`. Note this interacts with the bucket default `replicas: 1` (§4 step 5): apply the single-node default of `0` **before** this check (i.e. do not default `replicas` to 1 for buckets on `singleNode` clusters). Keyspace (scope/collection) names match `^[A-Za-z0-9_%-]{1,251}$` and must not start with `_` or `%` except `_default` (`KeyspaceNameIssue`).
   - hibernation: timezone required, window `HH:MM-HH:MM` with minutes `00|30` and hours 00–23, start before end, not off every day.
   - backupSchedule: `startTime` HH:MM; bucket schedule enums.
   - credentials: duplicate names; password policy check for resolved values via `secrets.MeetsRules` (message `RULES`); every `access[].resources.buckets[].name` must exist in the cluster's `buckets`, scopes must exist in that bucket.
   - project/cluster duplicates by name within scope; `projects:` and `project:` forms mutually exclusive (schema `oneOf`).
7. Issues → `*ConfigError` sorted by `(Line, Path)`.

### 4.1 Issues and errors (`issues.go`)
```go
type Issue struct {
	Path    string // FormatPath, e.g. "projects[0].clusters[1].name"
	Where   string // Describe(): "project nova › cluster nova-dev-01 › cloudProvider.region"
	Message string
	Line    int    // 0 = unknown
	Topic   string // guide topic, default "config"
	Fix     string // one-line next step, optional
}
func (i Issue) String() string // "<where> (line N)\n  <message>\n  action: <fix>"
type ConfigError struct{ Issues []Issue; Source string }
func (e *ConfigError) Error() string // "<source> has N problem(s):\n  <issue blocks indented>"
func RenderIssues(heading string, issues []Issue) string
func FormatPath(Path) string // "a.b[2].c"; "(root)" for empty
func ParsePath(string) Path
func Describe(root Obj, p Path) string // port NAMED_LISTS naming (project/cluster/service group/bucket/scope/collection/credential/allowed IP range), truncate names >40 chars
const FixProblems = "Fix each problem above (an 'action:' line says how), then run the command again."
```
`*ConfigError` maps to exit code 3 in `cli.HandleError` (add the case), printing `RenderIssues` then `FixProblems` as the action.

### 4.2 Warnings (`warnings.go`)
`func Warnings(cfg *Config) []Notice` (`type Notice struct{ What, Action string }`) — port `config_warnings`: e.g. `0.0.0.0/0` allowed, deletionProtection absent on provisioned clusters, boolean hibernation shorthand used, `flush` (deprecated) used, compute size not in `compute.yaml`, literal `flush`. `validate` prints all; `plan`/`apply` print only the **count** (`2 warnings; run 'cbctl capella validate' to see them`), mirroring capellactl's `warnings="count"` mode.

---------------------------------------------------------------------------------------------------

## 5. Editor (`editor.go`) — comment-preserving edits

yaml.v3's `Node` API round-trips comments and key order. Requirements and limits (be explicit in code comments):
- Files **written by cbctl** (sync/init/editor saves) use one canonical encoder: `yaml.NewEncoder` with `SetIndent(2)`, small leaf maps/lists (`FlowKeys`: `compute disk services privileges cloudProvider availability support couchbaseServer` and any list of scalars under `collections`) emitted in flow style (`Node.Style = yaml.FlowStyle`). A cbctl-written file re-saved with no edits is **byte-identical** (test).
- Hand-edited files may be re-indented/normalized on first save; comments and key order are kept; a `.bak` of the previous text is always written (below). This limitation is documented in `guide config`.
```go
type Editor struct { Path, Original string; /* doc *yaml.Node */ }
func OpenEditor(path string) (*Editor, error)           // reads Original; strips NewClusterTemplate comment block if present (port NEW_CLUSTER_TEMPLATE handling)
func (e *Editor) Text() (string, error)                 // canonical encoding of the current doc
func (e *Editor) Save(text string) (backup string, err error)
//   writes <Path>.bak (mode 0600, overwrite) containing the file's CURRENT on-disk text, then atomically replaces Path
//   (temp file in same dir + rename), preserving the original file mode. Returns the .bak path. No-op (returns "") if text == on-disk text.

func (e *Editor) EnsureProjects()                       // convert single-project form to `projects:` in place (CAP-040)
func (e *Editor) ProjectNames() []string
func (e *Editor) FindProject(name string) (*yaml.Node, bool)
func (e *Editor) AddProject(obj Obj)                    // appended; ensures `projects:` form
func (e *Editor) FindCluster(name, project string) (proj, cluster *yaml.Node, err error)
//   project=="" and name found in >1 project → *AmbiguousError{Name, Projects []string} (callers → CLIError exit 3, action "add --project NAME")
//   not found → (nil,nil,nil)
func (e *Editor) AddCluster(project *yaml.Node, cluster Obj)
func (e *Editor) RemoveCluster(project *yaml.Node, name string)
func (e *Editor) Group(cluster *yaml.Node, services string) (*yaml.Node, error) // by sorted-services key; ""=group running data; error if none/ambiguous
func (e *Editor) Set(node *yaml.Node, path []string, value any)                 // creates intermediate maps
func (e *Editor) Delete(node *yaml.Node, key string)
func PutKey(node *yaml.Node, key string, value any, order []string)             // insert before first existing key that `order` places later; else append
var ClusterKeyOrder = []string{"id","name","description","tier","configurationType","cloudProvider","couchbaseServer","availability","support","serviceGroups","zones","cmekId","enablePrivateDNSResolution","deletionProtection","allowAnyIp","hibernation","backupSchedule","allowedCidrs","buckets","databaseCredentials"}
var BucketKeyOrder  = []string{"name","type","storageBackend","vbuckets","memoryAllocationInMb","bucketConflictResolution","durabilityLevel","replicas","flushEnabled","timeToLiveInSeconds","evictionPolicy","priority","enableCrossClusterVersioning","backupSchedule","scopes"}
func ToNode(v any, key string) *yaml.Node               // plain data → nodes with FlowKeys styling
```
Helpers also used by Phase-3 pin/unpin: `func (e *Editor) SetID(kind, project, name, id string)` / `ClearID(...)` (insert `id` as first key).

---------------------------------------------------------------------------------------------------

## 6. Plan engine (`internal/plan`)

### 6.1 Types (`types.go`)
```go
type ActionType string
const ( Noop ActionType = "noop"; Create = "create"; Update = "update"; Replace = "replace"; Delete = "delete"; Blocked = "blocked"; Deferred = "deferred" )
var Symbol = map[ActionType]string{Create:"+", Update:"~", Replace:"±", Delete:"-", Noop:"=", Blocked:"!", Deferred:"…"}
func (t ActionType) IsWrite() bool // create|update|replace|delete

type Change struct{ Path string; Before, After any }
type Observed struct{ ID string; Body capellafile.Obj; ETag string }  // ID "" = none

type Action struct {
	Kind, Key   string
	Type        ActionType
	Reconciler  Reconciler
	Desired     any           // capellafile.Obj (or bool for deletion-protection)
	Observed    *Observed
	Parent      *Action
	Changes     []Change
	Reason, Topic, Summary, Label string
	Notes, Remarks []string
	Hidden      bool
	// filled during apply
	CreatedID string; Status string /* pending|done|failed|skipped|deferred|blocked|unchanged|interrupted */
	Duration time.Duration; Err string
	Data        map[string]any // reconciler scratch (state, free, power_on, sent, body…)
}
func (a *Action) ID() string                    // CreatedID else Observed.ID
func (a *Action) Exists() bool                  // ID()!="" && Type!=Delete
func (a *Action) Ancestor(kind string) *Action
func (a *Action) ToJSON() map[string]any        // {"kind","key","action","changes":[{"path","before","after"}], "reason"?, "summary"?, "notes"?}

type Plan struct{ Actions []*Action; Unmanaged []string }
func (p *Plan) Visible() []*Action
func (p *Plan) Count(t ActionType) int
func (p *Plan) HasChanges() bool                // any visible write action
func (p *Plan) Blocked() []*Action
func (p *Plan) SummaryLine() string
// "Plan: A to add, C to change, D to delete, B blocked." where A = creates+replaces; if deferred>0 the final "." becomes ", N deferred."
func (p *Plan) ToJSON() map[string]any          // {"actions":[visible…], "unmanaged"?:[…]}

type PlanError struct{ Msg string }             // planning cannot continue (e.g. pinned id missing) → exit 1

type Reconciler interface {
	Kind() string
	Flags() Flags                                // Grouped, Prunable, ReportsUnmanaged, ListedWhenAsked
	Children() []Reconciler
	DesiredItems(parentDesired any) []any
	Key(desired any, parent *Action) string
	Observe(ctx context.Context, rc *RunContext, parent *Action) ([]Observed, error)
	Match(desired any, observed []Observed) ([]Observed, error)         // default: by "name"; pinned ids per kind
	Manages(o Observed) bool                                            // false for system objects
	UnmanagedLabel(o Observed, parent *Action) string
	Diff(ctx context.Context, rc *RunContext, desired any, observed *Observed, parent *Action) (*Action, error)
	DeleteAction(ctx context.Context, rc *RunContext, o Observed, parent *Action) (*Action, error)
	ReadyForChildren(a *Action) bool                                    // default: a.Observed != nil
	AfterChildrenNote() string                                          // default "(after <kind> is created)"
	Apply(ctx context.Context, rc *RunContext, a *Action) error
	Wait(ctx context.Context, rc *RunContext, a *Action) error
	Refresh(ctx context.Context, rc *RunContext, a *Action) (*Action, error) // re-observe + re-diff
	EnsureReady(ctx context.Context, rc *RunContext, a *Action, child *Action) (bool, error)
	ChildBlockReason(ctx context.Context, rc *RunContext, a *Action, childKind string) (string, error)
}
type Flags struct{ Grouped, Prunable, ReportsUnmanaged, ListedWhenAsked bool }
type Base struct{ /* embeddable: Children list + default implementations of the optional methods above */ }
```
`RunContext` (`context.go`):
```go
type RunContext struct {
	Client *capellaapi.Client
	Config *capellafile.Config
	Clock  engine.Clock
	Timeout time.Duration; NoWait bool
	Progress func(string)                     // one line, to stderr unless quiet
	ProgressInline func(text string, start bool) // optional
	Warn func(what, action string)
	RotatePasswords, PowerOn, AllowServiceRemoval, AllowServiceGroupRemoval, SkipCapacityCheck bool
	Env func(string) string
	Prompt Prompter                            // for data-delete confirmations during apply --prune (nil in tests → refuse)
}
```

### 6.2 Normalization (`normalize.go`) — port `engine/normalize.py` exactly
```go
var IgnoredFields = set("id","audit","stats","connectionString","currentState","expressScaling","appServiceId","etag")
var PrivilegeAliases = map[string]string{"read":"data_reader","write":"data_writer"}
func GroupKey(group Obj) string                       // sorted services joined by ","
func CanonPrivileges([]string) []string               // alias + dedupe + sort
func CanonFlush(body Obj) (value bool, ok bool)       // flushEnabled wins; else deprecated flush
func CanonValue(path string, v any) any               // "support.plan" lower-cased; paths ending "services"/"privileges" sorted/canonicalized
func FieldChanges(desired, observed any, prefix string, skip map[string]bool) []Change
//   only keys present in `desired`; keys in `skip`/IgnoredFields skipped (skip applies to the TOP level only); recurse into maps; leaf compare after CanonValue; numbers compared as int64 when both integral
func DeepMerge(base, overlay any) any                 // observed overlaid with desired (§6.4 of the architecture doc)
```
Change `Path` strings use dotted paths and `serviceGroups[<groupkey>]` for groups.

### 6.3 Planner (`planner.go`)
```go
type Options struct {
	Targets       []string // "kind/name" (repeatable); cluster targets also accept "project/cluster"
	NoDescendants bool     // plan targeted resources without their children (e.g. `project update`)
	Prune         bool     // also plan DELETE for prunable kinds present in Capella but absent from the file
	AllUnmanaged  bool     // drift: list every undeclared resource of a prunable kind + undeclared clusters
}
func BuildPlan(ctx context.Context, rc *RunContext, root Reconciler, opts Options) (*Plan, error)
```
Port `build_plan` **faithfully** (read `planner.py`; the walk, `_target_match`, `_block_by_ancestor`, ambiguity BLOCKED text with ids, parent-not-yet-existing `after_children_note`, unmanaged/prune logic, `KIND_ORDER` stable sort). Normative details:
- `KindOrder = ["project","cluster","deletion-protection","schedule","backup-schedule","allowed-cidr","bucket","bucket-backup-schedule","scope","collection","credential"]`; the final ordering groups by (first appearance of project, first appearance of cluster, kind rank, original index).
- Target syntax error (`no "/"`) → `*cli.CLIError{ExitConfigInvalid, "invalid --target 'x': use KIND/NAME, for example cluster/nova-dev-01"}` (return a typed `TargetError` from `plan`; `cli` converts).
- Matching a name to >1 observed → BLOCKED with reason `N <kind>s share this name (ids: …), so cbctl cannot tell which one capella.yaml means; 'cbctl capella sync --force' rewrites the file from Capella with each one's id (the current file is kept as .bak)`, topic `idempotency`.
- A BLOCKED action with no observed resource has its children skipped.
- A write beneath an ancestor whose `ChildBlockReason` is non-empty becomes BLOCKED (topic `hibernation`).
- Context-only ancestors (targeted descendants) are `Hidden` unless they are CREATE.

### 6.4 Executor (`executor.go`)
```go
type ApplyResult struct{ Actions []*Action; Failed *Action; Err error; Interrupted bool; Extra map[string]any }
func (r *ApplyResult) OK() bool
func (r *ApplyResult) ToJSON() map[string]any  // {"status": ok|failed|interrupted, "actions":[{kind,key,action,status,id,durationSeconds,error?}]} (visible or write actions only)
func Execute(ctx context.Context, rc *RunContext, p *Plan) *ApplyResult
```
Port `execute`/`_apply_one` exactly:
- Skip semantics and statuses: after a stop, writes become `skipped`; parent `failed|skipped` → `skipped`; parent `deferred` → `deferred` + note `(waiting for <kind>)`; `Deferred` → `deferred`; `Noop` or hidden non-create → `unchanged`; `Blocked` → `blocked`; `parent.EnsureReady(...)==false` → `deferred` + note `(deferred: <kind> <key> is <state>)`.
- Progress lines: `→ <creating|updating|replacing|deleting> <kind> <key> …` then `  done <kind> <key> in <dur>`.
- Error recovery inside `applyOne`: `errors.Is(err, capellaapi.ErrConflict)` on **Create** → `Refresh`; if it now exists, adopt it (`  <kind> <key> already exists; using it`) and, if the refreshed diff is UPDATE, apply the update; `*capellaapi.AmbiguousWriteError` → `Refresh`; exists → adopt (`  … was created despite the lost response; using it`), else return the error; `ErrPreconditionFailed` → `Refresh`; NOOP → done; else retry `Apply` once; second 412 → `ConcurrentChange{...}` (`<kind> <key> changed concurrently; re-run plan`).
- `rc.NoWait` skips `Wait`. `context.Canceled` (SIGINT) → status `interrupted`, `Interrupted=true`, `Err="stopped waiting; the operation continues in Capella"`, stop.
- Any other error → status `failed`, `Err` set, stop. **The first failure stops the run**; re-running resumes because finished actions re-plan as no-ops.

### 6.5 Rendering (`render.go`) — byte-for-byte parity with capellactl `output/render.py`
```go
func FmtValue(v any) string        // nil→"(none)", bool→true/false, string→"\"s\"", list→"[a, b]" (elements formatted, quotes stripped), map→sorted-key JSON, else %v
func ActionLines(a *Action) []string
func RenderPlanText(p *Plan, header string, quiet bool) string
func RenderPlanJSON(p *Plan) map[string]any       // {"plan": {...}} with redact.Object applied
func RenderSummaryTable(actions []*Action) string // columns KIND NAME ACTION STATUS DURATION; skips hidden and noop; "" if no rows
```
`ActionLines`: `  <sym> <kind> <key>` + (` ` ×2 + summary for CREATE) + (3 spaces + label for UPDATE) + (2 spaces + reason for BLOCKED) + (DEFERRED: `  <summary> — <reason or "deferred">`) + (`  ` + notes joined by space); then per change `      <path>:  <before> → <after>`, except `serviceGroups[...]` add/remove rendered `    + <path>  <size>` / `    - <path>  <size>`; then `      <remark>` per remark; BLOCKED with a topic adds `      See: cbctl capella guide <topic>`. `RenderPlanText`: optional header + blank line, action lines, then if unmanaged: blank + `Unmanaged (not in capella.yaml; use --prune to delete):` + `    <label>` lines, blank, summary line. `quiet` and no blocked → only the summary line. All text passes through `redact.String`.
JSON output contract (stable): `{"plan": {"actions":[{"kind","key","action","changes":[{"path","before","after"}],"reason"?}], "unmanaged"?}, "result"?: {...}}`. Port `tests/schemas/{plan,apply,scale}.schema.json` into `internal/plan/testdata/` and validate outputs against them in tests.

---------------------------------------------------------------------------------------------------

## 7. Reconcilers (`internal/resources`)

`BuildTree() plan.Reconciler` returns: **root** (a synthetic reconciler whose children are `project`) → `project` → `cluster` → children in this order: `deletion-protection`, `schedule`, `backup-schedule`, `allowed-cidr`, `bucket` (→ children `bucket-backup-schedule`, `scope` → `collection`), `credential`. Free-tier clusters reuse the same reconcilers with `data["free"]=true` branches. `UnsupportedReconciler` is **not** ported (nothing is unsupported in this scope). Each reconciler lives in the file named in §1 and ports the Python file of the same name; the rules below are the normative summary.

Shared helpers (`guards.go`): `DeletionProtected(cluster Obj) bool`, `ProtectionMessage(cluster string) string` = `deletion protection is on for <c>; run 'cbctl capella cluster protect <c> --off' first`. Every delete path (cluster delete, bucket delete/prune, destroy, `project delete --cascade`) calls them **before any write**.

### 7.1 project (`project.go`) — natural key `name`; pinned `id` supported
- `DesiredItems`: one per `Config.Projects()` entry: `Data` copy without `clusters` plus `_clusters` ([]Obj).
- `Observe`: `ListProjects`; cache all (for rename detection).
- `Match`: if desired has `id` → exactly that id, else `PlanError("project <id> not found")`; else by exact name.
- `Diff`: no match → if no `id` and another Capella project (not in the file) holds one of this project's declared clusters → **BLOCKED** (`no project named X; project Y has its cluster Z, so it was probably renamed in Capella. Run 'cbctl capella sync' to adopt the new name, or rename it back in Capella (apply would create a second project)`, topic `idempotency`); else CREATE. Match → `FieldChanges(desired, observed, skip{_clusters,id[,name unless id pinned]})`; name change adds the RENAMED note `(renamed in Capella? 'cbctl capella sync' keeps the new name; apply renames it back)`.
- `Apply`: CREATE → `CreateProject(name, description?)` (sets `CreatedID`); UPDATE → `GetProject` (etag) then `UpdateProject` with `name` = desired only if `id` pinned else current, `description` = desired if present else current, `ifMatch`; DELETE → `DeleteProject`.
- `DeleteAction` (destroy): DELETE with key = name. `Prunable=false`.

### 7.2 cluster (`cluster.go`, `scaling.go`) — port `resources/cluster.py` and `scaling.py`
Constants: `CreateFields = name description configurationType cloudProvider couchbaseServer serviceGroups availability support zones cmekId enablePrivateDNSResolution`; `Immutable = configurationType cloudProvider availability zones cmekId enablePrivateDNSResolution couchbaseServer`; `NotAPI = id tier deletionProtection allowAnyIp allowedCidrs hibernation backupSchedule buckets databaseCredentials`; `OffStates = turnedOff, turningOff`; `Failed = deploymentFailed scaleFailed upgradeFailed rebalanceFailed peeringFailed destroyFailed turningOffFailed turningOnFailed`; `IsFree(body)` = `support.plan` lower == `free`.
- `Flags`: `ListedWhenAsked=true` (drift lists undeclared clusters; prune never deletes them).
- `Observe`: `ListClusters(project)`; for desired `tier: free` clusters with a pinned `id` not in the list, also `GetFreeCluster(id)` (404 tolerated). Remember per-project observed list for the rename hint.
- `Match`: pinned `id` → must exist else `PlanError("cluster <id> not found")`; else by name.
- `Diff` (order matters, copy exactly): no match → CREATE with `summary=CreateSummary(desired)`, `Data{state:nil, free}`, remarks `CreateGroupLines(desired)` (when ≥2 groups), rename hint note when exactly one undeclared cluster exists in the project (`(warning: no cluster named X; did you mean to rename Y? pin id: <id> to rename)`). Match → 1) free/provisioned mismatch → BLOCKED (topic `config`); 2) `deploymentFailed` → BLOCKED (`in deploymentFailed; delete it (cbctl capella cluster delete <k>) and re-run apply`, topic `safety`); 3) other `Failed`/`degraded`/`offline` → BLOCKED (`cluster is <s>; check the Capella UI activity log before changing it`); 4) immutable diffs → BLOCKED (`immutable: <paths> (requires a new cluster)`, topic `idempotency`; version compare: desired `7.6` matches observed `7.6.x`; `zones` skipped when GET omits it); 5) service-group diffs per `ServiceGroupChanges` (labels `scale out|scale in|scale up|scale down`; disk shrink → blocked reason `<path>: a → b GB  disk size cannot be reduced`; Azure non-Ultra disks drop `storage/iops` via `APIGroup`); 6) scale-in/down capacity guards (`scaling.Check`, §7.2.1) when state ∉ OffStates; 7) `OffStates && rc.PowerOn` → UPDATE with `Change{power: off→on}` + label `power on / …`, `Data{power_on,more}`; 8) `changes && OffStates` → BLOCKED (`turned off; power it on before <scaling|changing it> (cbctl capella cluster power <k> --on, or apply --power-on)`, topic `hibernation`); 9) changes → UPDATE (`Label` joined labels; RENAMED note; if no `serviceGroups.*` change and not free → `Data.settings_only` + the remark `note: Capella runs a cluster update job for this (shown as "scale" in the Capella UI); nodes do not change`; capacity `checks:` remark).
- `Apply`: DELETE → `DeleteFreeCluster` or `DeleteCluster(retainSnapshotBackups = !data.discard_snapshots)`; free CREATE → `CreateFreeCluster({name,description,cloudProvider})`; `power_on` → (wait `power_off` if `turningOff`) `TurnFreeClusterOn|TurnClusterOn`, wait `power_on`, return early if `!more`; free UPDATE → wait ready if not healthy, `GetFreeCluster`, `UpdateFreeCluster({name,description}, ifMatch)`; CREATE → body from `CreateFields` with `serviceGroups` via `APIGroup`, `CreateCluster`; UPDATE → wait ready if not healthy, `GetCluster` (+ETag), recompute changes (none → no write), `UpdateCluster(BuildUpdateBody(current, desired, rename=id pinned), ifMatch)`; set `Data.sent`/`state` as in the Python.
- `BuildUpdateBody(observed, desired Obj, rename bool) Obj`: `{name, description, support: DeepMerge(obs.support, des.support), serviceGroups}`; groups matched by `GroupKey`; observed groups missing from desired are **kept** when desired has no `serviceGroups` key, dropped otherwise; new desired groups appended; each group passed through `APIGroup` (only `node, numOfNodes, services`).
- `Wait`: if `Data.sent`: op = create/delete/update → `engine.WaitForCluster` with hooks printing `  cluster <k>: <state> (<elapsed>)` (state text `applying the change (Capella: <s>)` for `settings_only`) and dots via `ProgressInline`; free clusters use the free getter. Set `Data.state`.
- `ChildBlockReason`: nothing under a turned-off cluster may change (except `schedule`) unless `rc.PowerOn`: `cluster is turned off; run 'cbctl capella cluster power <k> --on' or pass --power-on` (+ ` (the schedule turns it on at <t>)` when an active schedule exists — port `next_on_text`).
- `EnsureReady`: healthy → true; `turnedOff` and child is `schedule` → true; transitional/unknown → if `NoWait` false, wait `ready` and re-check; else false.
- `AfterChildrenNote`: `(after cluster is healthy)`. `DeleteAction`: DELETE (`Data{state, free}`).
- **Scaling guards (`scaling.go`)**: `DataNodes`, `DataMemory`, `AfterChange`, `ReducesCapacity`, `Check(ctx, rc, projectID, clusterID, old, new []Obj) (blocked []string, checks string)` — port exactly including: last data group removal blocked; `--allow-service-removal` / `--allow-service-group-removal` handling; memory guard `allocated = total − free ≤ projected = total × newDataMem/oldDataMem` via `GetClusterStats` (stats unavailable → blocked unless `rc.SkipCapacityCheck`, then remark `memory not checked (--skip-capacity-check)`); replicas guard against the bucket with the most replicas (`replicas ≤ newDataNodes − 1`); success remark `checks: memory ok (A / ~P MB) · replicas ok (max R, needs R+1 data node[s])`.

### 7.3 deletion-protection (`deletion_protection.go`)
Kind `deletion-protection`, first child of cluster. `DesiredItems`: `[bool]` only if the cluster mapping has `deletionProtection`. `Observe`: derived from the parent's observed body (no API call). `Diff`: unobserved (new cluster): `false` → hidden NOOP; `true` → CREATE `summary: "deletionProtection: true"`. Observed equal → NOOP; else UPDATE with `Change{deletionProtection: cur→want}`. `Apply`: `SetDeletionProtection`; also update the parent's observed body so later delete guards in the same run see the new value.

### 7.4 schedule (hibernation, `schedule.go`)
Port `resources/schedule.py` including `Expand`/`DayValue`/`ExpandDay`/`ByDay`/`WeekSummary`/`StateAt`/`NextTransition`/`NextOnText` (timezone via `time.LoadLocation`; the schema's zone list must all load — test). `Flags`: `Prunable=true, ReportsUnmanaged=true` (an existing schedule with no `hibernation` key is left alone and listed). `Observe`: `GetSchedule` (404 → none). `Diff`: `ensure: absent` → NOOP(hidden) if none else DELETE; none observed → CREATE (`Data.body = Expand(desired)`, summary `<week summary>[ · paused]`); else compare `timezone` and each of 7 days (`ByDay`), plus `paused` vs `activationStatus` → changes; `Data.put` true when timezone/days differ. `Apply`: DELETE → `DeleteSchedule` (404 ok); CREATE → `CreateSchedule`, then `PauseSchedule` if `paused`; UPDATE → `UpdateSchedule(body, ifMatch)` if `put`, then pause/resume per `Data.status`. A schedule may be changed while its cluster is off.

### 7.5 backup schedules (`backup_schedule.go`) — kinds `backup-schedule` (cluster snapshots) and `bucket-backup-schedule`
Port `resources/backup_schedule.py` (`SnapshotBody`, `OptionsProblem`, `BucketSchedule`, summaries, hours/time helpers). Both `ensure: absent` aware, `Prunable=true, ReportsUnmanaged=true`. Cluster schedule: `GetSnapshotSchedule` (nil on 204); `OptionsProblem` uses `GetSnapshotScheduleOptions` + `ListSnapshotRegions` and returns a blocking message (exit 4) when interval/retention/regions are not allowed. Bucket schedule: `GetBucketBackupSchedule` (404 none), keys `dayOfWeek startAt incrementalEvery retentionTime costOptimizedRetention` → API `weeklySchedule`. Role-denied reads (403) are reported as a warning in `plan` (`backup schedules not read: …`) and treated as "unknown" — never as "absent" (so `apply` cannot create duplicates blindly; a 409 on create re-observes).

### 7.6 allowed-cidr (`cidr.go`) — port `resources/allowed_cidr.py`
Key = canonical CIDR (`Canonical`: `netip.ParsePrefix(...).Masked()`, bare IP → /32). `Flags{Prunable:true, ReportsUnmanaged:true}`. `Match` by canonical CIDR. **No update endpoint**: a changed `comment`/`expiresAt` → **REPLACE** (delete then create). Expired entries (`IsExpired`) in Capella are ignored as unmanaged. `allowAnyIp` gating happens in validation. `Apply`: CREATE → `CreateAllowedCIDR`; REPLACE → delete old id then create (re-observe on 409); DELETE → `DeleteAllowedCIDR`. `CreateBody` includes `comment` and `expiresAt` only when set.

### 7.7 bucket (`bucket.go`) — port `resources/bucket.py`
`Flags{Prunable:true}`. `Observe`: `ListBuckets` (free: `ListFreeBuckets` + cluster stats); also cache `GetClusterStats` (free memory, maxReplicas) per cluster for preflight. Key = name; bucket ID always taken from the list response (never recomputed). `Immutable = type storageBackend vbuckets bucketConflictResolution evictionPolicy` → BLOCKED (`<path>: a → b  BLOCKED (immutable; create a new bucket and migrate data)`). Updatable = `memoryAllocationInMb durabilityLevel replicas flushEnabled(+deprecated flush via CanonFlush) timeToLiveInSeconds priority enableCrossClusterVersioning`. `ObservedView` flattens observed (`stats` dropped, `flush`→`flushEnabled`). **Preflight** (`_preflight`): memory needed (create: `memoryAllocationInMb`, default 100 for free; update: only the increase) must fit `freeMemoryInMb` (running tally across buckets in one plan); `replicas` ≤ `maxReplicas` (a 1-data-node cluster still accepts 1 with note `ONE_NODE_NOTE`); violations → BLOCKED (topic `scaling`) with the capellactl messages. Replica changes add `REPLICA_NOTE`. `Apply`: CREATE → `CreateBucket(body from CreateFields)`, and if `enableCrossClusterVersioning` follow with `GetBucket` + `UpdateBucket(BuildUpdateBody)` (only accepted on update); UPDATE → `GetBucket` fresh, recompute, `UpdateBucket(BuildUpdateBody(current, desired))` (**observed overlaid with desired**; required fields `memoryAllocationInMb durabilityLevel replicas timeToLiveInSeconds` always present); DELETE → `DeleteBucket` (404 ok). Free clusters: `CreateFreeBucket({name, memoryAllocationInMb})`, update via `UpdateFreeBucket`, delete via `DeleteFreeBucket`. `DeleteAction`: BLOCKED with `ProtectionMessage` when the cluster has deletion protection. `DeleteDetails(...)` returns the lines shown before the typed-name confirmation (item counts, dependent credentials via `DependentCredentials`).

### 7.8 scope and collection (`scope.go`)
`_default` and `_system` scopes and the `_default` collection are **never created or deleted** and are excluded from unmanaged listings (`Manages`). Scopes: `ListScopes`, `CreateScope`, `DeleteScope`; no update. Collections: `ListCollections`, `CreateCollection(name, maxTTL)`, `UpdateCollection(maxTTL)` only when cluster server version ≥ 7.6.0 (`ParseVersion`/`ServerVersion` from the cluster's observed `couchbaseServer.version`; older → BLOCKED `maxTTL changes need Couchbase Server 7.6.0 or later`), `DeleteCollection`. In the file, `scopes[].collections` is a list of **objects** `{name, maxTTL?}` (schema: `additionalProperties:false`); credential `access[].resources.buckets[].scopes[].collections` is a list of **strings**. Do not conflate the two (see `scope.py` `_name` and `credential.py` `_resource_keys`). Delete of scope/collection is `Prunable`, blocked by deletion protection, and requires the typed-name confirmation with `DeleteDetails` (document counts are best-effort; missing stats never block).

### 7.9 credential (`credential.go`) — port `resources/credential.py`
Key = `name` in the cluster. `Observe`: `ListCredentials`; **advanced credentials** (`IsAdvanced`: `userRoles` present or `credentialType == "advanced"`) with a declared name → BLOCKED (`ADVANCED_REASON`); undeclared advanced ones are never pruned. `Flags{Prunable:true, ReportsUnmanaged:true}`.
Access comparison in canonical form: `CanonPrivileges` (`read`/`write` aliases), wildcard stripping (`WithoutWildcards`: Capella returns `*` for "all"), flatten to one privilege set per resource key (`(all buckets)`, `bucket`, `bucket/scope`, `bucket/scope/collection`) via `AccessMap`; `AccessChanges` yields `Change{<resource>: read → read+write}` with labels from `PrivilegeLabel`; `AccessSummary` for create lines (`read+write · orders/inventory/{items,suppliers}`); `APIAccess` strips tool-only keys. **Passwords are never compared** (Capella never returns them); every UPDATE plan line carries `note: passwords are not compared; use --rotate-password to change them`.
`Apply`: CREATE → resolve password (`${VAR}`/`${file:}` value already resolved in `Raw`; else **generate** with `secrets.GeneratePassword(32)`); **preflight the secret file first** (`secrets.Preflight` refuses to overwrite an existing secret file unless `--force-overwrite-secret`) so a credential is never created whose generated password cannot be saved; then `CreateCredential`; if the password was generated write it with `secrets.WriteGeneratedCredential` (0600) and print only `password saved to <path>`. UPDATE → `GetCredential`, `UpdateCredential({access?, password? only on rotation}, ifMatch)`. `rc.RotatePasswords` (from `apply --rotate-passwords`) adds an UPDATE with `Data.rotate=true` to **every** planned credential: new password = configured value if declared, else generated; rotation writes the secret file **before** sending (`WriteRotatedCredential`, previous kept as `.bak`) and `RestoreRotatedCredential` if the send fails. DELETE → `DeleteCredential`; the local secret file is **never** deleted (print `kept_secret_note`). Immutable: `credentialType`.

---------------------------------------------------------------------------------------------------

## 8. Secrets (`internal/secrets`)

```go
// password.go — the SINGLE implementation (Phase 2's generator calls this).
const Rules = "min 8, upper, lower, digit, special, none of < > ; . * & | £ ="
func MeetsRules(pw string) bool                     // len>=8 && has upper,lower,digit,non-alphanumeric && none of "<>;.*&|£"
func GeneratePassword(length int) string            // crypto/rand; one char from each class (upper, lower, digit, special "!@#$%^()-_+[]{}:,?"), rest from all; Fisher–Yates shuffle with crypto/rand; length<8 → 8; Capella forbids `=` so it is not generated

// files.go
type FileExistsError struct{ Path string }          // "refusing to overwrite <path>; move it or pass --force-overwrite-secret"
func SecretPath(dir, cluster, name string) string   // <dir>/<cluster>.<name>.json
func Preflight(dir, cluster, name string, force bool) (string, error)
func WriteGeneratedCredential(dir, cluster, name, credID, password string, force bool) (string, error)
//   mkdir 0700; file 0600 JSON {"cluster","name","id","password","createdAt":"RFC3339 UTC"}; registers password with redact
func WriteRotatedCredential(dir, cluster, name, credID, password string) (path, backup string, err error)
func RestoreRotatedCredential(path, backup string)
func SavedMessage(path string) string               // "password saved to <path>" — the ONLY thing printed about a generated password
func GitWarning(dir string) (what, action string, ok bool) // if dir is inside a git work tree and not ignored: ("<dir> is not git-ignored", "Add .cbctl/ to .gitignore so the saved password is never committed.") — uses `git rev-parse` / `git check-ignore` via os/exec with a 10s timeout; any error → ok=false
```
Secret-file directory = `Config.CredentialsOutput()`. Print `GitWarning` once per apply that wrote a file.

---------------------------------------------------------------------------------------------------

## 9. Commands (under `cbctl capella`, all take the Phase 2 §3.2 persistent flags plus `-c/--config`)

Shared plumbing (`capella_env.go`):
```go
type envRuntime struct{ *runtime; Cfg *capellafile.Config; Disc capellafile.Discovery }
func newEnvRuntime(cmd *cobra.Command, warnings WarningMode) (*envRuntime, error)
//  Discover → Load (ConfigError → exit 3) → settings.Resolve with FileOrgID=cfg.OrgID folded into org precedence
//  (precedence for org: flag → env → profile → file) → runtime/client (Phase 2 §3.3) → RunContext
func makePlan(ctx, e *envRuntime, opts plan.Options) (*plan.Plan, error)  // PlanError → CLIError{ExitError}
type WarningMode int // WarnNone, WarnCount, WarnAll
func planHeader(cfg, client) string   // "Project X · organization ID" / "Projects A, B · organization ID"
```
A missing config file → `CLIError{ExitConfigInvalid, "<path> not found", Action: "run 'cbctl capella sync' (or 'cbctl capella init') to create it"}`.

### 9.1 `validate`
Offline (no API call, no key needed). Text: `Cfg.Summary()` plus all warnings (`WarnAll`); JSON: `{"valid":true,"file":"…","counts":{…}}`. Invalid → exit 3 with the rendered issues; JSON mode prints `{"valid":false,"issues":[{path,where,message,line,fix}]}` to stdout **and** exits 3.

### 9.2 `plan [--target KIND/NAME]… [--detailed-exitcode]`
Read-only (GETs only — assert in tests that the fake saw no writes). Text: `RenderPlanText(plan, header, false)`; JSON: `RenderPlanJSON`. Any blocked action → exit 4 (after printing). `--detailed-exitcode` and `HasChanges()` → exit 2. Warnings mode `WarnCount`.

### 9.3 `apply [--target KIND/NAME]… [--prune] [--rotate-passwords] [--power-on] [--allow-service-removal] [--allow-service-group-removal] [--skip-capacity-check] [--force-overwrite-secret]`
Port `run_apply`/`apply_plan`/`_confirm`:
1. Plan; blocked → print plan, exit 4 `Apply refused: N blocked change(s); nothing was changed` (action: `Resolve each change marked ! in the plan above (its note says how), then run the command again.`).
2. No changes → text `No changes. Capella matches capella.yaml.` (plus deferred lines / unmanaged list if any); JSON `{…plan, "result":{"status":"ok","actions":[]}}`; exit 0.
3. Confirmation unless `--yes/--auto-approve`: not a TTY → `CLIError{ExitBlocked, "confirmation required but no terminal is attached", Action: "re-run with --yes"}`; print `Destructive changes:` (DELETE/REPLACE/`scale in` labels) and `Passwords to rotate:`; every bucket/scope/collection DELETE requires typing its name after printing `DeleteDetails` (`console.confirm_typed` equivalent: `confirmTyped(rt, prompt, expected)`); then `Apply N change(s) to <projects>?`. Declined → `Apply cancelled.` exit 0.
4. `Execute`; print summary table; print `connectionString (<cluster>): …` for each cluster created this run; `pin_created_ids`: write the new project/cluster **ids into the file** (`Editor.SetID`, comments kept, `.bak` written + `note`); GitWarning if a secret file was written.
5. Failure → exit 1 (5 for `*engine.WaitTimeout`) message `Failed: <kind> <key>: <err>` + `Completed before the failure: …` + hint (Capella `hint`; for 500 on POST/PUT the "internal error often caused by an unsupported value" hint; for 403 the `RequiredRoles` hint) + action `Then run the command again; completed changes are not repeated.` Interrupted → exit 130.
6. Success → `Apply complete in <dur>.` on stderr (not in JSON mode). JSON: `{plan…, result…, "connectionStrings"?:[…]}`.

### 9.4 `destroy [--discard-snapshots]`
Port `destroy`/`destroy_plan`: refuse first if any declared existing cluster has deletion protection (exit 4, lists them, `Nothing was deleted.`). Build the normal plan, derive DELETE actions for every declared resource that exists, **children first** (reverse order: credentials → collections → scopes → buckets → CIDRs → schedules → cluster → project); skip children of non-healthy clusters (the cluster delete removes them); keep a project containing undeclared clusters (`project X kept: contains undeclared cluster Y`); clusters get `Data.discard_snapshots`. Nothing exists → `nothing to destroy: none of the resources in capella.yaml exist in Capella`. Confirmation: **type the project name(s)** (comma-separated for several) — `--yes` skips. After success: `Editor.ClearID` for deleted projects/clusters (`unpin_deleted`) and print `Destroy complete in …. capella.yaml still describes the environment: run 'cbctl capella apply' to build it again.`

### 9.5 `drift [--detailed-exitcode]`
`makePlan(..., AllUnmanaged: true)`; `changed` = UPDATE|REPLACE|DELETE or BLOCKED-with-observed; `missing` = CREATE or BLOCKED-without-observed; `unmanaged` = `plan.Unmanaged`. Text sections exactly as in `drift()` (`Changed (…)`, `Missing (…)`, `Unmanaged (…)`, final `Drift: N changed, M missing, K unmanaged.` or `No drift: Capella matches capella.yaml.`); JSON `{"changed":[…],"missing":[…],"unmanaged":[…]}`. `--detailed-exitcode` + drift → exit 2.

### 9.6 `guide [TOPIC|topics]`
Embedded markdown topics adapted from `capellactl/help/*.md` (replace `capellactl` with `cbctl capella`, `capella.yaml` stays, update env/profile names to Phase 0, `.capellactl/` → `.cbctl/`): `getting-started config credentials scaling backup idempotency hibernation ci safety environment exit-codes`. `guide topics` lists them with one-line descriptions; unknown topic → exit 3 listing valid ones. `guide config [FIELD]`: print the schema description of a dotted field path (`cloudProvider.region`) using `schema.json` (`description`, type, enum, default); unknown field → did-you-mean. BLOCKED/issue `Topic` values must all exist as guide topics (test).

---------------------------------------------------------------------------------------------------

## 10. File-managed mode for Phase 2 resource commands

**Switch:** when the config file exists/was named (§3.3) the mutating commands below run `ChangeAndApply`; `--direct` forces the Phase 2 behaviour (acts on Capella only, prints `[warning] - capella.yaml was not updated`). `--no-apply` (file-managed only; invalid with `--direct`, exit 3) records the change and skips the apply.

```go
// capella_managed.go
type Mutation struct {
	Target      string                         // "cluster/nova-dev-01" (plan target)
	Question    string                         // confirmation prompt
	Edit        func(e *capellafile.Editor) error
	NoopMessage string
	NoDescendants bool
	Options     func(*plan.RunContext)         // e.g. SkipCapacityCheck, PowerOn, RotatePasswords
}
func ChangeAndApply(ctx context.Context, e *envRuntime, ed *capellafile.Editor, m Mutation, noApply bool) (*plan.Plan, error)
```
Algorithm (port `change_and_apply`): (1) `Edit` the in-memory doc → `text`; (2) validate the candidate text with `LoadText`; on `*ConfigError`, report **only the issues this command caused** vs. those already present in `Original` (`This change is not valid, so nothing was changed:` / `Already in <file> before this command:`; if none are new: `<file> already had N problem(s) before this command, so nothing was changed:`) → exit 3; (3) `BuildPlan` with `Targets=[m.Target]`; (4) no changes and not blocked → save text if it differs, print `NoopMessage` (default `No changes: capella.yaml and Capella already match.`), exit 0; (5) print plan; blocked → exit 4 `Nothing was written to capella.yaml or changed in Capella: the plan above has blocked changes (!)`; (6) confirm (`--yes` skips); declined → `Cancelled; capella.yaml and Capella are unchanged.`; (7) `Editor.Save(text)` (print `<file> updated (previous version kept as <file>.bak)`); `--no-apply` → `Not applied (--no-apply). Run 'cbctl capella apply' to deploy it.`; (8) `Execute` via the shared `applyPlan(..., shown=true, onFailure=restore)`; `restore`: if **no write was accepted** (`Client.WritesAccepted()` unchanged), the run was not interrupted, and no transport error occurred → write the previous text back and print `Capella did not accept the change, so <file> was restored: nothing was recorded or changed. Correct the values and run the command again.`; otherwise keep the file (Capella may have changed).

Command → edit (all take `--no-apply`; cluster positional resolved with `Editor.FindCluster`, ambiguity → exit 3 `add --project NAME`; cluster missing from the file → exit 3 `cluster X is not in capella.yaml`, action `run 'cbctl capella sync' to add it`):

| Command (Phase 2) | File edit | Target |
|---|---|---|
| `project create/update/delete` | add / set `description` / remove project (delete also runs `plan` DELETE with cascade rules, refusing protected clusters) | `project/NAME` (`NoDescendants` for update) |
| `cluster create` | build the cluster mapping from flags (port `cluster_ops.py` create builder: presets, `--group` specs, free tier, CIDR suggestion `SuggestCIDR`) and `AddCluster`; offline `validate` of the candidate | `cluster/NAME` |
| `cluster update` | `Set` description/support/compute/disk on the selected group | `cluster/NAME` |
| `cluster scale` | `Group(...)["numOfNodes"]=N` (`--step` computes from **live** counts then writes) | `cluster/NAME` (+ `SkipCapacityCheck`, allow-service flags) |
| `cluster group add/remove` | append/remove group | `cluster/NAME` |
| `cluster protect` | `deletionProtection: true/false` | `cluster/NAME` |
| `cluster delete` | remove cluster from file **and** DELETE in Capella | `cluster/NAME` |
| `cluster clone` | `AddCluster(BuildClone(...))` (port `build_clone`; passwords reference kept) | `cluster/NEW` |
| `cluster schedule set/delete/pause/resume` | `hibernation` mapping / `ensure: absent` / `paused` | `schedule/CLUSTER` |
| `cluster backup schedule set/delete`, `bucket backup schedule set/delete` | `backupSchedule` mapping / `ensure: absent` | `backup-schedule/CLUSTER`, `bucket-backup-schedule/CLUSTER/BUCKET` |
| `cidr create/delete` | add/remove `allowedCidrs` entry (`allowAnyIp` handling) | `allowed-cidr/CLUSTER/CIDR` |
| `bucket add/update/delete` | add/set/remove bucket entry | `bucket/CLUSTER/NAME` |
| `scope add/delete`, `collection add/update/delete` | edit `scopes` / `collections` | `scope/…`, `collection/…` |
| `credential create/update/delete` | `databaseCredentials` entry with `password: ${VAR}`/`${file:PATH}` from `--password-env/--password-file` (**never** a literal; when neither is given, omit `password` so apply generates and saves one) | `credential/CLUSTER/NAME` |

Commands that stay imperative (never touch the file): `cluster power`, `cluster backup create/retain/delete/restore/restores/regions/schedule show/options`, `bucket backup create/delete/restore/export/list/get`, all `list/get/options/disk-types/show`, `configure`. `cluster power` with a file present additionally prints a one-line note if the file declares the cluster (`capella.yaml is unchanged by power on/off`).

---------------------------------------------------------------------------------------------------

## 11. `sync` and `init` (`internal/importer`, `capella_sync.go`, `capella_init.go`)

### 11.1 Importer (`internal/importer`)
Port `importer/__init__.py`. Exporters (observed API body → file mapping; omit values equal to defaults unless `--full`): `ExportFreeCluster`, `ExportCluster(body, full, pinID)`, `ExportSnapshotSchedule`, `ExportBucketBackupSchedule`, `ExportSchedule` (7 days → compact `weekdays/weekend/<day>` form, `HHMM-HHMM`), `ExportCIDR` (`allowAnyIp` for `0.0.0.0/0`), `ExportBucket(b, scopes, full)` (`_system` scope dropped, `flushEnabled` from `flush`), `ExportCredential` (basic only; `password: ${CBCTL_PWD_<CLUSTER>_<NAME>}`-style **placeholder comment** — never a password; advanced credentials emitted as a comment block `# advanced credential <name> not managed`). `BuildDocument(ctx, client, projects, opts) (*Result, error)`:
```go
type Result struct{ Data capellafile.Obj; Comments map[string]string; Warnings []Notice; Unread map[string]UnreadInfo }
type Options struct{ Full bool; PinIDs bool; ClusterNames, ClusterIDs []string }
```
Reads only (GET). Powered-off clusters (`turnedOff|turningOff|turningOn`) → their buckets/credentials/CIDRs are **not read** and are recorded in `Unread` with the reason; role-denied reads (403) on backup schedules → warning `BACKUP_ROLE_WARNING`, never an error. `Render(res, orgName) string`: header comment, `apiVersion: cbctl/v1`, `organizationId`, `credentialsOutput`, projects; placeholders/comments inlined via `Dump` (canonical encoder, §5). `Summary(res)`.

### 11.2 `sync [--project NAME]… [--skip-project NAME]… [--project-id ID] [--cluster NAME]… [--cluster-id ID]… [--file PATH] [--dry-run] [--stdout] [--full] [--force]`
Read-only towards Capella. Port `sync.py` + `run_import`:
- Projects: all (minus `--skip-project`, minus `skipProjects` in an existing file) or the selected ones; name ambiguity → exit 4 (`N projects are named X (ids: …)`, action `add --project-id`); none/unknown → exit 1 with the list of names.
- **No file / `--force` / `--stdout`**: write a fresh document (`--force` keeps `<file>.bak`; `--stdout`/`--dry-run` print and write nothing).
- **Existing cbctl/capellactl file** (and not `--force`): *refresh in place* — for each Capella project/cluster already in the file update it from Capella while **carrying over local-only values** (`CarryOverProject`: `allowAnyIp`, password references; `KeepUnread`: previously-recorded buckets/credentials/CIDRs/schedules for clusters that could not be read now, with a `# kept from the previous capella.yaml: …` comment and a warning), add projects/clusters missing from the file, adopt Capella-side renames of pinned ids (`id` match, new name), and **never mix organizations**: if `DeclaredOrgID` ≠ client org → exit 3 `<file> is for organization A, not B; nothing was written` (action: `--force` or `--file`). A file that exists but is not a cbctl file → non-interactive exit 1 / interactive overwrite prompt (`.bak` kept). `--dry-run` prints a unified diff (`difflib`-style; use `github.com/pmezard/go-difflib` or a 30-line LCS diff) and writes nothing.
- Output: the summary (`Importer.Summary`) and warnings (what/action pairs) on stderr; JSON mode `{"file": path, "projects": n, "clusters": n, "warnings": [...]}`.
- Exit codes: 0 ok, 1 API error, 3 org mismatch/invalid, 4 ambiguous.

### 11.3 `init` (wizard) — `[--file PATH] [--non-interactive] [--dry-run] [--force] [--org-id] [--project] [--project-id] [--cluster] [--provider aws|gcp|azure] [--region] [--network-cidr] [--preset NAME|custom] [--nodes] [--cpu] [--ram] [--support-plan] [--protect/--no-protect] [--hibernation none|office-hours] [--office-hours HH:MM-HH:MM] [--timezone] [--cidr C]… [--allow-any-ip] [--bucket] [--bucket-memory] [--storage-backend] [--scope SCOPE[:COLL,…]]… [--credential] [--access read|write|read-write] [--password-env VAR]`
Port `cli/init.py`: interactive prompts (TTY only, using the Phase 2 prompt helpers) with defaults from flags then built-ins (`presets.yaml` presets `dev small medium large`; provider default `aws`; region default per provider; CIDR default first free `10.x.y.0/24` considering existing clusters when a key is available; protect default on; support plan default `developer pro`; office hours default as in capellactl `OFFICE_HOURS`/`TIMEZONE` constants; bucket memory 1024; storage `magma`); every answer validated against the schema/crossref **as it is given**; non-interactive requires all mandatory flags, validates the whole document, exits 3 listing problems. Credential passwords are written as `${VAR}` references (`--password-env`, else a generated `${CBCTL_PWD_…}` name with a comment) — never literals. Existing Capella project path: choose project (+clusters) then run the sync importer. Writes with the canonical encoder, header comment, `.bak` when overwriting (`--force`/confirmed). `--dry-run` prints.

---------------------------------------------------------------------------------------------------

## 12. Errors, exit codes and messages (cross-cutting rules)

- Every user-facing failure is a `*cli.CLIError{Code, Message, Action}`; **every error with a message has an `Action`** (capellactl CAP-063). Tests assert non-empty `Action` for each constructed `CLIError` in this phase (table test over a registry or via a lint-style test grepping `CLIError{` literals lacking `Action` — prefer the constructor `cli.Errorf(code, action, format…)` which requires `action`).
- Mapping: config problems → 3; BLOCKED plans / ambiguity / protection / non-TTY confirmation → 4; wait timeout → 5; API/transport errors → 1; SIGINT → 130; `plan`/`drift --detailed-exitcode` → 2.
- API-error enrichment in `apply`/`ChangeAndApply` failure messages: Capella `hint`; `ErrForbidden` → `Ask an Organization Owner to give the API key the <RequiredRoles> role.`; 500 on POST/PUT → the "unsupported value" hint.
- Notices/warnings go to stderr as `[warning] - <what>` + `  → <action>`; never in JSON stdout.

---------------------------------------------------------------------------------------------------

## 13. Tests

### 13.1 Unit
- `capellafile`: golden tests over `testdata/valid/*.yaml` and `testdata/invalid/*.yaml` with expected `ConfigError` text (path, `where`, line, message, fix) — one invalid file per rule in §4; both forms (`projects:` and single-project); `${VAR}`/`${file:}` resolution incl. missing/empty; literal-password rejection; `apiVersion` variants; `DeclaredOrgID`; discovery order incl. both-files error; `Describe` naming; did-you-mean.
- Editor: round-trip byte-identical for cbctl-written files; comment preservation for hand-edited files (assert comments still present, key order unchanged); `PutKey` ordering; single→projects conversion; `Save` writes `.bak` with previous text, 0600, atomic, preserves mode, no-op when unchanged.
- `plan/normalize`: table tests ported from `tests/unit` for FieldChanges/DeepMerge/CanonValue/GroupKey/privilege aliases/flush/case-insensitive `support.plan`; numbers `int64` vs `float64`.
- `plan/planner`: target matching (`cluster/x`, `project/cluster`, qualified duplicates, ancestors hidden), ambiguity BLOCKED text, parent-not-ready notes, prune/unmanaged/AllUnmanaged, kind ordering — use stub reconcilers (a `plan/plantest` helper package).
- `plan/executor`: stop-at-first-failure, skipped/deferred statuses, 409 adopt, ambiguous-POST adopt, 412 retry-once then `ConcurrentChange`, interrupt, `NoWait`.
- `plan/render`: exact text for every action type/label/remark/see-topic; summary table; JSON validates against ported schemas.
- Each reconciler: diff matrix (create/noop/update/blocked variants named in §7) and apply request shape against `capellafake` with request-log assertions. `scaling.Check` table (memory, replicas, last-data-group, service removal flags, stats unavailable ± skip).
- `secrets`: `MeetsRules`/`GeneratePassword` (1000 generated all pass, none contain forbidden chars, lengths), file modes 0600/0700, refuse-overwrite, rotate backup/restore, redaction registration.
- `guide`: every topic referenced by an Issue/Action topic exists; `guide config` field lookup.

### 13.2 Scenario tests (CLI in-process against `capellafake`, `FakeClock`) — extend the fake from Phase 2 §3.9 with: project/cluster/bucket/scope/collection/credential/CIDR/schedule/backup-schedule/free-tier endpoints complete enough for the commands, `deploying→healthy` progression, `scaling→healthy`, `turnedOff`, `/stats`, ETag/412 injection, 409 code 1010 on duplicates, lost-response injection for POST, `Seed(...)` helpers to pre-create environments. Required scenarios (each asserts exit code, stdout/stderr text, JSON shape, and `fake.Writes()`):
1. **Idempotency/re-run rule**: build a full environment (project→cluster→protection→schedule→cidrs→buckets→scopes→collections→credentials) with `apply --yes`; second `apply` → `No changes…`, `fake.Writes()` empty, exit 0. Repeat for each resource kind individually.
2. **Round trip**: seed Capella → `sync --stdout`/file → `validate` → `plan` → `No changes`.
3. Plan symbols/summary for every action type; `plan --detailed-exitcode` exit 2 vs 0; blocked → exit 4; `plan` performs zero writes.
4. Immutable changes BLOCKED (region, provider, storageBackend, cidr…) — apply refuses with exit 4 and **zero** writes; scale-in guards (memory, replicas, last data group, service removal flags); disk shrink blocked; Azure fixed-size disk bodies omit storage/iops.
5. Turned-off cluster: children BLOCKED with reason; `apply --power-on` turns on, waits, then applies; schedule changes allowed while off.
6. Races: 412 on cluster/bucket/schedule update (retry once, second 412 → exit 1 message `changed concurrently`), 409 on create adopts, lost POST response adopts (no duplicate created).
7. Failure stop: failing resource mid-plan → later actions `skipped`, exit 1, message lists completed; re-run completes.
8. `apply --prune`: undeclared bucket/scope/collection/CIDR/credential/schedule deleted (typed-name prompt simulated via injected `Prompter`); clusters/projects never pruned; protected cluster blocks bucket prune; `drift` lists them.
9. `destroy`: protected → exit 4 nothing deleted; order children-first; undeclared cluster keeps project; ids cleared from the file afterwards; `apply` rebuilds.
10. Credentials: generated password saved 0600, only `password saved to …` printed, never in stdout/stderr/JSON (assert with `assert_no_secret` equivalent `AssertNoSecret(t, out, secrets…)`); refuses to overwrite an existing secret file before creating the credential; `--rotate-passwords` writes `.bak`, restores on failed send; configured `${VAR}` password not printed.
11. File-managed mode: `cluster scale --nodes 5` edits file + applies; on Capella 4xx the file is restored byte-for-byte; `--no-apply` leaves Capella untouched; invalid candidate reports only new issues; `--direct` bypasses; ambiguity `--project`.
12. `sync`: fresh file; refresh preserving comments, `allowAnyIp`, password refs; org-mismatch exit 3; ambiguous project exit 4; powered-off cluster keeps previous buckets with warning; `--dry-run` diff and no write.
13. Exit-code table (§2) — each code reachable and tested.
14. Redaction: run every command with `--log-level debug` and a registered key/password; assert neither appears anywhere.

### 13.3 Coverage and traceability
≥ 85% statement coverage on `internal/plan`, `internal/resources`, `internal/config/capellafile` (Makefile target `cover-engine` fails below the threshold; add to CI). Tag scenario tests with the capellactl story id in the name (`Test<Subject>_CAP030_AC1_…`) where a story exists; no automated AC-coverage gate required.

### 13.4 Live tests (extend `internal/integration/capella_live_test.go`; same guards as Phase 2 §3.9.4)
With `CBCTL_LIVE_CLUSTER=1` additionally: write a `capella.yaml` under `t.TempDir()` for a `cbctl-ci-*` project with one **free-tier** (or smallest provisioned) cluster, a bucket/scope/collection, a credential (`${CBCTL_LIVE_PWD}` set by the test), one allowed CIDR; run `validate`, `plan`, `apply --yes`, assert second `apply` reports `No changes`, `drift` clean, mutate in Capella out-of-band (add a scope via Phase 2 command) → `drift` reports it unmanaged → `apply --prune --yes` removes it, then `destroy --yes`, project cleanup via janitor. Project-level (no cluster) live subtests: `sync --stdout` of the sandbox org parses back through `LoadText`, and `plan` of that output is "No changes" — this runs whenever the key is present (read-only).

---------------------------------------------------------------------------------------------------

## 14. Edge cases (each needs a test or explicit code path)

- File: empty, BOM, CRLF, tabs, duplicate YAML keys, `projects: []`, both forms present, unknown top-level key, numbers as strings, huge file (10k lines) loads < 1 s.
- Secrets: `${VAR}` unset/empty; `${file:…}` relative to the config file's dir (not cwd), missing, empty, trailing newline trimmed, CRLF; password containing `${` literal; password referenced but not meeting rules; same var in two credentials.
- Names: project/cluster names with spaces/unicode; two projects with the same name (→ BLOCKED until `id:` pinned; `sync --force` pins); renamed project/cluster in Capella with pinned id (UPDATE with rename note) vs unpinned (BLOCKED/hint).
- Pinned `id` that no longer exists → `PlanError` exit 1 with action `remove the id from capella.yaml or run 'cbctl capella sync'`.
- Cluster states: every state in the 00-architecture §8 table (create/update/power/delete success/failure/transitional); `deploying` found at plan time (wait, then reconcile); `turnedOff` found with no schedule; `degraded`.
- Free tier: exactly one per org; forbidden fields; bucket field restrictions; no scale/protect/schedule/backup; activation endpoints.
- Scale: group matching by services set irrespective of order; adding+removing groups; services-set change = remove old + add new (both guard sets apply); total > 27; singleNode; Azure disks; disk shrink; RAM decrease → memory guard.
- Buckets: ephemeral rules; `flush` vs `flushEnabled`; `priority` unset vs server version default; replicas vs maxReplicas on 1-node cluster; memory preflight running tally across multiple new buckets; `enableCrossClusterVersioning` on create.
- Scopes/collections: `_default`/`_system` never touched/listed; scope `collections` objects vs credential `collections` strings; `maxTTL` on server < 7.6; deleting a non-empty scope requires typed-name confirmation; protection blocks.
- Credentials: access wildcard normalization; same access written differently (`read` vs `data_reader`, order, grouping) = NOOP; advanced credential with declared name; undeclared advanced credential during prune; existing secret file; secret directory not writable (fail **before** creating the credential); rotation failure rollback.
- CIDRs: bare IP, host-bits-set prefix, IPv6, duplicates, expired entries, 75-entry limit, REPLACE sequence interrupted between delete and create (re-run converges: missing → CREATE).
- Concurrency/robustness: Ctrl-C during wait (exit 130; re-run resumes); `--no-wait` (children `deferred`); `--timeout` exceeded (exit 5); API key lacking backup roles (warning, not error); rate limiting during a long plan (client retries); fake with 500 on POST → hint text.
- Multi-project files: targets with `project/cluster`; clusters with identical names in two projects; `skipProjects` honoured by sync only.
- Editor: editing a hand-written file with anchors/aliases (yaml.v3 expands aliases — detect and refuse with exit 3 `capella.yaml uses YAML anchors/aliases, which cbctl cannot edit safely; edit the file by hand or remove them`); file changed on disk between plan and save (compare `Original` with on-disk text before `Save`; mismatch → exit 1 `capella.yaml changed while the command was running; nothing was written`).
- Windows paths and `os.Rename` semantics for atomic save (fallback copy+remove).

---------------------------------------------------------------------------------------------------

## 15. Acceptance criteria

1. `go build ./... && go vet ./... && go test -race ./internal/...` green; no unit test uses the network, the OS keychain or the real clock; import-graph test (§1) passes.
2. **Port parity**: for each ported Python unit-test file in `capellactl/tests/unit/` covering normalize/diff/guards/schedule expansion/config validation/redaction/password rules, an equivalent Go test exists (list the mapping in the PR description) and passes.
3. **Idempotency**: scenario 13.2-1 passes for the full environment and for each kind; `fake.Writes()` is empty on every second run.
4. **Round-trip**: scenario 13.2-2 passes on a seeded environment that includes free-tier + provisioned clusters, schedules, backup schedules, CIDRs (incl. `0.0.0.0/0`), ephemeral + couchbase buckets, nested scopes/collections, basic + advanced credentials.
5. **Output contract**: every plan/apply/drift/validate JSON output validates against the ported JSON schemas; text output matches golden files for each action type.
6. **Safety**: no scenario deletes anything without `delete`/`destroy`/`--prune`; protected clusters block cluster delete, bucket prune and destroy with zero writes; immutable changes produce exit 4 and zero writes; scale-in guards enforced.
7. **Secrets**: no password/API key appears in any captured output across all scenarios at `--log-level debug` (scenario 14); generated passwords exist only in 0600 files; secret files are never deleted by cbctl; a failed secret-file preflight happens **before** the credential is created.
8. **File safety**: every write to `capella.yaml` leaves a `.bak` with the previous text; `ChangeAndApply` failure-before-write restores the file byte-for-byte; cbctl-written files round-trip byte-identically; anchors/aliases and concurrent modification are refused.
9. **Exit codes** 0/1/2/3/4/5/130 each reachable and tested exactly as specified in §2/§12; every `CLIError` carries an `Action`.
10. **Compatibility**: a real `capella.yaml` produced by capellactl (copy one from `capellactl/tests/` or `docs/`) validates under cbctl unchanged (`apiVersion: capellactl/v1`), and `plan` against the fake seeded from the same scenario is `No changes`; all Phase 0–2 acceptance tests still pass (flag-snapshot test updated only for the added `-c/--config`, `--direct`, `--no-apply`, new commands).
11. **Docs**: README gains a "Declarative workflow" section (file format, command flow `sync → validate → plan → apply`, `drift`, `destroy`, file-managed mode, secrets, exit codes); `.cursor/rules/cbctl.mdc` updated; `guide` topics shipped; `NOTICE` updated (schema, topics and engine derived from capellactl, Apache-2.0, Couchbase, Inc.).
12. Manual (with `.env`, `CBCTL_LIVE_CLUSTER=1`): §13.4 passes end-to-end on a sandbox org and `make janitor` reports zero leftovers.

---------------------------------------------------------------------------------------------------

## 16. Non-goals / deferred

Advanced credentials (`userRoles`) and custom roles; `import` as a separate verb (it is `sync`; `cbctl capella import` remains the JSONL data importer); hidden `project apply`/`cluster apply` aliases (not needed — no prior cbctl users); shell completion beyond cobra's built-in; Terraform interop; state files; parallel apply (the executor is strictly sequential); partial-failure rollback beyond the file-restore rule in §10; cloning from backups; everything in capellactl 1.5.0/2.0.0.
