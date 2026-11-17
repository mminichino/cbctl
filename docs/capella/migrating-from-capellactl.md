# Migrating from capellactl

capellactl is no longer supported. Everything it did is in `cbctl`: the Capella commands are under
`cbctl capella`, and the profile commands are `cbctl configure`. capella.yaml files written by
capellactl load, plan and apply unchanged. This page lists everything that is spelled or behaves
differently. `cbctl capella guide migrate` prints a short version.

## Steps

```sh
cbctl configure migrate --dry-run                  # what would be copied from capellactl
cbctl configure migrate                            # copy profiles and keychain API keys
cbctl capella doctor                               # list what is still left over
cbctl capella validate                             # your capella.yaml, unchanged
cbctl capella plan                                 # should show no changes
```

Then replace `capellactl` with `cbctl capella` in scripts and pipelines, using the table below for
the few verbs that changed. Keep capellactl installed until `cbctl capella plan` shows no changes
for every environment.

## Commands

The rule: `capellactl X` is `cbctl capella X`, except for these rows.

| capellactl | cbctl | Notes |
|---|---|---|
| `capellactl configure …` | `cbctl configure …` | `cbctl capella configure` is an alias. `--api-base-url` is accepted as `--api-host` |
| `capellactl configure list\|show -o json` | `cbctl configure list\|show -o json --output-format legacy` | Without `--output-format legacy` the JSON is an array of rows |
| `capellactl bucket create CLUSTER NAME` | `cbctl capella bucket add CLUSTER NAME` | `bucket create` with capellactl's arguments still works and forwards to `add` |
| `capellactl scope create CLUSTER BUCKET NAME` | `cbctl capella scope add CLUSTER BUCKET NAME` | `scope create` with three arguments forwards to `add` |
| `capellactl collection create CLUSTER BUCKET SCOPE NAME` | `cbctl capella collection add CLUSTER BUCKET SCOPE NAME` | `collection create` with four arguments forwards to `add` |
| `capellactl import …` (hidden alias of `sync`) | `cbctl capella sync …` | `cbctl capella import` is the JSON Lines data importer; given a `sync` flag it exits 3 with this hint |
| `capellactl help` | `cbctl capella guide` | `help topics`, `help TOPIC` and `help config FIELD` are `guide topics`, `guide TOPIC` and `guide config FIELD` |
| `capellactl help cluster scale` | `cbctl capella cluster scale --help` | `cbctl help capella cluster scale` also works |
| `capellactl completion bash\|zsh\|fish` | `cbctl completion bash\|zsh\|fish\|powershell` | `cbctl capella completion` exits 3 and names the root command |
| `capellactl project apply NAME` (hidden) | `cbctl capella apply --target project/NAME` | The old form still works, hidden, with a deprecation warning |
| `capellactl cluster apply NAME` (hidden) | `cbctl capella apply --target cluster/NAME` | The old form still works, hidden, with a deprecation warning; `--project P` selects `cluster/P/NAME` |
| `capellactl cluster schedule apply NAME` | `cbctl capella cluster schedule apply NAME` | Same as `apply --target schedule/NAME` |
| `capellactl --version` | `cbctl capella --version` | Same text as `cbctl --version`: version, Management API version, licence |
| `--no-disk-auto-expansion`, `--no-cost-optimized`, `--no-flush`, `--no-cross-cluster-versioning`, `--no-protect` | Same | Accepted wherever the positive flag is, and also as `--flush=false` and so on |

Prompts work as in capellactl when a terminal is attached: `cluster create` without a name runs
the wizard, `cluster clone` without arguments lets you pick the source, then asks for the region,
the name and the target project, `bucket add` asks for `--memory`, `project create` asks for a
description, and `cluster group add` asks for `--services`. Without a terminal a missing value
exits 3 with `missing X: there is no terminal to ask for it`.

Other differences in these commands:

- Without a capella.yaml, `project create` creates the project at once; capellactl asked for
  `--auto-approve`. With a capella.yaml it asks, as capellactl did (`-y` skips the question).
- `project list` has the columns `NAME ID DESCRIPTION` (capellactl: `S.NO NAME DESCRIPTION CREATED
  IN FILE ID`). `-o json` lists the projects in the same order.
- `configure list` shows the organization as `NAME (ID)` and whether a key is stored, with no
  config-file column.
- `cluster protect --off` in file-managed mode writes `deletionProtection: false` into
  capella.yaml; capellactl removed the field.

### Commands cbctl had before the port

cbctl had its own Capella commands before the port. They are kept for one release, hidden, and
print a deprecation warning naming the replacement:

| Deprecated | Use |
|---|---|
| `cbctl capella cluster create --database …` (and the other pre-port flags) | `cbctl capella cluster create NAME [--size …] [--group …]`, then `cidr create` and `credential create` |
| `cbctl capella cluster exists` | `cbctl capella cluster get CLUSTER` |
| `cbctl capella cluster destroy` | `cbctl capella cluster delete CLUSTER -y` |
| `cbctl capella cluster test` | `cbctl capella credential create`, then `cbctl cluster test --host CONNECTION-STRING --ssl` |
| `cbctl capella bucket create NAME --database …` | `cbctl capella bucket add CLUSTER NAME` |
| `cbctl capella bucket exists` | `cbctl capella bucket get CLUSTER NAME` |

