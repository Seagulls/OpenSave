# Opt-in directed first copy

This is optional. No row in `game_first_copies` means the feature is off.
Ordinary tracking, offers, holds, release, and sync are unchanged.

## Why a hold is not enough

`release-provisioning` with `{"autoSync": false}` stops this device from
starting a sync. It does not stop another unheld peer from requesting the
manifest, blocks, or a delete. A directional copy has to be enforced on the
serve path as well as the local sync path.

## Chosen minimum

One durable lease per game:

- `role=source`, `peerId=<target>`: that signed peer may read the manifest
  and blocks. Deletes, sync triggers, snapshot download, and a local sync
  are refused. Unsigned requests are refused.
- `role=target`, `peerId=<source>`: a local sync may contact only that peer,
  and only to pull. This device does not serve the game, and it does not ask
  the source to delete or to pull.

The game stays held through `POST /first-copy/finish`. Finish records
verification but does not release the hold. `POST /first-copy/activate`
atomically releases only this device's hold and retains a durable named-peer
fence. Activated mode intentionally does not synchronize; AutoSync must remain
off until a separate, verified `open` operation. This applies only to games
that explicitly entered first copy.

A source Begin captures one immutable snapshot generation before publishing
the lease. Capture is fail-closed: every configured root and file must be
readable, unsafe filesystem entries are rejected, the completed ZIP is read
back in full, and full content manifests taken before and after capture must
match the archive. Retry and restart renew the same transaction and generation;
they never replace it with newer live content.

The manifest advertises the complete primary-plus-named-root archive layout.
Every LAN and WAN block request repeats the generation protocol, transaction,
snapshot ID, branch and digest; the source rejects any mismatch and never
falls back to live files. Large entries are streamed in bounded blocks rather
than loaded whole. The first request after restart revalidates the archive in
full; later requests reuse that certification only while file metadata is
unchanged and verify each returned block against it.

A crashed process keeps the row. Restart does not watch a held game and does
not treat a failed lease read as permission to sync.

An older peer that does not understand the lease still gets a refusal from a
patched source, because the refusal is on the server. An older source cannot
enforce a lease a new target expects. The target's sync then fails closed on
the existing hold refusal. Do not release the hold to compensate.

## Transaction states and safe handover

- `copying -> verified`: `Finish` verifies the per-game/all-roots digest. A
  repeated Finish with the identical digest may succeed only in `verified`.
- `verified -> activated`: `Activate` revalidates content and removes this
  device's hold, preserving the named-peer fence; `autoSync=true` is rejected
  rather than promising synchronization that the fence currently suppresses.
- `activated -> open`: explicit `POST /api/games/{id}/first-copy/open` with
  `{"txId":"...","expectHash":"..."}` revalidates the local digest and
  removes the fence, but NEVER turns AutoSync on by itself.
- Abort works only in `copying` or `verified` while the provisioning hold
  still exists. Lease deletion and generation unpin are one transaction. It
  cannot delete an activated fence. Finish cannot demote
  `activated` back to `verified`. Original lease expiry cannot open access.

`Open` is a PERMISSION EXPANSION: a signed paired third peer can read once the
fence has been removed. **Local digest comparison is not remote attestation.**
The caller must separately prove both devices are activated,
with matching current full-root digests and intended peer identities, and
obtain explicit permission before opening either side. Phase A did not implement authenticated completion receipts. The stacked
Phase B prototype adds paired-key authenticated two-way activated receipts,
bound to both local transactions, the pinned generation, the branch and the
full-root digest. Both peers retain their fences; signed receipts DO NOT
release holds, automatically Open, or enable AutoSync. A local Open now
requires a fresh matching authenticated peer receipt, but distributed Open
is still not atomic and must be separately orchestrated and authorized.
The combined protocol is NOT approved for hardware testing. Do not automatically
invoke `Open` from an activation retry or timeout.

A count of paired devices is not an authorization rule. A target with
pre-existing divergent files is refused before first-copy writes. An
expired nonactivated lease is never a permission and cannot be stolen by a
different peer. No ordinary game is affected when first copy was not opted in.

Snapshot rollback, single-file restore, branch switch, backup overwrite, and accepting a cloud offer are refused while a first-copy row exists. They proceed normally when no row exists. A refused peer untrack does not write a tombstone or clear lineage.

Game operations are restricted while a first-copy row exists, including the
activated phase after the native provisioning hold is released: untrack,
peer-originated untrack/retrack and relink/unlink are refused before metadata
mutation. The store's DeleteGame additionally has an atomic SQL fence against
ON DELETE CASCADE removing the first-copy row. App-ID auto-matching calls
AddGameAlias directly, so AddGameAlias and RemoveGameAlias also use atomic
SQLite guards (including bounded alias chains). These restrictions are scoped
only to explicitly opted-in games; normal unfenced game lifecycle is unchanged.
An activated game also refuses settings edits that turn on AutoSync, switch
branches or change the tracked save layout; store-level add/note/remove root
operations refuse the activated state. Provisioning root configuration before
activation and cosmetic metadata edits remain allowed.

The generation snapshot is a protocol dependency, not merely a user pin.
Database triggers prevent manual deletion or unpinning while any durable
first-copy row references it. Retention, age pruning and branch cleanup
therefore cannot remove a live or recoverable generation.

## Capture consistency boundary

Portable filesystem APIs cannot atomically freeze several independent save
roots. This implementation does not claim that they can. It detects changes by
comparing complete, uncached content manifests before capture, from the
archive, and after capture; each file also has stable size and modification
metadata across its archive read. A mutation that produces a different
pre/archive/post view rejects Begin without publishing a lease.

The residual limitation is an adversarial or exceptionally timed mutation
that changes bytes and restores the exact original bytes and metadata between
the independent observations. Filesystem-native snapshots or a game-specific
quiesce mechanism would be required to eliminate that limitation.

Per-device sync-ignore rules are not accepted on a first-copy target in this
revision because asymmetric exclusions cannot prove identical endpoint
content. Dot entries and interrupted-patch temporary files remain in the
backup archive but are excluded from the transferable manifest exactly as in
ordinary sync.

## Not in this change

No second sync engine, no peer-targeted ordinary sync, no version vectors,
no automatic distributed Open and no automatic enablement of bidirectional
AutoSync. Phase B adds an authenticated remote receipt but not distributed
commit or remote filesystem measurement.

## Stacked Phase C.1 preparation (no automatic Open)

The next opt-in phase records separate durable local operator intent and a
pairing-key signed remote prepare, bound to Phase B receipts and the same
source generation and transaction pair. Preparation expires for readiness
purposes, rejects authenticated replay after reboot, and never removes a
first-copy fence or enables AutoSync. See
`docs/engineering/first-copy-phase-c-preparation.md` for the operator contract,
fault boundary and deferred distributed commit semantics.
