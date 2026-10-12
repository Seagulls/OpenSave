#!/usr/bin/env bash
# Exact-HEAD, one-shot source-only gate; never deploys or modifies a live save.
set -u
expected="${1:-}"
branch="$(git branch --show-current 2>/dev/null)"
head="$(git rev-parse HEAD 2>/dev/null)"
if [[ -z "$expected" || "$head" != "$expected" || "$branch" != "perf/first-copy-hotpath-hardening" || -n "$(git status --porcelain)" ]]; then
  echo "PRECHECK FAIL: exact commit/branch/clean worktree required"
  exit 2
fi
logs="${OPENSAVE_FIRST_COPY_LOG_DIR:-${XDG_STATE_HOME:-${HOME:-/tmp}/.local/state}/opensave/performance}"
mkdir -p "$logs" || exit 1
summary="$logs/firstcopy-performance-summary.log"
: > "$summary"
failed=0
run() {
  local name="$1" rc=0
  shift
  echo "BEGIN $name: $*" | tee -a "$summary"
  "$@" >"$logs/$name.log" 2>&1 || rc=$?
  echo "END $name: rc=$rc log=$logs/$name.log" | tee -a "$summary"
  if ((rc!=0));then failed=1; fi
}
run focused env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 12m -run 'PeerTransferAccess|ReadGenerationBlocksSelectedRanges' ./internal/store/ ./internal/snapshot/
run firstcopy-e2e env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 18m -run 'TestFirstCopy_' ./e2e/
run race env GOTOOLCHAIN=go1.26.4 go test -race -count=1 -timeout 20m -run 'PeerTransferAccess|ReadGenerationBlocksSelectedRanges|PhaseC3Guarded' ./internal/store/ ./internal/snapshot/ ./e2e/
run vet env GOTOOLCHAIN=go1.26.4 go vet ./internal/store/... ./internal/p2p/... ./internal/snapshot/... ./internal/api/... ./internal/daemon/... ./e2e/...
run backend-e2e env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 35m . ./cmd/opensave-cli ./cmd/opensave-relay ./internal/... ./relay/... ./testutil/... ./e2e/...
# This is a same-environment A/B microbenchmark of the existing 3-read path
# versus one statement. It is recorded evidence, not a claimed speedup target.
run benchmark env GOTOOLCHAIN=go1.26.4 go test -run '^$' -bench '^BenchmarkPeerAccess' -benchmem -benchtime=250ms -count=3 ./internal/store/
run diff-check git diff --check HEAD^ HEAD
if ((failed==0));then echo 'FIRST-COPY PERFORMANCE FULL GATE PASS' | tee -a "$summary";else echo 'FIRST-COPY PERFORMANCE FULL GATE FAIL' | tee -a "$summary";fi
exit "$failed"
