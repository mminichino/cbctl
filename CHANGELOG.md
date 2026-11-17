# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [0.6.1] - 2026-11-17

### Changed

- CI action versions bumped: `actions/checkout` 4.4.0 → 7.0.1, `actions/setup-go` 5.6.0 → 7.0.0,
  `actions/upload-artifact` 4.6.2 → 7.0.2, `goreleaser/goreleaser-action` 6.4.0 → 7.3.0 (all
  Node 24). `golangci/golangci-lint-action` stays at v9.3.0, now pinned to its commit SHA instead
  of the annotated tag object.

### Fixed

- Managed `cluster delete` of a name missing from `capella.yaml` falls through to Capella, so a
  failed clone that rolled back the file can still be torn down.
- `internal/redact` unit tests no longer run in parallel against the shared secret registry, which
  made `TestRegisterAndString` flaky (seen on macOS CI).

## [0.6.0] - 2026-11-17

Folds capellactl into cbctl: every capellactl command is available under `cbctl capella`, and
capellactl is no longer supported. See [Migration](#migration).

### Added

- File-managed `cbctl capella cluster create` (capellactl parity): checks the file and the Capella
  network before asking anything, refuses a CIDR overlapping a cluster that is in the project in
  Capella but not in `capella.yaml` (the wizard re-asks it), warns on overlap with another project,
  replaces an entry that was never deployed instead of refusing it, and records the Couchbase
  Server version Capella chose so `sync` finds nothing to add.
- `cbctl capella cluster update NAME` with no flags on a terminal asks each value (Enter keeps it).
- File-managed `cluster scale --step` reports per-group results (`-o json` matches
  `scale.schema.json`, without `clusterId`); `cluster group add` is refused on free tier;
  `cluster update --disk-size/--disk-iops` on a fixed-size Azure disk (P6…P60) exits 3.

- Capella fold-in Phases 0–3: Management API client, Capella resource commands under `cbctl capella`,
  declarative `capella.yaml` engine (`sync` / `validate` / `plan` / `apply` / `drift` / `destroy` / `init`),
  file-managed mode for Phase 2 mutators, presets, guide topics, and secret handling.
- Capella init wizard polish and live §13.4 declarative cluster flow (gated by `CBCTL_LIVE_CLUSTER=1`).
- Hermetic `cli.Run` / `cli.Deps` entry for in-process tests; fake Inventory route parity test.
- Phase 4 scaffolding: `cmd/cbdev`, CI libraries (`secretscan`, `releasegate`, `janitor`), `clitest`,
  nightly health report.
- Tiered coverage gate (`make cover-gate`, `cbdev ci cover-gate --tiers default --profile P...`):
  merges `-coverpkg=./internal/...` profiles from the unit suite and the Server Docker flow and
  checks each package against its tier in `internal/ci/covergate/tiers.json`; a missing package
  fails. Replaces `cover-engine` in CI (`cover` job).
- The nightly is a health signal, not a gate: `release.yml` runs `cbdev ci release-gate
  --report-only` in an informational `nightly-health` job (`continue-on-error`; goreleaser does not
  need it), and no merge, push or tag waits for a green nightly. The report counts a night only
  when its `integration` job ran (deduped runs are ignored), one run per UTC day (newest attempt),
  and prints `UNKNOWN` when the runs cannot be read. The workflow defaults to `contents: read`; goreleaser has
  `contents: write` and a 30-minute timeout. `--repo` defaults to the `origin` remote.
- Phase 4 docs/backlog port: `docs/backlog/` (SOURCE, 1.0.0 `stories.yaml` + architecture,
  26× release-1.5.0 CAP stories, API snapshots), `internal/docgen/port` rewrite engine,
  `cbdev docs port`, and stub Capella guides under `docs/capella/`.

- Sandbox integration safety: an organization allowlist (`CBCTL_IT_ALLOWED_ORG_IDS`) and a reachability
  preflight, `CBCTL_IT_REQUIRE_TARGET=1` (a missing target fails the run), a validated
  `CBCTL_TEST_PREFIX`, a watchdog and SIGINT/SIGTERM sweep of the run's projects, and artifact paths
  resolved against the workspace.
- `cbdev ci dedupe` and a `dedupe` job in the nightly; `cbdev ci scan-secrets --require-secrets N
  --allow-missing`.
- `cbctl capella janitor --max-total-minutes`; the janitor continues past a failed cluster or project
  and lists projects whose creation time it cannot read. `.github/workflows/janitor.yml` runs it weekly.
- `cbctl --version` prints a second line with the licence (`Licensed under Apache-2.0 · Copyright
  Couchbase, Inc. and contributors`), as capellactl did; the first line is unchanged.
- CI builds the real binary on Linux, macOS and Windows and smoke-tests it (`TestBinarySmoke`).
- Capella credential create sends `credentialType: basic`, as capellactl did; the Capella fake answers a
  duplicate bucket, scope, collection, credential or allowed CIDR with Capella's code 1010.
- LICENSE is byte-identical to the Apache License 2.0 text again (final newline restored); the runbook's
  CI/CD section gives the right exit code (4) for a change without a terminal or `-y`.
- Couchbase Server commands: IPv6 literals work in `--host` and node specs (`::1`, `[::1]:8091`);
  multi-node `cluster create` keeps a node spec's non-default management port when adding the node;
  `--ext-api` is used for alternate-address setup as for every other step; a query service that is
  not ready after provisioner `cluster create` is reported as a warning instead of silently ignored.
- `TestCapellactlCompat` checks every capellactl command and option against cbctl, from a dump of
  capellactl's command tree.

### Changed

- The Server Docker integration test (`TestServerCLIFlow`) needs `-tags=integration,server`
  (`make test-server-it`) and fails rather than skips without Docker when
  `CBCTL_IT_REQUIRE_DOCKER=1`.
- Capella failures use structured exit codes (0/1/2/3/4/5/130) with action hints.
- Every usage mistake exits 3: an unknown or ambiguous subcommand (it used to print help and exit
  0), a bad flag or choice, a missing required flag, a wrong argument count.
- `capella bucket add` defaults to 1024 MB, like capellactl, and leaves the storage backend to
  Capella.
- `CBCTL_QUIET` accepts `1`, `true`, `yes` and `on`.
- `docs/capella/reference.md` lists every field of capella.yaml, including fields inside lists
  (`serviceGroups`, `buckets.scopes.collections`, `databaseCredentials.access`).
- `capella project create --direct` looks for a project with the name before creating one. Capella
  allows two projects with the same name, so cbctl no longer relies on Capella to refuse it.
- `capella project delete` of a missing project is a no-op (exit 0), so a rerun succeeds; on a
  terminal it asks for the project name to be typed. `project create` of an existing name exits 4
  with a `sync --project` hint, and `--config FILE` naming a missing file starts that file.
- `capella project apply NAME` (hidden alias) applies the project only, not its clusters.
- Managed `cluster protect --on` no longer asks for confirmation.
- `configure` without an OS keychain saves the profile and warns (it used to exit 3), as capellactl
  did. It reports what it cleaned from a pasted key and prints a checklist when Capella rejects it.
- `configure` checks an existing capella.yaml against the key's organization and projects, offers
  to write capella.yaml on a terminal (`--import` / `--no-import` choose without asking), and
  prints the next step otherwise.

### Added (capellactl port)

- `docs/capella/migrating-from-capellactl.md` and `cbctl capella guide migrate`.
- The capellactl operator guides, ported: `getting-started.md`, `runbook.md`,
  `custom-clusters.md`, `terraform.md`, `migrating-from-scale-script.md`, `backup-restore.md`.
- `cbctl configure migrate` (alias `import-capellactl`) and `cbctl capella doctor`.
- capellactl's prompts on a terminal: the `cluster create` wizard, `cluster clone` without
  arguments, `bucket add --memory`, `project create` description, `cluster group add --services`.
- The `cluster create` wizard asks capellactl's questions: one or several service groups (unused
  services as the default), a known compute size picker, a disk-type picker per group (also on
  `cluster group add`), and accepts the start of an answer (`m` for `multiple groups`).
