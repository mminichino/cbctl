# cbctl — Couchbase Server / Capella CLI

.PHONY: build test test-unit test-race test-integration test-integration-fake test-capella-live
.PHONY: janitor vet fmt-check tidy-check tidy cover-engine cover-profile cover-server cover-gate test-server-it test-server-multi-it docs docs-check index index-check
.PHONY: coverage coverage-check ac-coverage port-table port-footer port-gaps links-check lint check release-snapshot

build:
	go build -o bin/cbctl ./cmd/cbctl

test-unit:
	go test -race ./internal/...

test-race: test-unit

# Capella integration against the in-memory fake (every push). Skips cleanly when the
# Phase-4 harness (harness.go) is not present yet.
test-integration-fake:
	@if [ ! -f internal/integration/harness.go ]; then \
		echo "skip: internal/integration/harness.go not present yet"; \
	else \
		CBCTL_IT_TARGET=fake go test -tags=integration -race -timeout 20m ./internal/integration/...; \
	fi

# Sandbox Capella integration; needs CAPELLA_SANDBOX_API_KEY / CAPELLA_SANDBOX_ORG_ID.
test-integration:
	@if [ ! -f internal/integration/harness.go ]; then \
		echo "skip: internal/integration/harness.go not present yet"; \
	else \
		CBCTL_IT_TARGET=sandbox go test -tags=integration -timeout 300m ./internal/integration/... -v; \
	fi

# Live Capella tests; need CAPELLA_API_KEY and CAPELLA_ORG_ID (CBCTL_IT_CLUSTERS=1 adds the billable cluster parts).
test-capella-live:
	go test -tags=integration -timeout 90m -run Capella ./internal/integration -v

# Delete leftover live-test projects (CAP-032 AC3). PREFIX must end with '-' (default cbctl-ci-).
# Delete leftover test projects: PREFIX (default cbctl-ci-), older than HOURS (default 24).
janitor: build
	@prefix="$(or $(PREFIX),cbctl-ci-)"; hours="$(or $(HOURS),24)"; \
	./bin/cbctl capella janitor --prefix "$$prefix" --older-than-hours "$$hours" --yes

test: test-unit

vet:
	go vet ./...

fmt-check:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then \
		echo "$$out"; \
		echo "fmt-check: files need gofmt" >&2; \
		exit 1; \
	fi

tidy:
	go mod tidy

tidy-check:
	go mod tidy
	git diff --exit-code go.mod go.sum

cover-engine: cover-gate

# P-TST-18 tiered coverage gate. One -coverpkg=./internal/... profile from the unit suite plus
# one from the Server Docker flow (the only test that drives internal/server against a real
# cluster); tiers are internal/ci/covergate/tiers.json. Needs Docker.
COVER_DIR ?= .cover

cover-profile:
	@mkdir -p $(COVER_DIR)
	go test -count=1 -coverpkg=./internal/... -coverprofile=$(COVER_DIR)/unit.out ./internal/...

cover-server:
	@mkdir -p $(COVER_DIR)
	CBCTL_IT_REQUIRE_DOCKER=1 go test -count=1 -tags=integration,server -run 'TestServerCLIFlow|TestServerManifestApply|TestServerManifestMulti' -timeout 20m \
		-coverpkg=./internal/... -coverprofile=$(COVER_DIR)/server.out ./internal/integration/

cover-gate: cover-profile cover-server
	go run ./cmd/cbdev ci cover-gate --tiers default --profile $(COVER_DIR)/unit.out --profile $(COVER_DIR)/server.out

# Server Docker integration on its own (no coverage).
test-server-it:
	CBCTL_IT_REQUIRE_DOCKER=1 go test -count=1 -tags=integration,server -run 'TestServerCLIFlow|TestServerManifestApply|TestServerManifestMulti' -timeout 20m -v ./internal/integration/

# Two-node Server test from inside a Linux container, for macOS/Windows Docker Desktop hosts
# where container network addresses are not routable. On a Linux host use
# CBCTL_IT_SERVER_MULTI=1 make test-server-it instead. See scripts/server-it-linux.sh.
test-server-multi-it:
	scripts/server-it-linux.sh TestServerManifestMulti

docs:
	go run ./cmd/cbdev docs index
	go run ./cmd/cbdev docs coverage
	go run ./cmd/cbdev docs reference

docs-check:
	go run ./cmd/cbdev docs index --check
	go run ./cmd/cbdev docs coverage --check
	go run ./cmd/cbdev docs reference --check
	go run ./cmd/cbdev docs links --check
	go run ./cmd/cbdev docs doc-map --check

index:
	go run ./cmd/cbdev docs index

index-check:
	go run ./cmd/cbdev docs index --check

coverage:
	go run ./cmd/cbdev docs coverage

coverage-check:
	go run ./cmd/cbdev docs coverage --check

ac-coverage:
	go run ./cmd/cbdev docs ac-coverage --docs docs/backlog/release-1.5.0

# capellactl test parity: every capellactl test is covered or documented n/a (the merge-to-main bar).
PORT_MIN ?= 100
PORT_ARGS = --port-table internal/cli/testdata/port-map.tsv \
	--source-list internal/cli/testdata/capellactl-tests.txt \
	--footer internal/cli/testdata/scenario-port.md

port-table:
	go run ./cmd/cbdev docs ac-coverage $(PORT_ARGS) --min-covered $(PORT_MIN)

port-footer:
	go run ./cmd/cbdev docs ac-coverage $(PORT_ARGS) --write-footer

port-gaps:
	go run ./cmd/cbdev docs ac-coverage $(PORT_ARGS) --gaps

links-check:
	go run ./cmd/cbdev docs links --check

GOLANGCI_LINT_VERSION ?= v2.15.0

lint:
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "golangci-lint not found; install $(GOLANGCI_LINT_VERSION):" >&2; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)" >&2; \
		exit 1; \
	fi
	golangci-lint run ./...

check: vet fmt-check lint test-unit test-integration-fake cover-gate docs-check ac-coverage port-table tidy-check

release-snapshot:
	goreleaser release --snapshot --clean
