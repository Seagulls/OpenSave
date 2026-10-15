## Problem

A snapshot ZIP can pass OpenSave's CRC/integrity verification yet be **impossible to extract** (for example, a file and directory collision). The restore path may clear existing saves **before** it discovers the invalid archive topology. Restore returns an error, but the original live save files have already disappeared.

**Verified on 2026-10-15:** Linux amd64, Go 1.26.4, OpenSave commit [`346d9bda0749`](https://github.com/Liquid-co/OpenSave/commit/346d9bda0749fb57b48fd266f13e535af80f0285). The relevant upstream `main` implementation files have identical Git blob hashes to the tested revision. All inputs are synthetic and under Go `t.TempDir()`; no real game saves or user data were used.

## Minimal reproduction

1. Register a disposable game, write `important.sav = CURRENT-ONLY`, and create snapshot `S1`.
2. Replace the saved ZIP at `S1.ZipPath` with a standard, checksum-valid ZIP containing these entries **in order**:

   ```text
   block            (regular file with valid CRC)
   block/child.sav  (regular file with valid CRC)
   ```

   On disk, `block` cannot simultaneously be a regular file and a parent directory.

3. Confirm `VerifyArchive(S1.ZipPath)` returns nil.
4. Call `Manager.Restore("game1", S1.ID)`. Inspect both the returned extraction error and the original live file.
5. Independently test `UnzipRoots` with an additional named root and the same collision under that root to check cross-root preservation.

**Public runnable RED tests:** [checksum-valid ZIP collision through `Manager.Restore` and multi-root `UnzipRoots`](https://github.com/Seagulls/OpenSave/blob/58e46e03ef388cfb2ad17b807e5c64b8235b3cf0/internal/snapshot/zip_restore_regression_test.go). They use only synthetic file contents, the project's existing public snapshot test fixture and `t.TempDir()`.

```bash
# With the linked test file in internal/snapshot/:
go test -count=1 ./internal/snapshot -run '^TestRegression(RestoreValid|UnzipExtra)'
```

### Expected vs actual

| | Expected | Observed |
|---|---|---|
| ZIP preflight | CRC verification may pass; structural validation must still occur before mutation | `VerifyArchive` passes |
| Extraction | Returns an error for impossible `block/child.sav` path | Returns an error |
| Current save files | Original bytes remain untouched when extraction fails | `important.sav` is missing |
| Multiple mapped roots | No existing root loses files on a late extraction error | Both primary and additional-root live files were removed |

The two cases were repeated on the audited revision and are failing for their intended safety assertions. Existing valid single/multi-root restore controls continue to pass.

**Impact and recovery boundary:** the working save tree can disappear after an unsuccessful restore. A safety snapshot can provide a recovery path, so the demonstrated defect is a failed *nontransactional restore*, **not** proof of universally irreversible data loss. This is **not** an archive path-traversal escape.

## Why CRC validation is not enough

[`Manager.Restore`](https://github.com/Liquid-co/OpenSave/blob/main/internal/snapshot/snapshot.go#L727) calls `VerifyArchive`, then extracts through [`UnzipRoots`](https://github.com/Liquid-co/OpenSave/blob/main/internal/snapshot/zip_roots.go#L198) or [`UnzipTo`](https://github.com/Liquid-co/OpenSave/blob/main/internal/snapshot/zip.go). These paths may clear the live destination *before* all archive entries are checked for safe path topology and successful extraction.

Integrity of individual ZIP members does not establish that the entire archive can be safely restored.

## Suggested acceptance criteria

- [ ] Reject invalid entry names, file/directory collisions and invalid mapped-root layouts **before** touching live files.
- [ ] Stage and verify extraction to temporary locations before committing, or implement complete rollback across all mapped roots.
- [ ] On any extraction failure, preserve original file contents and restoration metadata.
- [ ] Make the linked single-root and multi-root RED cases pass, without breaking valid-archive restoration.

These regression tests intentionally exclude production paths, credentials, identifiers and diagnostic logs.