#!/usr/bin/env bash
# Exact-head, source-only Phase C.2 software gate. No hardware or deployment.
set -u
expected="${1:-}"
[[ -n "$expected" ]] || { echo 'Usage: validate-first-copy-phase-c2.sh SHA'; exit 2; }
head="$(git rev-parse HEAD 2>/dev/null)"
branch="$(git branch --show-current 2>/dev/null)"
[[ "$head" == "$expected" && "$branch" == 'feat/first-copy-phase-c-decision' && -z "$(git status --porcelain)" ]] || {
  echo 'PRECHECK FAIL: branch/HEAD/clean tree mismatch'; exit 2;
}
LOGDIR="${OPENSAVE_FIRST_COPY_LOG_DIR:-${XDG_STATE_HOME:-${HOME:-/tmp}/.local/state}/opensave/phase-c2}"
mkdir -p "$LOGDIR" || exit 1
summary="$LOGDIR/phase-c2-summary.log"
: > "$summary"
fail=0
run() {
  local name="$1" rc; shift
  echo "BEGIN $name: $*" | tee -a "$summary"
  "$@" > "$LOGDIR/phase-c2-$name.log" 2>&1
  rc=$?
  echo "END $name: rc=$rc log=$LOGDIR/phase-c2-$name.log" | tee -a "$summary"
  if ((rc != 0)); then fail=1; fi
}
run store-decision env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 8m -run 'TestFirstCopyDecision_' ./internal/store/
run decision-e2e env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 12m -run 'TestFirstCopy_PhaseCDecisionWithoutOpen' ./e2e/
run firstcopy-e2e env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 15m -run 'TestFirstCopy_' ./e2e/
run race env GOTOOLCHAIN=go1.26.4 go test -race -count=1 -timeout 20m -run 'FirstCopyDecision|FirstCopy_PhaseC' ./internal/store/... ./e2e/...
run vet env GOTOOLCHAIN=go1.26.4 go vet ./internal/store/... ./internal/p2p/... ./internal/api/... ./internal/daemon/... ./e2e/...
run backend-e2e env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 30m . ./cmd/opensave-cli ./cmd/opensave-relay ./internal/... ./relay/... ./testutil/... ./e2e/...
run diff git diff --check
if ((fail==0)); then echo 'PHASE C.2 FULL GATE PASS' | tee -a "$summary"; else echo 'PHASE C.2 FULL GATE FAIL' | tee -a "$summary"; fi
exit "$fail"
