# cbctl — Couchbase Server / Capella CLI

.PHONY: build test test-unit test-integration vet tidy release-snapshot

build:
	go build -o bin/cbctl ./cmd/cbctl

test-unit:
	go test ./internal/...

test-integration:
	go test -tags=integration -timeout 20m ./internal/integration/ -v

test: test-unit

vet:
	go vet ./...

tidy:
	go mod tidy

release-snapshot:
	goreleaser release --snapshot --clean
