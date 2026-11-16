# cbctl — Couchbase Server / Capella CLI

.PHONY: build test test-unit test-race test-integration test-integration-fake test-capella-live
.PHONY: janitor vet fmt-check tidy-check tidy cover-engine docs docs-check index index-check
.PHONY: coverage coverage-check ac-coverage links-check lint check release-snapshot

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

# Live Capella tests; need CAPELLA_API_KEY and CAPELLA_ORG_ID (CBCTL_LIVE_CLUSTER=1 adds the billable cluster parts).
test-capella-live:
	go test -tags=integration -timeout 90m -run Capella ./internal/integration -v

# Delete leftover live-test projects (CAP-032 AC3). PREFIX must end with '-' (default cbctl-ci-).
# Age filtering lands with the Phase-4 janitor CLI flags; until then pass an explicit prefix.
janitor: build
	@prefix="$(or $(PREFIX),cbctl-ci-)"; \
	./bin/cbctl capella janitor --prefix "$$prefix" --yes

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

cover-engine:
	go run ./cmd/cbdev ci cover-gate --min 85 ./internal/plan ./internal/resources ./internal/config/capellafile

docs:
	go run ./cmd/cbdev docs index
	go run ./cmd/cbdev docs coverage
	go run ./cmd/cbdev docs reference

docs-check:
	go run ./cmd/cbdev docs index --check
	go run ./cmd/cbdev docs coverage --check
	go run ./cmd/cbdev docs reference --check
	go run ./cmd/cbdev docs links --check

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

check: vet fmt-check lint test-unit test-integration-fake cover-engine docs-check ac-coverage tidy-check

release-snapshot:
	goreleaser release --snapshot --clean