## Environment variables

capellactl's names work for one release. Each prints one deprecation warning on stderr. When both
names are set the cbctl name wins.

| capellactl | cbctl |
|---|---|
| `CAPELLACTL_CONFIG` | `CBCTL_CONFIG` |
| `CAPELLACTL_PROFILE` | `CBCTL_PROFILE` |
| `CAPELLACTL_QUIET` | `CBCTL_QUIET` (both take `1`, `true`, `yes`, `on`) |
| `CAPELLACTL_HOME` | `CBCTL_HOME` |
| `BEARER_TOKEN` | `CAPELLA_API_KEY` |
| `ORG_ID` | `CAPELLA_ORG_ID` |
| `API_BASE_URL` | `CAPELLA_API_BASE_URL` |

`CAPELLA_API_KEY`, `CAPELLA_ORG_ID` and `CAPELLA_API_BASE_URL` are the same in both tools.

## Profiles, keychain and credential files

| | capellactl | cbctl |
|---|---|---|
| Profiles | `~/.config/capellactl/profiles.yaml` (`CAPELLACTL_HOME`) | `~/.config/cbctl/profiles.yaml` (`CBCTL_HOME`) |
| API keys | OS keychain, service `capellactl` | OS keychain, service `cbctl` |
| Generated passwords | `./.capellactl/credentials` | `./.cbctl/credentials` |

- Until you migrate, cbctl reads a profile or key that only capellactl has, with a warning.
  `cbctl configure migrate` copies them. It skips a profile cbctl already has with the same
  settings. A profile that differs is a conflict and exits 4 unless you pass `--force`.
  `--remove-legacy` deletes each profile and key it copied from capellactl. API keys are never
  printed.
- Without an OS keychain, `cbctl configure` saves the profile and warns, as capellactl did; the
  key is never written to disk, so set `CAPELLA_API_KEY`.
- `cbctl configure remove` asks before removing the profile. Without a terminal it needs `-y`.
- When `./.cbctl/credentials` does not exist and `./.capellactl/credentials` does, cbctl uses the
  capellactl directory, so passwords saved earlier are found. An explicit `credentialsOutput` in
  capella.yaml is always honored.
- In a git repository, cbctl refuses (exit 4) to write a password file that git would commit.
  `init` and `sync` add `.cbctl/` to `.gitignore`. `--force-overwrite-secret` skips the check.
- `init --password-env` defaults to `CBCTL_PWD_<CLUSTER>_<NAME>` (capellactl: `<NAME>_PASSWORD`).
  The name is written into capella.yaml, so files written by capellactl keep theirs.

## capella.yaml and `apiVersion`

`apiVersion: capellactl/v1` and `apiVersion: cbctl/v1` mean the same schema. `cbctl capella sync`
keeps the `apiVersion` of the file it updates, and writes `cbctl/v1` for a new file. Choose with
`--api-version cbctl/v1|capellactl/v1`. A file kept on `capellactl/v1` still works with capellactl,
so you can go back during the migration. `cbctl capella doctor` lists files still on
`capellactl/v1`.

## Exit codes and messages

| Situation | capellactl | cbctl |
|---|---|---|
| Unknown command, bad flag, bad choice, wrong argument count | 2 | 3 |
| Confirmation needed, no terminal, no `-y` | 1 | 4 |
| Unknown field in `guide config FIELD` | 2 | 3 |
| `plan`/`drift --detailed-exitcode`, `sync --dry-run` found a difference | 2 | 2 |
| Everything else (0, 1, 3, 4, 5, 130) | | same |

The refusal text differs too. capellactl printed `refusing to <verb> without approval: there is no
terminal to ask`. Every cbctl command prints `confirmation required but no terminal is attached`, with
`re-run with --yes` as the action, or `add --yes to this pipeline step` when `CI` is set.

Messages are prefixed `[error] -`, `[warning] -` and `→` for the suggested action, instead of
`[ERROR]:`, `[WARNING]:` and `[ACTION]:`. Scripts that match on exit codes need only the two rows
above; scripts that match on message text need the new prefixes.

While waiting, a terminal shows progress dots without capellactl's spinner between them. As before,
there are no dots off a terminal, with `CI` set, or with `TERM=dumb`.

## JSON output

`-o json` output has the same fields as capellactl for every Capella command, with two exceptions:
`configure list` and `configure show`. Pass `--output-format legacy` to get capellactl's shape (a
map of profiles, and a map of `{value, source}` per setting).

## Not carried over

These were never in capellactl either and remain out of scope: App Services, audit logs, private
endpoints, network peering, organization users and API keys, alerts, and bucket storage
migration. `cluster clone` does not copy CMEK or private DNS settings, as in capellactl.
