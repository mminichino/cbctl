# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- Capella fold-in Phases 0–3: Management API client, Capella resource commands under `cbctl capella`,
  declarative `capella.yaml` engine (`sync` / `validate` / `plan` / `apply` / `drift` / `destroy` / `init`),
  file-managed mode for Phase 2 mutators, presets, guide topics, and secret handling.
- Capella init wizard polish and live §13.4 declarative cluster flow (gated by `CBCTL_LIVE_CLUSTER=1`).
- Hermetic `cli.Run` / `cli.Deps` entry for in-process tests; fake Inventory route parity test.
- Phase 4 scaffolding: `cmd/cbdev`, CI libraries (`secretscan`, `releasegate`, `janitor`), `clitest`,
  nightly/release gates (landing progressively).
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

### Changed

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
- capellactl spellings: `bucket|scope|collection create` with capellactl's arguments,
  `cluster schedule apply`, `--no-disk-auto-expansion`, `--no-cost-optimized`, `--no-flush`,
  `--no-cross-cluster-versioning`, `configure --api-base-url`, and
  `configure list|show --output-format legacy`.

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
- Without an OS keychain, `configure` exits 3 and saves nothing (capellactl saved the profile).
- `init --password-env` defaults to `CBCTL_PWD_<CLUSTER>_<NAME>` (capellactl: `<NAME>_PASSWORD`).
- Writing a password file that git would commit exits 4 (`--force-overwrite-secret` overrides).

### Migration

Run `cbctl configure migrate`, then `cbctl capella doctor`, then `cbctl capella plan` (it should
show no changes). capella.yaml files with `apiVersion: capellactl/v1` work unchanged, and `sync`
keeps that version. See [docs/capella/migrating-from-capellactl.md](docs/capella/migrating-from-capellactl.md).

### Security

- Secret redaction in logs and CLI output; generated credential passwords written only to 0600 files.
