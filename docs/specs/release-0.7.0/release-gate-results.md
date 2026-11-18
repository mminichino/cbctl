# 0.7.0 Release gate results

Run on branch `mm--release-0-7-0` (macOS arm64, Go from `go.mod`, Docker Desktop available) on 2026-11-17.
Source checklist: [04-test-and-release.md](04-test-and-release.md) section 2.

Legend: **pass** = ran here and succeeded; **not-run** = needs something this run did not have;
**manual** = a step only the maintainer can do (merge, tag, publish).

## Automated checks

| Item | Result | Notes |
|---|---|---|
| `gofmt -l .` | pass | no files listed |
| `go build ./...` for `GOOS=linux`, `GOOS=darwin`, `GOOS=windows` | pass | cross-compile only; the CI matrix runs the tests on each OS |
| `go vet ./...` and `go vet -tags=integration,server ./...` | pass | |
| `golangci-lint run ./...` | pass | 0 issues |
| `go test -race -count=3 ./...` | pass | 40 packages; was failing before the fixes in "Flakes" below |
| `go test -race ./...` | pass | final run on the release commit |
| `make check` | pass | vet, fmt-check, lint, test-unit, test-integration-fake, cover-gate, docs-check, ac-coverage, port-table, tidy-check |
| `make docs-check` | pass | generated reference, index and doc-map are current |
| `make cover-gate` | pass | Docker works here; unit + `TestServerCLIFlow`/`TestServerManifestApply` profiles; every tier at or above its floor (cli 86.1% vs 85%) |
| `make ac-coverage` | pass | 0 stories in `docs/backlog/release-1.5.0` need tagged tests |
| `make tidy-check` | pass | `go mod tidy` leaves `go.mod`/`go.sum` unchanged |
| `make port-table` | pass | 878 of 878 capellactl tests covered or n/a (100%) |
| `make test-integration-fake` | pass | |
| `scripts/sync-byte-identity.sh` (baseline `v0.6.1`) | pass | 6 sync files byte-identical |
| `go run ./cmd/cbdev ci notices` | pass | `THIRD_PARTY_NOTICES.md` lists `go.yaml.in/yaml/v3` v3.0.6; `fatih/color` and `gopkg.in/yaml.v3` gone |
| `TestBinarySmoke` (built binary, all CAP045 cases) | pass | was 2 failures, now isolated from `.env`/keychain; see "Flakes" |

## Binary end-to-end smoke (`bin/cbctl`, built with `-X main.version=0.7.0`)

| Check | Result |
|---|---|
| `--version` prints `cbctl version 0.7.0 (commit=none date=unknown)` | pass |
| `--help` | pass |
| `--color always` colors only the label word (`ESC[1;31m[error]ESC[0m - ...`); `never` and `auto` (piped) print no escapes; `--color bogus` exits 3 | pass |
| `CLICOLOR_FORCE=1` and `FORCE_COLOR=1` force color on a pipe; `FORCE_COLOR=0`, `CLICOLOR=0`, `NO_COLOR=1`, `TERM=dumb` suppress it (NO_COLOR and TERM=dumb beat CLICOLOR_FORCE) | pass |
| `cluster exists` prints exactly `false`, also with `--color always` and `CLICOLOR_FORCE=1` | pass |
| `apply -f` `CouchbaseServer` `--dry-run`: exit 0, `dry run, nothing was changed`; unset `${CB_PASSWORD}` exits 3 with file:line:col and an action hint | pass |
| `apply -f` `-o json` `--dry-run`: one JSON document on stdout, `[info]` status line on stderr | pass |
| `apply -f` unknown field exits 3 with `did you mean nodes?` | pass |
| `apply -f` `CouchbaseCapella` with a missing size/serviceGroups exits 3 offline | pass |
| `capella validate -c` on a file with `kind:` exits 3 with a pointer to `cbctl apply -f` | pass |
| `apply -f` `CouchbaseCapella` dry run against Capella | not-run: needs a Capella API key |

## Tests that need an environment this run did not have