- `cluster create` warns about a compute size the provider does not list; `--group` usage errors
  match capellactl's; an unknown service suggests the closest one.
- Fix: `cluster update --disk-type` keeps the group's current disk size when the new type allows it
  (it used to reset it to the type's smallest size and fail as a shrink).
- capellactl spellings: `bucket|scope|collection create` with capellactl's arguments,
  `cluster schedule apply`, `--no-disk-auto-expansion`, `--no-cost-optimized`, `--no-flush`,
  `--no-cross-cluster-versioning`, `configure --api-base-url`, and
  `configure list|show --output-format legacy`.
- Backups, as in capellactl: backup, export and restore waits print each state with its elapsed time
  and a dot every 30 s on the same line; `bucket backup schedule set` on a terminal without flags asks
  for each field; with a capella.yaml, a bucket schedule set on a free-tier cluster exits 4 and a new
  bucket schedule records every default field; `cluster backup list` adds a PROJECT column only when
  it lists several projects; every backup verb's `--help` has examples; a managed schedule delete of
  a missing schedule says "has no backup schedule; nothing to do".
- capellactl behaviour the port had missed: `configure` on a terminal offers to write `capella.yaml`
  from Capella when the folder has none (or says the organization has no projects yet);
  `project create` on a terminal without a `capella.yaml` starts one with the new project; `project list` shows
  an `IN FILE` column when there is a `capella.yaml`; `cluster list` sorts by project and name and
  its JSON carries capellactl's `projectName`; `cluster create` with several projects in the file
  and no `--project` asks which one on a terminal.

### Fixed (capellactl port)

- A managed edit of a single-project file (`project:` + `clusters:`) now writes it in the
  `projects:` form, as `cbctl capella guide config` documents (the `.bak` keeps the old form).
- `sync` writes advanced credentials' reference comments under their cluster's
  `databaseCredentials`, not at the end of the file.
- `configure` stores the key in the same keychain the other commands read.
- `cluster create` wizard: when the answers make the cluster invalid (for example size `dev` with
  `--availability multi`), it explains why and asks the provider and size again, keeping the name.
- Shell completion completes CLUSTER arguments from the cluster names in `capella.yaml`, and
  `cbctl completion SHELL` writes to the command's output.
- `--version` also prints `Capella Management API v4.0`, and `cbctl capella --version` prints the
  same text (scripts that replaced `capellactl` with `cbctl capella` keep working).
- The hidden `capella cluster apply NAME` accepts capellactl's `--project P` (= `cluster/P/NAME`).
- `configure` keeps the organization name in the profile (and `configure migrate` carries
  capellactl's over); `configure list` shows `NAME (ID)` as capellactl did.
- No progress dots with `TERM=dumb` (as with `CI` set or off a terminal).
- Every error has a next step, including wrapped Server command errors; config actions name a
  command instead of a field to type into `capella.yaml`.
- On/off schedule windows are encoded as live Capella accepts them (probe: nightly run
  44141806450). Capella reads `to` literally (0:00 is the start of the day) and refuses hour 24, so
  `HH:MM-24:00` is sent with no `to`, which Capella keeps as "until midnight". `00:00-24:00` is
  sent as `on`. capellactl sent `to: 0:00` for both, and Capella refused that with 422. A week that
  is on every day is refused before any call: `schedule set` exits 3 and `validate` reports it,
  because Capella answers 500. Delete the schedule to keep a cluster always on.
- `cluster clone` (direct and managed) and `apply` create clusters with the release line
  (`8.0`) of a recorded version. Capella reports the patch it runs (`8.0.3`) but refuses a patch
  version on create. A recorded `8.0.3` still matches a cluster now on `8.0.4`, so `plan` is
  clean.
- The test fake rejects both of these request bodies as the live API does, so the offline suite
  catches them.
- Managed `cluster clone` picks its default network from every project's clusters in Capella,
  not only from `capella.yaml`, as `cluster create` already did. Capella refuses a range that is
  "not unique within this organization", and the fake now does too.
- Changing an allowed CIDR's `comment` or `expiresAt` in `capella.yaml` now applies (nightly run
  44144706943). Capella has no update for allowed CIDRs, so the entry is replaced. The replace
  sent the new entry first and fell back to delete-then-create only on 409, but live Capella
  refuses a CIDR the cluster already has with 422 ("The CIDR provided already exists for the
  cluster"). A replace now deletes the old entry first, then creates the new one, retrying for up
  to 30s while Capella still counts the deleted entry. If the create still fails, the error says
  the entry was removed and that running `apply` again adds it back. Access from the range stops
  briefly, and a warning says so. `cidr create` and `cluster clone --with-cidrs` treat that 422 as
  "already allowed", as they did with 409. The fake now answers a duplicate CIDR with that 422.
  Only allowed CIDRs are replaced. Every other kind is updated in place, so no other kind had
  this problem.

### Deprecated

- `CBCTL_LIVE_CLUSTER` remains accepted; use `CBCTL_IT_CLUSTERS=1`.
- capellactl's environment variables, accepted for one release with a warning:
  `CAPELLACTL_CONFIG`, `CAPELLACTL_PROFILE`, `CAPELLACTL_QUIET`, `CAPELLACTL_HOME`, `BEARER_TOKEN`,
  `ORG_ID`, `API_BASE_URL`. capellactl's profiles, keychain entries and `./.capellactl/credentials`
  are read as fallbacks until `cbctl configure migrate` copies them.
- The pre-port Capella commands, hidden and warning: `capella cluster exists|destroy|test`,
  `capella bucket exists`, `capella cluster create|bucket create` with `--database`, and
  `capella cluster|project apply NAME`.

### Breaking (for capellactl users)

- The command is `cbctl capella …` (`cbctl configure …` for profiles); capellactl's `import` is
  `cbctl capella sync`, its `help` is `cbctl capella guide`, its `completion` is `cbctl completion`.
- Usage errors exit 3 (capellactl: 2). A confirmation without a terminal and without `-y` exits 4
  (capellactl: 1).
- Messages are prefixed `[error] -`, `[warning] -` and `→` (capellactl: `[ERROR]:`, `[WARNING]:`,
  `[ACTION]:`).
- `configure list|show -o json` print arrays of rows unless `--output-format legacy`.
- `init --password-env` defaults to `CBCTL_PWD_<CLUSTER>_<NAME>` (capellactl: `<NAME>_PASSWORD`).
- Writing a password file that git would commit exits 4 (`--force-overwrite-secret` overrides).

### Migration

Run `cbctl configure migrate`, then `cbctl capella doctor`, then `cbctl capella plan` (it should
show no changes). capella.yaml files with `apiVersion: capellactl/v1` work unchanged, and `sync`
keeps that version. See [docs/capella/migrating-from-capellactl.md](docs/capella/migrating-from-capellactl.md).

### Security

- Secret redaction in logs and CLI output; generated credential passwords written only to 0600 files.

[Unreleased]: https://github.com/mminichino/cbctl/compare/v0.6.1...HEAD
[0.6.1]: https://github.com/mminichino/cbctl/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/mminichino/cbctl/compare/v0.5.0...v0.6.0
