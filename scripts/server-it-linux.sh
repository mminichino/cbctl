#!/usr/bin/env bash
# Runs the Server Docker integration tests from inside a Linux container.
#
# Why: TestServerManifestMulti talks to Couchbase containers on their Docker network addresses.
# Those addresses are reachable from a Linux host, but not from macOS or Windows (Docker Desktop
# keeps the containers in a VM), so the test skips there. This script compiles the test binary for
# Linux and runs it in a container that shares the Docker VM's network namespace (--network host)
# and talks to the same Docker daemon through the mounted socket. The test itself is unchanged
# and checks the same things as on a Linux host; only where the test process runs differs.
#
# Usage: scripts/server-it-linux.sh [go test -run pattern]
#   default pattern: TestServerManifestMulti
# Environment: CBCTL_IT_IMAGE overrides the container image that supplies the docker CLI
# (default docker:cli); GOMODCACHE/GOCACHE are used as set for the build.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
pattern="${1:-TestServerManifestMulti}"
image="${CBCTL_IT_IMAGE:-docker:cli}"

arch="$(docker version --format '{{.Server.Arch}}')"
case "$arch" in
aarch64) arch=arm64 ;;
x86_64) arch=amd64 ;;
esac

out="$root/bin/linux-$arch"
mkdir -p "$out"
(
	cd "$root"
	CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
		go test -c -tags=integration,server -o "$out/integration.test" ./internal/integration
)

# --network host: the Docker VM's namespace routes to every container network.
# The socket gives testcontainers (and `docker info`) access to the daemon.
# Ryuk's port is published on the daemon host, which is localhost for this container.
exec docker run --rm \
	--network host \
	-v /var/run/docker.sock:/var/run/docker.sock \
	-v "$root:/work:ro" \
	-w /work/internal/integration \
	-e CBCTL_IT_SERVER_MULTI="${CBCTL_IT_SERVER_MULTI:-1}" \
	-e CBCTL_IT_REQUIRE_DOCKER=1 \
	-e TESTCONTAINERS_HOST_OVERRIDE=localhost \
	"$image" \
	/work/bin/linux-"$arch"/integration.test \
	-test.v -test.count=1 -test.timeout=20m -test.run "$pattern"
