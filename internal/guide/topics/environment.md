# Environment, profiles and precedence

**Profiles**: `cbctl capella configure [--profile NAME]` verifies an API key, lets you pick the
organization, saves settings in `~/.config/cbctl/profiles.yaml` and the key in the OS keychain.
Switch with `--profile NAME` or `CBCTL_PROFILE`. `cbctl capella configure show` prints every
effective setting and where it came from.

**If the key is rejected (401)**: check the API key's allowed IP list includes this machine's public IP,
that you copied the **API Key Token** (not the API Key ID), and that it has not expired. Read the key from a pipe
instead of pasting: `pbpaste | cbctl capella configure --api-key-stdin`.

**Environment variables**

| Variable | Meaning |
|---|---|
| `CAPELLA_API_KEY` | API key (Bearer token) |
| `CAPELLA_ORG_ID` | Organization ID |
| `CAPELLA_TOKEN` | Alias of `CAPELLA_API_KEY` |
| `CAPELLA_API_BASE_URL` | Default `https://cloudapi.cloud.couchbase.com` |
| `CBCTL_CONFIG` | Config file path |
| `CBCTL_HOME` | Directory of `profiles.yaml` (default `~/.config/cbctl`) |
| `CBCTL_PROFILE` | Profile name |
| `CBCTL_QUIET` | `1`/`true`/`yes`/`on` turns on `--quiet` |

capellactl's names still work for one release, with a deprecation warning on stderr:
`CAPELLACTL_CONFIG`, `CAPELLACTL_PROFILE`, `CAPELLACTL_QUIET`, `CAPELLACTL_HOME`, `BEARER_TOKEN`,
`ORG_ID` and `API_BASE_URL`. When both names are set the cbctl name wins. Profiles and keychain
entries that only capellactl has are read too (with a warning); `cbctl configure migrate` copies
them for good and `cbctl capella doctor` lists what is left. See `cbctl capella guide migrate`.

**Precedence** (highest first): command-line flags → environment variables → profile →
`organizationId` in the config file.

**API key roles**: Organization Owner, or Project Creator to create projects plus Project Owner or
Project Manager for clusters and their contents.
