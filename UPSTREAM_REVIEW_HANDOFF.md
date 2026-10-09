# OpenSave upstream review handoff

Updated after the staged-release and mixed-version tranche. This file stays
on `review/upstream-handoff` only. It is not part of either code branch.

No Steam Deck, Bazzite, Raspberry Pi, installed daemon, or real save was
contacted. No upstream PR was opened. `Seagulls/Savesync` was not modified.
No force-push.

Toolchain for every command below: `go version go1.27.1-X:nodwarf5 linux/amd64`.
`go.mod` requests `go 1.26.4`. `GOTOOLCHAIN=auto` stayed on 1.27.1. Go 1.26.4
was not downloaded. Linux only. No Windows or macOS run.

## 1. Branch tips

| Branch | Base | Tip | Pushed |
| --- | --- | --- | --- |
| `feat/atomic-safe-game-provisioning` | `346d9bda0749fb57b48fd266f13e535af80f0285` | `af05e652f050404f8dcdb99f93a2e49ab9bb9891` | `Seagulls/OpenSave` only |
| `fix/agreed-base-precedes-mtime` | same base | `8d893ff8bd12acf7f53de3f84783d7fe9849dbff` | `Seagulls/OpenSave` only |
| `review/upstream-handoff` | docs only | this commit | docs only |

Previous reviewed tips were `1508845` and `feedc3b`. Both are ancestors of the
tips above. Ancestry from `346d9bda` was checked before editing.

Compare:

- https://github.com/Liquid-co/OpenSave/compare/main...Seagulls:feat/atomic-safe-game-provisioning
- https://github.com/Liquid-co/OpenSave/compare/main...Seagulls:fix/agreed-base-precedes-mtime

Provisioning commits after stock:

1. `4b50182` store: persist a per-game provisioning hold
2. `e840f37` sync: keep a held game out of every transfer path
3. `3b760e2` test: prove a held game survives restart and one-sided release
4. `1508845` sync: fail closed when a provisioning hold cannot be read
5. `af05e65` api: stage a hold release without turning AutoSync on

Mtime commits after stock:

1. `feedc3b` sync: let an agreed base beat a misleading mtime
2. `8d893ff` test: cover the other agreed-base direction and a deletion

Throwaway combined tree, **not pushed**:

- path: `/tmp/kilo/opensave-combined2`
- local SHA: `d7ef7c67490bdf6397a5d9f626d045de90c9cfcf`
- parents: `af05e65` and `8d893ff`
- only conflict: `CHANGELOG.md`, both notes kept

## 2. Requirement matrix

| Item | Status |
| --- | --- |
| Create/place a game that cannot sync until released | Supported. `provisioningHold: true` or `autoSync: false` on create/place. Omitted flags are unchanged. |
| `autoSync: false` at creation vs ordinary AutoSync | Deliberately the hold, not a weaker flag. A weaker flag does not stop a peer pulling the manifest. |
| Later enable via ordinary PATCH | Unsupported on purpose. `PATCH /api/games/{id}` cannot clear the hold, and a held game's `autoSync` column is not changed. Tested. |
| Intentional unblock | Supported. `POST /api/games/{id}/release-provisioning`. Empty body enables AutoSync. `{"autoSync": false}` or CLI `--no-autosync` does not. Repeat after the hold is gone does not change AutoSync. |
| Default release means "no transfer until explicit sync" | **Not true.** Witness: `TestProvisioningHold_DefaultReleaseLetsReconcileSync`. Reconcile/`SyncAllGames` copies bytes after a default release. |
| Staged release | Supported and tested. Hold cleared, AutoSync left off, reconcile/restart/file write do not copy. Explicit `POST /sync` does. A still-held third peer refuses. Both writer orders. |
| Peer-targeted `SyncGame(game, peer)` | Unsupported. Explicit sync still contacts every online peer. Unheld peers participate. Held peers refuse. Do not add this in this PR. |
| Same canonical id on the Bridge without an offer | Supported as a local API (`id` only while held). Not automatic. SaveSync must call it. |
| Convert an already tracked game into a hold | Unsupported. Fails, leaves AutoSync and bytes alone. |
| Mixed stock client to patched held server | Supported for the cases run. Manifest 409, delete 409, stock sync status `error`, files unchanged. |
| Stock daemon on a patched database | **Unsafe.** Stock has no hold field and serves the held manifest (HTTP 200, file hash included). |
| Agreed-base beats misleading mtime | Supported when one whole manifest equals the confirmed base. Both directions, primary and named root. |
| Missing or stale agreed base | Deliberately still mtime. Not #29/#30. |
| Hardware | Untested. |

## 3. Safety entry points

Provisioning hold, fail closed on a read error:

