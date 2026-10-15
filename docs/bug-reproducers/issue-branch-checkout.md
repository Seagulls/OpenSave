## Summary

Switching to a branch whose latest snapshot record references a **missing or corrupt ZIP** clears the current live save directory and advances the active branch even though the target could not be restored. The manager returns success, and the real CLI can print a successful checkout message with an **empty working save**.

The target branch is not intentionally empty: it has a recorded snapshot, but its archive is unavailable. There are two related faults in one state transition: **destructive work occurs before target validation** and **target restore errors are not propagated**.

## Environment and evidence

- **Source:** OpenSave commit \`346d9bda0749fb57b48fd266f13e535af80f0285\`.
- **Execution:** Linux amd64, Go 1.26.4, disposable file trees. Test fixtures contain only synthetic strings; no personal saves, credentials, or host paths.
- **Upstream comparison (2026-10-15):** \`snapshot.go\`, \`zip.go\`, \`zip_roots.go\` and the snapshot test helper have the same Git blob hashes on upstream \`main\` as on the tested source.
- **Reproduction:** two isolated manager tests (missing and corrupt ZIP) plus a separate real CLI/daemon reproduction. Both manager tests fail on the audited implementation for the intended preservation assertions.

## Reproduction

1. In a disposable game called \`game1\`, write \`slot.sav = INITIAL\`. Create a second branch \`other\` with a seed snapshot, then return to \`main\`.
2. Write \`slot.sav = CURRENT-ONLY\` on \`main\`; do **not** add a manual snapshot containing these newer bytes.
3. Find the latest snapshot metadata for \`other\`.
4. **Variant A:** delete the ZIP at that snapshot's \`ZipPath\`. **Variant B:** replace it with non-ZIP bytes.
5. Call \`Manager.SwitchBranch("game1", "other")\`.
6. Check the returned error, original \`slot.sav\`, and \`game.ActiveBranch\`. Repeat through the CLI if desired.

**Public executable tests:** [missing and corrupt destination archive regressions](https://github.com/Seagulls/OpenSave/blob/58e46e03ef388cfb2ad17b807e5c64b8235b3cf0/internal/snapshot/branch_checkout_regression_test.go). They use the repository's existing \`setup(t)\` and \`writeSave(t)\` test helpers and synthetic \`t.TempDir()\` data; no external games or hardware are needed.

\`\`\`bash
# With the linked regression file placed in internal/snapshot:
go test -count=1 ./internal/snapshot -run '^TestRegressionBranchSwitch'
\`\`\`

### Expected versus observed

| Check | Expected on failed checkout | Observed on affected implementation |
|---|---|---|
| API result | Non-nil error identifying the unavailable destination archive | \`SwitchBranch\` returns \`nil\` |
| \`slot.sav\` | Remains present, with \`CURRENT-ONLY\` bytes | File is **missing** |
| \`ActiveBranch\` | Remains \`main\` | Becomes \`other\` |
| CLI exit / message | Nonzero exit and unambiguous failure | Exit **0**, followed by "Switched to branch \\"other\\"" |

The same misbehavior was observed with **both** a missing and a corrupt ZIP. The positive branch-switching controls, including rejection when the *outgoing* safety backup cannot be created, passed in the focused validation.

### Independent CLI/daemon observation

In a separate isolated process-level reproduction, the CLI returned success even though the daemon logged the failed incoming restore:

\`\`\`text
checkout exit status: 0
active slot.sav before: present (new local progress)
active slot.sav after:  MISSING
[snapshot] failed to restore branch snapshot: ... file does not exist
Switched to branch "other"
\`\`\`

This is a sanitized representation of the observed output; user-specific paths and save contents are deliberately excluded.

**Recoverability qualification:** that CLI run also took an outgoing automatic snapshot containing the current progress, making recovery possible. This report documents **unsafe mutation of the active save and false success**, **not** proven irreversible destruction of every copy.

## Root-cause analysis

[\`SwitchBranch\`](https://github.com/Liquid-co/OpenSave/blob/346d9bda0749fb57b48fd266f13e535af80f0285/internal/snapshot/snapshot.go#L904) correctly attempts a protective snapshot of the **outgoing** state, then calls \`clearSavePathGuarded\` for the current save and additional roots and switches \`ActiveBranch\`. Only **afterward** does it call \`OpenArchive\` and \`UnzipRoots\` for the incoming branch. If the incoming archive fails to open or extract, the error is printed to stderr but checkout still returns \`nil\`.

A valid outgoing backup does not mean the **incoming target** is valid. The current ordering makes a missing destination archive a destructive operation rather than a failed no-op.

## Suggested fix and acceptance criteria

- [ ] Read target branch/snapshot metadata and preflight the target ZIP **before** clearing any current root or changing \`ActiveBranch\`.
- [ ] Validate and stage incoming contents (including all mapped roots), or provide durable rollback if extraction/commit fails.
- [ ] A missing, unreadable, corrupt, or unextractable target ZIP must leave current file contents, branch selection, and sync metadata consistent and return a meaningful error.
- [ ] CLI must report failure and exit nonzero when its target cannot be restored.
- [ ] Both public RED regression tests pass; normal seeded/empty branch switching and outgoing-backup safety controls remain passing.

**Related, not duplicate:** [#60](https://github.com/Liquid-co/OpenSave/issues/60) concerns a restore ZIP that *opens and verifies* but fails later during extraction; this issue additionally covers branch selection and suppressed checkout errors.
