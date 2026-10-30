# bug(native): delayed sync-complete report contaminates a re-added named root's lineage

## Summary

A delayed `sync-complete` event from an authenticated peer can refer to an **old mapping** of a named root, arrive after that root is removed and re-added under the **same name**, and populate the replacement mapping's `game_root_sync_state` with old `pulledFiles`. Signing/attributing a report to a peer does not prove it refers to the current root incarnation.

**Observed defect:** persisted stale lineage metadata. This reproduction does **not** establish a destructive file write or deletion.

**Affected source:** `Liquid-co/OpenSave:dev-public@a0f6032b8ba36da25e3e0e5e90c5ace9ba0c1fc7`, with disposable native handler and SQLite fixtures on Linux amd64.

## Independent reproduction

[Public Go regression](https://github.com/Seagulls/OpenSave/blob/7a430835448cbbacdb84d5619f06df68e8f8c443/internal/p2p/native_stale_root_report_regression_test.go) · [Reproduction guide](https://github.com/Seagulls/OpenSave/blob/7a430835448cbbacdb84d5619f06df68e8f8c443/docs/native-regression-repro-20261030.md)

```bash
git checkout repro/native-root-lineage-import-20261030
go test -count=1 -race -v ./internal/p2p -run '^TestRegressionDelayedReportCannotAttachToReaddedRoot$'
```

1. Create a disposable native P2P fixture with an additional mapped `config` root.
2. Remove `config`, then re-add `config` at a different temporary path. The replacement root's lineage should be empty.
3. Deliver a `sync-complete` event attributed to the fixture peer, naming `root: config` and `pulledFiles: ["old-save.bin"]` from the old incarnation.
4. Read `game_root_sync_state` for the **new** `config`.

**Stock result:** `old-save.bin` is incorrectly recorded in the new root's lineage. **Required result:** the report does not mutate the replacement mapping's lineage.

The test uses the native P2P test handler; it is not a test of a complete deployed LAN/relay connection.

## Source-level cause

[`internal/p2p/routes.go`](https://github.com/Liquid-co/OpenSave/blob/a0f6032b8ba36da25e3e0e5e90c5ace9ba0c1fc7/internal/p2p/routes.go) accepts a `sync-complete` event and forwards its root name and file list. [`internal/p2p/syncengine/engine.go`](https://github.com/Liquid-co/OpenSave/blob/a0f6032b8ba36da25e3e0e5e90c5ace9ba0c1fc7/internal/p2p/syncengine/engine.go) can merge these paths against the current named root based on name alone. [`internal/store/game_roots.go`](https://github.com/Liquid-co/OpenSave/blob/a0f6032b8ba36da25e3e0e5e90c5ace9ba0c1fc7/internal/store/game_roots.go) stores mapping timestamps, but the incoming old event is not bound to the incarnation that produced it.

This is distinct from active-writer-versus-removal: a lock around the removal cannot identify a stale message arriving later.

## Bounded suggested fix

Bind a named-root state-bearing report to a **receiver-issued** mapping generation (for example the receiver's mapped timestamp echoed by the pulling peer) and check that generation **in the final authoritative SQLite write**. On a missing/old token, do not accept the report as confirmed lineage; fall back to safe fresh manifest reconciliation where appropriate. Preserve current primary-root behaviour and protocol compatibility.

Avoid a global version-vector protocol, new distributed coordinator or general maintenance state.

## Acceptance criteria

- [ ] Stock test fails for stale `old-save.bin` contamination and patched test passes.
- [ ] Old same-name reports cannot affect a replacement root after remove/re-add, including rapid re-add and remap.
- [ ] Valid new-root reports are recorded; LAN/relay callbacks and deletion lineage still behave correctly.
- [ ] Older peers lacking a generation token fail safely or reconcile with documented compatibility limits.
- [ ] Go/race regression suites and supported builds pass.

**Evidence limit:** No claim of user-save loss or that a full two-daemon delayed delivery has been executed by this public test.
