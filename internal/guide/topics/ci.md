# CI/CD pipelines

cbctl never waits for input without a terminal, so a pipeline cannot hang on a prompt.

## Approval

- `--auto-approve` (or `--yes`, `-y`) approves every change that command makes: creates, updates,
  scaling, deletes and `apply --prune`.
- It is the only way to approve. There is no environment variable or profile setting for it, so approval is
  always visible in the pipeline file and its logs, and a variable left set in a shell can never delete data.
- Without a terminal and without the flag, every command that changes Capella or capella.yaml stops with
  exit code 4 and changes nothing: `[error] - confirmation required but no terminal is attached`,
  then `→ re-run with --yes`.
- A question that has a default uses it. A question that needs an answer stops with exit code 3.

## Credentials

Set `CAPELLA_API_KEY` and `CAPELLA_ORG_ID` from the pipeline's secret store instead of running `configure`.
See `cbctl capella guide environment`.

## Quiet output

`--quiet` (`-q`) or `CBCTL_QUIET=1` drops progress lines, hints and "previous version kept" notes.
Before a change, only the plan summary line is printed, then the result table (or, with `-o json`, the JSON
document). Errors and blocked changes are always printed in full, and exit codes do not change.

## Installing in a pipeline

Download the `cbctl` binary from the GitHub Release (see the README) or build it with `go install`.

## Gating on changes

`cbctl capella plan --detailed-exitcode` exits 0 when Capella matches capella.yaml, 2 when there are changes,
3 when the file is invalid and 4 when a change is blocked. `cbctl capella drift --detailed-exitcode` works the
same way for changes made outside cbctl. See `cbctl capella guide exit-codes`.

## GitHub Actions

```yaml
jobs:
  capella:
    runs-on: ubuntu-latest
    env:
      CAPELLA_API_KEY: ${{ secrets.CAPELLA_API_KEY }}
      CAPELLA_ORG_ID: ${{ secrets.CAPELLA_ORG_ID }}
    steps:
      - uses: actions/checkout@v4
      - run: cbctl capella validate
      - run: cbctl capella plan --detailed-exitcode || [ $? -eq 2 ]   # 2: changes to apply
      - run: cbctl capella apply --auto-approve --quiet
        if: github.ref == 'refs/heads/main'
```

On a Windows runner (`runs-on: windows-latest`), steps run in PowerShell, so the gate is written differently:

```yaml
      - run: cbctl capella plan --detailed-exitcode; if ($LASTEXITCODE -notin 0, 2) { exit $LASTEXITCODE }; exit 0
```

## GitLab CI

```yaml
capella:
  variables:
    CBCTL_QUIET: "1"
  script:
    - cbctl capella validate
    - cbctl capella apply --auto-approve
  rules:
    - if: $CI_COMMIT_BRANCH == "main"
```

`CAPELLA_API_KEY` and `CAPELLA_ORG_ID` come from the project's masked CI/CD variables.

A second run of the same `apply` makes no changes and exits 0.
