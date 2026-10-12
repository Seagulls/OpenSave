# First-copy request-path performance and hardening

This follow-up is stacked on the guarded pair-only finalization draft. It does not change the first-copy protocol, transfer direction, the signed handover state machine, native AutoSync, snapshot ownership, or any permission-expanding endpoint.

## Ordinary peer request hot path

`Store.PeerTransferAccess(gameID)` obtains provisioning status (including a one-step alias lookup), the active first-copy direction lease, and the independent Phase C guard **in one SQLite statement**. The statement is fresh per request, not a long-lived permission cache. Existing store-level transaction rechecks, pinned snapshot validation, LAN/WAN authentication, and SQL protection triggers remain intact.

The central P2P handler and the native sync engine consume the same request-scoped state. Unrelated games have no lease or guard and continue their ordinary native synchronization. Invalid/unreadable guard or database state fails closed. For reference, the original normal path separately performed a pair-guard query, first-copy query, hold query and sometimes an alias query; the new single statement is intentionally benchmarked against that exact legacy helper path. Benchmarks are not a substitute for latency/throughput measurements on real devices.

Do not cache an answer of `not held` across requests or skip revalidation before permission-changing operations.

## Status polling

`GET /api/games/{id}/first-copy/handover/finalize?verifyContent=0` returns the durable journal **without hashing the live save files**. It reports `contentChecked=false` and leaves `contentValid=false`, meaning *unknown*, not *invalid* or *approved*. `fenceActive`, `openAuthorized=false`, `autoSync=false` and the durable recovery state remain available. No status call authorizes Open.

The default `GET` and `?verifyContent=1` retain the previous live content preflight and report `contentChecked=true`, `contentValid=true|false`. Operators and controllers must freshly verify content before every action that changes admission or release semantics. The protocol's existing handover preflights still hash the save at those boundaries.

## Snapshot blocks

Pinned source generation archives are certified with a complete manifest at creation and again on first serve/restart. For an individual block request, every **returned** block is checked against that certified manifest. If the request only needs an early block, the decoder need not also decompress the rest of the file. The ZIP index, duplicate entry and requested block checks remain. This is a bounded optimization, not random ZIP access; late blocks still require sequential decompression of their predecessors.

This shortcut assumes the existing full-certification and unchanged-archive checks in `sourceGeneration`; it does not change native ZIP storage or add a second transfer engine.

## Adversarial checks

The Phase C.3 three-daemon LAN/relay suite now directly requests a signed manifest, immutable blocks and a delete through the *third* paired peer's native transport, in addition to asking that peer to sync. All must refuse and no file hashes or game content may change. Invalid durable guards, expired leases, activated leases and ordinary unheld games are tested independently.

## Validation and limitations

`bash scripts/validate-first-copy-performance.sh <exact-commit>` runs focused store/snapshot tests, all first-copy E2E, race, vet, the full backend/E2E suite, an ordinary-path same-machine microbenchmark and a diff check. The frontend app requires generated assets and is excluded from the source-only gate. Save data, actual devices, backward-version behaviour and performance on Deck hardware remain untested in this development tranche.

A one-query access snapshot cannot guarantee cross-process atomicity with filesystem reads. The existing request-specific read/write settlement gates, the store's permission-change transaction checks, and the pinned immutable source generation remain necessary and are not removed. Further cross-version and physical-device tests are required before upstream acceptance.
