# Spec: Fold capellactl into cbctl — Phases 0–2

Status: ready for implementation. Companion to the approved plan (`~/.claude/plans/goofy-crunching-lark.md`).
Scope: Phase 0 (settings/auth/.env/profiles), Phase 1 (hardened Capella API client), Phase 2 (imperative `cbctl capella …` resource commands). **Out of scope:** `capella.yaml`, `plan/apply/destroy/drift/sync/init/validate`, `--no-apply` (Phase 3); the 1.5.0 API backlog (networking, audit logs, users, API keys, billing…).

Source to port from (read alongside this spec; it is authoritative for request-body field names that this spec does not list):
`/Users/michael/IdeaProjects/capellactl/capellactl/` — `api/{client,errors,endpoints}.py`, `engine/waiter.py`, `config/{settings,presets.yaml,compute.yaml,disks.yaml}`, `output/redact.py`, `cli/{resource_cmds,cluster_ops,cluster_clone,cluster_group,cluster_backup,bucket_backup,bucket_cmds,scope_cmds,credential_cmds,cidr_cmds,schedule_cmds,power_cmds,configure}.py`. Ignore everything in those files that reads/writes `capella.yaml` (`open_editor`, `ConfigEditor`, `change_and_apply`, `locate`) — in cbctl the commands act directly on Capella.

Capella API reference: https://docs.couchbase.com/cloud/management-api-reference/index.html. Base URL `https://cloudapi.cloud.couchbase.com`, all paths under `/v4`. Where this spec and the live docs disagree, the docs win; note the divergence in the PR.

---------------------------------------------------------------------------------------------------

## 0. Conventions (apply to every phase)

- Go module `github.com/mminichino/cbctl`, Go 1.26. Stdlib `testing` only (no testify). Table-driven tests, `t.Run`, `t.Parallel()` where there is no shared state.
- Every exported identifier has a doc comment. Errors wrapped with `%w`. `context.Context` is the first parameter of every function that does I/O.
- New dependencies (add with `go get`, then `make tidy`): `github.com/joho/godotenv v1.5.1`, `github.com/zalando/go-keyring v0.2.6`, `gopkg.in/yaml.v3 v3.0.1`, `golang.org/x/term` (latest). Nothing else.
- **No secret may reach any output** (stdout, stderr, debug logs, error strings, JSON output, test failure messages). See §1.6.
- **Do not break existing behaviour.** Existing `cbctl capella cluster create|exists|destroy|test`, `capella bucket|scope|collection create|exists`, `capella import`, and all Server commands keep their flags, output and exit codes (exception: §2.8 changes how returned errors are printed; this is a deliberate fix).
- Commit style: `type(cbctl): subject` (e.g. `feat(cbctl): add settings resolver`). One PR per phase.
- Offline by default: **no unit test may touch the network or the OS keychain**. Live tests are behind `//go:build integration` (§3.9).

---------------------------------------------------------------------------------------------------

## 1. Phase 0 — Settings, auth, `.env`, profiles

### 1.1 Package layout (new)

```
internal/settings/
  settings.go      // Settings, Value, Inputs, Resolve, NormalizeBaseURL, SanitizeAPIKey
  dotenv.go        // LoadDotEnv, findProjectRoot
  profiles.go      // Profile, ProfileStore (YAML file), HomeDir
  keychain.go      // Keyring interface, OSKeyring, MemKeyring (tests)
  settings_test.go dotenv_test.go profiles_test.go
internal/redact/
  redact.go        // Register, String, Object, MaskTail
  redact_test.go
internal/cli/
  configure.go     // `cbctl configure` command group
  errors.go        // ExitCode, CLIError, HandleError (see §2.8)
```

### 1.2 Data model

```go
package settings

// Source says where a resolved value came from; it is shown by `configure show`.
const (
	SourceFlag    = "flag"
	SourceEnv     = "env"        // Value.Detail holds the variable name, e.g. "CAPELLA_API_KEY"
	SourceProfile = "profile"    // Detail holds the profile name
	SourceKeychain= "keychain"   // Detail holds the profile name
	SourceDefault = "default"
	SourceNone    = "not set"
)

type Value struct {
	Value  string
	Source string
	Detail string
}
func (v Value) IsSet() bool { return v.Value != "" }

type Settings struct {
	Profile  string   // resolved profile name ("default" when unspecified)
	APIKey   Value
	OrgID    Value    // may be unset: callers then auto-discover (§1.5)
	BaseURL  Value    // always set; normalized, no trailing slash
	Warnings []string // human-readable, printed once via logging.Warning by the caller
}

// Inputs are the already-parsed command-line values plus injectable dependencies.
type Inputs struct {
	Profile string // --profile ("" = unset)
	Token   string // --token
	OrgID   string // --org-id
	APIHost string // --api-host (bare host OR full URL)
	Env     func(string) string // default os.Getenv
	Home    string              // default HomeDir(Env)
	Keyring Keyring             // default OSKeyring{}
}

func Resolve(in Inputs) (*Settings, error)
```

### 1.3 Precedence (exact)

| Value | Order (first non-empty wins) |
|---|---|
| API key | `--token` → env `CAPELLA_API_KEY` → env `CAPELLA_TOKEN` (alias; the existing integration stub reads it) → keychain entry for the resolved profile, **only if** that profile exists in `profiles.yaml` |
| Org ID | `--org-id` → env `CAPELLA_ORG_ID` → `profiles.yaml[profile].organizationId` → unset |
| Base URL | `--api-host` → env `CAPELLA_API_BASE_URL` → `profiles.yaml[profile].apiBaseUrl` → `https://cloudapi.cloud.couchbase.com` (`config.DefaultCapellaAPIHost` prefixed with `https://`) |
| Profile name | `--profile` → env `CBCTL_PROFILE` → `"default"` |

Notes:
- `--api-host` has a non-empty cobra default on the existing leaf commands (`config.DefaultCapellaAPIHost`). Resolution must use `cmd.Flags().Changed("api-host")` to decide whether the flag was **explicitly** set; an untouched default must not shadow env/profile. Implement by passing `Inputs.APIHost` only when `Changed` is true (helper `flagIfChanged(cmd, name) string` in `internal/cli/flags.go`).
- The deprecated capellactl aliases (`BEARER_TOKEN`, `ORG_ID`, `API_BASE_URL`) are **not** supported.
- `--profile X` where X is not in `profiles.yaml`: not an error; append warning `profile "X" not found; run 'cbctl configure --profile X' to create it`, and continue with env/flag values only.
- Corrupt `profiles.yaml` (YAML parse error): return an error naming the file path; never silently ignore.
- Keychain read errors (locked, backend missing): treated as "no key" plus a warning `keychain unavailable: <err>`; never fatal unless nothing else supplies the key (then the normal "API key not set" error applies, §1.7).

### 1.4 `NormalizeBaseURL` and `SanitizeAPIKey`

```go
// NormalizeBaseURL turns "host", "host:port", "https://host/" into "https://host" (no trailing slash, no path).
// A scheme other than https is rejected UNLESS the host is localhost, 127.0.0.1, [::1] or ends in ".localhost"/".test"
// (used by the fake server in tests); then http is allowed. Empty input returns the default.
func NormalizeBaseURL(s string) (string, error)

// SanitizeAPIKey trims whitespace/CR/LF, a UTF-8 BOM, one pair of surrounding single or double quotes, a leading
// "Bearer " (case-insensitive), and terminal bracketed-paste markers ("\x1b[200~", "\x1b[201~", "[200~", "[201~").
func SanitizeAPIKey(s string) string
```
Both are applied in `Resolve` (to every source, including env and keychain values). Table tests for: quotes, BOM, `Bearer abc`, `bearer abc`, pasted markers, internal spaces (left untouched → will fail at the API with 401, which is correct).

### 1.5 `.env` loading

