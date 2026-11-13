# cbctl — Couchbase Server / Capella CLI

.PHONY: build test test-unit test-integration test-capella-live janitor vet tidy release-snapshot

build:
	go build -o bin/cbctl ./cmd/cbctl

test-unit:
	go test -race ./internal/...

test-integration:
	go test -tags=integration -timeout 20m ./internal/integration/ -v

# Live Capella tests; need CAPELLA_API_KEY and CAPELLA_ORG_ID (CBCTL_LIVE_CLUSTER=1 adds the billable cluster parts).
test-capella-live:
	go test -tags=integration -timeout 90m -run Capella ./internal/integration -v

# Delete leftover live-test projects: make janitor PREFIX=cbctl-ci-
janitor: build
	@test -n "$(PREFIX)" || { echo "usage: make janitor PREFIX=cbctl-ci-"; exit 1; }
	./bin/cbctl capella janitor --prefix "$(PREFIX)" --yes

test: test-unit

vet:
	go vet ./...

tidy:
	go mod tidy

release-snapshot:
	goreleaser release --snapshot --clean
