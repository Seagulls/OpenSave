#!/usr/bin/env bash
# Reproducible software gate for the stacked first-copy branches.
# Usage: scripts/validate-first-copy-v2.sh <expected-sha> [phase-a|phase-b]

set -u

expected_sha="${1:-}"
phase="${2:-phase-a}"
log_dir="${OPENSAVE_FIRST_COPY_LOG_DIR:-${XDG_STATE_HOME:-${HOME:-/tmp}/.local/state}/opensave/first-copy-v2}"

if [[ -z "${expected_sha}" ]]; then
  echo "usage: $0 <expected-sha> [phase-a|phase-b]"
  exit 2
fi
if [[ "${phase}" != "phase-a" && "${phase}" != "phase-b" ]]; then
  echo "phase must be phase-a or phase-b"
  exit 2
fi

repo_root="$(git rev-parse --show-toplevel 2>/dev/null)"
head_sha="$(git rev-parse HEAD 2>/dev/null)"
branch="$(git branch --show-current 2>/dev/null)"
status="$(git status --porcelain=v1 2>/dev/null)"

echo "repo=${repo_root}"
echo "branch=${branch}"
echo "head=${head_sha}"
echo "expected=${expected_sha}"
echo "phase=${phase}"
echo "go=$(GOTOOLCHAIN=go1.26.4 go version 2>&1)"

preflight_rc=0
if [[ "${head_sha}" != "${expected_sha}" ]]; then
  echo "PRECHECK FAIL: HEAD does not match expected SHA"
  preflight_rc=1
fi
if [[ -n "${status}" ]]; then
  echo "PRECHECK FAIL: checkout is dirty"
  printf '%s\n' "${status}"
  preflight_rc=1
fi
case "${branch}" in
  feat/first-copy-core-v2|feat/first-copy-receipts-v1) ;;
  *)
    echo "PRECHECK FAIL: unexpected branch ${branch}"
    preflight_rc=1
    ;;
esac
if [[ "${preflight_rc}" -ne 0 ]]; then
  echo "preflight_rc=${preflight_rc}"
  exit "${preflight_rc}"
fi

mkdir -p "${log_dir}"
summary="${log_dir}/${phase}-summary.log"
: >"${summary}"

run_logged() {
  name="$1"
  shift
  log="${log_dir}/${phase}-${name}.log"
  echo "BEGIN ${name}: $*" | tee -a "${summary}"
  "$@" >"${log}" 2>&1
  rc=$?
  echo "END ${name}: rc=${rc} log=${log}" | tee -a "${summary}"
  return "${rc}"
}

overall=0
run_logged focused env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 10m ./internal/snapshot ./internal/store ./internal/p2p/... ./internal/daemon ./internal/api || overall=1
run_logged first-copy-e2e env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 8m -run TestFirstCopy_ ./e2e/ || overall=1
run_logged race env GOTOOLCHAIN=go1.26.4 go test -race -count=1 -timeout 12m -run 'FirstCopy|Generation|Receipt' ./internal/snapshot ./internal/store ./internal/p2p/... || overall=1
run_logged vet env GOTOOLCHAIN=go1.26.4 go vet ./internal/snapshot ./internal/store ./internal/p2p/... ./internal/daemon ./internal/api ./e2e/... || overall=1

# A source-only checkout intentionally has no generated frontend/dist tree.
# Record that desktop exclusion explicitly, then run every backend and E2E
# package without spending this protocol gate rebuilding an unrelated UI.
if run_logged desktop-probe env GOTOOLCHAIN=go1.26.4 go test -count=1 ./cmd/opensave-app; then
  :
elif grep -q 'pattern all:frontend/dist: no matching files found' "${log_dir}/${phase}-desktop-probe.log"; then
  echo "desktop-probe: excluded (clean checkout has no frontend/dist)" | tee -a "${summary}"
else
  overall=1
fi
run_logged full-backend-e2e env GOTOOLCHAIN=go1.26.4 go test -count=1 -timeout 30m . ./cmd/opensave-cli ./cmd/opensave-relay ./internal/... ./relay/... ./testutil/... ./e2e/... || overall=1
run_logged diff-check git diff --check || overall=1

echo "overall_rc=${overall}" | tee -a "${summary}"
echo "summary=${summary}"
exit "${overall}"
