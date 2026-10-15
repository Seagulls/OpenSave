## Problem

Switching to a branch whose recorded snapshot ZIP has gone missing or become corrupt **clears the active save, advances `ActiveBranch`, and returns success** even though the destination cannot be restored.

This is not the intended behavior of switching to a deliberately empty branch: the target has a snapshot record but its backing archive is unavailable. The resulting CLI message misleadingly states that checkout succeeded.

**Verified on 2026-10-15:** Linux amd64, Go 1.26.4, OpenSave commit [`346d9bda0749`](https://github.com/Liquid-co/OpenSave/commit/346d9bda0749fb57b48fd266f13e535af80f0285). The relevant upstream `main` implementation files have identical Git blob hashes to the tested revision. All inputs are synthetic and under Go `t.TempDir()`; no real game saves or user data were used.

## Reproduction

1. Register a test game with `slot.sav = INITIAL` and create a second seeded branch `other`.
2. Change the active save to `slot.sav = CURRENT-ONLY` (no new manual snapshot).
3. Remove `other`'s snapshot ZIP; repeat separately with the ZIP replaced by invalid bytes.
4. Call `Manager.SwitchBranch("game1", "other")` and inspect the returned error, `slot.sav` and `game.ActiveBranch`.

**Public self-contained regression cases (in the project's `internal/snapshot` test package):** [missing and corrupt target ZIP tests](https://github.com/Seagulls/OpenSave/blob/58e46e03ef388cfb2ad17b807e5c64b8235b3cf0/internal/snapshot/branch_checkout_regression_test.go). The tests reuse the existing public `setup(t)` test helper and are completely isolated from user saves.

```bash
# With the linked test file in internal/snapshot/:
go test -count=1 ./internal/snapshot -run '^TestRegressionBranchSwitch'
```

### Expected vs actual

| | Expected | Observed |
|---|---|---|
| Checkout error | Non-nil error; current save bytes unchanged | `SwitchBranch` returns nil |
| Live save | `slot.sav = CURRENT-ONLY` | `slot.sav` missing |
| Active branch | Remains `main` | Advances to `other` |

Both cases are reproducible and intentionally **RED** until the defect is fixed. The existing positive tests for a normal branch switch and a refused switch when the *outgoing* backup cannot be taken continue to pass.

An independent end-to-end test using a real CLI and daemon also observed a **zero exit status** and a success message after the active save disappeared.

**Recovery limitation:** in that real CLI run, an outgoing auto-snapshot contained the original current save and could be used to recover it. This report demonstrates destructive active-state mutation and erroneous success reporting; it does **not** claim that this particular run irreversibly lost all copies of the data.

## Failure boundary

In [`SwitchBranch`](https://github.com/Liquid-co/OpenSave/blob/main/internal/snapshot/snapshot.go#L904), the current save roots are cleared and the active branch is switched **before** the incoming snapshot ZIP has been fully validated and staged. The later restore error is logged but does not abort/undo the switch.

## Suggested acceptance criteria

- [ ] Preflight that the destination archive exists and is readable/extractable *before* changing live saves or active branch metadata.
- [ ] On target ZIP errors, preserve every current mapped save root and `ActiveBranch`, and return a meaningful API/CLI failure.
- [ ] Stage/commit or roll back the full transition so a partial extraction failure is not falsely reported as a successful checkout.
- [ ] Make both linked regression cases pass without breaking normal seeded and deliberately empty branch switching.

Related but separate finding: failed restore after ZIP extraction starts can also remove current saves; that is a distinct test case, not a duplicate of this checkout preflight bug.