# Cloud provider integrity research (2026-10-11)

This report records isolated research for:

- [#44 — WebDAV upload verification can fail open](https://github.com/Liquid-co/OpenSave/issues/44)
- [#45 — a failed same-name WebDAV retry can replace a good archive](https://github.com/Liquid-co/OpenSave/issues/45)
- [#46 — a missing local/NAS folder can look empty and accept fallback writes](https://github.com/Liquid-co/OpenSave/issues/46)

The work is on `Seagulls/OpenSave` branch
`research/cloud-provider-integrity-20261011`. It is **not merged upstream** and
does not represent an accepted upstream design.

## Starting state

- Upstream branch: `Liquid-co/OpenSave:dev-public`
- Exact base: `7b690420c0fa03513764f4937af29bebf7f391a7`
- Reported application version in `internal/version/version.go`: `2.4.1`
- Module requirement: Go `1.26.4`
- Local validation toolchain: Go `1.27.2-X:nodwarf5 linux/amd64`

The relevant WebDAV source is byte-identical between the v2.4.1 tag
(`346d9bda0749fb57b48fd266f13e535af80f0285`) and the verified `dev-public`
base. The post-v2.4.1 commits do not correct any of the three reported
contracts.

## Results

| Issue | Result | Production change | Remaining boundary |
|---|---|---|---|
| #44 | Fixed for the tested provider contract | A PUT is successful only after an authenticated GET readback has the expected byte count and SHA-256. HTTP errors, transport errors, timeouts, truncation, and same-length corruption fail closed. | Every upload now reads the complete object back, adding remote egress, latency, and server load. The read is bounded to the source size plus one byte. |
| #45 | Reproduced only | None. The focused #44 fix detects a damaged final object but does not stop direct PUT from replacing an existing object first. | Safe publication needs a separately reviewed WebDAV staging/MOVE protocol and server compatibility contract. |
| #46 | Partially fixed | `localFolder.list` now propagates `os.ReadDir` failures rather than reporting an empty successful listing. | Upload still creates a missing ordinary local folder by design. A writable directory beneath a vanished mount cannot be distinguished from the intended filesystem without an explicit mount-identity contract. |

## Baseline reproductions

`research/cloud-provider-integrity/run.sh` checks out the exact base in a
temporary detached worktree and injects the fixture into the real
`internal/cloud` package. The baseline witnesses demonstrate:

1. A WebDAV server stores one third of the PUT and answers 503 to HEAD;
   `Service.Upload` returns success.
2. A WebDAV server changes a byte without changing length; `Service.Upload`
   returns success.
3. A same-name retry replaces a pre-existing complete object with one third of
   the retry body. OpenSave notices the length mismatch only after the original
   has been lost.
4. A renamed-away local destination lists as empty without error, then upload
   recreates the configured path locally while the disconnected destination
   receives nothing.

These are negative witnesses: PASS means the affected baseline behavior was
observed. Fixtures use `httptest.Server`, `t.TempDir`, and synthetic bytes only.

## Corrected behavior and tests

`internal/cloud/provider_webdav_integrity_test.go` and `internal/cloud/provider_local_integrity_test.go` covers:

- truncated and equal-length-corrupt WebDAV objects;
- verification GET responses 403, 404, 500, and 503;
- interrupted and timed-out verification reads;
- WebDAV installations where HEAD is unsupported, unavailable, or lacks a
  content length (HEAD is no longer part of the verification contract);
- credentials and custom headers on verification GET;
- a successful complete WebDAV round trip;
- absent and unreadable local-folder destinations; and
- successful ordinary local-folder listing.

The existing WebDAV provider positive control was extended so its first GET is
the verification readback and its later GET remains a download test.

## Exact production changes

- `internal/cloud/provider_webdav.go`
  - hashes the local source with SHA-256 before PUT and rewinds it;
  - performs an authenticated, custom-header-aware GET after a 2xx PUT;
  - rejects non-2xx, timeout, transport/read, size, and digest failures;
  - bounds readback to the expected size plus one byte.
- `internal/cloud/provider_local.go`
  - wraps and returns `os.ReadDir` errors.
- `internal/cloud/cloud.go`
  - exposes the existing per-request transfer deadline as a package variable so
    the real transfer path can be tested with a bounded timeout.

No P2P, reconciliation, peer-state, snapshot-format, or product API behavior
was changed.

## WebDAV publication design recommendation for #45

Do not silently fall back to direct final-name PUT. A candidate protocol needs
maintainer agreement and compatibility testing against supported servers:

1. PUT to a unique name that cannot be parsed as a snapshot.
2. GET and hash the staged object.
3. MOVE it to the final URL with `Overwrite: F`.
4. If the final URL already exists, hash it: matching content makes the retry
   idempotent; different content is a conflict and must remain untouched.
5. On an interrupted MOVE response, inspect both names before deciding the
   outcome. Clean up staging objects best-effort.
6. If MOVE or its non-overwrite semantics are unsupported, report that safe
   publication is unsupported rather than issuing a destructive final PUT.

This still cannot promise atomicity beyond what a particular WebDAV server
provides. A server matrix must demonstrate MOVE behavior, concurrent writers,
failure recovery, and cleanup before production adoption.

## Local/NAS limitation for #46

The listing fix makes a missing or unreadable configured directory visible to
callers. It does not identify a filesystem. `writeFileWhole` intentionally
uses `MkdirAll`, which is correct for ordinary local-folder users, and a path
that remains writable after unmount is indistinguishable from an intended
local directory.

A strict NAS mode would need explicit opt-in and a persisted/validated identity
such as a platform mount ID, filesystem/device identity, or controller-owned
mount assertion. That is a feature and portability decision, not part of this
minimal error-contract fix.

## Validation

The final evidence archive contains command output and an exact manifest. The
reproducible commands are:

```sh
research/cloud-provider-integrity/run.sh
go test ./internal/cloud -count=1
go test ./internal/cloud -run 'Test(WebDAVUpload|LocalFolderList)' -count=10
go test -race ./internal/cloud -count=1
go vet ./internal/cloud
go test ./... -timeout 2700s
gofmt -w internal/cloud/cloud.go internal/cloud/cloud_test.go \
  internal/cloud/provider_local.go internal/cloud/provider_webdav.go \
  internal/cloud/provider_integrity_test.go
git diff --check
```

## Proposed upstream patch series

1. Ready for review: local-folder list error propagation plus focused positive
   and negative tests (#46 partial fix).
2. Ready for review, with an explicit performance decision: WebDAV GET/SHA-256
   fail-closed verification and compatibility tests (#44).
3. Design input required: staged WebDAV publication using MOVE, non-overwrite
   semantics, recovery, and a server compatibility matrix (#45).
4. Separate feature discussion: optional strict mounted-storage identity for
   local/NAS destinations (#46 remaining scope).

No pull request was created by this research assignment.
