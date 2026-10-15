# Minimal snapshot safety regression reproducers

Two independent, sanitized bug reproducers for OpenSave's Go snapshot package, validated on Linux amd64 with Go 1.26.4 against revision `346d9bda0749fb57b48fd266f13e535af80f0285`. The relevant source files on public upstream `main` had the same blob hashes on 2026-10-15.

- [Branch checkout preflight regression](../../internal/snapshot/branch_checkout_regression_test.go): a missing or corrupt destination ZIP must not clear current saves or advance the branch.
- [ZIP restore atomicity regression](../../internal/snapshot/zip_restore_regression_test.go): a checksum-valid but structurally invalid ZIP must not clear existing saves before extraction fails.

From the repository root, run:

```bash
go test -count=1 ./internal/snapshot -run '^TestRegressionBranchSwitch'
go test -count=1 ./internal/snapshot -run '^TestRegression(RestoreValid|UnzipExtra)'
```

**Safety:** All tests use the project's existing `setup(t)` / `writeSave(t)` helpers or `t.TempDir()` and synthesized file bytes. No real game saves, account information, credentials, logs, or local machine paths are included. These intentionally fail on the affected revision and should pass after the fixes. Normal snapshot/branch-switch controls pass.

**Scope:** In the observed branch-switch CLI reproduction, a newly created outgoing snapshot held a recoverable copy of the current progress. The issue is failed checkout changing active state and falsely reporting success. ZIP extraction failures likewise may leave a recoverable safety snapshot; the defect is that a failed restore has already destroyed the live working tree. Neither reproducer claims universal irreversible loss.