```go
// LoadDotEnv looks for ".env" in dir, then in each parent up to and including the first directory containing
// go.mod or .git (the "project root"). The first file found is loaded with godotenv semantics but NEVER overrides a
// variable that is already set in the process environment. Returns the path loaded ("" if none) and the names set.
func LoadDotEnv(startDir string) (path string, names []string, err error)
```
- Called once from `cli.Execute()` before cobra parses (`os.Getwd()`); a failure to *parse* `.env` is a warning (`logging.Warning("ignoring .env: %v")`), a missing file is silent. A failure to read the working directory is ignored.
- Debug log (level debug) prints the path and variable **names** only, never values.
- Only variables the process doesn't already have are set (use `godotenv.Read` + `os.LookupEnv` check + `os.Setenv`; do not use `godotenv.Overload`).
- `.env` is already in `.gitignore`. Add `.env.example` (committed):
  ```
  # Capella Management API v4 key (Settings → API Keys in the Capella UI). Use the key's *token*, not its ID.
  CAPELLA_API_KEY=
  CAPELLA_ORG_ID=
  # Optional: override the API host (default https://cloudapi.cloud.couchbase.com)
  # CAPELLA_API_BASE_URL=
  # Live-test sandbox project prefix (default cbctl-ci)
  # CBCTL_TEST_PREFIX=cbctl-ci
  ```

### 1.6 `internal/redact`

```go
func Register(secret string)           // ignores values shorter than 4 chars; safe for concurrent use
func String(s string) string           // replaces every registered secret (longest first) with "***"
func Object(v any) any                 // deep copy of map[string]any / []any / string with secrets masked; additionally masks
                                       // values of keys (case-insensitive) in {password, authorization, apikey, api_key,
                                       // secret, secretkey, token} when the value is non-empty
func MaskTail(s string) string         // "••••" + last 4 chars (ASCII "****" when stdout is not a TTY / NO_COLOR); "" → "(not set)"
func Reset()                           // tests only
```
Integration points (all required):
1. `settings.Resolve` registers the API key.
2. `capellaapi.Client` registers its key on construction and any `password`/`secretKey`/`token` string returned by `CreateCredential`/`RotateKey`-style responses.
3. `logging.logMsg` passes the formatted message through `redact.String` before writing.
4. `cli.HandleError` (§2.8) passes the final error text through `redact.String`.
5. JSON/text renderers redact all strings except the one-time password deliberately shown by `credential create` (§3.7), which is printed through a dedicated `printSecretOnce` helper.

### 1.7 Profiles and keychain

```go
type Profile struct {
	OrganizationID string `yaml:"organizationId,omitempty"`
	APIBaseURL     string `yaml:"apiBaseUrl,omitempty"`
}
type ProfileStore struct{ Path string }               // <Home>/profiles.yaml
func (s ProfileStore) Load() (map[string]Profile, error) // missing file → empty map, nil
func (s ProfileStore) Save(map[string]Profile) error     // dir 0700, file 0600, atomic (write temp + rename)
func HomeDir(env func(string) string) string
// CBCTL_HOME → (windows) %APPDATA%\cbctl → $XDG_CONFIG_HOME/cbctl → ~/.config/cbctl

type Keyring interface {
	Get(profile string) (string, error)   // returns ("", ErrNotFound) when absent
	Set(profile, apiKey string) error
	Delete(profile string) error
}
var ErrNotFound = errors.New("keychain entry not found")
// OSKeyring wraps go-keyring with service "cbctl", user = profile name. MemKeyring is an in-memory fake for tests.
```
The API key is **never** written to any file. If the OS keychain is unavailable, `configure` fails with: `no OS keychain available; set CAPELLA_API_KEY in your environment or .env instead`.

### 1.8 `cbctl configure`

Registered in `root.go` `init()` as `newConfigureCmd()`. Top-level (not under `capella`), but also reachable as `cbctl capella configure` (alias: add the same constructor result to `newCapellaCmd()`; two separate `*cobra.Command` instances from one constructor).

```
cbctl configure [--profile NAME] [--org-id ID] [--api-host HOST|URL] [--api-key-stdin]
cbctl configure list
cbctl configure show [--profile NAME]
cbctl configure remove [--profile NAME]
```
Behaviour of `configure` (save):
1. Obtain key: `--api-key-stdin` → read all of stdin, `SanitizeAPIKey`. Else if stdin is a TTY → prompt `Capella API key (input hidden): ` via `term.ReadPassword`. Else → `CLIError{Code: ExitConfigInvalid, "no API key supplied", Action: "pass --api-key-stdin or run in a terminal"}`.
2. Empty key → same error. Register with `redact`.
3. Validate: build a `capellaapi.Client` (base URL from `--api-host`/env/default) and call `ListOrganizations`. `ErrUnauthorized` → error `the API key was rejected (401)`, action `use the key's token, not its ID, and check the key's IP allow-list`; **nothing is saved**. `ErrForbidden` → same but 403 wording. Transport errors → exit 1, nothing saved.
4. Org ID: `--org-id` if given (must appear in the returned list, else error `organization X is not visible to this key`). Else if exactly one org returned → use it. Else if TTY and multiple → numbered prompt. Else error listing `name (id)` lines and action `pass --org-id`.
5. Save: `Keyring.Set(profile, key)` then `ProfileStore.Save` (merge, do not drop other profiles). If the profile-file write fails after the keychain write succeeded, delete the keychain entry (rollback) and return the error.
6. Output (text): `[info] - Saved profile "<name>" (organization <name> <id>)`. JSON (`-o json`): `{"profile":…,"organizationId":…,"apiBaseUrl":…}`. Never prints the key.

`list`: table `PROFILE  ORGANIZATION  API HOST  KEY` (KEY = `stored`/`missing`); JSON array. `show`: rows `profile | api key (masked) | organization | api base URL`, each with its Source (and Detail) from `Resolve`. `remove`: delete keychain entry (not-found is fine) and the profile; absent profile → `[warning]` and exit 0; with no `--yes` on a TTY ask `Remove profile "x"? [y/N]`.

### 1.9 Wiring into existing commands

`capellaFlags.config()` (internal/cli/flags.go) must call `settings.Resolve` and populate `config.PropCapellaToken`, `PropCapellaAPIHost` (host form: strip scheme), and `PropCapellaOrgID` when the corresponding flag is empty. Existing flags keep working and take precedence. The error text when no key is found (used by `capella.NewClientFromConfig` and the new commands):
`Capella API key not set; use --token, set CAPELLA_API_KEY (or put it in .env), or run 'cbctl configure'`.

### 1.10 Phase 0 acceptance criteria

1. `go test ./internal/settings/... ./internal/redact/...` passes with no network/keychain access (use `MemKeyring`, `t.TempDir()`, injected `Env`).
2. Table test proves every precedence row in §1.3, including "explicit `--api-host` beats env" and "default `--api-host` does not beat env".
3. `.env` test: file in parent dir found; existing env var not overridden; `.env` with quotes/`export` prefix parsed; missing file silent; malformed file → warning, not error; search stops at the dir containing `go.mod`.
4. `profiles.yaml` is created with mode `0600` in a `0700` directory (skip the mode assertion on Windows); no file under `HomeDir` contains the API key (test greps the temp dir).
5. `configure` with a fake API (`httptest`): 401 saves nothing; success with 1 org auto-selects; multiple orgs without `--org-id` errors listing them; keychain-ok/profile-write-fail rolls the keychain back.
6. A test that registers a secret and asserts it never appears in: captured `logging` output, a returned `CLIError` message after `HandleError`, and `-o json` output of `configure show`.
7. `cbctl configure show` against a real shell with only `.env` populated prints the key masked and Source `env CAPELLA_API_KEY`.

---------------------------------------------------------------------------------------------------

## 2. Phase 1 — Hardened Capella API client

### 2.1 Package layout (new package `internal/capellaapi`; legacy `internal/capella` stays)

```
internal/engine/
  clock.go           // Clock interface, RealClock, FakeClock
  waiter.go          // WaitForCluster, WaitForStatus (+ errors) — consumed in Phase 2
internal/capellaapi/
  client.go          // Client, Option, New, Do
  retry.go           // backoff, retry policy, request-sent tracking
  errors.go          // APIError, sentinels, AmbiguousWriteError, TransportError
  paginate.go        // Paginate[T]
  roles.go           // RequiredRoles(method, path)
  models.go          // typed models (§2.5)
  inventory.go       // Inventory []Endpoint (method, path template)
  endpoints_org.go endpoints_project.go endpoints_cluster.go endpoints_freetier.go
  endpoints_schedule.go endpoints_cidr.go endpoints_bucket.go endpoints_scope.go
  endpoints_collection.go endpoints_credential.go endpoints_snapshot.go endpoints_backup.go
  *_test.go          // httptest contract tests per file
```
The legacy `internal/capella` package **imports** `capellaapi` (never the reverse) and keeps its public surface; see §2.7.

### 2.2 Client

