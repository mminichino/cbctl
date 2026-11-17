# capellactl → cbctl parity audit: CLI SURFACE

- capellactl: `/Users/michael/IdeaProjects/capellactl` @ 6389d5a (Python/click). Click tree dumped by introspection (`/tmp/dumpcap.py`, read-only; 504 lines).
- cbctl: `/Users/michael/IdeaProjects/cbctl` branch `mm--capellactl-port` @ b05642b (Go/cobra). Tree dumped by running `--help` recursively on a binary built into `/tmp` (107 commands).
- Neither repo was modified. No live Capella calls were made. Behaviour was checked offline with `--token x --api-host http://127.0.0.1:1` where needed.

Status legend: MATCH · RENAMED (doc? = whether a user-facing doc gives the mapping) · PARTIAL · DIFFERENT · MISSING · INTENTIONALLY-DROPPED (just? = whether the justification still holds).

## 0. Re-verification before the merge to main (2026-11-17, release 0.6.0)

The tables below are the audit snapshot at b05642b and are kept unchanged as the record. Every
row that was not MATCH was checked again against the branch tip. **No MISSING row remains.**
Command paths and long options are now checked mechanically: `TestCapellactlCompat`
(`internal/cli/capellactl_tree_test.go`) loads capellactl's whole click tree
(`internal/cli/testdata/capellactl-tree.tsv`, 98 commands) and fails if a path or option does not
resolve under `cbctl capella`, except for documented exceptions (`completion`, `import`'s sync flags).

| Row (snapshot status) | Now | Evidence |
|---|---|---|
| `--version` (PARTIAL: no API version; `cbctl capella --version` unknown flag) | MATCH: API version and licence lines; `cbctl capella --version` prints the same | `TestCLI_CAP001_AC1_VersionOutput`, `TestCLI_CAP001_AC1_CapellaVersionMatchesRoot` |
| `CAPELLACTL_*` env vars (RENAMED, no mapping, ignored silently) | RENAMED, documented; old names honoured with one warning | guide "Environment variables"; `TestCapellactlConfigEnvVar`, `TestLegacyEnvAuth` |
| `CBCTL_QUIET` accepts only `1` (PARTIAL) | MATCH: 1/true/yes/on, and a warning for anything else | `TestEnvQuiet` |
| `--timeout 90` (PARTIAL) | MATCH: plain seconds, `90S`, `5m`, `1h30m` | `TestCapellaTimeoutParse` |
| Unknown sub-command exits 0 (DIFFERENT) | exit 3 with a hint (capellactl: 2; cbctl uses 3 for every usage error) | `TestUnknownSubcommand`, `TestUnknownSubcommandHints` |
| Usage errors exit 1 (DIFFERENT) | exit 3, documented | guide "Exit codes and messages" |
| No terminal, no `-y` (DIFFERENT: 4 vs 1) | kept at 4 ("blocked"), documented | guide "Exit codes and messages" |
| Missing answer without a terminal; prompts for project create/update, cluster clone, group add, bucket add (PARTIAL) | MATCH | `TestPrompts`, `TestPrompts_CAP042_AC2_AskUsesDefaultOrNamesFlag`, `TestClusterWizard` |
| `help …` (RENAMED, no doc) | `guide …`, hidden `help` alias, documented | `TestCapellaHelpAlias` |
| completion of CLUSTER names (PARTIAL) | MATCH | `TestCompletion_CAP034_AC6_ClusterNamesFromConfig` |
| `cluster schedule apply NAME` (MISSING) | MATCH (visible) | `TestScheduleApplyIsTargetedApply` |
| `cluster apply` / `project apply` (dropped) | hidden, deprecated aliases; `cluster apply --project P` selects `cluster/P/NAME` | `TestScheduleApplyIsTargetedApply` |
| `configure --import/--no-import` (MISSING) | MATCH | `TestConfigure_OffersImport` |
| `configure --api-base-url` (RENAMED, no doc) | accepted as `--api-host`, documented | guide "Commands" table |
| profile default from `CBCTL_PROFILE` (PARTIAL) | MATCH | `activeProfile` in `configure.go`; `TestConfigureMigrateCommand` |
| `organizationName` profile key (PARTIAL) | MATCH: stored by `configure`, carried over by `migrate`, shown by `configure list` as `NAME (ID)` | `TestMigrateKeepsOrganizationName` |
| 401 help (PARTIAL) | MATCH: checklist with key length and tail, allow-list, token vs ID | `TestConfigure_CAP037_AC7_RejectionShowsHintAndChecklist` |
| `configure list|show` JSON (DIFFERENT) | `--output-format legacy` gives capellactl's shape; rows are sorted | `TestConfigureLegacyOutputFormat` |
| profile store / keychain service (DIFFERENT, no import) | read as a fallback with a warning; `configure migrate` copies them | `TestLegacyProfileAndKeychainFallback`, `TestConfigureMigrateCommand` |
| no keychain → exit 3 (DIFFERENT) | MATCH: profile saved, key not, warning | `configure.go` (`no OS keychain available`) |
| `credentialsOutput` default (DIFFERENT) | `./.capellactl/credentials` is used while `./.cbctl/credentials` does not exist; `doctor` reports it | `TestResolveCredentialsDir`, `TestMigrationEndToEndDoctorClean` |
| `bucket|scope|collection create` with capellactl's arguments (RENAMED + collision) | forwards to `add` with a note | `TestCapellactlCreateForwardsToAdd` |
| `cluster create NAME` positional, wizard, direct mode (PARTIAL) | MATCH | `TestDirectClusterCreate`, `TestClusterWizard` |
| `--no-disk-auto-expansion`, `--no-cost-optimized`, `--no-flush` … (PARTIAL) | MATCH | `TestNegationFlags` |
| hidden `import` = sync (DIFFERENT) | `capella import` with sync flags exits 3 and names `sync` | `TestImportSyncHint` |
| `init --password-env` default name (DIFFERENT) | kept (`CBCTL_PWD_<CLUSTER>_<NAME>`), documented | guide "capella.yaml and apiVersion" |
| Message prefixes (DIFFERENT) | kept (project logging rule), documented | guide "Exit codes and messages" |

## 1. Global / root surface

| capellactl | cbctl | Status | Notes |
|---|---|---|---|
| `capellactl <cmd>` (entry point `capellactl.cli.main:main`, `python -m capellactl`) | `cbctl capella <cmd>` (and `cbctl configure`) | RENAMED (doc? partial: README lists `cbctl capella …`, but no capellactl→cbctl table) | Prefix change; no shim binary/alias |
| `--version` → `capellactl X (Management API v4.0)` + licence | `cbctl --version` / `-v` (root only); `cbctl capella --version` → `unknown flag` exit 1 | PARTIAL | No API version in output |
| `-h/--help` | `-h/--help` | MATCH | |
| `-c/--config PATH` | `-c/--config` (persistent on `capella`) | MATCH | |
| env `CAPELLACTL_CONFIG` | env `CBCTL_CONFIG` | RENAMED (doc? only in guide `environment`; no mapping) | old name ignored silently |
| `--profile NAME` | `--profile` | MATCH | |
| env `CAPELLACTL_PROFILE` | env `CBCTL_PROFILE` | RENAMED (doc? no mapping) | old ignored silently |
| env `CAPELLACTL_HOME` | env `CBCTL_HOME` | RENAMED (doc? no) | |
| `-o/--output text\|json` (default text) | `-o/--output text\|json` | MATCH | bad value: capellactl exit 2, cbctl exit 3 |
| `--log-level error\|warn\|info\|debug` | same | MATCH | |
| `--auto-approve/--yes/-y` | `-y/--yes` + `--auto-approve` | MATCH | |
| `-q/--quiet`, env `CAPELLACTL_QUIET` (click bool: 1/true/yes/on) | `-q/--quiet`, env `CBCTL_QUIET` (only the value `"1"`) | RENAMED + PARTIAL | `CBCTL_QUIET=true` is ignored |
| `--no-wait` | `--no-wait` | MATCH | |
| `--timeout DURATION` default `60m`; accepts `90s`, `5m`, `1h30m`, **plain seconds `90`** (case-insensitive) | `--timeout duration` (Go) default 1h; `90` → `missing unit` exit 1 | PARTIAL | |
| (none) | per-leaf `--token`, `--org-id`, `--api-host` | cbctl extra | |
| (none) | `.env` auto-load (cwd → project root) | cbctl extra | |
| env `CAPELLA_API_KEY`, `CAPELLA_ORG_ID`, `CAPELLA_API_BASE_URL` | same, plus `CAPELLA_TOKEN` alias | MATCH | |
| env `BEARER_TOKEN`, `ORG_ID`, `API_BASE_URL` (deprecated, warn) | not read | INTENTIONALLY-DROPPED (just? spec phases-0-2:99; not told to users) | |
| env `CI` → spinner off, refusal hint | `CI` → progress off | MATCH | `TERM=dumb` not honoured (low) |
| Unknown command → `unknown command 'x'. Did you mean 'y'?`, exit 2 (groups.py:12-17) | Under `capella`, any unknown sub-command prints the parent's help and **exits 0** | DIFFERENT | see P-CLI-01 |
| Usage errors (bad flag, bad choice, arg count) → exit 2 | cobra errors → exit 1; cbctl's own usage checks → exit 3 | DIFFERENT | cbctl `guide exit-codes` still says 2 |
| Messages `[ERROR]: …`, `[WARNING]: …`, `[ACTION]: …`, `See 'capellactl help TOPIC'` | `[error] - …`, `[warning] - …`, `  → action` | DIFFERENT (project logging rule) | low |
| No terminal and no `-y` on a confirmation → exit **1** (`refusing to … without approval`) | exit **4** (`confirmation required but no terminal is attached`) | DIFFERENT (doc? the cbctl `ci` guide says 4; no migration note) | |
| Missing answer without a terminal → exit 3 `missing --FLAG` | no prompts; required flags → exit 3 | PARTIAL | |
| Ctrl-C 130, timeout 5, blocked 4, config 3, changes 2 | same | MATCH | |
| `help` (no args: root help, getting started, topics) | `cbctl capella guide` | RENAMED (doc? spec R3 only) | |
| `help topics` | `capella guide topics` | RENAMED | |
| `help TOPIC` (11 topics) | `capella guide TOPIC` (same 11) | RENAMED | |
| `help config` / `help config FIELD` (single-project paths normalised) | `capella guide config [FIELD]` | RENAMED/MATCH | unknown field: exit 2 → exit 3 |
| `help COMMAND WORDS…` (e.g. `help cluster scale`) | `cbctl help capella cluster scale` or `--help` | RENAMED (doc? no) | `cbctl capella help …` prints capella help, exit 0 |
| `completion bash\|zsh\|fish` | `cbctl completion bash\|zsh\|fish\|powershell` (cobra, root) | RENAMED | capellactl completes CLUSTER names (`shell_complete=complete_cluster_names`); cbctl has no `ValidArgsFunction` → PARTIAL |

## 2. Declarative workflow

| capellactl | cbctl | Status | Notes |
|---|---|---|---|
| `validate` | `capella validate` | MATCH | |
| `plan [--target]… [--detailed-exitcode]` | same | MATCH | |
| `apply --target --prune --rotate-passwords --power-on --allow-service-removal --allow-service-group-removal --skip-capacity-check` | same + `--force-overwrite-secret` | MATCH (+extra) | capellactl refuses an existing secret file with a message naming `--force-overwrite-secret` too |
| `destroy [--discard-snapshots]` | same | MATCH | |
| `drift [--detailed-exitcode]` | same | MATCH | |
| `sync --project… --project-id… --cluster… --cluster-id… --skip-project… --dry-run --stdout --full --force` | same | MATCH | `--force` also overrides the "other organization" refusal |
| `import` (HIDDEN alias of sync, "kept for one release"; `--project… --project-id --cluster… --cluster-id… --file --full --stdout --force`) | `capella import FILE KEYSPACE` = JSONL data importer | DIFFERENT (name collision; spec phase-4 R4 notes it for docs only) | |
| `init` (all 29 flags) | `capella init` with the same names | MATCH | `--access` adds `write`; `--password-env` default `<CRED>_PASSWORD` → `CBCTL_PWD_<CLUSTER>_<NAME>` (DIFFERENT default); `--protect/--no-protect` present |
| `project apply [NAME]` (HIDDEN, deprecated) | – | INTENTIONALLY-DROPPED (just? phase-3:646 says "no prior cbctl users", which is no longer true) | use `apply --target project/NAME` or `project update` |
| `cluster apply NAME` (HIDDEN, deprecated) | – | INTENTIONALLY-DROPPED (same weak justification) | use `apply --target cluster/NAME` |
| `cluster schedule apply NAME` (**visible**) | – | MISSING | use `apply --target schedule/NAME`; unknown sub-command exits 0 |

## 3. configure / profiles / credentials storage

| capellactl | cbctl | Status | Notes |
|---|---|---|---|
| `configure [--profile] [--org-id] [--api-key-stdin] [--config-file]` | `configure` / `capella configure` with the same flags | MATCH | |
| `configure --api-base-url URL` | `--api-host HOST\|URL` | RENAMED (doc? no) | |
| `configure --import/--no-import` (after saving, offer `sync`; check an existing capella.yaml against the key's organization, CAP-036/CAP-047) | – | MISSING | run `capella sync` by hand |
| profile name default: `--profile` → `CAPELLACTL_PROFILE` → `default` (configure.py:80-81) | save/remove: `--profile` → `default` (configure.go:86-89, 324-327); `CBCTL_PROFILE` ignored | PARTIAL | |
| profile store `~/.config/capellactl/profiles.yaml` (`$CAPELLACTL_HOME`, `%APPDATA%\capellactl`, `$XDG_CONFIG_HOME/capellactl`) | `~/.config/cbctl/profiles.yaml` (`$CBCTL_HOME` …) | DIFFERENT; no import | see P-CLI-02 |
| profile keys `organizationId`, `organizationName`, `apiBaseUrl`, `configFile` | `organizationId`, `apiBaseUrl`, `configFile` (no `organizationName`) | PARTIAL | |
| keychain service `capellactl`, account = profile | service `cbctl` | DIFFERENT; no import | |
| no keychain → profile saved, key not saved, warning | no keychain → exit 3, nothing saved | DIFFERENT | |
| 401 help: key length, `…tail`, IP allow-list, Token vs ID, curl test (configure.py:56-77) | one-line hint | PARTIAL | low |
| `configure list` (cols PROFILE ORGANIZATION(name+id) API BASE URL CONFIG FILE KEY, sorted; JSON = profiles map) | cols PROFILE ORGANIZATION(id) API HOST KEY, unsorted map order; JSON = array of rows | PARTIAL / DIFFERENT JSON | |
| `configure show [--profile]` text `key  value  (source)`; JSON `{field:{value,source}}` | text `key \| value \| source`; JSON `[{field,value,source}]` | DIFFERENT JSON | |
| `configure remove [--profile]` (default: the active profile; no prompt; also deletes a keychain entry that has no profile) | asks `[y/N]`; without a terminal and `-y` → **exit 4**; default `default`; profile missing → warning, keychain left | DIFFERENT | |
| `credentialsOutput` default `./.capellactl/credentials` | `./.cbctl/credentials` (an explicit `.capellactl` path is honoured) | DIFFERENT default | files saved earlier are not found where cbctl writes rotations/`.bak` |
| `apiVersion: capellactl/v1` | accepted | MATCH (documented) | |

## 4. Resource commands

Every cbctl resource command has `--token/--org-id/--api-host`. Mutating ones also have `--direct` (Capella only) and `--no-apply`. capellactl's mutating commands **always** need capella.yaml (`open_editor`, cluster_ops.py:169-175 → exit 3); cbctl falls back to direct mode when there is no file.

| capellactl | cbctl | Status | Notes |
|---|---|---|---|
| `project list` / `get NAME` | same | MATCH | |
| `project create NAME [--description]` (description asked when omitted) | same, no prompt | PARTIAL (prompt) | |
| `project update NAME [--description] [--no-apply]` (asked) | same, no prompt | PARTIAL (prompt) | |
| `project delete NAME [--cascade] [--discard-snapshots]` | same | MATCH | |
| `cluster list [--project]`, `get NAME [--project]`, `options`, `disk-types [--provider]` | same | MATCH | |
| `cluster create NAME` (positional) + `--provider --region --cidr --size --nodes --cpu --ram --group… --disk-type --disk-size --disk-iops --services --availability --support-plan --support-timezone --description --project --free-tier --no-apply --single-node --server-version --zone --cmek-id --private-dns --disk-auto-expansion/--no-disk-auto-expansion`; interactive wizard (31 prompts) | `capella cluster create --name NAME` (flag) + the same flags; positional NAME **silently ignored**; no wizard; `--no-disk-auto-expansion` missing (use `=false`); without capella.yaml (or with `--direct`) runs the **legacy** create (`--database`, `--user-email`, `-u/-p`, `--allow-cidr 0.0.0.0/0`) and ignores every sizing flag | DIFFERENT | see P-CLI-03 |
| `cluster clone [SOURCE] [NEW_NAME]` (asked) + `--region --project --to-project --cidr --support-plan --server-version --description --with-cidrs --with-credentials --no-data-model --no-apply` | same flags; args required; no prompts | PARTIAL (prompt) | |
| `cluster update NAME --description --support-plan --support-timezone --cpu --ram --disk-type --disk-size --disk-iops --disk-auto-expansion/--no-… --group --project --no-apply` | same; `--no-disk-auto-expansion` → `--disk-auto-expansion=false` | RENAMED (doc? in --help) | |
| `cluster scale NAME --nodes --group --step --min --max --all-groups --skip-capacity-check --project --no-apply` | same (`--step` int accepts `+1`) | MATCH | |
| `cluster delete NAME [--discard-snapshots] [--cascade] [--project]` | same | MATCH | |
| `cluster protect NAME --on/--off` | same | MATCH | |
| `cluster power NAME --on/--off [--with-app-service]` | same | MATCH | |
| `cluster group list NAME` | same | MATCH | |
| `cluster group add NAME --services (asked) --nodes --cpu --ram --disk-* --disk-auto-expansion/--no-… --no-apply` | `--services` required; no `--no-disk-auto-expansion` | PARTIAL | |
| `cluster group remove NAME SERVICES --allow-service-removal --skip-capacity-check --no-apply` | same | MATCH | |
| `cluster schedule show\|set\|pause [--power-on]\|resume\|delete` (+ all day flags) | same | MATCH | `apply` MISSING (sec. 2) |
| `cluster backup schedule show\|options\|set (--every --retain --start --copy-to… --no-copies)\|delete` | same | MATCH | |
| `cluster backup list [NAME]\|create (--retain --copy-to…)\|retain (--retain req.)\|delete\|restore (--from-region…)\|restores\|regions` | same | MATCH | |
| `bucket list CLUSTER`, `get CLUSTER NAME`, `delete CLUSTER NAME` | same | MATCH | |
| `bucket create CLUSTER NAME --memory (asked, default 1024) --type --storage-backend --vbuckets --replicas --durability --ttl --flush/--no-flush --conflict-resolution --eviction-policy --priority --project --no-apply` | `capella bucket add CLUSTER NAME …`; `capella bucket create` = **legacy** `[name] --quota --replicas -u -p --database …` | RENAMED (doc? README says "bucket create\|exists remain for the legacy flow", no mapping) + name collision | defaults differ: `--memory 100` always sent/recorded; direct mode sends `storageBackend: couchstore` when not given (capellactl left both to Capella); `--no-flush` missing on add; `--eviction-policy` also allows `valueOnly` |
| `bucket update CLUSTER NAME …` | same | MATCH | |
| `bucket backup schedule show\|set (--day --start-at --incremental-every --retention --cost-optimized/--no-cost-optimized --no-apply)\|delete` | same; `--no-cost-optimized` → `--cost-optimized=false` | RENAMED (doc? in --help) | |
| `bucket backup list\|get\|create\|delete\|restore (all 11 flags)\|export [--export-id]`, `cycle list\|get` | same | MATCH | |
| `scope list CLUSTER BUCKET`, `delete CLUSTER BUCKET NAME` | same | MATCH | |
| `scope create CLUSTER BUCKET NAME [--no-apply]` | `capella scope add …`; `scope create` = legacy `[name] -b BUCKET` (SDK) | RENAMED + collision | 3 args → `accepts 1 arg(s), received 3` exit 1 |
| `collection list`, `update --max-ttl`, `delete` | same | MATCH | |
| `collection create CLUSTER BUCKET SCOPE NAME [--max-ttl] [--no-apply]` | `capella collection add …`; `collection create` = legacy `[name] -b -s` | RENAMED + collision | |
| `cidr list\|create (--comment --expires-at --allow-any-ip --no-apply)\|delete` | same | MATCH | |
| `credential list\|get\|create\|update\|delete` (all flags) | same | MATCH | `get`'s "generated password: saved in FILE" row not checked |

## 5. Legacy cbctl commands (not from capellactl)

| cbctl command | capellactl counterpart | Notes |
|---|---|---|
| `capella cluster create` (legacy flags) | `cluster create NAME` has the same name but a different design (file-managed) | Shares the cobra leaf with the managed create (capella_managed_ops.go:83-84) |
| `capella cluster exists` | none | prints true/false |
| `capella cluster destroy` | none (`cluster delete` is the capellactl verb, and it exists in cbctl) | |
| `capella cluster test [-b]` | none | SDK KV put/get |
| `capella bucket create [name] --quota --replicas` | name only (`bucket create CLUSTER NAME`) | |
| `capella bucket exists` | none | |
| `capella scope create\|exists`, `collection create\|exists` | names only | (not in the brief, same pattern) |
| `capella import FILE KEYSPACE` | name only (hidden `import` = sync) | (not in the brief) |

## 6. Status counts (rows in sections 1–4)

| Status | Count |
|---|---|
| MATCH | 38 |
| RENAMED | 17 (none has a user-facing capellactl→cbctl mapping; some are mentioned in `--help` or specs) |
| PARTIAL | 11 |
| DIFFERENT | 12 |
| MISSING | 2 |
| INTENTIONALLY-DROPPED | 3 (justification is weak for all 3) |
| cbctl extra (no capellactl source) | 2 |

Each row is counted under its first status. Rows are grouped (for example, one row covers all `cluster backup` verbs), so these are row counts, not flag counts. Section 5 lists cbctl-only commands and is not counted.

## 7. Gap findings

### P-CLI-01 — HIGH — Unknown or removed sub-commands exit 0 and print help
- capellactl: groups.py:12-17 (`SuggestingGroup`, `Did you mean`, exit 2).
- cbctl: capella.go:17,48,178 (`Run: func… { _ = cmd.Help() }`). Verified: `cbctl capella bogus` and `cbctl capella cluster schedule apply x` both print help and exit 0.
- Impact: CI scripts using removed verbs (`cluster schedule apply`, `cluster apply`, `project apply`) appear to succeed while doing nothing.
- Fix: in `internal/cli/root.go`, add `func unknownSubcommand(cmd *cobra.Command, args []string) error` and use it as `RunE` (with `Args: cobra.ArbitraryArgs`) on every group. With no args, show help and exit 0. With args, return `&CLIError{Code: ExitUsage(3), Msg: "unknown command \"x\" for \"cbctl capella …\"", Hint: "Did you mean …?"}` using `cmd.SuggestionsFor`. Turn on `SuggestionsMinimumDistance=2`. For the removed verbs, add a `removedAlias` table `map[string]string{"cluster schedule apply": "capella apply --target schedule/NAME", …}` and print that hint.
- Acceptance: `cbctl capella bogus` exits 3 with a suggestion. `cbctl capella cluster schedule apply x` exits 3 and the hint names `apply --target schedule/x`. `cbctl capella` alone still exits 0. Add a table test over every group.

### P-CLI-02 — HIGH — capellactl profiles and keychain keys are not read and there is no import
- capellactl: settings.py:22 (`KEYRING_SERVICE="capellactl"`), :29-34 (home `capellactl`).
- cbctl: profiles.go:30-50 (`cbctl` home), keychain.go:13 (`keyringService="cbctl"`). There is no fallback or import command.
- Impact: each user must re-enter the API key for every profile and re-pick org/base URL/configFile. Scripts that rely on `--profile prod` fail with "no API key".
- Fix: add `cbctl configure import-capellactl [--profile NAME|--all] [--dry-run] [--force]` in `internal/cli/configure.go`. In `internal/settings/legacy.go`, add `func LegacyProfilesDir(env func(string) string) string`, `func LoadLegacyProfiles(dir string) (map[string]Profile, error)` and `func ImportLegacy(ctx context.Context, opts ImportOptions) (ImportReport, error)`, with `type ImportReport struct{ Imported, Skipped []string; KeyMissing []string }`. Read the `capellactl` keychain item for each profile and write it to `cbctl`. Map `organizationName` to nothing. Do not overwrite existing cbctl profiles without `--force` (exit 3 and list the conflicts). A missing legacy dir means "nothing to import", exit 0. A locked keychain imports the profile and lists it in KeyMissing with a warning. Optionally, when a cbctl profile is missing but a capellactl one exists, show a one-time hint: "run 'cbctl configure import-capellactl'".
- Acceptance: with a fake `CAPELLACTL_HOME` and a fake keyring, the import copies 2 profiles and their keys. A second run reports both as skipped. `--force` overwrites. `--dry-run` writes nothing. `capella project list --profile prod` then resolves the key from `cbctl`.

### P-CLI-03 — HIGH — `capella cluster create NAME` loses the positional name, the wizard, and (with no capella.yaml) the whole feature
- capellactl: cluster_ops.py `create` (positional NAME, interactive wizard; capella.yaml is required by `open_editor`, :169-175).
- cbctl: capella.go:58 (legacy create) shares the leaf with the managed create (capella_managed_ops.go:83-84). Verified: with capella.yaml, `cluster create foo --no-apply` → `the cluster name is required … pass --name` exit 3, so the positional is ignored. With no capella.yaml it runs the legacy SDK create (`requires capella.database.name`), exit 1, and `--size/--group/--provider…` are ignored.
- Impact: every capellactl `cluster create NAME …` invocation fails. Interactive users lose the guided prompts.
- Fix: in `newCapellaClusterCreateCmd`, accept `Args: cobra.MaximumNArgs(1)`. Use `args[0]` as `--name`, and exit 3 if both are given and differ. If the managed flags (`--size --group --provider --region --cidr --free-tier …`) are present and there is no capella.yaml, run the direct v4 create (or the file-managed create, matching `--direct`), not the legacy path. Only take the legacy path when `--database` or `-u/-p` is set. Add `--no-disk-auto-expansion` as a hidden alias. Prompting can be omitted if documented.
- Acceptance: `cluster create foo --size small --provider aws --region us-east-1` edits capella.yaml when one is present, or does the direct POST against capellafake when there is none. `cluster create foo --name bar` exits 3. The legacy flags still work.

### P-CLI-04 — HIGH — `bucket/scope/collection create` now means a different command
- capellactl: bucket_cmds.py:158-165 (`bucket create CLUSTER NAME`, memory default 1024); scope/collection `create` with 3/4 positionals.
- cbctl: capella.go:439 (legacy `bucket create [name]`, `ExactArgs`) and scope/collection legacy. Verified: `capella scope create c b s` → `accepts 1 arg(s), received 3` exit 1.
- Impact: every capellactl data-model script breaks. The error does not point to `add`.
- Fix: in the legacy `create` commands, when `len(args)` equals capellactl's arity (2/3/4) and `--database` is not set, forward to the `add` RunE with a deprecation warning (`'bucket create CLUSTER NAME' is 'bucket add' in cbctl`). Otherwise, return exit 3 with that hint. This is the route if the legacy commands are hidden (see §8).
- Acceptance: `capella bucket create c1 orders --memory 1024` behaves like `bucket add` and warns on stderr. `capella bucket create orders --database db` still uses the legacy path.

### P-CLI-05 — HIGH — There is no capellactl→cbctl migration guide
- capellactl: README/docs/reference.md use the `capellactl …` syntax throughout.
- cbctl: README.md, docs/capella/reference.md and internal/guide/topics contain no mapping. `capellactl` appears only for `apiVersion: capellactl/v1` and `skipProjects` (reference.md:22,34) and in the backlog link (README:341).
- Fix: add `docs/capella/migrating-from-capellactl.md` with the §1–4 mapping (prefix, `help`→`guide`, `create`→`add`, `import`→`sync`, removed `apply` aliases, env and paths, exit 1→4 on no terminal, `--timeout` units, defaults). Add the guide topic `cbctl capella guide migrate` (internal/guide/topics/migrate.md) and a link from the README.
- Acceptance: the doc exists. `cbctl capella guide topics` lists `migrate`. A test checks that each removed capellactl verb named in the doc gets a hint from P-CLI-01.

### P-CLI-06 — HIGH — `CAPELLACTL_*` env vars are ignored without any message
- capellactl: settings.py:149-174, main.py:160.
- cbctl: capella_globals.go:82 and settings read only `CBCTL_*`.
- Impact: CI pipelines with `CAPELLACTL_CONFIG=envs/prod.yaml` silently use `./capella.yaml` or the default profile, which can point at the wrong environment.
- Fix: in `internal/settings/settings.go`, add `var legacyEnv = map[string]string{"CBCTL_CONFIG":"CAPELLACTL_CONFIG","CBCTL_PROFILE":"CAPELLACTL_PROFILE","CBCTL_QUIET":"CAPELLACTL_QUIET","CBCTL_HOME":"CAPELLACTL_HOME"}` and `func lookupEnv(env func(string) string, name string) (val, source string, deprecated bool)`. The new name wins. Use the old name with a one-time `[warning]` (`CAPELLACTL_CONFIG is deprecated; use CBCTL_CONFIG`). Report it in `configure show` as the source. Also accept `BEARER_TOKEN/ORG_ID/API_BASE_URL`, with a warning, for one release, or document the removal (P-CLI-05).
- Acceptance: a table test where `CAPELLACTL_CONFIG` alone resolves and warns once, and both set means the new one wins with no warning.

### P-CLI-07 — HIGH — `cbctl capella import` means something different from capellactl's hidden `import`
- capellactl: env.py:644 (hidden `import` = `sync`).
- cbctl: `capella import FILE KEYSPACE` (JSONL loader). Verified: `capella import --project p` → `accepts 2 arg(s)` exit 1.
- Impact: low frequency (the command was hidden and deprecated), but no data is lost; the command just fails.
- Fix: when `import` is called with no positionals and with any of `--project/--project-id/--cluster/--cluster-id/--full/--stdout`, exit 3 with the hint `'capellactl import' is 'cbctl capella sync'`. Document this in the migration guide.
- Acceptance: the call above exits 3 and the hint names `sync`.

### P-CLI-08 — MEDIUM — No terminal and no `-y` exits 4 instead of 1
- capellactl: console.py:207 (`ExitCode.ERROR`). cbctl: capella_env.go:280,293; capella_cluster_util.go:555 (`blockedError`).
- Impact: wrappers that branch on exit 1 change behaviour. cbctl's behaviour is arguably better, since 4 means blocked.
- Fix: keep 4 and document it in the migration guide and in `guide exit-codes`.
- Acceptance: the doc lists the change.

### P-CLI-09 — MEDIUM — `cluster schedule apply NAME` is missing
- capellactl: schedule_cmds.py:152. cbctl: none, and it falls into P-CLI-01's exit 0.
- Fix: add `newCapellaScheduleApplyCmd() *cobra.Command`, `Args: ExactArgs(1)`, which calls the env apply with `targets=[]string{"schedule/"+args[0]}` and passes through `-y/--no-wait`. Alternatively, a hidden alias with a warning.
- Acceptance: against capellafake, it gives the same plan and writes as `apply --target schedule/NAME`.

### P-CLI-10 — MEDIUM — `--timeout` no longer accepts plain seconds
- capellactl: main.py `--timeout` parser (`90` = 90 s; default 60m). cbctl: capella_globals.go (Go `DurationVar`, default 1h). Verified: `--timeout 90` → `missing unit` exit 1.
- Fix: add `type capellaTimeout time.Duration` implementing `pflag.Value` (`Set` accepts a bare integer as seconds, case-insensitive units, and rejects ≤0 with exit 3).
- Acceptance: `90`, `90S`, `5m` and `1h30m` parse. `0` and `-1` exit 3.

### P-CLI-11 — MEDIUM — `bucket add` defaults differ from capellactl's `bucket create`
- capellactl: bucket_cmds.py:165 (`--memory` asked, default 1024; backend left to Capella). cbctl: capella_bucket.go:29 (`defaultBucketMemoryMB = 100`), :417 (direct mode forces `storageBackend: couchstore`).
- Impact: buckets created with no `--memory` are 10× smaller, and direct mode always uses couchstore instead of Capella's magma default for 7.6+/8.x.
- Fix: use 1024 to match, or document 100. Leave `StorageBackend` empty unless set. Add the `--no-flush` hidden flag on `add`.
- Acceptance: a capellafake request body with no `--memory` equals the doc'd default. The backend is absent unless the flag is given.

### P-CLI-12 — MEDIUM — Different default credential-file directory, and direct mode prints the password
- capellactl: `credentialsOutput` default `./.capellactl/credentials`; the password is never printed. cbctl: model.go:29 (`./.cbctl/credentials`); in direct mode (no capella.yaml) the generated password is printed once on stdout (rule doc).
- Impact: existing capellactl secret files are not found where cbctl writes rotations and `.bak`. A password on stdout can leak into CI logs.
- Fix: when `credentialsOutput` is unset and `./.capellactl/credentials` exists while `./.cbctl/credentials` does not, use the legacy dir and warn. Document this. Keep the direct-mode print, but mention it in the migration doc.
- Acceptance: a test with only `.capellactl/credentials` present shows rotation writes there.

### P-CLI-13 — MEDIUM — `configure` differences: no `--import`, `--api-base-url` renamed, keychain failure, JSON shapes, remove prompt
- capellactl: configure.py:80-81 (profile from env), :92 (`--import/--no-import`), list/show JSON = maps; `remove` does not prompt. cbctl: configure.go:86-89, 324-327 (profile default ignores `CBCTL_PROFILE`); `remove` needs `-y` with no terminal (exit 4); list/show JSON = arrays; with no keychain, exits 3 and saves nothing.
- Fix: add a hidden `--api-base-url` alias. Add `--import/--no-import` (run `sync` after saving when there is a terminal; check an existing capella.yaml's org). Default the profile from `CBCTL_PROFILE`. With no keychain, save the profile and warn. Document the JSON shape change, or add `--output-format=legacy`.
- Acceptance: one unit test per flag or behaviour.

### P-CLI-14 — LOW — Hidden `cluster apply` / `project apply` aliases dropped
- capellactl: resource_cmds.py:306,490. cbctl: none, justified only by "no prior cbctl users" (phase-3 spec).
- Fix: covered by P-CLI-01's removed-alias hints.

### P-CLI-15 — LOW — `help` became `guide`, `--version` is root-only, completion has no cluster names, `CBCTL_QUIET` accepts only `1`
- capellactl: help.py, main.py:160 (click bool env). cbctl: capella_guide.go, capella_globals.go:82.
- Fix: add a hidden `capella help TOPIC` that forwards to `guide` when the arg is a topic. Accept `1/true/yes/on` via `strconv.ParseBool` plus `yes/on`. Add `ValidArgsFunction` for cluster names (cached `cluster list`, honouring `--profile`). Mention `cbctl --version` in the guide.

### P-CLI-16 — LOW — `init --password-env` default name changed; output prefixes and usage exit codes differ
- capellactl: init.py:255 (`<CRED>_PASSWORD`). cbctl: capella_init.go:395 (`CBCTL_PWD_<CLUSTER>_<NAME>`). Cobra usage errors exit 1 where capellactl exited 2; `guide config nosuch` exits 3 where capellactl exited 2; message prefixes are `[error] -` instead of `[ERROR]:`.
- Fix: document all of these in the migration guide. Optionally route cobra flag errors through `SetFlagErrorFunc` to exit 3, so cbctl's own exit-code table holds.

## 8. Legacy-command verdict

`capella cluster create` (the legacy `--database/--allow-cidr/-u/-p/--user-email` path), `cluster exists|destroy|test` and `bucket create [name]|exists` (along with `scope/collection create|exists`) are **not capellactl commands**. They come from cbctl's own earlier SDK-based Capella flow (internal/capella/orchestrate.go). capellactl never connected through the SDK, never created a database user as part of cluster create, and has no exists/test/destroy verbs (`cluster delete` exists in both). So **hiding and deprecating them loses no capellactl functionality.**

The risk runs the other way: the legacy commands **occupy capellactl's verbs** (`cluster create` with no capella.yaml, `bucket/scope/collection create`). Hiding them alone does not restore capellactl behaviour. A non-lossy replacement must:
1. Make `capella cluster create [NAME]` always the v4/managed create (positional or `--name`; direct when there is no file). Use the legacy path only when its flags are passed, with a deprecation warning (P-CLI-03).
2. Make `capella bucket create CLUSTER NAME`, `scope create CLUSTER BUCKET NAME` and `collection create CLUSTER BUCKET SCOPE NAME` aliases of `add` with capellactl's arity, and keep `--database` for legacy arity until removal (P-CLI-04).
3. Give cbctl-only users replacements before removal: `cluster exists` → `cluster get NAME` (exit 3 = not found), `cluster destroy` → `cluster delete`, `bucket exists` → `bucket get` (exit 3), `cluster test` → no direct v4 equivalent. Keep `test` or document `cbctl cluster test --host <conn> --ssl` against the Capella endpoint. The database user created by legacy create maps to `capella credential create` and the allow-CIDR to `capella cidr create`.
4. Have hidden commands print `Deprecated:` (cobra `Deprecated` field) so scripts keep working while users are warned.

## 9. Migration-guide verdict

**Absent.** cbctl has no capellactl→cbctl command mapping (README, docs/capella/*, guide topics), no config/credential migration (it does not read `~/.config/capellactl/profiles.yaml`, the `capellactl` keychain service, `CAPELLACTL_*` env vars, `BEARER_TOKEN/ORG_ID/API_BASE_URL`, or the `./.capellactl/credentials` default), and no import command. The one migration-friendly behaviour is that `apiVersion: capellactl/v1` capella.yaml files are accepted unchanged (reference.md:22), and an explicit `credentialsOutput: ./.capellactl/credentials` is honoured. See P-CLI-02, -05, -06 and -12 for the fix.

| Severity | Count |
|---|---|
| critical | 0 |
| high | 7 |
| medium | 6 |
| low | 3 |
