# Phase C.3 — guarded pair-only finalization (source of truth)

**Scope:** stacked on signed two-device Phase C.2 decisions. This is a *safe
blocking-and-recovery* milestone, not unrestricted bidirectional syncing.
No synthetic acceptance result authorizes real production saves or automation.

## The safety invariant

Both operators separately authorize a C.2 commit, with a corresponding signed
counterpart decision. C.3 installs a durable `game_first_copy_pair_guards` row
**before** any finalization message is sent. The guard independently restricts
readers to the named source->target transfer and refuses every write/deletion,
peer snapshot and third-peer read. The original activated `game_first_copies`
row and pinned source snapshot are retained as a second safety layer. Even when
both pair-finalization journals report success, neither device gets normal
AutoSync nor global peer admission. The original Phase B `Open` stays refused.

The pair is **finalized at the control-plane level**, not yet converged in the
native ordinary bidirectional sync lineage. Later peer admission and AutoSync
are separate, explicit, independently reviewed protocol milestones.

## Local-only operator API

| Request | Effect |
| --- | --- |
| `POST /api/games/{id}/first-copy/handover/finalize` with `{"txId":"<local>"}` | Explicitly install durable guard bound to the committed C.2 decision; refuse if content changed, paused, active sync or AutoSync enabled. Must run independently on both devices. |
| `POST /api/games/{id}/first-copy/handover/finalize/send` with `{"txId":"<local>","stage":"offer"}` | Source persistently records `offered`, then signs and sends its finalization offer. |
| Same request with `stage=ready` | Target, after receiving offer and explicit local guard, persists and sends `ready`. |
| Same with `stage=commit` | Source first persists `pair_finalized`, **retains both guards**, and sends the commit. |
| Same with `stage=ack` | Target, after persisting `pair_finalized`, sends signed confirmation. |
| `GET /api/games/{id}/first-copy/handover/finalize` | Read-only local journal and content validity; never implies safe unrestricted Open. |
| `DELETE /api/games/{id}/first-copy/handover/finalize` with `txId` | Locally cancel and persist a veto **only before pair finalization**. Once cancelled, use `stage=abort` to notify counterpart; the guard remains. |

These calls do not change native sync, watcher, provisioning holds, snapshots,
file contents or AutoSync. They do not create a second save transfer engine.

## Signed peer protocol and recovery

`POST /api/p2p/first-copy/handover/finalize/{gameId}` (LAN), or signed WAN
`/first-copy/handover/finalize/{gameId}`, carries version `1`, game, both local
transaction IDs, source generation ID, branch, digest, random source decision
ID, complementary peer role and a stage `offer|ready|commit|ack|abort`.
Transport supplies the authenticated peer and nonce; neither comes from JSON.

| Local state | Signed input / outgoing stage | Next state |
| --- | --- | --- |
| Source `intent` | Send `offer` | `offered` |
| Target `intent` | Receive signed `offer` | `offered` |
| Target `offered` | Send `ready` | `ready` |
| Source `offered` | Receive signed `ready` | `ready` |
| Source `ready` | Send `commit` | `pair_finalized` (guard stays) |
| Target `ready` | Receive signed `commit` | `pair_finalized` (guard stays) |
| Target `pair_finalized` | Send `ack` | unchanged |
| Source `pair_finalized` | Receive signed `ack` | `peerAck=true` |
| Either precommit state | Local cancel + signed `abort` | `cancelled` and durable veto; all access further restricted |

A lost acknowledgement is retried with the **same decision identity** and a
new pairing-authenticated nonce. The source may be `pair_finalized` with
`peerAck=false` while the target is offline; both remain fenced, no timeout
releases either device. Restart preserves the signed replay nonce ledger and
cancelled-decision tombstones. A source finalization cannot be cancelled
unilaterally once `pair_finalized`; human recovery is required.

A signed cancellation arriving after an already persisted commit cannot roll
it back. If one device commits while the other vetoes, **block and surface a
recovery-required condition**; do not fabricate a global abort or permission.

## Durable access enforcement

- **SQLite migration `0047`:** new guard, nonce/veto history and triggers for
  deleting the source first-copy row, game or referenced pinned snapshot.
  Guard persists independently; trigger refuses deletion of original row.
- **P2P `firstCopyServe`:** consult guard before original lease. In an intact
  guard, only signed named target may read source (immutable generation
  checked by existing generation serve code). Third peers and deletes refused.
- **Syncengine entry:** block ALL ordinary sync while a C.3 guard exists,
  including manual, reconnect and queued attempts; unrelated games unchanged.
- **Phase B Open:** still refused by the C.2 barrier, with direct pair-guard
  veto as defence in depth. No admission if guard cannot be read.
- **Lifecycle:** retaining the activated original row preserves existing game
  identity/root/alias fences. SQLite triggers independently protect it.

**Downgrades:** retained first-copy row provides a second barrier to known
Phase C.1/C.2 implementations. Daemons older than the first-copy protocol
cannot be assumed to enforce it. SQLite accepting a newer schema is NOT
proof of downgrade safety. There is no automatic mixed-version admission;
physical acceptance requires an independently verified anti-downgrade policy.

**Consistency:** the native game files may change after a digest check; the
finalization sender and receiver rehash before each signed transition. The
SQLite decision and filesystem are not one atomic transaction. Any detected
mutation blocks transitions, while the durable guard remains. There is no
claim of atomic distributed commit, source filesystem freezing, or all-peer
convergence.

## Acceptance gates

1. Focused SQLite fixture tests for committed-decision prerequisite, correct
   complementary role, incorrect digest/peer, replay, cancel tombstone,
   restart survival and ledger-vs-lease constraints.
2. Deterministic transaction-scoped recheck of C.2 prepare/receipt evidence
   after local cancellation, as well as signed C.2 retransmission recovery.
3. Synthetic LAN and relay/WAN source+target+third-peer full protocol; check
   hashes after each stage, lost Ack, restart before Ack, idempotent retries,
   legacy Open denial and AutoSync off throughout.
4. One exact-head full backend/E2E, race, vet, and diff gate; desktop excluded
   if generated `frontend/dist` is absent.
5. **Separate future hardware acceptance** only after controlled test payloads,
   downgrade and mixed-version hazards, and operator-controlled failure
   recovery are explicitly accepted. Never touch existing saves by default.

## Explicitly deferred

No general third-peer admission, automatic/normal bidirectional convergence,
final removal of original first-copy row/pin, automatic AutoSync, automatic
rollback of one-sided commit, or promise of safe operation with unpatched
stock/legacy daemons. These require a separate permission-expanding review.
