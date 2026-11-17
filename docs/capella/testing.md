# Testing cbctl's Capella features

cbctl's Capella commands are tested at four levels. Day-to-day work needs only the first two, which
run offline in seconds; the sandbox suite runs nightly in CI against a real Capella organization.

| Level | Command | Talks to | When |
|---|---|---|---|
| Unit and CLI | `make test` (`go test -race ./internal/...`) | the in-memory fake (`internal/capellafake`) | every change |
| Full local gate | `make check` | nothing | before every push |
| Integration, fake | `make test-integration-fake` | the fake, through the built commands | every push (CI) |
| Integration, sandbox | `make test-integration` | a Capella sandbox organization | nightly (CI) |

## The fake

`internal/capellafake` serves the Capella Management API v4 routes cbctl uses, in memory, with
asynchronous states (`deploying` → `healthy`), failures you can inject, and a request log. CLI tests
point cbctl at it with `CAPELLA_API_BASE_URL`; nothing leaves the machine. A test that needs a
terminal (prompts, confirmations) fakes one; without that, commands behave as in a pipeline and
never wait for input.

## Try the workflow offline

With a `capella.yaml` in the working directory, the core loop is the commands below. The blocks
marked `bash run` are executed against the fake by `TestGuides_CAP033_AC5_GettingStartedRun`, so
they stay correct:

```bash run
cbctl capella validate
```

```bash run
cbctl capella plan
```

```bash run
cbctl capella apply --yes
```

```bash run
cbctl capella drift
```

```bash run
cbctl capella plan --detailed-exitcode
```

A second apply should report no changes:

```bash run
cbctl capella apply --yes
```

## Integration suites

`internal/integration` (build tag `integration`) runs whole workflows (create, scale, back up,
restore, clone, delete) through the commands. `CBCTL_IT_TARGET` picks the backend:

| Variable | Meaning |
|---|---|
| `CBCTL_IT_TARGET` | `fake` or `sandbox` |
| `CBCTL_TEST_PREFIX` | Name prefix of everything the run creates (default `cbctl-ci-`); cleanup deletes only these |
| `CBCTL_IT_CLUSTERS` | Also run the parts that create billable clusters (sandbox only) |
| `CBCTL_IT_REPORT`, `CBCTL_IT_LOG` | Where the run writes its report and its log. A relative path is taken from `$GITHUB_WORKSPACE`, else the repository root |
| `CBCTL_IT_SECRETS_DIR` | Where generated (and test-supplied) passwords go during the run, for the secret scan |
| `CBCTL_IT_ALLOWED_ORG_IDS` | Comma-separated organization IDs the suite may use. Required in CI; locally an empty list allows any organization with a warning |
| `CBCTL_IT_REQUIRE_TARGET` | `1`: a test that would skip for lack of credentials fails instead, and so does a run where no test used a target |

The sandbox suite needs `CAPELLA_API_KEY` and `CAPELLA_ORG_ID` for a sandbox organization. In CI
they come from the `CAPELLA_SANDBOX_API_KEY` secret and the `CAPELLA_SANDBOX_ORG_ID` repository
variable, which is also the allowlist. Never point it at an organization with anything you want
to keep.

Note that a `.env` file at the repository root is loaded by the suite: with Capella credentials in
it, `go test -tags=integration` runs against that organization unless `CBCTL_IT_TARGET=fake`.

### Guards

Before the first write the suite checks that the organization is in the allowlist and that the key
can read it; either failure fails the run. `CBCTL_TEST_PREFIX` must match `^cbctl-ci-([a-z0-9]+-)*$`
(at most 40 characters), and CI only takes the default, so the weekly janitor always matches what a
run left. Every project of a run is named `<prefix><run id>-<attempt>-<purpose>`.

When the `-timeout` deadline is near (the watchdog fires 26 minutes before it, or after four fifths
of a shorter timeout) or the process gets SIGINT/SIGTERM, the suite deletes every project with its
run prefix, writes the report and exits 1. The shared project is registered before it is applied,
so a failed apply is still torn down.

### Nightly workflow

`.github/workflows/nightly.yml` runs at 07:00 UTC, and on demand:

1. A `dedupe` job (`cbdev ci dedupe`) skips the run when another run of the same commit is queued
   or in progress, when a manual dispatch follows a success on the same commit within 20 hours, or
   when the cron fired twice on one day. A scheduled run is never skipped for an earlier night's
   success: the release gate needs one per night. If the run list cannot be read, it warns and runs.
2. A preflight fails on an empty secret or variable.
3. The suite runs with `CBCTL_IT_REQUIRE_TARGET=1`.
4. Whatever happened, a sweep deletes this run's projects (`janitor --prefix
   cbctl-ci-<run id>-<attempt>- --older-than-hours 0`), the secret scan checks the log and the
   report (`--require-secrets 1`; a hit, a missing file or no secrets is red), and both files are
   uploaded (a missing file is red).

A second run waits for the first (`concurrency: nightly-sandbox`, never cancelled mid-teardown).
GitHub keeps only one pending run per group, so a dispatch can replace a waiting scheduled run;
that night then has no scheduled run and the release gate's streak restarts. Re-dispatching a
failed night is an attempt of the same run and is not skipped.

## Cleaning up

Every resource a run creates starts with its prefix. If a run is killed, delete what it left with:

```sh
make janitor PREFIX=cbctl-ci-
```

`cbctl capella janitor --prefix P --yes` deletes the projects whose names start with `P` (at
least 4 characters, ending in `-`) and are older than `--older-than-hours` (default 24), and their
clusters. It is hidden and meant for test cleanup only. One project or cluster that fails does not
stop the others; any failure exits 1. Projects whose creation time cannot be read are kept and
listed. `--max-total-minutes` bounds the whole run (projects not reached are reported as skipped,
exit 1); `--dry-run` lists, and `--fail-on-leftovers` exits 1 when anything was found.

`.github/workflows/janitor.yml` runs the janitor for `cbctl-ci-` projects older than 24 hours every
Sunday at 09:00 UTC, with `--fail-on-leftovers`: a leftover means a nightly leaked it.

## Documentation checks

`make docs-check` checks that generated documents are current (the configuration reference, the
API coverage reports, the backlog index), that links and anchors resolve, and that every capellactl
document is mapped (`docs/capella/doc-map.yaml`). `go test ./internal/docs` checks that every
`cbctl` command shown in the README, the agent rules, these guides and the `guide` topics names a
real command with real flags.
