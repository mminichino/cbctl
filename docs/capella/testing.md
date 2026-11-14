# Testing Capella features

> Stub guide (Phase 4).

| Layer | How |
|---|---|
| Unit / fake | `go test ./internal/capellafake/...` and Capella CLI package tests |
| Hermetic CLI | `cli.Run` + `internal/clitest` scenarios (Phase 4) |
| Integration (fake) | `make test-integration-fake` when wired; `CBCTL_IT_TARGET=fake` |
| Full local gate | `make check` |

Prefer the fake for day-to-day work; live Capella stays behind explicit env gates.