```go
type Client struct { /* unexported */ }
type Option func(*Client)

func WithBaseURL(u string) Option            // normalized with settings.NormalizeBaseURL semantics (copy the logic; do not import settings)
func WithOrgID(id string) Option
func WithHTTPClient(h *http.Client) Option
func WithClock(c engine.Clock) Option
func WithMaxAttempts(n int) Option           // default 6; n<1 → 1
func WithRand(r *rand.Rand) Option           // deterministic jitter in tests
func WithUserAgent(version string) Option    // "cbctl/<version> (go/<runtime.Version()>)"

func New(apiKey string, opts ...Option) (*Client, error) // error if apiKey == ""; registers key with redact
func (c *Client) OrgID() string
func (c *Client) SetOrgID(id string)
func (c *Client) OrgPath() (string, error)   // "/v4/organizations/<id>"; error "organization ID is not set" if empty
func (c *Client) WritesAccepted() int        // count of 2xx non-GET responses (atomic)

type RequestOptions struct {
	Query   url.Values
	IfMatch string // bare version, e.g. "12"
	Body    any    // marshaled once to []byte; nil = no body
}
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}
func (r *Response) ETag() string // parses `"Version: 12"`, `Version: 12`, `"12"` → "12"; "" if absent

func (c *Client) Do(ctx context.Context, method, path string, opt RequestOptions, out any) (*Response, error)
```
Convenience wrappers (all return `(*Response, error)`; `out` may be nil): `Get(ctx, path, q url.Values, out)`, `Post(ctx, path, body, out)`, `Put(ctx, path, body, ifMatch, out)`, `Delete(ctx, path, q url.Values)`.

`Do` rules:
- Headers: `Authorization: Bearer <key>`, `Accept: application/json`, `User-Agent`, `Content-Type: application/json` only when a body exists, `If-Match` only when set.
- Body marshaled once; each attempt gets a fresh `bytes.Reader`.
- 204/empty body with `out != nil` → leave `out` untouched, no error. Non-JSON 2xx body with `out != nil` → `fmt.Errorf("decode %s %s: %w")`.
- HTTP transport defaults (when `WithHTTPClient` not used): `DialContext` timeout 10 s, `TLSHandshakeTimeout` 10 s, `ResponseHeaderTimeout` 60 s, no overall `Client.Timeout` (the context governs), `ForceAttemptHTTP2: true`.
- Debug logging (`logging.Debug`): `METHOD path -> status (Nms)`; with request body redacted via `redact.Object` after unmarshal-to-`any`.

### 2.3 Retry policy (exact port of capellactl `client.py`)

- `maxAttempts` default 6. Retryable statuses: 429, 500, 502, 503, 504.
- Backoff before attempt *n+1*: `rand.Float64() * min(30s, 1s * 2^(n-1))` (full jitter). If the response has `Retry-After` (integer seconds **or** HTTP-date), use that instead (clamped ≥ 0, ≤ 5 min).
- **GET/PUT/DELETE**: retry on retryable statuses and on any transport error.
- **POST**: retry only on 429, or on a transport error where the request provably was not sent. Track "sent" with `httptrace.ClientTrace{WroteRequest: …}`: if `WroteRequest` fired (even with err), the request may have been applied → return `*AmbiguousWriteError`, no retry. A dial/DNS/TLS-handshake failure (no `WroteRequest`) is retryable.
- Sleeping uses `Clock.Sleep(ctx, d)` and aborts immediately with `ctx.Err()` on cancellation.
- Exhausted retries on a transport error → `*TransportError{Msg: "<METHOD> <path>: could not reach Capella (<kind>)", Cause: err}`; exhausted retries on a retryable status → the normal `*APIError` for the last response.
- Retries log at `warning` level: `retrying GET /v4/... (attempt 2/6) after 503`.

### 2.4 Errors

```go
type APIError struct {
	Status  int
	Code    int    // Capella "code" field, 0 if absent
	Message string // redacted
	Hint    string // redacted
	Method  string
	Path    string
	Body    string // raw body, redacted, truncated to 2 KiB — for debug/legacy compat only
}
func (e *APIError) Error() string // "<METHOD> <path>: <status> <message>" (+ " (hint: …)")
func (e *APIError) Is(target error) bool // maps Status → sentinel below

var (
	ErrUnauthorized       = errors.New("unauthorized")        // 401
	ErrForbidden          = errors.New("forbidden")           // 403
	ErrNotFound           = errors.New("not found")           // 404
	ErrConflict           = errors.New("conflict")            // 409
	ErrPreconditionFailed = errors.New("precondition failed") // 412
	ErrValidation         = errors.New("validation failed")   // 400, 422
	ErrRateLimited        = errors.New("rate limited")        // 429
	ErrServer             = errors.New("server error")        // >= 500
)
type AmbiguousWriteError struct{ Method, Path string; Cause error } // Unwrap → Cause
type TransportError struct{ Msg string; Cause error }                // Unwrap → Cause
```
Parsing: Capella error body is `{"httpStatusCode","code","message","hint"}`. If the body is not JSON or lacks `message`, `Message = "HTTP <status>"` + `": " + first 200 chars of body, newlines → spaces`.
403 enrichment: `Hint` falls back to `"requires: " + RequiredRoles(method, path)` when Capella gave none and `RequiredRoles` knows the endpoint.
401 enrichment: `Hint` = `use the API key token (not the key ID) and check the key's IP allow-list`.

`roles.go`: `func RequiredRoles(method, path string) string` — port `_ROLES` from `endpoints.py` verbatim (regex list; first match wins; `""` if none).

### 2.5 Pagination

```go
// Paginate GETs path with page/perPage=100 and follows cursor.pages.next until absent. key is the JSON field holding
// the items ("data" for almost everything; "scopes" for scope lists). A top-level JSON array response is returned as-is.
func Paginate[T any](ctx context.Context, c *Client, path, key string, q url.Values) ([]T, error)
```
Guard against loops: stop if `next` ≤ current page or after 10 000 pages (return an error). A 404 returns the error (callers decide whether it means empty — unlike the legacy `GetPaged` which swallowed it).

### 2.6 Models (`models.go`)

All JSON tags exactly as the API; `omitempty` on optional fields; pointers for optional scalars where zero is meaningful.
Reuse field shapes from `internal/capella/types.go` (`ProjectData`, `ClusterData`, `ServiceGroupData`, `NodeConfig`, `DiskConfig`, `CapellaBucketData`, `CredentialData`, `DatabaseAccessEntry`, `AllowedCIDRData`, `AuditData`) by **copying** them under the shorter names `Project`, `Cluster`, `ServiceGroup`, `NodeConfig`, `Disk`, `Bucket`, `Credential`, `AccessEntry`, `AllowedCIDR`, `Audit`, `Organization` (no import cycle). Additions:

```go
type Cluster struct { /* as ClusterData, plus: */
	DeletionProtection *bool `json:"deletionProtection,omitempty"`
	ETag               string `json:"-"` // set by GetCluster from the response header
}
type ClusterStats struct { FreeMemoryInMb, TotalMemoryInMb, MaxReplicas int }  // json: freeMemoryInMb, totalMemoryInMb, maxReplicas
type CreateClusterRequest struct { /* copy from capella.CreateClusterRequest */ }
type UpdateClusterRequest struct {
	Name, Description string
	Support       *Support       `json:"support,omitempty"`
	ServiceGroups []ServiceGroup `json:"serviceGroups,omitempty"`
}
type Object = map[string]any // for payloads whose shape is passed through verbatim (see below)
```
**Pass-through payloads.** On/off schedules, cloud-snapshot schedules/restores/creates, bucket-backup schedules/restores and free-tier clusters use `Object`. The CLI layer builds the maps using exactly the keys capellactl's `cli/schedule_cmds.py`, `cli/cluster_backup.py`, `cli/bucket_backup.py` and `resources/*` use. Do not invent field names; if a key is not in those files, check the docs.

### 2.7 Endpoint functions

One method per row of `Inventory` (80 operations; list in `capellactl/api/endpoints.py` `INVENTORY`). Method names and signatures (all `ctx context.Context` first; `p` = projectID, `cl` = clusterID, `b` = bucketID **already base64url-encoded** — see below):

