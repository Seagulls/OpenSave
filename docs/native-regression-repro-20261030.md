# Independent native OpenSave regression witnesses

These are **reproduction tests**, intentionally expected to fail on **unmodified** OpenSave `dev-public` commit `a0f6032b8ba36da25e3e0e5e90c5ace9ba0c1fc7`.

This branch contains **only** three focused Go test files plus this guide; it introduces no production fix. The tests use `t.TempDir()`, OpenSave's existing API/P2P test helpers, synthetic game IDs, and synthetic save bytes. Do not run them against an ordinary user's OpenSave home.

## Reproduce (Linux, Go version supported by go.mod)

```sh
git checkout repro/native-root-lineage-import-20261030
go test -count=1 -race ./internal/api -run '^TestRegressionRemoveRootRefusesActiveNativeWriter$' -v
go test -count=1 -race ./internal/p2p -run '^TestRegressionDelayedReportCannotAttachToReaddedRoot$' -v
go test -count=1 -race ./internal/api -run '^TestRegressionUntrackedImportCannotOverwriteOwnedRoot$' -v
```

Every failure must be inspected for the **specific assertion** below; a compile error, network failure or timeout is not proof of the defect.

| Witness | Expected failure on baseline | Safe implementation must |
| --- | --- | --- |
| `TestRegressionRemoveRootRefusesActiveNativeWriter` | RemoveRoot acknowledges while a native writer gate is held | Return conflict and keep the mapping; allow removal after writer completes without changing save bytes |
| `TestRegressionDelayedReportCannotAttachToReaddedRoot` | An old same-name root's `old-save.bin` enters the re-added mapping's lineage | Refuse stale lineage updates |
| `TestRegressionUntrackedImportCannotOverwriteOwnedRoot` | Portable backup changes another game's owned save bytes; exact primary, named, nested cases | Preserve original bytes and reject a conflicting imported registration |

**Scope of proof:** The RemoveRoot test uses OpenSave's real native writer-admission gate, not a live two-device file transfer. The lineage test sends the event through the existing local native P2P handler fixture, not a deployed network peer. The backup test exercises the real localhost HTTP export/import endpoints and a disposable SQLite database.

**Safety and limitations:** These tests do not establish that any user save has been lost. They do not prove exhaustive network, platform, crash or concurrency behaviour. No private repository, CI runner, username, device address, credentials, save paths, or internal integration project details are included.

The tests may be copied unchanged into the corresponding package of a future upstream patch checkout to establish **RED on baseline / GREEN on candidate**. They are published independently of any patch so the defect can be reviewed before choosing a fix.