- `internal/store/provisioning.go` `ProvisioningBlocks`, `ProvisioningHeldSet`
- `internal/p2p/engine.go` `refuseIfProvisioning`, `provisioningServeRefusal`, `SyncAllGames` returns if the set cannot be read
- `internal/p2p/syncengine/engine.go` `SyncGame` returns `ErrProvisioningUnreadable` before `FetchManifest`
- `internal/p2p/routes.go` and `wanclient_handlers.go` manifest, blocks, delete, snapshot
- `internal/daemon/daemon.go` `provisioningBlocks` for watch, cloud upload, untrack notify, link
- `internal/api/server.go` `provisioningHoldState`: failed read sets `provisioningHold=true` and `provisioningHoldUnknown=true`

Release:

- `internal/store/provisioning.go` `ReleaseProvisioningMode` — one transaction, no-op if not held
- `internal/daemon/daemon.go` `ReleaseProvisioningMode` — watches only when AutoSync is enabled; never calls `SyncGame`
- `internal/api/routes_provisioning.go` empty body vs `{"autoSync": false}`
- `internal/api/routes.go` `handleUpdateGame` restores `autoSync` while held
- `internal/cliapp/commands_games.go` `release --no-autosync`; `set auto-sync` errors while held

Agreed base:

- `internal/p2p/syncengine/decision.go` around the differing-hash switch: whole-manifest base is checked before `MtimeMs`
- `internal/p2p/syncengine/multiroot.go` `syncOneRoot` resolves `GetAgreedHashForRoot` and passes that filtered base to `ComputeWithBase`
- `TestANewerSideStillWinsRegardlessOfTheBase` is still present. Its expected outcome is the correction, not the old mtime win.

## 4. Red on stock, green on the mtime tip

On unmodified `346d9bda`:

```text
go test -count=1 -timeout 60s -run 'TestBaseFirstPreventsMtimeRollback' ./internal/p2p/syncengine/
RC=1
local changed, remote base has later mtime: FilesToPull:[slot.sav] want push
remote changed, local base has later mtime: FilesToPush:[slot.sav] want pull
```

On `8d893ff` the same test passes, as do both daemon directions. This does not
prove the Crash 4 hardware incident. It proves the source decision.

## 5. Commands and results

Provisioning tip `af05e65`:

```text
GOTOOLCHAIN=auto go test -count=1 -timeout 20m ./internal/p2p/... ./internal/store/... ./internal/daemon/... ./internal/api/... ./internal/delta/...
RC=0
GOTOOLCHAIN=auto go test -count=1 -timeout 25m ./e2e/...
RC=0   (755.886s)
GOTOOLCHAIN=auto go test -race -count=1 -timeout 25m -run 'TestProvisioning|TestUnreadable|TestStockClient|TestSyncAllGamesStops' ./e2e/ ./internal/store/ ./internal/p2p/ ./internal/p2p/syncengine/
RC=0
GOTOOLCHAIN=auto go test -count=3 -timeout 10m -run 'TestProvisioningHold_StagedRelease|TestProvisioningHold_DefaultRelease|TestUnreadableProvisioning' ./e2e/ ./internal/p2p/syncengine/
RC=0
GOTOOLCHAIN=auto go vet ./internal/p2p/... ./internal/store/... ./internal/daemon/... ./internal/api/... ./internal/delta/...
RC=0
```

Mtime tip `8d893ff`:

```text
GOTOOLCHAIN=auto go test -count=1 -timeout 20m ./internal/p2p/... ./internal/store/... ./internal/daemon/... ./internal/api/... ./internal/delta/...
RC=1
```

The only failure is `TestSessionNamesTheSnapshotAlreadyTaken` in
`internal/daemon`. It also failed on unmodified `346d9bda` in the previous
tranche. This branch does not touch `sessions.go`. Re-run on this tip also
failed. Not introduced here. Not fixed here.

```text
GOTOOLCHAIN=auto go test -count=1 -timeout 25m ./e2e/...
RC=0   (763.827s)
GOTOOLCHAIN=auto go test -race -count=1 -timeout 20m -run 'TestAgreedBase|TestBaseFirst|TestAnMtimeTie|TestANewerSide' ./e2e/ ./internal/p2p/syncengine/
RC=0
GOTOOLCHAIN=auto go test -count=3 -timeout 10m -run 'TestBaseFirstPreventsMtimeRollback|TestAgreedBase' ./internal/p2p/syncengine/ ./e2e/
RC=0
go vet (same packages) RC=0
```

`go test ./...` is still blocked for `cmd/opensave-app` by
`//go:embed all:frontend/dist`. `frontend/dist` is not in the clone. Not
stubbed. Not counted green.

Mixed-version log, two processes, isolated `HOME`, ports 18421/18422/18423:

`/home/guy/Documents/ai/Savesync-logs/savesync-grok-opensave-mixed-version.log`

```text
MANIFEST_STATUS=409 configuring message, not "not found"
DELETE_STATUS=409, patched file still ONLY-PATCHED
stock POST /sync results status=error, STOCK_FILE=ONLY-STOCK, PATCHED_FILE=ONLY-PATCHED
downgrade manifest HTTP 200 with slot.sav hash af49b721...
```

Binaries used for that probe, not install candidates:

```text
opensave-stock    cfe071e0c4c543e253e1bd9adf6f7e384f85c39e77561d4dec5f692af041f22c
opensave-patched  4c74af3d617cc856dd9af9d916e5b38cbd8d4fe980a055a7038903a98f6a8c23
```

