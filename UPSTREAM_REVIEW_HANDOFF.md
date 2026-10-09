# OpenSave upstream review handoff

Evidence for independent review before any upstream PR. This file is on
`review/upstream-handoff` only. It is not part of either code branch.

No Steam Deck, Bazzite, Raspberry Pi, installed daemon, or real save was
contacted. No upstream PR was opened. `Seagulls/Savesync` was not modified.

## 1. Branch and commit table

| Branch | Base | Tip | Pushed |
| --- | --- | --- | --- |
| `feat/atomic-safe-game-provisioning` | `346d9bda0749fb57b48fd266f13e535af80f0285` | `1508845f93bf1f9affe3259c1b35a786fe183fb2` | `Seagulls/OpenSave` only |
| `fix/agreed-base-precedes-mtime` | same base | `feedc3b7dfcb21a1f913ad9e88ebb8109d300db5` | `Seagulls/OpenSave` only |
| `review/upstream-handoff` | provisioning tip | this commit | docs only |

The two code branches are independent. They both start at upstream `main`
`346d9bda` (v2.4.1). Neither contains the other.

Compare:

- https://github.com/Liquid-co/OpenSave/compare/main...Seagulls:feat/atomic-safe-game-provisioning
- https://github.com/Liquid-co/OpenSave/compare/main...Seagulls:fix/agreed-base-precedes-mtime

Throwaway combined tree, **not pushed**, not an upstream PR:

- path: `/tmp/kilo/opensave-combined`
- local SHA: `23914b4b70b852a71aa9bd52517f0e7bcab61943`
- parents: provisioning `1508845` and mtime `feedc3b`
- only conflict was `CHANGELOG.md`, resolved by keeping both notes

Provisioning commits:

1. `4b50182` store: persist a per-game provisioning hold
2. `e840f37` sync: keep a held game out of every transfer path
3. `3b760e2` test: prove a held game survives restart and one-sided release
4. `1508845` sync: fail closed when a provisioning hold cannot be read

Mtime commits:

1. `feedc3b` sync: let an agreed base beat a misleading mtime

Toolchain: `go version go1.27.1-X:nodwarf5 linux/amd64`. `go.mod` requests
`go 1.26.4`. `GOTOOLCHAIN=auto` stayed on 1.27.1 because it is newer. Tests
were run with `GOTOOLCHAIN=local`. Go 1.26.4 was not downloaded.

## 2. Exact code impact

### Provisioning (issue #40)

Schema: `internal/store/migrations/0038_game_provisioning_holds.sql`.

```sql
CREATE TABLE game_provisioning_holds (
    game_id TEXT PRIMARY KEY REFERENCES games(id) ON DELETE CASCADE,
    held_at TEXT NOT NULL
);
```

A table, not a column on `games`. Older builds `SELECT *` into `games` and a
new column breaks them. No row means not held. Existing games are unchanged.
Downgrade: an older build ignores the table. The rows remain. Upgrading again
still sees them. A downgraded build will sync a held game, because it does
not know the table. That is a mixed-version limit, not a silent migration of
existing games.

Public API, opt-in only:

- `POST /api/games` accepts optional `provisioningHold: true`, optional
  `autoSync: false` (pointer; omitted is unchanged), and optional `id` only
  when a hold is requested. `id` must be `ValidExplicitGameID` (the same slug
  `SlugifyGameID` would produce, max 128).
- `POST /api/offered-games/{id}/place` accepts the same two flags. Omitted,
  the body is still `{"path": ...}` and placement syncs as today.
- `POST /api/games/{id}/release-provisioning` returns
  `{"id","released","alreadyReleased"}`. It clears the hold and sets
  `auto_sync=1` in one transaction. It does **not** call `SyncGame`.
- Game JSON includes `provisioningHold`. A failed read is reported as true,
  not false.
- CLI: `opensave add --hold [--id slug] <name> <path>`,
  `opensave offers place --hold <id> <folder>`,
  `opensave game <id> release`.

Why this is not `autoSync: false`: that flag only stops this device from
starting a sync. A peer that knows the id still fetches the manifest and can
pull or delete. Pause is in memory and dies on `daemon.New` + `Start`. The
hold is checked on outbound sync, reconcile, retry, watch, cloud publish, and
inbound manifest, blocks, delete, and snapshot download. A failed SQLite read
is an error. Callers refuse. They do not treat it as not held.

Release does not fan out. `SyncGame` contacts every online peer. The caller
syncs after releasing only the peers that should converge. A peer that is
still held refuses, so a later reconcile cannot move that peer's files.

An already tracked game cannot be converted into a hold. The call fails and
leaves `AutoSync` and the bytes alone.

