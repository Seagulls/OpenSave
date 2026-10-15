## Summary

`Manager.Restore` verifies an archive's ZIP checksums, but a **CRC-valid ZIP can still be structurally impossible to extract**. The restore implementation may **clear existing live saves before discovering that extraction must fail**. It returns an error with the original working save files already removed.

A minimal reproducer is a ZIP with a regular file `block` followed by `block/child.sav`: both entries have valid CRCs, but `block` cannot simultaneously be a file and a parent directory. This is a **validation-order and transactional restore bug**, not an arbitrary-file-write or successful path-traversal exploit.

## Environment and test evidence

- **Source:** OpenSave commit `346d9bda0749fb57b48fd266f13e535af80f0285`.
- **Execution:** Linux amd64, Go 1.26.4. Both regressions run on synthetic files under `t.TempDir()`; no actual user save data is involved.
- **Upstream comparison (2026-10-15):** `snapshot.go`, `zip.go`, `zip_roots.go` and the existing test helper were blob-identical between the audited revision and upstream `main`.
- **Result:** both single-root `Manager.Restore` and mapped-additional-root `UnzipRoots` tests reproduce loss of existing working files. Existing successful single/multi-root restore controls passed during focused validation.

## Minimal reproduction

1. Register a disposable game `game1` with `important.sav = CURRENT-ONLY`; create snapshot `S1`.
2. Replace `S1.ZipPath` with a syntactically valid ZIP containing these entries, **in this order**:

   ```text
   block            (ordinary file; valid CRC)
   block/child.sav  (ordinary file; valid CRC)
   ```

3. Verify `VerifyArchive(S1.ZipPath)` returns `nil`. The member bytes are valid, but the file/directory layout is impossible.
4. Call `Manager.Restore("game1", S1.ID)`. Read the returned error and inspect `important.sav`.
5. **Multi-root variant:** call `UnzipRoots` with a primary save root and a mapped `config` location. Put the impossible file/directory collision under `config`, with existing unrelated files in **both** roots. Inspect them after the extraction error.

**Public executable tests:** [single-root restore and multi-root failure preservation](https://github.com/Seagulls/OpenSave/blob/58e46e03ef388cfb2ad17b807e5c64b8235b3cf0/internal/snapshot/zip_restore_regression_test.go). Both are deliberately RED until the implementation preserves the original working tree on failed extraction.

```bash
# With the linked regression file placed in internal/snapshot:
go test -count=1 ./internal/snapshot -run '^TestRegression(RestoreValid|UnzipExtra)'
```

### Expected versus observed

| Stage / invariant | Expected | Observed |
|---|---|---|
| CRC check | Check member integrity; CRC-valid ZIP may pass | `VerifyArchive` returns `nil` |
| Structural validation | Reject impossible directory topology **before** touching saves | Collision is discovered **during extraction** |
| Manager restore | Return extraction error; preserve original `important.sav` | Error returned; `important.sav` is **missing** |
| Additional-root extraction | Error must leave **all** current roots intact | Files in both primary and `config` roots are removed |

These assertions fail reproducibly using disposable test directories. This is **not** a claim that arbitrary archives pass all checks: the precise point is that the currently used CRC check does not establish that a full restore can succeed.

## Code-path analysis

[`Manager.Restore`](https://github.com/Liquid-co/OpenSave/blob/346d9bda0749fb57b48fd266f13e535af80f0285/internal/snapshot/snapshot.go#L727) opens the archive and calls `VerifyArchive` before starting extraction. But CRC integrity checks the compressed member data, **not** whether the archive's member paths can coexist safely on disk.

The [`UnzipRoots`](https://github.com/Liquid-co/OpenSave/blob/346d9bda0749fb57b48fd266f13e535af80f0285/internal/snapshot/zip_roots.go#L198) implementation opens the ZIP, clears the primary save location, and clears an additional mapped root on its first matching entry, **before** the loop finishes calling `extractEntryAs`. Likewise, [`UnzipTo`](https://github.com/Liquid-co/OpenSave/blob/346d9bda0749fb57b48fd266f13e535af80f0285/internal/snapshot/zip.go#L239) can clear a single-root target before extracting every entry.

When a later member cannot be created due to `block` already existing as a file, the error is returned after the original directory contents have already gone. With multiple roots, this can leave both an unrelated primary root and the failing secondary root empty or partially restored.

A separate regression using a rejected `../bad.sav` member established the **ordering hazard** for unsafe paths; it did **not** show an actual escape from the extraction destination. This report centers on the cleaner, CRC-valid topology collision instead.

## Impact and recovery limitation

A failed restore can destroy the **active working save**, precisely when users are attempting recovery. In an ordinary workflow a pre-restore safety snapshot may hold the displaced bytes, so we do **not** claim that every occurrence is irreversible. The bug is that the operation reports failure **after** mutating the original files.

## Suggested fix and acceptance criteria

- [ ] Validate archive member names, file types, path traversal, path collisions, and mapped-root layout across the **whole archive** before any destination is cleared.
- [ ] Stage and verify a complete extraction in temporary directories; commit all affected roots safely, or implement rollback robust to later failures and separate filesystems.
- [ ] On any extract/validation error, preserve existing file contents and relevant state across **all** configured roots.
- [ ] Make the two public RED regressions pass, while preserving successful restore, missing-archive preflight and normal multi-root behavior.
- [ ] Add a regression for extraction failure after several valid entries have been written, not just the first conflicting path.

**Related, not duplicate:** [#59](https://github.com/Liquid-co/OpenSave/issues/59) concerns switching branches *before validating the incoming snapshot* and then incorrectly returning success. This report is the separate nontransactional ZIP extraction path.
