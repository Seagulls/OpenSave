# First-copy test ledger

Branch: `grok/opt-in-scoped-directed-first-copy`
Base: `2fee76ffe91e9387d6ee717727a6b85779ddf0fd` (PR #43 tip)
Go: `go1.26.4 linux/amd64` via `GOTOOLCHAIN=go1.26.4`
These are isolated daemon tests. No hardware was contacted.

| Command | RC | Notes |
| --- | --- | --- |
| `go test -count=1 -timeout 180s -run TestFirstCopy_ ./e2e/` | 0 | Real paired daemons. Named target received `SOURCE-BYTES` with matching hash. Source did not gain target or third-peer files. Unsigned manifest and delete refused. Third peer after staged release did not exchange bytes. Unrelated game synced. Restart kept hold and lease. Finish left AutoSync off. |
| `go test -count=1 -timeout 180s -run 'TestFirstCopy_\|TestProvisioningHold_StagedReleaseAllowsPeerInitiatedSync\|TestProvisioningHold_OrdinaryTrackUnchanged' ./e2e/` | 0 | Staged release without a lease still allows a peer-initiated read. That is the gap this lease closes. Ordinary track unchanged. |
| `go test -count=1 -timeout 8m ./internal/p2p/... ./internal/api/... ./internal/store/...` | 0 | |
| `go vet ./internal/p2p/... ./internal/api/... ./internal/store/...` | 0 | |
| `git diff --check` | 0 | |

| `go test -count=1 -timeout 25m ./e2e/...` | 0 | 756.682s. Includes the existing provisioning suite and `TestFirstCopy_OnlyNamedTargetReceivesAndSourceIsNotWritten`. |
| `go test -race -count=1 -timeout 15m ./internal/p2p/... ./internal/store/...` | 0 | |

`TestSessionNamesTheSnapshotAlreadyTaken` remains a pre-existing stock failure and was not changed. `cmd/opensave-app` still needs `frontend/dist`. Full `go test ./...` was not claimed.
