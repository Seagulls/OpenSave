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
  still exists. It cannot delete an activated fence. Finish cannot demote
  `activated` back to `verified`. Original lease expiry cannot open access.

`Open` is a PERMISSION EXPANSION: a signed paired third peer can read once the
fence has been removed. **Local digest comparison is not remote attestation.**
The caller (e.g. SaveSync) MUST separately prove both devices are activated,
with matching current full-root digests and intended peer identities, and
obtain explicit permission before opening either side. This PR does not
implement distributed handover, source-frozen transfers or mixed-version WAN
acceptance. It is NOT approved for hardware testing. Do not automatically
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

## Not in this change

No second sync engine, no peer-targeted ordinary sync, no version vectors,
and no automatic enablement of bidirectional AutoSync.
