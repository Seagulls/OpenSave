#!/usr/bin/env bash
# Source-only, single-invocation software gate for Phase C.1.
set -u
expected="${1:-}"
[[ -n "$expected" ]] || { echo "Usage: $0 EXACT_COMMIT_SHA"; exit 2; }
head="$(git rev-parse HEAD 2>/dev/null)"
branch="$(git branch --show-current 2>/dev/null)"
[[ "$head" == "$expected" && "$branch" == "feat/first-copy-phase-c-prepare" && -z "$(git status --porcelain)" ]] || {
  echo 'PRECHECK FAIL: branch/HEAD/clean state mismatch';exit 2;
}
logdir="${OPENSAVE_FIRST_COPY_LOG_DIR:-${XDG_STATE_HOME:-${HOME:-/tmp}/.local/state}/opensave/phase-c-prepare}"
mkdir -p "$logdir" || exit 1
summary="$logdir/phase-c-summary.log"
: > "$summary"
fail=0
run(){
  name="$1";shift
  log="$logdir/phase-c-$name.log"
  echo "BEGIN $name: $*" | tee -a "$summary"
  "$@" > "$log" 2>&1
  rc=$?
  echo "END $name: rc=$rc log=$log" | tee -a "$summary"
  (( rc == 0 )) || fail=1
}
run focused env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 12m ./internal/store/... ./internal/p2p/... ./internal/daemon/... ./internal/api/... ./internal/snapshot/...
run handover-e2e env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 10m -run 'TestFirstCopy_PhaseCPrepareOnly' ./e2e/
run firstcopy-e2e env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 12m -run TestFirstCopy_ ./e2e/
run race env GOTOOLCHAIN=go1.26.4 go test -race -count=1 -timeout 15m -run 'FirstCopyHandover|FirstCopy_PhaseC' ./internal/store/... ./e2e/...
run vet env GOTOOLCHAIN=go1.26.4 go vet ./internal/store/... ./internal/p2p/... ./internal/daemon/... ./internal/api/... ./e2e/...
run all-backend-e2e env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 30m . ./cmd/opensave-cli ./cmd/opensave-relay ./internal/... ./relay/... ./testutil/... ./e2e/...
run diff-check git diff --check
if [[ "$fail" -eq 0 ]];then echo 'PHASE C FULL GATE PASS' | tee -a "$summary";else echo 'PHASE C FULL GATE FAIL' | tee -a "$summary";fi
exit "$fail"