```go
// org
func (c *Client) ListOrganizations(ctx) ([]Organization, error)
func (c *Client) GetOrganization(ctx) (*Organization, error)
// projects
func (c *Client) ListProjects(ctx) ([]Project, error)
func (c *Client) GetProject(ctx, p string) (*Project, string /*etag*/, error)
func (c *Client) CreateProject(ctx, name, description string) (id string, err error)
func (c *Client) UpdateProject(ctx, p, name, description, ifMatch string) error
func (c *Client) DeleteProject(ctx, p string) error                 // 409 when not empty → ErrConflict
// clusters
func (c *Client) ListClusters(ctx, p) ([]Cluster, error)
func (c *Client) GetCluster(ctx, p, cl) (*Cluster, error)           // Cluster.ETag populated
func (c *Client) CreateCluster(ctx, p string, req CreateClusterRequest) (id string, err error)
func (c *Client) UpdateCluster(ctx, p, cl string, req UpdateClusterRequest, ifMatch string) error
func (c *Client) DeleteCluster(ctx, p, cl string, retainSnapshotBackups bool) error // ?retainsnapshotbackups=true|false
func (c *Client) SetDeletionProtection(ctx, p, cl string, enabled bool) error
func (c *Client) TurnClusterOn(ctx, p, cl string, withAppService bool) error   // POST …/activationState {"turnOnLinkedAppService":…}
func (c *Client) TurnClusterOff(ctx, p, cl string) error                      // DELETE …/activationState
func (c *Client) GetClusterStats(ctx, p, cl) (*ClusterStats, error)
// free tier (all take/return Object where noted in capellactl)
CreateFreeCluster, GetFreeCluster, UpdateFreeCluster, DeleteFreeCluster, TurnFreeClusterOn, TurnFreeClusterOff,
ListFreeBuckets (returns buckets + ClusterStats, NOT paginated), GetFreeBucket, CreateFreeBucket, UpdateFreeBucket, DeleteFreeBucket
// on/off schedule: GetSchedule(→ (Object, etag, error); ErrNotFound when none), CreateSchedule, UpdateSchedule(ifMatch), DeleteSchedule,
//   PauseSchedule (DELETE …/onOffSchedule/activationState), ResumeSchedule (POST …/activationState)
// allowed CIDRs: ListAllowedCIDRs, GetAllowedCIDR, CreateAllowedCIDR(ctx,p,cl, cidr, comment string, expiresAt string) (id), DeleteAllowedCIDR
// buckets: ListBuckets, GetBucket, CreateBucket(CreateBucketRequest) (id), UpdateBucket(ifMatch), DeleteBucket
// scopes:  ListScopes (key "scopes"), GetScope, CreateScope(name), DeleteScope
// collections: ListCollections, GetCollection, CreateCollection(name string, maxTTL *int), UpdateCollection(maxTTL, ifMatch), DeleteCollection
// credentials: ListCredentials, GetCredential, CreateCredential(CreateCredentialRequest) (id, password string, err error),
//              UpdateCredential(ifMatch), DeleteCredential
// snapshots (cluster backups): GetSnapshotSchedule (nil,nil on 204), UpsertSnapshotSchedule, DeleteSnapshotSchedule, GetSnapshotScheduleOptions,
//   ListSnapshotRegions, ListSnapshots, ListProjectSnapshots, CreateSnapshot(→ backupId), UpdateSnapshotRetention, DeleteSnapshot,
//   RestoreSnapshot(→ restoreId), ListSnapshotRestores
// bucket backups: Get/Create/Update/DeleteBucketBackupSchedule, ListBackupCycles, ListCycleBackups, CreateBucketBackup (no body returned),
//   ListClusterBackups, GetBackup, DeleteBackup, RestoreBackup, ExportBackup(→ exportId), GetBackupExport
```
Rules:
- **Bucket IDs** are URL-safe base64 (unpadded? — use `base64.RawURLEncoding`) of the bucket name. Provide `func BucketID(name string) string`; endpoint functions take the **ID**, not the name. Verify encoding against the live docs; if Capella requires padded, switch to `URLEncoding` and note it. Scope/collection names go in the path through `url.PathEscape`.
- Create calls return the new ID parsed from the response body (`{"id":…}`); cluster create returns 202.
- `CreateCredential` registers the returned password with `redact` immediately.
- Passwords/keys are never logged.
- `Inventory` is exported as `[]Endpoint{Method, Path string}`; a test asserts every inventory row is exercised by at least one contract test (the test harness records `(method, normalized path)` hits and compares).

### 2.8 Error printing and exit codes (`internal/cli/errors.go`)

```go
type ExitCode int
const (
	ExitOK ExitCode = 0; ExitError = 1; ExitChanges = 2 /*reserved: Phase 3*/; ExitConfigInvalid = 3
	ExitBlocked = 4; ExitTimeout = 5; ExitInterrupted = 130
)
type CLIError struct { Code ExitCode; Message, Action string; Err error } // Unwrap → Err
func Errorf(code ExitCode, action, format string, a ...any) *CLIError
func HandleError(err error) ExitCode   // prints and returns the code; used by main
```
`HandleError`: `context.Canceled`/SIGINT → 130 (print nothing but a newline). `*CLIError` → `[error] - <Message>` then `[action] - <Action>` (if any; add `Action` as a plain stderr line `  → <Action>`). `*capellaapi.APIError` → code 1, message from `Error()`, action = its Hint. `*TransportError`/`*AmbiguousWriteError` → code 1 with an action `re-run the command; check the resource before retrying`. engine timeouts → 5. Everything else → 1 with `err.Error()`. All text redacted. Printed to stderr, once.
`cmd/cbctl/main.go` becomes: `os.Exit(int(cli.HandleError(cli.Execute())))` with `ExitOK` for nil. `rootCmd.SilenceErrors` stays true; `Execute()` installs a `signal.NotifyContext(SIGINT, SIGTERM)` and runs `rootCmd.ExecuteContext(ctx)`; commands read `cmd.Context()`.
Legacy commands that call `exitErr()` directly keep doing so (unchanged).

### 2.9 Legacy bridge (`internal/capella/client.go`)

Keep the `Client` struct fields and all methods used by `organization.go`, `project.go`, `cluster.go`, etc. Re-implement `doJSON` as a thin adapter:
1. Lazily construct a `capellaapi.Client` from `Token`, `APIHost`/optional new `BaseURL` field (if `BaseURL` != "" it wins over `https://APIHost`), `HTTP`.
2. Call `Do(context.Background(), …)`; set `lastStatus`/`lastBody` from the response.
3. Map `*capellaapi.APIError` → `*CapellaAPIError{Code: Status, Body: e.Body, Message: "Capella API error" (or "not found" for 404)}` so existing `IsNotFound`, `AsCapellaAPI` keep working. Other errors pass through.
4. `IsNotFound`/`AsCapellaAPI`/`AsCapellaNotFound` re-implemented with `errors.As` (behaviour unchanged).
5. `GetPaged` keeps its current 404→`nil,nil` behaviour (existing callers rely on it) but uses `capellaapi.Paginate` internally.
Result: existing commands gain retries and redaction with no signature change. Existing tests in `internal/capella` must still pass untouched.

### 2.10 `internal/engine` (used by Phase 2 and Phase 3)

```go
type Clock interface {
	Now() time.Time
	Monotonic() time.Duration         // since an arbitrary fixed point
	Sleep(ctx context.Context, d time.Duration) error // returns ctx.Err() if cancelled first
}
type RealClock struct{}
type FakeClock struct{ /* Advance(d); Sleep advances instantly and records durations */ }

type Op string
const (OpCreate Op = "create"; OpUpdate = "update"; OpReady = "ready"; OpPowerOff = "power_off"; OpPowerOn = "power_on"; OpDelete = "delete")

var Transitional = set{"draft","deploying","scaling","upgrading","rebalancing","peering","destroying","turningOff","turningOn"}
// Success/Failure state sets: copy SUCCESS/FAILURE from capellactl engine/waiter.py exactly
// (create/update/ready→healthy; power_off→turnedOff; power_on→healthy; delete→404).

type WaitFailed struct{ Name, State string }     // Error(): "cluster X entered S; check the Capella UI activity log"
type WaitTimeout struct{ Name, State string; Timeout time.Duration }
type StatusFailed struct{ Label, State string }
type StatusTimeout struct{ Label, State string; Timeout time.Duration }

type WaitHooks struct {
	OnState func(state string, elapsed time.Duration) // called on every state change (incl. first)
	OnTick  func()                                    // every 30 s of waiting (CLI prints a dot to stderr)
}
func WaitForCluster(ctx, c *capellaapi.Client, p, cl string, op Op, timeout time.Duration, clk Clock, name string, h WaitHooks) (string, error)
func WaitForStatus(ctx, read func(context.Context) (string, error), done, failed map[string]bool, timeout time.Duration, clk Clock, label string, h WaitHooks) (string, error)
```
Polling: first poll immediately, then intervals `10s, 15s, 22.5s, 33.75s, 50.6s, 60s, 60s…` (×1.5, cap 60 s); never sleep past the timeout (final sleep = remaining time, then one last poll). `op == OpDelete` treats `ErrNotFound` as success `"deleted"`; other ops return the error. `WaitForStatus`: `read` returning `""` means "not listed yet" → state `"queued"`; comparison is case-insensitive. Provide `PollIntervals(n int) []time.Duration` for tests.
The legacy 600×1s loop in `capella.Cluster.Wait` is left alone.

