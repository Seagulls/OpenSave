#!/bin/sh
set -eu

BASE_SHA=7b690420c0fa03513764f4937af29bebf7f391a7
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)
BASE_PARENT=$(mktemp -d /tmp/opensave-cloud-integrity-baseline.XXXXXX)
BASE_TREE="$BASE_PARENT/repo"
TASK_GOCACHE=${GOCACHE:-/tmp/opensave-cloud-integrity-gocache}

cleanup() {
	rc=$?
	if [ -d "$BASE_TREE" ]; then
		git -C "$REPO_ROOT" worktree remove --force "$BASE_TREE" >/dev/null 2>&1 || true
	fi
	rm -rf "$BASE_PARENT"
	printf 'opensave cloud-provider-integrity return code: %d\n' "$rc"
	exit "$rc"
}
trap cleanup EXIT INT TERM

printf 'baseline: %s\n' "$BASE_SHA"
git -C "$REPO_ROOT" worktree add --detach "$BASE_TREE" "$BASE_SHA"
cp "$SCRIPT_DIR/baseline_provider_integrity_test.go.fixture" \
	"$BASE_TREE/internal/cloud/provider_integrity_baseline_test.go"
(
	cd "$BASE_TREE"
	GOCACHE="$TASK_GOCACHE" go test ./internal/cloud -run '^TestBaselineWitness' -count=1 -v
)
git -C "$REPO_ROOT" worktree remove --force "$BASE_TREE"

printf '\ncurrent branch focused regressions (3 repetitions):\n'
(
	cd "$REPO_ROOT"
	GOCACHE="$TASK_GOCACHE" go test ./internal/cloud \
		-run 'Test(WebDAVUpload|LocalFolderList)' -count=3
)

printf '\ncurrent branch affected package race run:\n'
(
	cd "$REPO_ROOT"
	GOCACHE="$TASK_GOCACHE" go test -race ./internal/cloud -count=1
)

printf '\ncurrent branch affected package vet:\n'
(
	cd "$REPO_ROOT"
	GOCACHE="$TASK_GOCACHE" go vet ./internal/cloud
)