### Agreed base (issue #41)

`ComputeWithDeletions` ranked differing files by `MtimeMs` before asking
whether one whole manifest was still `baseHash`. A touched copy of the agreed
base could overwrite the only edit. The new cases run first: if one manifest
hash equals the base and the other does not, the changed side wins. Equal
mtime ties still use the base. A missing or stale base still uses mtime.
That limit is pinned, not papered over.

`syncOneRoot` used to call `Compute` with an empty base, then load
`GetAgreedHashForRoot` only for `DetectConflict`. It now resolves that same
base and passes it to `ComputeWithBase`.

`TestANewerSideStillWinsRegardlessOfTheBase` was kept and its expected
outcome reversed. It used to require the newer mtime to win even when that
side was the agreed base. That was the bug.

This is not #29 / #30. No version vector, no new conflict policy for two
sides that both differ from the base.

## 3. Threat and safety matrix

| Scenario | Result | Evidence |
| --- | --- | --- |
| Hold read fails, game row still readable | PASS | `TestUnreadableProvisioningHoldDoesNotFetchManifest`: `FetchManifest` calls = 0, error is `ErrProvisioningUnreadable`. `TestSyncAllGamesStopsWhenHoldSetUnreadable`: returns before nil `Sync`. `TestProvisioningReadFaultIsNotNotHeld`. |
| Partial setup / crash after commit | PASS | `TestProvisioningHold_PairedCreateRestartAndIsolation` restarts via `daemon.New` + `Start` on the same home and port. Hold remains. No pause required. |
| Alias of a held game | PASS | `TestProvisioningReadFaultIsNotNotHeld` alias blocks. Alias query error is returned, not treated as no alias. |
| Offered placement with `autoSync: false` | PASS | `TestProvisioningHold_PlaceOfferWithoutSyncing`. Repeat place does not duplicate. |
| Already tracked game | PASS | `TestProvisioningHold_DoesNotConvertAnExistingGame`. |
| Peer manifest / blocks / delete | PASS | Same restart test: manifest is `ProvisioningMessage` and does not contain "not found". Delete does not remove the file. WAN serve uses the same refusal. |
| Unrelated game keeps syncing | PASS | Same restart test writes `UNRELATED-FRESH` and waits for it on the other peer. 100-game test does the same. |
| Mixed patched/unpatched peer | PARTIAL | `TestStockClientDoesNotTreatProvisioningRefusalAsMissing` shows stock classifiers do not treat the 409/503 body as "not found" or emptied-hold. A live unpatched daemon was not run. An older build that ignores the table will sync a held game. Do not promise mixed-version safety beyond the stock error classifier. |
| One-sided release | PASS | Restart test releases source only, restarts both, bridge hold remains, no bytes move. |
| Three-peer star, third still held | PASS | `TestProvisioningHold_StarLeavesHeldEndpointAlone`. Bazzite is not paired with Deck. Release does not copy. Explicit sync converges the released pair. Deck keeps `DECK-ONLY`. Bridge reconcile does not move Deck. |
| Three-peer divergent writers, no hold | PASS on combined tree | `TestSaveSyncStarDualWriterPreservesBoth` both orders: status `conflict`, each endpoint keeps its unique file. Bridge does not invent a mixture. This is existing conflict handling, not a new policy. |
| Named roots before release | PASS | `TestProvisioningHold_NamedRootsBeforeRelease`. |
| Named root agreed-base vs mtime | PASS | `TestAgreedBaseBeatsNewerMtimeOnPrimaryAndExtraRoot` on real daemons. |
| Absent agreed base | PASS as a limit | `TestBaseFirstRetainsLegacyMtimeWithoutBase`: newer mtime still wins. Not fixed. |
| Stale agreed base matching neither side | PASS as a limit | `TestStaleAgreedBaseDoesNotOverrideMtime`. Mtime still decides. Not fixed. |
| Symlink / occupied / duplicate id | PASS | `TestProvisioningHold_RejectsUnsafePathsAndDuplicates`. |
| Ordinary track unchanged | PASS | `TestProvisioningHold_OrdinaryTrackUnchanged`. |
| 100 held games, one daemon | PASS | `TestProvisioningHold_HundredGamesStayOneDaemon` (0.84s on the first run). |
| Full TOCTOU of the filesystem | UNTESTED | Path checks are the existing ones plus a symlink-root refusal on hold. No claim of a full race-free filesystem guarantee. |
| Hardware | UNTESTED | No device was used. |

## 4. Test evidence

Host: Arch, `go1.27.1-X:nodwarf5`. Commands used `GOTOOLCHAIN=local`.