### 2.11 Logging changes (`internal/logging/logging.go`)

Add levels: `type Level int; const (LevelError Level = iota; LevelWarn; LevelInfo; LevelDebug)`; `SetLevel(Level)`, `ParseLevel(string) (Level, error)` (accepts error|warn|warning|info|debug). Default `LevelInfo`. `Info` is suppressed below info; `Warning` below warn; add `Debug(msg, args...)` → stderr, format `[debug] - …`. `Println` (machine-readable stdout) is **never** suppressed. All output passes through `redact.String`. Existing `logging_test.go` must pass unchanged.

### 2.12 Phase 1 acceptance criteria

1. Contract tests (`httptest.Server`, `FakeClock`, seeded `rand`) cover, per endpoint family file, request method/path/query/headers/body and response decoding; the inventory-coverage test passes.
2. Retry tests: 503→503→200 succeeds with 2 recorded sleeps ≤ jitter ceilings; `Retry-After: 7` sleeps exactly 7 s; GET retried on connection reset; POST on 500 is **not** retried (returns `*APIError`); POST on 429 is retried; POST where the server closes the connection after reading the request → `*AmbiguousWriteError`; POST to a closed port → retried then `*TransportError`; 6 attempts max; context cancel during backoff returns promptly with `context.Canceled`.
3. Error tests: each status maps to its sentinel via `errors.Is`; non-JSON body; 403 gets role hint; 401 gets key hint; secret registered earlier is masked in `Error()`.
4. Pagination tests: 3 pages via `cursor.pages.next`; `perPage=100` sent; top-level array; loop guard; `key:"scopes"`.
5. ETag parsing test: `"Version: 12"`, `Version: 12`, `"12"`, missing.
6. Waiter tests (FakeClock): intervals equal `PollIntervals`; create→healthy; `deploymentFailed` → `*WaitFailed`; timeout → `*WaitTimeout` with no sleep beyond the deadline; delete → success on 404; `OnTick` fires every 30 s of fake time.
7. `go test -race ./internal/capellaapi/... ./internal/engine/... ./internal/capella/...` passes; existing `internal/capella` tests unchanged and green.
8. `HandleError` table test covers each error type → exit code and exact stderr text; secrets masked.
9. Manual: with `.env` populated, a throwaway `go run` snippet (not committed) calling `ListOrganizations` returns the real org.

---------------------------------------------------------------------------------------------------

## 3. Phase 2 — Imperative `cbctl capella` commands

### 3.1 Files

```
internal/cli/
  capella_globals.go   // persistent flags, apiFlags, runtime (client + options) construction
  capella_resolve.go   // name → ID resolution
  capella_output.go    // Renderer, table/JSON helpers, confirm()
  capella_project.go capella_cluster.go capella_cluster_group.go capella_cluster_schedule.go
  capella_cluster_backup.go capella_cidr.go capella_bucket.go capella_bucket_backup.go
  capella_scope.go capella_collection.go capella_credential.go
internal/presets/
  presets.go           // //go:embed presets.yaml compute.yaml disks.yaml; Preset, ComputeSizes, DiskTypes
  presets.yaml compute.yaml disks.yaml   // copied verbatim from capellactl/config
internal/capellafake/
  fake.go              // in-memory Capella (httptest.Server) — minimal version used by CLI tests (extended in Phase 4)
```
`newCapellaCmd()` in `capella.go` additionally registers: `project`, `cidr`, `credential`, `configure`, and **extends** existing `cluster`, `bucket`, `scope`, `collection` groups with new subcommands. Existing subcommand names (`create`, `exists`, `destroy`, `test`) are untouched; where a new subcommand has the same name as an existing one (`bucket create`, `scope create`, `collection create`, `cluster create`, `cluster exists`) **the existing one is kept as-is** and the new capability is reached through new flags only when explicitly listed in §3.6. This guarantees no regression; unification is Phase 3/4.

Naming clash to avoid: the existing legacy `capellaFlags` uses `-u/-p` shorthands. New commands do not use `-u`/`-p`.

### 3.2 Global flags (persistent on `capella`)

| Flag | Type/default | Meaning |
|---|---|---|
| `-o, --output` | `text`\|`json`, `text` | output format; invalid → `CLIError{ExitConfigInvalid}` |
| `-y, --yes` (alias `--auto-approve`) | bool | skip confirmation prompts |
| `-q, --quiet` | bool (env `CBCTL_QUIET=1`) | log level warn; no progress dots/hints |
| `--no-wait` | bool | return once the API accepts an async operation |
| `--timeout` | duration, `60m` | max wait for async operations (`time.ParseDuration`; also accept `90m`, `2h`) |
| `--log-level` | error\|warn\|info\|debug, `info` | |
| `--profile` | string | profile name (§1.3) |

The legacy `cluster test` command's local `--timeout` (int seconds, in `cluster.go`) lives on a *server* command, not under `capella`, so no clash; verify with `go build` + `cbctl capella cluster test --help`. Persistent-flag shorthands must not collide with any leaf shorthand (`-u`, `-p`): `-o`, `-y`, `-q` are free.

Per-leaf flags for every **new** command (added by `addAPIFlags(cmd)`): `--token`, `--org-id`, `--api-host`. (Same names as legacy, so docs stay consistent.) `--project NAME` is added only where §3.4 says.

### 3.3 Runtime construction

```go
type runtime struct {
	Ctx      context.Context
	Client   *capellaapi.Client
	Settings *settings.Settings
	Out      Renderer
	JSON     bool; Yes, Quiet, NoWait bool; Timeout time.Duration
	Clock    engine.Clock
}
func newRuntime(cmd *cobra.Command, f *apiFlags) (*runtime, error)
```
Steps: apply `--log-level`/`--quiet`; `settings.Resolve` (print its warnings); error if API key unset (message in §1.9, code `ExitConfigInvalid`); build client (`capellaapi.New`); **org resolution**: if `OrgID` unset call `ListOrganizations`: exactly one → use it (debug log); zero → error `no organizations visible to this API key`; more than one → `CLIError{ExitConfigInvalid, "several organizations: <name (id)>, …", Action: "pass --org-id or save one with 'cbctl configure'"}`. The `capellafake` base URL for tests is injected via `CAPELLA_API_BASE_URL`.

### 3.4 Name resolution (`capella_resolve.go`)

```go
func (r *runtime) resolveProject(name string) (*capellaapi.Project, error)            // by exact name
func (r *runtime) resolveCluster(name, projectName string) (*capellaapi.Project, *capellaapi.Cluster, error)
func (r *runtime) resolveBucket(p, cl, name string) (*capellaapi.Bucket, error)       // BucketID(name) then GetBucket; 404 → not found error
```
`resolveCluster`: if `projectName != ""` look only in that project; else list all projects and their clusters. Zero matches → `CLIError{ExitConfigInvalid, "cluster X not found", Action: "check the name with 'cbctl capella cluster list'"}`. More than one → `CLIError{ExitConfigInvalid, "several clusters are named X (projects A, B)", Action: "add --project NAME"}`. A positional argument that looks like a UUID (`^[0-9a-f]{8}-…$`) is matched against ID first (convenience; names win on tie). Project-not-found: `project X not found`, action `check the name with 'cbctl capella project list'`. Matching is exact and case-sensitive.

### 3.5 Output (`capella_output.go`)

