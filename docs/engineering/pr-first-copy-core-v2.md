# PR: immutable, root-complete first-copy generations

## Dependency and base

This contribution is based on the stacked directed-first-copy safety work:

1. upstream provisioning holds (#43)
2. fork directed first copy (#3)
3. fork persistent named-peer fencing (#4)

Proposed branch: `feat/first-copy-core-v2`.

## Problem

A first-copy source previously hashed the ZIP it happened to produce without
proving that all intended files were captured. Snapshot creation could skip an
unreadable file, multiple Begin calls could race generation creation and
cleanup, named roots were omitted from pinned manifests, WAN block requests
had no root, and files over 32 MiB were rejected or read wholly into memory.

In a realistic failure, a live save changes or becomes unreadable during
capture, the archive is nevertheless certified, a retry serves that partial
archive, and the receiver treats it as the source generation. A concurrent
failed Begin could also abort or unpin the generation another Begin had
successfully bound.

## What changed

- Added a first-copy-only strict capture path with full uncached manifests
  before and after capture, per-file mutation checks, full ZIP readback and
  canonical root-layout digesting.
- Made source generation binding and snapshot pinning one database
  transaction. Concurrent Begin calls converge on one winner; retries and
  restart renew the same transaction and generation.
- Added database guards that prevent a referenced generation from being
  unpinned or deleted. Abort and explicit Open release ownership
  transactionally.
- Serve the complete primary and named-root manifest on LAN and WAN.
- Bind every fenced block request to protocol version, source transaction,
  generation ID, digest and branch.
- Added root-aware WAN block serving and removed live-file fallback for a
  pinned source.
- Replaced whole-entry reads and the 32 MiB entry cap with bounded streaming
  manifest hashing and block reads.
- Reject malformed paths, duplicate ZIP entries, unsafe source types,
  missing roots, bad block sizes/indices and corrupt/truncated archives.
- Advertise first-copy capability revision `2`.

## Safety guarantees

- A published source transaction has exactly one pinned immutable archive.
- Retry cannot substitute newer live content.
- A failed concurrent Begin cannot abort or unpin the winning generation.
- Missing, corrupt, mismatched or root-incomplete generations fail closed.
- Pinned first-copy serving never falls back to live files.
- LAN and WAN use the same complete root layout and generation-bound block
  protocol.
- Retention and manual snapshot operations cannot remove a referenced
  generation.

## Tests

Synthetic coverage includes source mutation across capture, unreadable or
missing roots, unsafe entries, duplicate/traversal ZIP entries, corruption,
invalid block boundaries, 40 MiB files, same-named files in different roots,
concurrent Begin, restart ownership, abort cleanup, manual deletion/unpin
refusal, missing archive refusal, and paired-daemon LAN/WAN multi-root parity.

## Remaining limitations

- Independent filesystem roots are not atomically frozen. Full pre/archive/
  post comparison detects ordinary concurrent mutation, but cannot prove away
  a change that restores identical bytes and metadata between observations.
- Per-device sync-ignore rules are rejected on a first-copy target because
  asymmetric exclusions cannot certify identical endpoints in this revision.
- Authenticated remote completion receipts and safe distributed opening are
  deliberately a separate stacked contribution.
- Hardware behavior has not been tested.

## Validation

Focused snapshot, store, P2P, daemon/API compile, paired first-copy E2E, race,
vet and full backend/E2E results are recorded in the branch validation log.
Software success is not hardware acceptance.