Combined checkout CLI builds, not installed:

```text
linux/amd64 fc3c59465b85b93878138ec0f42c7cf46ed77e2d677201c79bef6bfd5c8c8525
linux/arm64 5ff686835a8608e481e6d8af0447f5547dd9890fca693c462d9fe01a52aad64a
```

## 6. Combined checkout

`/tmp/kilo/opensave-combined2` at `d7ef7c6`, not pushed.

```text
go test -count=1 -timeout 15m -run 'TestSaveSyncStar|TestProvisioningHold_|TestAgreedBase|TestBaseFirst' ./e2e/ ./internal/p2p/syncengine/
RC=0
go test -count=1 -timeout 8m -v -run '^TestSaveSyncStar' ./e2e
RC=0
```

Log: `/home/guy/Documents/ai/Savesync-logs/savesync-grok-opensave-three-node.log`

- `TestSaveSyncStarOfflineRoundTrip` PASS, files=4
- `TestSaveSyncStarDualWriterPreservesBoth` PASS both orders, status=conflict, unique files kept
- `TestSaveSyncStarIdleBridgeMtimeCannotRollback` PASS

Three-node files were copied into the throwaway `e2e/` only. SaveSync was not edited.

## 7. Draft PR text

Do not open these yet.

### Issue #40

Title: Hold a new game until it is explicitly released

```markdown
Fixes #40

`autoSync: false` after a normal track is not safe. A paired device can still
request the manifest, and pause does not survive `daemon.New`. Creation and
offer placement therefore record a provisioning hold in the same transaction
as the game when the caller passes `provisioningHold: true` or `autoSync: false`.
Omitted, behaviour is unchanged.

A PATCH cannot clear that hold. The unblock is
`POST /api/games/{id}/release-provisioning`.

- Empty body turns AutoSync on. Reconcile and a peer coming online can then
  sync that game with every online peer that is not still held. Release itself
  does not call SyncGame, but that is not "nothing moves until an explicit sync".
- `{"autoSync": false}` clears the hold and leaves AutoSync off. Reconcile,
  reconnect and the watcher do not sync. The caller syncs explicitly, then
  turns AutoSync on. An explicit sync still contacts every online peer.
  A peer that is still held refuses.

An explicit id is accepted only while creating a hold, so the other device
can be registered without an offer. This does not find or create that id on
another machine by itself. A failed hold read is not reported as an ordinary
false (`provisioningHoldUnknown`).

A stock build serving this database will serve the held save. Do not downgrade
a device that has a held game.
```

### Issue #41

Title: Prefer the agreed save base over a misleading mtime

```markdown
Fixes #41

When one whole manifest is still the confirmed common version and the other
has changed, the changed side wins even if the unchanged files have a later
timestamp. Named extra locations use that same agreed hash. A missing or
stale base still uses mtime; that limit is tested. This is not the version
vector work in #29 / #30.

Red on `346d9bda`: `TestBaseFirstPreventsMtimeRollback` followed the newer
mtime. Green on this branch, including both directions on a primary folder
and a named root.
```

## 8. Gate

| Gate | Decision |
| --- | --- |
| (a) Issue #41 PR review | **GO** for review. Not a hardware explanation. Pre-existing `TestSessionNamesTheSnapshotAlreadyTaken` fails on this tip and on stock; it is not part of this diff. |
| (b) Issue #40 PR review | **GO** for review of the hold and staged release. **NO-GO** as unattended SaveSync/Bridge enablement. Default release lets reconcile sync. Explicit sync is not peer-targeted. The Bridge id is not created automatically. |
| (c) Combined isolated testing | **GO**. Local `d7ef7c6` passed the focused set and `TestSaveSyncStar*`. Not a PR. |
| (d) Live hardware | **NO-GO**. |

## 9. What to inspect next

1. `ReleaseProvisioningMode` and the empty-body default. Confirm the project lead accepts that default release is not staged.
2. `handleUpdateGame` restoring `autoSync` while held. Confirm a dashboard round-trip cannot release.
3. The mixed-version log. Stock-to-patched did not move bytes. Stock serving a patched DB did.
4. `decision.go` base-before-mtime, and that `TestANewerSideStillWinsRegardlessOfTheBase` was rewritten rather than deleted.
5. Do not treat #29/#30 as done.

SaveSync integration, contract only, no code in this tranche:

1. Keep `new-protection-paired-topology` fail-closed until this is merged and SaveSync calls it.
2. Source: `POST /api/games` with an explicit slug, `provisioningHold: true`.
3. Bridge: the same id and a Bridge-local path. There will be no offer.
4. Configure roots while held.
5. Release both with `{"autoSync": false}`. Leave the other endpoint held.
6. `POST /api/games/{id}/sync` on the source. Compare hashes. The third peer must still be held, because this sync contacts every online peer.
7. Only then release the third peer, sync, compare, and turn AutoSync on.
8. Do not downgrade a device that has a held game. Do not PATCH a held game to enable sync.
