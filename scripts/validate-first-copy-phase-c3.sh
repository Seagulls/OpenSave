#!/usr/bin/env bash
# One exact-head software gate. No deployment or production save payloads.
set -u
expected="${1:-}"
branch="$(git branch --show-current 2>/dev/null)"
head="$(git rev-parse HEAD 2>/dev/null)"
[[ -n "$expected" && "$head" == "$expected" && "$branch" == "feat/first-copy-phase-c-finalize" && -z "$(git status --porcelain)" ]] || {
  echo "PRECHECK FAIL: exact head/branch/clean state mismatch";exit 2
}
logs="${OPENSAVE_FIRST_COPY_LOG_DIR:-${XDG_STATE_HOME:-${HOME:-/tmp}/.local/state}/opensave/phase-c3}"
mkdir -p "$logs" || exit 1
summary="$logs/phase-c3-summary.log"
: > "$summary"
fail=0
run(){
  name="$1";shift
  log="$logs/phase-c3-$name.log"
  echo "BEGIN $name: $*" | tee -a "$summary"
  "$@" > "$log" 2>&1
  rc=$?
  echo "END $name: rc=$rc log=$log" | tee -a "$summary"
  (( rc==0 )) || fail=1
}
run focused env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 12m -run 'FirstCopyPairFinalize|FirstCopyDecision_' ./internal/store/
run finalize-e2e env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 15m -run TestFirstCopy_PhaseC3GuardedFinalization ./e2e/
run all-firstcopy env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 15m -run TestFirstCopy_ ./e2e/
run race env GOTOOLCHAIN=go1.26.4 go test -race -count=1 -timeout 18m -run 'FirstCopyPairFinalize|FirstCopyDecision_|PhaseC3Guarded' ./internal/store/ ./e2e/
run vet env GOTOOLCHAIN=go1.26.4 go vet ./internal/store/... ./internal/p2p/... ./internal/daemon/... ./internal/api/... ./e2e/...
run full-backend-e2e env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 30m . ./cmd/opensave-cli ./cmd/opensave-relay ./internal/... ./relay/... ./testutil/... ./e2e/...
run diff-check git diff --check
if ((fail==0));then echo 'PHASE C.3 FULL GATE PASS' | tee -a "$summary";else echo 'PHASE C.3 FULL GATE FAIL' | tee -a "$summary";fi
exit "$fail"