```go
type Renderer interface {
	Table(cols []string, rows [][]string)      // text: aligned columns, header row, 2-space gutter; JSON mode: no-op
	Object(v any)                              // text: key: value lines (sorted as given by struct order); JSON: indented JSON
	List(v any)                                // JSON: array; text: caller uses Table
	Line(format string, a ...any)              // text only, stdout; suppressed in JSON
	Progress(dot string)                       // stderr, only when stderr is a TTY, !quiet, CI unset
}
func confirm(rt *runtime, question string) (bool, error)
```
`confirm`: `--yes` → true; stdin not a TTY and no `--yes` → `CLIError{ExitBlocked, "confirmation required but no terminal is attached", Action: "re-run with --yes"}`; otherwise prompt `<question> [y/N]: ` on stderr, accept `y`/`yes`. Used by destructive commands only (§3.6 marks them ⚠).
JSON rules: stdout contains **only** the JSON document (single object or array); all logs go to stderr; secrets redacted except the one-time credential password. Timestamps pass through as the API returns them.
Text tables: columns exactly as listed in §3.6.

### 3.6 Command catalogue

Legend: ⚠ = asks for confirmation (`--yes` skips); **W** = async, honours `--no-wait`/`--timeout` via `engine`; `[P]` = accepts `--project NAME` (disambiguation only). `CLUSTER` positional = cluster name or ID. All commands also take the §3.2/§3.3 flags.

**project**
| Command | Behaviour |
|---|---|
| `project list` | `ListProjects`; cols `NAME ID DESCRIPTION` |
| `project get NAME` | `Object` of the project |
| `project create NAME [--description S]` | `CreateProject`; text `created project NAME (ID)`; name already exists → `ErrConflict` surfaced as `project NAME already exists` (exit 1) |
| `project update NAME [--description S]` | GET (etag) → PUT with `ifMatch`; on 412 re-GET and retry once; no changes given → error `nothing to update` (exit 3) |
| `project delete NAME ⚠ [--cascade] [--discard-snapshots]` | without `--cascade`: `DeleteProject`; 409 → error `project NAME is not empty`, action `delete its clusters first or pass --cascade`. With `--cascade`: for each cluster in the project (any with deletion protection on → abort with exit 4 before deleting anything) call the cluster-delete flow (**W**, sequential), then delete the project |

