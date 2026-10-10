# feat: opt-in one-way first copy for one held game

Depends on #43.

## Summary

A provisioning hold stops every transfer. Releasing it, even with AutoSync left off, does not stop another unheld peer from requesting the save. This adds an optional lease so one named paired peer can read a held source, or this device can pull only from one named peer, without accepting a reverse write.

Ordinary tracking, offers, holds, and release are unchanged when the new routes are not called.

## API

```http
POST /api/games/{id}/first-copy
Content-Type: application/json

{"role": "source", "peerId": "<target-peer-id>"}
```

```http
POST /api/games/{id}/first-copy
Content-Type: application/json

{"role": "target", "peerId": "<source-peer-id>"}
```

The game must already be held. The same role and peer returns the same transaction. A different peer is rejected. The hold stays.

The target then calls the existing `POST /api/games/{id}/sync`. That sync contacts only the named source and does not ask the source to delete or to pull. The source serves manifest and blocks only to that signed peer. Deletes, triggers, snapshot download, and unsigned reads are refused.

```http
POST /api/games/{id}/first-copy/finish
Content-Type: application/json

{"txId": "<id>", "expectHash": "<source-digest>"}
```

Finish checks the digest and marks the lease verified. It does not release the hold and does not turn AutoSync on. A third peer is still refused. Repeat finish with the same transaction is safe.

```http
POST /api/games/{id}/first-copy/activate
Content-Type: application/json

{"txId": "<id>"}
```

Activate releases the hold on this device only and keeps the named-peer fence. A third paired device still cannot read. `autoSync` defaults to false. `POST /api/games/{id}/first-copy/open` removes the fence. That is the step that allows other paired peers to read. A count of paired devices is not the rule. An expired verified lease cannot activate. `DELETE` aborts and leaves the hold, including an expired row.

`GET /api/capabilities` and `GET /api/p2p/capabilities` return `{"firstCopy":"1"}`. A missing route or a body without that field is unsupported. Keep the game held.

CLI: `opensave game <id> first-copy --as source|target --peer <peerId>`.

## Safety

- Source bytes are not pulled or deleted by the named target or by a third peer.
- A third peer does not receive the save.
- Other games keep syncing. No global pause and no daemon restart is required.
- Restart keeps the hold and the lease.
- A failed lease read is not permission to sync or serve.
- An older peer cannot make a patched source serve or accept a write. An older source cannot enforce a lease; the new target stays held and the sync fails closed. Do not release the hold to work around an old peer.

## Non-goals

No second sync engine, no ordinary peer-targeted sync, no version vectors, and no automatic bidirectional AutoSync.

Migration `0039_game_first_copies.sql` is the next number after the hold table in #43. Draft #30 also uses `0039` for a different design. Renumber if both land.
