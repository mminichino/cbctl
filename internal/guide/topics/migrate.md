# Migrating from capellactl

`capellactl X` is `cbctl capella X`. capella.yaml files written by capellactl work unchanged.

```
cbctl configure migrate --dry-run     # what would be copied
cbctl configure migrate               # copy profiles and keychain API keys
cbctl capella doctor                  # anything left over
cbctl capella plan                    # should show no changes
```

**Renamed**

| capellactl | cbctl |
|---|---|
| `capellactl configure` | `cbctl configure` (`--api-base-url` still accepted) |
| `bucket create`, `scope create`, `collection create` | `cbctl capella bucket add`, `scope add`, `collection add` (the old verbs forward) |
| `import` (alias of sync) | `cbctl capella sync` (`cbctl capella import` loads JSON Lines data) |
| `help`, `help TOPIC`, `help config FIELD` | `cbctl capella guide`, `guide TOPIC`, `guide config FIELD` |
| `completion SHELL` | `cbctl completion SHELL` |
| `project apply NAME`, `cluster apply NAME` | `cbctl capella apply --target project/NAME` (old forms warn) |

Deprecated pre-port cbctl commands, hidden and warning: `cbctl capella cluster exists`,
`cluster destroy`, `cluster test`, `bucket exists`, and `cluster create`/`bucket create` with
`--database`.

**Environment**: `CAPELLACTL_CONFIG`, `CAPELLACTL_PROFILE`, `CAPELLACTL_QUIET`, `CAPELLACTL_HOME`,
`BEARER_TOKEN`, `ORG_ID` and `API_BASE_URL` work for one release with a warning. Use
`CBCTL_CONFIG`, `CBCTL_PROFILE`, `CBCTL_QUIET`, `CBCTL_HOME`, `CAPELLA_API_KEY`, `CAPELLA_ORG_ID`
and `CAPELLA_API_BASE_URL`.

**Files**: profiles move from `~/.config/capellactl` to `~/.config/cbctl`, keychain service
`capellactl` to `cbctl`, generated passwords from `./.capellactl/credentials` to
`./.cbctl/credentials` (the old directory is used while the new one does not exist). Password
files git would commit are refused (exit 4).

**apiVersion**: `capellactl/v1` and `cbctl/v1` are the same schema; `sync` keeps the file's
(`--api-version` to choose).

**Exit codes**: usage errors 2 → 3; a confirmation without a terminal and without `-y` 1 → 4.
Messages read `[error] -`, `[warning] -` and `-> action` (`→` on a color terminal). Success lines read
`[ok] -` where capellactl printed `[info]`.

**JSON**: `configure list|show -o json --output-format legacy` gives capellactl's shape.

Full guide: docs/capella/migrating-from-capellactl.md
