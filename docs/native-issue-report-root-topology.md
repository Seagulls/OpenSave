# bug(native): RemoveRoot can unmap a path while its transfer writer is still admitted

## Summary

OpenSave can acknowledge ordinary native `RemoveGameRoot` **while a previously admitted native writer still owns the root's captured filesystem path**. The mapping is removed before that writer has finished using it. This is a root-topology admission race; removal itself is not intended to delete files.

**Baseline:** `Liquid-co/OpenSave:dev-public@a0f6032b8ba36da25e3e0e5e90c5ace9ba0c1fc7`. Reproduced on Linux amd64 against disposable native HTTP/SQLite fixtures and synthetic files only.

## Independent reproduction

[Public Go regression](https://github.com/Seagulls/OpenSave/blob/7a430835448cbbacdb84d5619f06df68e8f8c443/internal/api/native_root_writer_regression_test.go) · [Reproduction guide](https://github.com/Seagulls/OpenSave/blob/7a430835448cbbacdb84d5619f06df68e8f8c443/docs/native-regression-repro-20261030.md)

```bash
git checkout repro/native-root-lineage-import-20261030
go test -count=1 -race -v ./internal/api -run '^TestRegressionRemoveRootRefusesActiveNativeWriter$'
```

The test registers an additional `config` root with `slot.sav = ORIGINAL`, holds OpenSave's real native `Sync.Writing(gameID)` gate, then calls `DELETE /api/games/{id}/roots/config`. Stock returns **200**, and the mapping disappears while the writer remains admitted. Required: **409**, intact mapping, then successful removal after the writer releases, without deleting `slot.sav`.

This focused witness models an active writer gate; it is not a complete two-peer file-transfer replay.

## Root cause

[`internal/api/routes_roots.go`](https://github.com/Liquid-co/OpenSave/blob/a0f6032b8ba36da25e3e0e5e90c5ace9ba0c1fc7/internal/api/routes_roots.go) permits topology mutation without excluding native path users. [`internal/store/game_roots.go`](https://github.com/Liquid-co/OpenSave/blob/a0f6032b8ba36da25e3e0e5e90c5ace9ba0c1fc7/internal/store/game_roots.go) serializes the SQLite change, not outstanding transfers that have already captured paths. Supported CLI root commands should not circumvent the running daemon's admission even when its published `daemon.addr` file is missing.

## Bounded suggested fix

Use a per-game, nonblocking native topology reservation held across the authoritative mutation and subsequent root/watch updates, with a truthful conflict while previously accepted writers or queued work still use the root. Route the independent OpenSave CLI through that authority or refuse unsafe offline mutation. Do not introduce a new transfer engine, permanent managed-game mode or unrelated lifecycle framework.

## Acceptance criteria

- [ ] The exact-stock test fails for the expected HTTP 200/removed-mapping assertion; patched implementation returns 409 and preserves the mapped root.
- [ ] AddRoot, RemoveRoot, remap and supported CLI entry points respect already admitted writers, including with missing `daemon.addr`.
- [ ] Removal after work finishes succeeds and never deletes physical files; queued/native work is not silently lost.
- [ ] Existing unrelated-game operations, Go tests, race checks, Linux build and Windows cross-build pass.

**Evidence limit:** No claim of deployed user-save loss, full two-peer replay, or native Windows runtime verification.
