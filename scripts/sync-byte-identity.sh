#!/usr/bin/env bash
# Proves that `cbctl capella sync` writes the same bytes on two commits.
#
# It runs TestSyncDump (internal/cli/syncdump_test.go) against the fake Capella on a baseline
# ref (default: the last release tag, v0.6.1) and on the working tree, then diffs the files.
# The baseline gets a copy of the test file, since it predates it. Exit 0 = identical.
#
# Usage: scripts/sync-byte-identity.sh [BASE_REF]
set -euo pipefail

base="${1:-v0.6.1}"
root="$(git rev-parse --show-toplevel)"
tmp="$(mktemp -d)"
trap 'git -C "$root" worktree remove --force "$tmp/base" >/dev/null 2>&1 || true; rm -rf "$tmp"' EXIT

git -C "$root" worktree add --detach "$tmp/base" "$base" >/dev/null
cp "$root/internal/cli/syncdump_test.go" "$tmp/base/internal/cli/syncdump_test.go"

run() { # dir out
	(cd "$1" && CBCTL_SYNC_DUMP_DIR="$2" go test ./internal/cli -run '^TestSyncDump$' -count=1 >/dev/null)
}
run "$tmp/base" "$tmp/out-base"
run "$root" "$tmp/out-head"

if diff -ru "$tmp/out-base" "$tmp/out-head"; then
	echo "sync output is byte-identical to $base ($(find "$tmp/out-head" -type f | wc -l | tr -d ' ') files)"
else
	echo "sync output differs from $base" >&2
	exit 1
fi