Provisioning branch `1508845`, before the mtime merge:

```text
go test -count=1 -timeout 180s -run 'TestProvisioning|TestUnreadable|TestStockClient|TestSyncAllGamesStops' \
  ./internal/store/ ./internal/p2p/ ./internal/p2p/syncengine/ ./e2e/
RC=0
```

Verbose new cases, all PASS:

- `TestProvisioningHold_DoesNotConvertAnExistingGame` 0.02s
- `TestProvisioningHold_StarLeavesHeldEndpointAlone` 0.22s
- `TestSyncAllGamesStopsWhenHoldSetUnreadable` 0.02s
- `TestUnreadableProvisioningHoldDoesNotFetchManifest` 0.02s
- `TestStockClientDoesNotTreatProvisioningRefusalAsMissing` 0.00s

Earlier on `3b760e2`, before the fail-closed commit:

```text
go test -count=1 -timeout 300s -v -run 'TestProvisioningHold_' ./e2e
RC=0
go test -race -count=1 -timeout 300s -run 'TestProvisioningHold_|TestCreateHeldGame|TestValidExplicitGameID' ./e2e/ ./internal/store/
RC=0
go test ./... -timeout 2700s
```

That full run passed every package that has tests, including `./e2e` (765s)
and `./internal/p2p`. `cmd/opensave-app` did not compile:
`//go:embed all:frontend/dist` and `frontend/dist` is not in the clone.
Pre-existing. The full `./...` run was **not** repeated on `1508845`.

Mtime branch, red then green. On unmodified `346d9bda`:

```text
go test -count=1 -timeout 60s -run 'TestBaseFirstPreventsMtimeRollback' ./internal/p2p/syncengine/
RC=1
local changed, remote base has later mtime: FilesToPull:[slot.sav] want push
remote changed, local base has later mtime: FilesToPush:[slot.sav] want pull
```

After `feedc3b`:

```text
go test -count=1 -timeout 120s -run 'TestBaseFirst|TestANewerSideStillWins|TestAnMtimeTie' ./internal/p2p/syncengine/
RC=0
go test -count=1 -timeout 180s -run 'TestAgreedBaseBeatsNewerMtimeOnPrimaryAndExtraRoot' ./e2e/
RC=0
go test -count=1 -timeout 300s ./internal/p2p/syncengine/ ./internal/delta/ ./internal/p2p/
RC=0
```

Combined local tree `23914b4` (not pushed):

```text
gofmt -l <changed files>   RC=0
go vet ./internal/p2p/... ./internal/daemon/ ./internal/store/ ./internal/api/ ./internal/delta/
RC=0
go test -count=1 -timeout 300s -run 'TestProvisioningHold_|TestAgreedBaseBeatsNewerMtime|TestBaseFirst|TestANewerSideStillWins' \
  ./e2e/ ./internal/p2p/syncengine/
RC=0
```

Log: `/tmp/kilo/opensave-review-logs/combined-focused.log`

Three-node fixtures copied from `Seagulls/Savesync` `tests/native/three_node_integrity*_test.go`
into the throwaway `e2e/` only. SaveSync was not edited. Loopback daemons only.

```text
go test -count=1 -timeout 480s -v -run '^TestSaveSyncStar' ./e2e
RC=0
```

- `TestSaveSyncStarOfflineRoundTrip` PASS, files=4, Bridge carried the hop
- `TestSaveSyncStarDualWriterPreservesBoth` PASS both orders, status=conflict, unique files kept
- `TestSaveSyncStarIdleBridgeMtimeCannotRollback` PASS

Log: `/tmp/kilo/opensave-review-logs/three-node.log`

Race: the provisioning e2e and store tests were raced on `3b760e2`, not
re-raced after `1508845`. Not a full `-race ./...`.

Linux only. No Windows or macOS run. Synthetic fixtures only.

## 5. Known blockers and remaining risk

1. **Mixed-version daemon, medium.** An unpatched peer does not read
   `game_provisioning_holds`. If it has the game and this build is not the
   one serving, it will sync. Our patched serve path returns 409/503 that
   stock classifiers do not treat as deletion. That classifier test is not a
   two-version process test.
2. **Absent or stale agreed base, medium, intentional.** Mtime still wins.
   `TestBaseFirstRetainsLegacyMtimeWithoutBase` and
   `TestStaleAgreedBaseDoesNotOverrideMtime` pin this. #29/#30 are not in
   this change. Do not claim the Crash 4 hardware incident was proved to be
   this branch. Only the source decision is reproduced.
