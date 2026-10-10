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

The game stays held until `POST /first-copy/finish`. Finish drops the lease
and releases the hold. `autoSync` defaults to false.

A crashed process keeps the row. Restart does not watch a held game and does
not treat a failed lease read as permission to sync.

An older peer that does not understand the lease still gets a refusal from a
patched source, because the refusal is on the server. An older source cannot
enforce a lease a new target expects. The target's sync then fails closed on
the existing hold refusal. Do not release the hold to compensate.

## Not in this change

No second sync engine, no peer-targeted ordinary sync, no version vectors,
and no automatic enablement of bidirectional AutoSync.