| Item | Result | Reason / how to run |
|---|---|---|
| `TestServerManifestMulti` (two-node Server) | not-run | skips unless `CBCTL_IT_SERVER_MULTI=1` and Linux (container IPs are not reachable from a macOS host). Run on a Linux Docker host: `CBCTL_IT_SERVER_MULTI=1 make test-server-it` |
| `make test-server-it` (`TestServerCLIFlow`, `TestServerManifestApply`) | pass | ran inside `make cover-gate` with `CBCTL_IT_REQUIRE_DOCKER=1` |
| `TestCapellaManifestApply_Live` and the rest of `make test-integration` | not-run | need live Capella credentials (`CAPELLA_API_KEY`, `CAPELLA_ORG_ID`); the billable parts also need `CBCTL_IT_CLUSTERS=1`. Then run the janitor with `--dry-run` for `cbctl-ci-` leftovers older than 24 h |
| CI matrix on Windows and Linux | not-run | only the cross-compile ran locally; CI on the PR covers the OS matrix |

## Checklist items from section 2

| Item | Result | Notes |
|---|---|---|
| P1–P6 merged through PRs with CI green | manual | stacked PRs #19, #18, #20, #21, #22, #23 (state not checked: `gh` is not logged in here); this release PR contains all of them (`mm--apply-capella` already contained every other phase branch, so the integration was a fast-forward and no conflicts arose) |
| Every AC in 01 section 8, 02b section 6, 03 section 10 checked in its PR | manual | maintainer review |
| `make check` green on `main` at the release commit | manual | green on the release branch; repeat on `main` after merge |
| Output contract table (03 section 6) has a passing test per row | pass | covered by `internal/cli` golden tests and the env-matrix tests |
| README: Manifests section, units table, Terraform example, Output and color, `[ok]` examples, pin `v0.7.0` | pass | |
| `.cursor/rules/cbctl.mdc`: apply line and behaviour rule, `--color`, agent rule 3, `cluster join --arbiter` | pass | |
| `.cursor/rules/cli-logging.mdc` matches the design (no `fatih/color`) | pass | already rewritten by P2; verified |
| Manifest spec amended for D3 and checked in | pass | C17 clarified: `--dry-run` exits 0 with or without changes |
| `docs/manifests.md`, `docs/capella/terraform.md`, guide topic `ci.md` | pass | `migrate.md` already describes `[ok]` |
| `make docs` run, generated docs current | pass | `make docs-check` is clean |
| `THIRD_PARTY_NOTICES.md` and `NOTICE` | pass | |
| CHANGELOG `[0.7.0]` with Added, Changed (breaking), Fixed, Migration; `[Unreleased]` empty | pass | |
| Commit `chore(release): 0.7.0`, PR, CI green, `gh pr merge` | manual | PR is opened; merge is the maintainer's call |
| `git tag -a v0.7.0 -m "cbctl 0.7.0"` and `git push origin v0.7.0` | manual | deliberately not done |
| Verify release assets (linux/darwin amd64+arm64, windows/amd64, `checksums.txt`) and smoke `apply -f` with the README Server example against a local container | manual | after `release.yml` finishes |

## Flakes found and fixed

| Symptom | Root cause | Fix |
|---|---|---|
| `TestInitNonInteractive`, `TestInitInteractive`, `TestProjectDeleteCascade` (and `TestScheduleApplyIsTargetedApply`, `TestValidateOfflineNeedsNoKey`, `TestConfigDiscovery`, `TestPlanApplyIdempotent`, `TestSyncRoundTrip`) failed under `-count=2`/`-count=3` | Tests that run `rootCmd` with `-o json` set `json: true` on the process-wide Printer (`logging.Current()`). The state outlived the test, so later tests saw `[ok]`/`[info]` lines on stderr instead of stdout. Order dependent, which is why a single run passed | `isolatePrinter(t)` (`internal/cli/printer_isolation_test.go`) installs a fresh Printer and restores the old one in `t.Cleanup`; applied to the configure, legacy-help and completion tests that leaked |
| `TestBinarySmoke/CAP045_AC2_init_presets_bundled` and `CAP045_AC5_no_credentials_exits_3` | Not the keychain: `findDotEnv` walks up from the working directory to the first `go.mod`/`.git`. The test's temp dir lives under `GOTMPDIR` inside the repo, so it found the developer's `.env` with a real `CAPELLA_API_KEY` | The test writes a `go.mod` into its work dir so the walk stops there |