**cluster** (`cluster create|exists|destroy|test` already exist; add the following)
| Command | Behaviour |
|---|---|
| `cluster list [--project]` | all clusters (all projects when omitted); cols `NAME PROJECT PROVIDER REGION NODES STATE` (NODES = sum of `numOfNodes`) |
| `cluster get CLUSTER [P]` | `Object`: id, name, project, state, provider/region/cidr, server version, availability, support, service groups, connection string, deletion protection |
| `cluster options` | offline + live: free-tier availability (does the org already have a free cluster?), provider list, presets (`presets.yaml`), compute sizes (`compute.yaml`), support plans/timezones, service-group limits. JSON mode emits the same structure as capellactl's `cluster_options` (`freeTier`, `provisioned.{providers,sizes,supportPlans,supportTimezones,regionsInUse,services,groupLimits,nodeSizes,diskTypes,serverVersionsInUse}`) |
| `cluster disk-types [--provider P]` | from `disks.yaml` |
| `cluster scale CLUSTER [P] (--nodes N \| --step +1\|-1) [--group SERVICES] [--all-groups] [--min N] [--max N] [--skip-capacity-check]` **W** | Port `step_scale`/`scale_in_file` semantics. `--nodes` and `--step` mutually exclusive (exit 3). Free-tier → exit 4 `free-tier clusters have 1 node and a fixed size`. If cluster state ∈ Transitional: with `--no-wait` → exit 4; else `WaitForCluster(OpReady)` first, then re-GET. Group selection: `--group a,b` (services sorted, comma-joined key) or default = the group containing `data`; zero/multiple matches → exit 3 listing groups; `--all-groups` selects all. Limits: single-node cluster 1..1; else group index 0 → 3..27, others 2..24 (overridable by `--min/--max`); `--nodes` outside limits → exit 3. `--step` at a limit → success, no write, status `at_limit`. Capacity pre-check (unless skipped): `GetClusterStats`; if growing and `freeMemoryInMb` is insufficient → exit 4 (replicate capellactl's check; if its rule is unclear, skip with a debug log rather than guessing). Write: `UpdateCluster` with the current cluster's `name/description/support/serviceGroups` with only `numOfNodes` changed, `ifMatch` = ETag; 412 → re-GET, recompute, retry once. Then `WaitForCluster(OpUpdate)`. JSON: single object `{cluster, group, from, to, status}` or array for multiple groups; `status` ∈ scaled\|at_limit |
| `cluster update CLUSTER [P] [--description S] [--support-plan P] [--support-timezone T] [--cpu N --ram N] [--disk-type T --disk-size N --disk-iops N --disk-auto-expansion[=bool]] [--group SERVICES]` **W** | Read-modify-write via `UpdateCluster` (compute/disk flags apply to the selected group). No flag → exit 3. Support plans: `basic`, `developer pro`, `enterprise`; timezones `ET PT GMT IST` (validate before calling). Warn (do not fail) when `cpu/ram` not in `compute.yaml` for the provider |
| `cluster power CLUSTER [P] (--on \| --off) [--with-app-service]` **W** | Exactly one of `--on/--off` (exit 3). Free tier uses the `freeTier` activation endpoints (detect via cluster `configurationType`/free-tier listing as capellactl `is_free` does). Already in target state (`turnedOff` for off, `healthy` for on) → success no-op `already off/on`. `--off` ⚠. Waits `OpPowerOff` / `OpPowerOn` |
| `cluster protect CLUSTER [P] (--on \| --off)` | `SetDeletionProtection`; no wait |
| `cluster delete CLUSTER [P] ⚠ [--discard-snapshots] [--cascade]` **W** | Deletion protection on → exit 4 `cluster X has deletion protection on`, action `run 'cbctl capella cluster protect X --off' first`. `DeleteCluster(retainSnapshotBackups = !discardSnapshots)`; free tier uses `DeleteFreeCluster`. `--cascade` additionally deletes nothing extra in Phase 2 (reserved; accept and ignore with a debug log — project cascade handles clusters). Waits `OpDelete` |
| `cluster clone SOURCE NEW_NAME --region R [--project P] [--to-project P2] [--cidr C] [--support-plan P] [--server-version V\|latest] [--description S] [--with-cidrs] [--with-credentials] [--no-data-model]` **W** | Port `find_source`/`build_clone` **minus all YAML**. Source must be healthy or turnedOff (else exit 4); free-tier source → exit 4. Target project defaults to the source's; must exist (exit 3). Name must not already exist in the target project (exit 4). CIDR default = first free `10.N.M.0/24` not overlapping any cluster CIDR in source+target projects (port `suggest_cidr`; port `_overlap` with `net/netip`); explicit overlapping `--cidr` → exit 3. Same region *and* project as source → ⚠ confirm. Steps: `CreateCluster` (copy `configurationType`, `availability`, `support`, `serviceGroups`, server version unless `latest`, `deletionProtection` applied after healthy) → wait healthy → if `--with-cidrs` create each source CIDR with comment `(cloned from SOURCE)`; unless `--no-data-model` create buckets/scopes/collections (skip `_default`/`_system`) with source settings; if `--with-credentials` create credentials with **generated** passwords (see §3.7) and print them once. Print a "not copied" summary (documents, hibernation schedule, CMEK, private DNS, and any option not passed). Failure after cluster creation: leave the cluster, print exactly which steps completed and which did not, exit 1 |
| `cluster group list CLUSTER [P]` | cols `SERVICES NODES CPU RAM DISK` |
| `cluster group add CLUSTER [P] --services S --nodes N --cpu N --ram N [--disk-type T --disk-size N --disk-iops N --disk-auto-expansion]` **W** | append group via `UpdateCluster`; services must be a set of `data,index,query,search,analytics,eventing` not already forming an existing identical group; limits per capellactl `GROUP_LIMITS` |
| `cluster group remove CLUSTER SERVICES [P] ⚠ [--allow-service-removal] [--skip-capacity-check]` **W** | refuse removing the group that holds `data` (exit 4); removing a group that is the only host of a service requires `--allow-service-removal` (exit 4 otherwise) |
| `cluster schedule show\|set\|pause\|resume\|delete CLUSTER` | on/off (hibernation) schedule: `GetSchedule` (404 → `no schedule` exit 0 text / `null` JSON for show), `set` flags `--timezone --weekdays HH:MM-HH:MM --weekend HH:MM-HH:MM` + per-day `--monday … --sunday` (window or `off`), create-vs-update decided by GET; `pause` = `PauseSchedule` (`--power-on` also turns the cluster on); `resume`; `delete ⚠`. Body keys exactly as capellactl `schedule_cmds.py` |
| `cluster backup schedule show\|options\|set\|delete CLUSTER` | cloud-snapshot schedule: `set` flags `--every DUR --retain DUR --start HH:MM --copy-to REGION… --no-copies`; durations like `24h`, `7d`, converted to hours; validated against `GetSnapshotScheduleOptions` (intervals, min/max retention) → exit 3 with the allowed values |
| `cluster backup list [CLUSTER]` / `create CLUSTER [--retain DUR] [--copy-to R…]` **W** / `retain CLUSTER BACKUP_ID --retain DUR` / `delete CLUSTER BACKUP_ID ⚠` / `restore CLUSTER BACKUP_ID [--from-region R] ⚠⚠` **W** / `restores CLUSTER` / `regions CLUSTER` | `restore` replaces ALL data → requires typing the cluster name to confirm unless `--yes`. `create` waits via `WaitForStatus` on `ListSnapshots` for the returned `backupId` (done: `complete`/`ready`; failed: `failed`; per capellactl `cluster_backup.py`) |

**cidr**
`cidr list CLUSTER` (cols `CIDR TYPE STATUS EXPIRES COMMENT ID`); `cidr create CLUSTER CIDR [--comment S] [--expires-at RFC3339] [--allow-any-ip]`; `cidr delete CLUSTER CIDR ⚠`. Validation (all exit 3): CIDR parses (`net/netip.ParsePrefix` or a bare IP → `/32`); `0.0.0.0/0` rejected unless `--allow-any-ip`; `--expires-at` parses and is in the future; max 75 entries per cluster (check `len(list)`, exit 4 when full); duplicate CIDR already present → success no-op `already allowed`. `delete` matches by CIDR string (normalized), not found → exit 0 with warning `not found`. No update endpoint (document: delete + create).

**bucket** (existing `bucket create|exists` untouched)
`bucket list CLUSTER` (cols `NAME TYPE STORAGE MEMORY_MB REPLICAS DURABILITY TTL FLUSH`); `bucket get CLUSTER NAME`; `bucket update CLUSTER NAME [--memory MB] [--replicas 0-3] [--durability L] [--ttl SECONDS] [--flush/--no-flush] [--priority N] [--cross-cluster-versioning/--no-cross-cluster-versioning]` (read-modify-write, `ifMatch`, 412 retry once; immutable fields — type, storage backend, vbuckets, conflict resolution, eviction — are **not** flags); `bucket delete CLUSTER NAME ⚠`. Creating buckets with the full flag set (`--type --storage-backend --vbuckets 128|1024 --replicas --durability --ttl --flush --conflict-resolution --eviction-policy --priority`, default `--memory 100`, `couchbase`/`magma`-valid combinations per capellactl `bucket_cmds.py`) is exposed as **`bucket add CLUSTER NAME`** to avoid changing the legacy `bucket create`. Durability values: `none|majority|majorityAndPersistActive|persistToMajority`. Free-tier clusters use the freeTier bucket endpoints (only `--memory`, `--cross-cluster-versioning` honoured; other flags → exit 3 `not supported on free-tier clusters`).
`bucket backup schedule show|set|delete CLUSTER BUCKET` (`set`: `--day`, `--start-at HH:MM`, `--incremental-every DUR`, `--retention DUR`, `--cost-optimized/--no-cost-optimized`), `bucket backup cycle list|get`, `bucket backup list|get|create|delete`, `bucket backup restore CLUSTER BUCKET BACKUP_ID [--to-cluster C --services S --include-data … --exclude-data … --map-data … --filter-keys … --filter-values … --replace-ttl … --replace-ttl-with … --force-updates --auto-remove-collections]` **W** ⚠, `bucket backup export … [--export-id ID]` **W** (prints the 1-hour `backupDownloadURL` — treat as secret: not redacted but warn it expires in 1 h). Bodies per `bucket_backup.py`.

**scope** (existing `scope create|exists` untouched)
`scope list CLUSTER BUCKET` (cols `NAME COLLECTIONS`); `scope add CLUSTER BUCKET NAME` (idempotent: 409/exists → no-op message `already exists`, exit 0); `scope delete CLUSTER BUCKET NAME ⚠` (refuse `_default` and `_system`: exit 3).

**collection** (existing `collection create|exists` untouched)
`collection list CLUSTER BUCKET SCOPE` (cols `NAME MAX_TTL`); `collection add CLUSTER BUCKET SCOPE NAME [--max-ttl SECONDS]` (idempotent); `collection update … NAME --max-ttl N` (server ≥ 7.6.0: read `couchbaseServer.version` from the cluster; lower → exit 4 `requires Couchbase Server 7.6.0 or later`); `collection delete CLUSTER BUCKET SCOPE NAME ⚠` (refuse `_default`).

**credential**
`credential list CLUSTER` (cols `NAME ID ACCESS`; ACCESS summarized `read:travel-sample.*` etc.); `credential get CLUSTER NAME`; `credential create CLUSTER NAME --privileges read|write|read+write [--bucket B] [--scope S] [--collection C]… [--all-buckets] [--password-env VAR | --password-file PATH]`; `credential update CLUSTER NAME [same access flags] [--rotate-password] [--password-env|--password-file]`; `credential delete CLUSTER NAME ⚠`.
Access model → `[]AccessEntry`: `--all-buckets` ⇒ resources `{}`-style all-buckets form exactly as capellactl; `--bucket` required unless `--all-buckets` (exit 3); `--scope` requires `--bucket`; `--collection` requires `--scope` (repeatable; produces collection list). Privilege mapping: `read`→`["data_reader"]`, `write`→`["data_writer"]`, `read+write`→both.

### 3.7 Passwords (credential create/update, clone `--with-credentials`)

- `--password-env VAR`: read `os.Getenv(VAR)`; empty/unset → exit 3 `environment variable VAR is empty`. `--password-file PATH`: read file, trim one trailing newline; file mode checked: warn if group/world-readable. Both given → exit 3.
- Neither given: generate with `crypto/rand`, length 32, using capellactl's `output/password_gen.py` rules **exactly**: at least 8 chars with an upper-case letter, a lower-case letter, a digit and a special character, and none of `< > ; . * & | £`; special alphabet `!@#$%^()-_=+[]{}:,?`. (An earlier draft of this spec listed `&` and `*` as specials; Capella rejects both.) Implement it once in `internal/secrets/password.go` (`GeneratePassword(length int) string`, `MeetsRules(string) bool`, see the Phase 3 spec §6.2) — Phase 2 may create that file first; Phase 3 reuses it. `--password-env/--password-file` values are also checked with `MeetsRules` and rejected (exit 3) with the rules text when they fail.
- Printed once to stdout via `printSecretOnce` as `password: <value>` (text) or the `password` JSON field, after a stderr line `[warning] - this password is shown only once`. With `--password-env/--password-file` the password is **not** printed.
- Phase 2 does not write passwords to disk (capellactl's `.capellactl/credentials/` is a Phase 3 concern).
- `--rotate-password` generates a new one as above and updates.

### 3.8 Async behaviour (all **W** commands)

- Issue the call; on API acceptance log `[info] - <op> accepted for cluster NAME`.
- `--no-wait` → return success immediately (JSON includes `"status":"accepted"` plus IDs).
- Otherwise `engine.WaitForCluster`/`WaitForStatus` with `rt.Timeout`; progress: state changes as `[info] - cluster NAME: deploying (1m10s)`, dots on stderr per `OnTick`. Ctrl-C → context cancelled → exit 130, and print `the operation continues in Capella` to stderr.
- `*WaitTimeout` → exit 5, action `run the command again to keep waiting, or pass a longer --timeout`. `*WaitFailed` → exit 1, action `open the cluster's activity log in the Capella UI`.
- Idempotence: every create-type command first looks the resource up by natural key; if it exists in an equivalent state, report `already exists` and exit 0 (this also resolves `AmbiguousWriteError`: on that error, re-observe by natural key once; if the resource exists treat as success, else exit 1 with the ambiguity message).

### 3.9 Test requirements

1. **`internal/capellafake`**: `httptest.Server` implementing, statefully, every endpoint used by §3.6 (organizations, projects, clusters incl. state progression `deploying→healthy` after N polls via an injectable hook, free tier, allowed CIDRs, buckets with base64url IDs, scopes, collections, users with one-time password, on/off schedule, snapshot schedule/backups/restores, bucket backups). Behaviours: 202/204/201 status codes as documented, ETag `Version: N` + 412 on stale `If-Match`, 409 conflicts (duplicate names, delete non-empty project), 404 on unknown, pagination (`perPage` respected, `cursor.pages.next`), optional fault injection (`FailNext(method, pathRegex, status, times)`), request log for assertions. `capellafake.New(t) *Fake` with `.URL`, `.OrgID`, `.APIKey`.
2. CLI tests (`internal/cli/*_test.go`): run the cobra tree in-process via `rootCmd.SetArgs`, `SetOut/SetErr`, `logging.SetOutputs`, env `CAPELLA_API_BASE_URL=<fake.URL>`, `CAPELLA_API_KEY`, `CAPELLA_ORG_ID`, `CBCTL_HOME=t.TempDir()`, and `FakeClock` injected through a package-level test hook (`var clockFactory = func() engine.Clock { return engine.RealClock{} }`). Each command in §3.6 has ≥1 happy-path test and ≥1 failure-path test; text **and** JSON output asserted (JSON via `json.Unmarshal`, text via substring).
3. Unit tests for: name resolution (ambiguous/not-found/ID match), `suggest_cidr`/overlap, password generator (policy, uniqueness, length), scale limit math (table: single node, data group 3..27, other groups 2..24, step at limit), duration parsing (`24h`, `7d`, `90m`, bad input), base64url bucket IDs (`BucketID("travel-sample")` known vector, name with `/`, `+`, unicode), confirm() with/without TTY.
4. Live tests `internal/integration/capella_live_test.go` (`//go:build integration`): load `.env` via `settings.LoadDotEnv`; `t.Skip` unless `CAPELLA_API_KEY` **and** `CAPELLA_ORG_ID` are set. Prefix all created names with `CBCTL_TEST_PREFIX` (default `cbctl-ci`) + `-` + 8 random hex chars. `t.Cleanup` deletes in reverse dependency order and **fails loudly** if cleanup fails (print the leftover names). Flow (single test, sequential subtests, guarded by `CBCTL_LIVE_CLUSTER=1` for the cluster-cost parts): project create/list/get/update → (if `CBCTL_LIVE_CLUSTER=1`) cluster create via the free tier when available else smallest provisioned → bucket add → scope add → collection add → credential create → cidr create/list/delete → cluster protect on/off → cluster scale (provisioned only) → cluster power off/on (provisioned only) → cluster backup schedule set/show/delete → deletes → project delete. Without `CBCTL_LIVE_CLUSTER`, only project-level operations and read-only calls (`organization`, `cluster options`, `cluster list`) run. Add hidden `cbctl capella janitor --prefix P [--yes]` deleting projects (cascading) whose name starts with the prefix, and `make test-capella-live` (`go test -tags=integration -timeout 90m -run Capella ./internal/integration -v`) and `make janitor`.
5. Makefile: add `-race` to `test-unit`; CI `go test -race ./internal/...`. Live tests are **not** run in CI.

### 3.10 Documentation deliverables (same PR as the code they describe)

- `README.md` Capella section: auth precedence, `.env`, `cbctl configure`, command tables (§3.6), JSON output, exit codes, async flags.
- `.cursor/rules/cbctl.mdc`: replace "Server management only (not Capella)" with the new command map.
- `.env.example` (§1.5). `docs/specs/` keeps this file.
- Attribution: add `NOTICE` stating that Capella command semantics, presets, size tables and endpoint inventory are derived from capellactl (Apache-2.0, Couchbase, Inc.).

### 3.11 Phase 2 acceptance criteria

1. `make vet && make test-unit` green with `-race`; no test touches the network or keychain.
2. `cbctl capella --help` lists `project cluster bucket scope collection cidr credential configure import`; every command in §3.6 has `--help` with a one-line `Short`, and examples in `Long` for `cluster scale`, `cluster clone`, `credential create`, `cidr create`.
3. For each command: text mode output matches the columns in §3.6; `-o json` prints a single valid JSON document on stdout and nothing else (test: `json.Unmarshal` of the full stdout succeeds even when warnings are emitted).
4. Exit codes: 0 success/no-op, 1 API/transport failure, 3 invalid usage/config (missing key, bad flag combination, unknown/ambiguous name), 4 blocked (deletion protection, not-clonable state, free-tier restriction, no TTY for confirmation), 5 wait timeout, 130 interrupt. Each has at least one test.
5. Idempotence tests: `project create` twice (second → conflict message, exit 1), `scope add`/`collection add`/`cidr create` twice (second → `already exists`/`already allowed`, exit 0), `cluster power --off` on an off cluster (no-op, exit 0), `cluster scale --step +1` at max (exit 0, no PUT recorded by the fake).
6. Concurrency/etag test: fake returns 412 once on `cluster scale`; command re-reads and succeeds; the request log shows GET→PUT(412)→GET→PUT(204).
7. Async tests (FakeClock): `cluster delete` waits through `destroying` to 404; `--no-wait` returns after the first 202 with zero polls; `--timeout 30s` with a cluster stuck in `deploying` → exit 5 and message containing "continues in Capella".
8. Secrets: a test sets `CAPELLA_API_KEY=sk-test-SECRET-1234`, runs every command against the fake with `--log-level debug`, and asserts the string never appears in captured stdout/stderr; the credential password appears exactly once on stdout and zero times on stderr.
9. Regression: `go test ./internal/capella/... ./internal/rest/... ./internal/server/...` unchanged; legacy `cbctl capella cluster create/exists/destroy` flag sets are byte-identical in `--help` output (snapshot test of `cmd.Flags()` names for the legacy leaves).
10. Live (manual, with `.env`): `make test-capella-live` passes project-level subtests; with `CBCTL_LIVE_CLUSTER=1` the full flow passes and `make janitor` afterwards reports 0 leftovers.

---------------------------------------------------------------------------------------------------

## 4. Edge cases checklist (each must have a test or an explicit code path)

- API key with whitespace/quotes/`Bearer ` prefix/BOM in env, `.env`, or flag.
- `.env` exists but empty; `.env` in a parent directory; `.env` var already exported in the shell (shell wins).
- `--api-host` given as bare host, `host:443`, `https://host/`, `http://host` (rejected unless localhost).
- Profile exists in `profiles.yaml` but keychain entry deleted → "API key not set" error mentioning the profile.
- Org with zero / one / many orgs visible; key lacks org access (403 on org GET).
- Cluster name duplicated across projects; cluster referenced by UUID; project with spaces/unicode in the name (URL-escaped); bucket names with `.`, `%`, `-`, `_` (base64url of the exact UTF-8 bytes).
- List endpoints returning 404 for a deleted parent (surface the error; never return empty silently in the new client).
- 429 with and without `Retry-After`; 502 HTML bodies from a proxy; connection reset mid-response on GET; TLS handshake timeout.
- POST whose response was lost (ambiguous) for: project create, cluster create, bucket add, credential create (re-observe by name; for credentials the password is lost → report that the credential exists and must be updated with `--rotate-password`).
- Cluster state `turningOff`/`scaling` when issuing a conflicting command (Capella 409) → exit 4 with the state in the message.
- Deletion-protected cluster in `project delete --cascade` (abort before any deletion).
- Free-tier cluster in each command (supported / blocked per §3.6).
- `--timeout 0` (means wait forever? **No**: reject, exit 3 `--timeout must be positive`); `--timeout` given with `--no-wait` (ignored, debug log).
- Terminal vs pipe: no TTY ⇒ no dots, no prompts (prompt-requiring command without `--yes` ⇒ exit 4).
- Windows: `HomeDir` uses `%APPDATA%`; skip the 0600 mode assertion; keychain via Credential Manager.
- SIGINT during a wait: exit 130, no partial JSON on stdout.

## 5. Explicit non-goals / deferred

`capella.yaml` and everything that reads or writes it (`--no-apply`, `init`, `sync`, `plan`, `apply`, `destroy`, `drift`, `validate`, `help <topic>`), credential files under `.capellactl/`, unification of legacy `capella.Client` types with `capellaapi` models, the unified `bucket/scope/collection create` semantics, App Services, network peering, private endpoints, audit logs, org users, API-key management, billing, XDCR, Data API, eventing, query indexes (capellactl 1.5.0 backlog).