3. **Release then reconcile, low if sequencing is followed.** After release,
   periodic reconcile syncs that game with every online peer that is not
   held. SaveSync must leave the other endpoint held until the first pair has
   converged. OpenSave has no peer-targeted sync. Do not release all three
   and expect this PR to choose a winner. The dual-writer fixture shows
   ordinary conflict handling preserves unique files; that is not a new
   guarantee invented here.
4. **Full suite not re-run on `1508845` or `feedc3b`, low.** Focused packages
   and the three-node set passed on the combined tree. `cmd/opensave-app`
   still cannot be tested without `frontend/dist`.
5. **Filesystem TOCTOU, low, untested as a proof.** No claim.

No red test was skipped. The mtime reproducing test failed on stock and
passes on `feedc3b`.

## 6. Draft PR text

Do not open these yet.

### PR 1 — issue #40

Title: Hold a new game until it is explicitly released

```markdown
Fixes #40

`TrackGame` persists `AutoSync=true` and can sync before a later update.
`AutoSync=false` only stops this device from starting a sync. A paired
device can still request the manifest. Pause does not survive `daemon.New`.

This adds one table, `game_provisioning_holds`, written in the same
transaction as the game. Older builds that `SELECT *` from `games` keep
working. No row means not held.

- `POST /api/games` and `POST /api/offered-games/{id}/place` accept
  `provisioningHold: true` or `autoSync: false`. Omitted, behaviour is unchanged.
- An explicit id is accepted only while creating a hold, so the other device
  can be registered without an offer and without sending save bytes.
- While held, that game is excluded from watch, reconcile, retry, direct sync,
  inbound manifest/blocks/delete, and snapshot download. A failed hold read
  is refused, not treated as not held.
- `POST /api/games/{id}/release-provisioning` clears the hold and does not
  sync. `SyncGame` would contact every online peer.

Tests cover restart, one-sided release, a three-peer star with the third
peer still held, offer placement, symlink and occupied paths, named roots,
a failed hold read that must not call FetchManifest, and unchanged ordinary
tracking.
```

### PR 2 — issue #41

Title: Prefer the agreed save base over a misleading mtime

```markdown
Fixes #41

`DetectConflict` already treats one side that still holds `agreedHash` as a
one-sided edit. `ComputeWithDeletions` then ranked the file by `MtimeMs`
first, so a touched copy of the agreed base could overwrite the only edit.
Named extra locations called `Compute` with an empty base even though they
already loaded `GetAgreedHashForRoot` for conflict detection.

If one whole manifest equals the confirmed base and the other does not, the
changed side wins. Equal mtime ties are unchanged. A missing or stale base
still uses mtime; that limit is tested, not hidden. This is not the version
vector work in #29 / #30.

Red on `346d9bda`: `TestBaseFirstPreventsMtimeRollback` pulled or pushed the
newer-mtime base. Green on this branch, including a two-daemon primary and
extra-root case.
```

## 7. Reproduction

These leave the shell open and print `RC`. They do not SSH or touch live saves.

```bash
set -o pipefail
cd /home/guy/Documents/ai/codex/projects/OpenSave-upstream-pr
git fetch fork feat/atomic-safe-game-provisioning
git rev-parse HEAD; echo RC:$?
git merge-base --is-ancestor 346d9bda0749fb57b48fd266f13e535af80f0285 HEAD; echo ancestor_rc:$?
GOTOOLCHAIN=local go test -count=1 -timeout 300s -run 'TestProvisioningHold_|TestUnreadable|TestSyncAllGamesStops|TestStockClient' ./e2e/ ./internal/p2p/ ./internal/p2p/syncengine/ ./internal/store/ 2>&1 | tee /tmp/kilo/opensave-review-logs/repro-provisioning.log
echo RC:$?
```

```bash
set -o pipefail
cd /home/guy/Documents/ai/codex/projects/OpenSave-mtime-pr
git rev-parse HEAD; echo RC:$?
GOTOOLCHAIN=local go test -count=1 -timeout 180s -run 'TestBaseFirst|TestANewerSideStillWins|TestAgreedBaseBeatsNewerMtime' ./internal/p2p/syncengine/ ./e2e/ 2>&1 | tee /tmp/kilo/opensave-review-logs/repro-mtime.log
echo RC:$?
```

## 8. Recommendation

| Gate | Decision |
| --- | --- |
| (a) Independent upstream PR review | **GO** for review of the two fork branches above. Do not open the upstream PRs until that review finishes. |
| (b) Combined isolated testing | **GO**. Throwaway `23914b4` passed the focused set, vet, and `TestSaveSyncStar*`. That tree is not a PR. |
| (c) Physical SaveSync hardware testing | **NO-GO**. No device evidence. SaveSync paired-protection guards stay fail-closed. |
