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

### Changed

- Capella failures use structured exit codes (0/1/2/3/4/5/130) with action hints.

### Deprecated

- `CBCTL_LIVE_CLUSTER` remains accepted; prefer `CBCTL_IT_CLUSTERS=1` once the Phase 4 integration
  harness is the primary entry (alias with a deprecation log).

### Security

- Secret redaction in logs and CLI output; generated credential passwords written only to 0600 files.
