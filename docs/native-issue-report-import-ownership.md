# bug(native): untracked backup overwrite writes another game's owned save path before admission

## Summary

A portable v2 backup for an **untracked** game can be imported with `mode: "overwrite"` into a folder already owned by a **different tracked game**. The importer extracts archived bytes before it establishes authoritative game/root ownership. An eventual registration refusal is too late: the tracked owner's live files have already changed.

**Affected source:** `Liquid-co/OpenSave:dev-public@a0f6032b8ba36da25e3e0e5e90c5ace9ba0c1fc7` on Linux amd64. The result is confirmed with synthetic files and actual localhost native HTTP export/restore plus SQLite. No real game saves were used.

**Related but distinct:** [upstream #130](https://github.com/Liquid-co/OpenSave/issues/130) / [PR #131](https://github.com/Liquid-co/OpenSave/pull/131) correct ordinary registration ownership, but a backup import must enforce that check **before extracting**. This does not duplicate snapshot-ID collision reports.

## Independent reproduction

[Public Go regression](https://github.com/Seagulls/OpenSave/blob/7a430835448cbbacdb84d5619f06df68e8f8c443/internal/api/native_import_ownership_regression_test.go) · [Reproduction guide](https://github.com/Seagulls/OpenSave/blob/7a430835448cbbacdb84d5619f06df68e8f8c443/docs/native-regression-repro-20261030.md)

```bash
git checkout repro/native-root-lineage-import-20261030
go test -count=1 -race -v ./internal/api -run '^TestRegressionUntrackedImportCannotOverwriteOwnedRoot$'
```

Three separate test cases exercise the **exact primary**, **exact named `config`**, and **nested under named `config`** save-root scenarios:

1. Export an untracked synthetic backup containing `ARCHIVED-OLD` at a chosen temporary location.
2. Track a different game as native owner of that exact or enclosing save root. Replace its live `slot.sav` contents with `OWNER-LIVE`.
3. Call `POST /api/backup/restore` with `{sourcePath: archive, mode: "overwrite"}`.
4. Read the owner's actual file bytes and verify a conflicting second native game was not registered.

| Scenario | Required result | Stock `dev-public` |
| --- | --- | --- |
| Destination equals another game's primary root | Preserve `OWNER-LIVE`; refuse/skip | `slot.sav` overwritten with archived bytes |
| Destination equals another game's mapped named root | Preserve `OWNER-LIVE`; refuse/skip | `slot.sav` overwritten |
| Destination nested inside another game's mapped named root | Preserve `OWNER-LIVE`; refuse/skip | `slot.sav` overwritten |

In the primary case a subsequent registration can fail **after** extraction. For named/nested roots, conflicting registration also occurs on stock in the observed fixture. The test's decisive safety assertion is that the owner's file bytes remain unchanged.

## Root cause

[`internal/api/routes_backup.go`](https://github.com/Liquid-co/OpenSave/blob/a0f6032b8ba36da25e3e0e5e90c5ace9ba0c1fc7/internal/api/routes_backup.go) performs `CheckRestoreTarget` (directory suitability), then `snapshot.UnzipRoots` (**filesystem mutation**), then `TrackGame` (ownership admission). The imported game's own write gate cannot stand in for the existing **other** game's ownership.

## Bounded suggested fix

For **untracked overwrite imports**, perform authoritative path-ownership refusal **before any mutation**, including primary/named/nested roots, resolvable aliases and inventory-read failure. Reuse the transactional ownership predicate proposed in [PR #131](https://github.com/Liquid-co/OpenSave/pull/131), and hold a bounded native admission/claim through extraction and registration to avoid time-of-check/time-of-use races. Preserve valid unclaimed imports, tracked-game restore semantics, and the existing pre-import safety backup.

Do not introduce a new save-sync engine, a permanent managed-game class or a general first-copy coordinator.

## Acceptance criteria

- [ ] All three exact-stock tests fail for the intended overwrite and pass with original owner bytes preserved; no second owner is registered.
- [ ] Physical path aliases, unreadable inventory, and simultaneous native registration/root edits fail closed before extraction.
- [ ] Valid unclaimed untracked imports and existing tracked-game imports continue to work.
- [ ] A returned extraction/registration error restores the pre-import safety copy rather than reporting a successful import; distinguish this from unverified power-loss/SIGKILL durability.
- [ ] Linux Go/race tests and supported platform builds pass; native Windows lock semantics should be checked separately if used.

**Evidence limit:** Reproduced native writes to disposable files only. No evidence of real user-save loss, deployed exploitation, or a complete crash-restart durability guarantee.
