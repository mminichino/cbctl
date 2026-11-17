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
| `CBCTL_IT_REPORT`, `CBCTL_IT_LOG` | Where the run writes its report and its log |
| `CBCTL_IT_SECRETS_DIR` | Where generated passwords go during the run |

The sandbox suite needs `CAPELLA_API_KEY` and `CAPELLA_ORG_ID` for a sandbox organization. In CI
they come from the `CAPELLA_SANDBOX_API_KEY` secret and the `CAPELLA_SANDBOX_ORG_ID` repository
variable. Never point it at an organization with anything you want to keep.

## Cleaning up

Every resource a run creates starts with its prefix. If a run is killed, delete what it left with:

```sh
make janitor PREFIX=cbctl-ci-
```

`cbctl capella janitor --prefix P --yes` deletes the projects whose names start with `P` (at
least 3 characters), and their clusters. It is hidden and meant for test cleanup only.

## Documentation checks

`make docs-check` checks that generated documents are current (the configuration reference, the
API coverage reports, the backlog index), that links and anchors resolve, and that every capellactl
document is mapped (`docs/capella/doc-map.yaml`). `go test ./internal/docs` checks that every
`cbctl` command shown in the README, the agent rules, these guides and the `guide` topics names a
real command with real flags.
