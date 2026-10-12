# Phase B: authenticated first-copy completion receipts (prototype)

**Status: Phase B software gate verified; not hardware/release accepted.** This
work builds on the rewritten Phase A feature commit.

## Authority and safety boundary

A Phase A row in `game_first_copy_receipts` is **not** remote proof. Its writer
accepted caller-supplied peer identity and overwrote acknowledgements. Phase B
disables that writer and removes its table from all `Open` authorization.

New receipts are game-scoped, versioned and immutable for each local
transaction. Their authenticated sender comes from the existing pairing-key
MAC on LAN or relay, never the receipt JSON. The receipt states both local
transaction IDs, source snapshot generation, active branch, complete-root
canonical manifest digest, role and *activated* phase. No anonymous, IP-only, legacy,
mispaired, replayed, cross-game, cross-generation, wrong-role or mixed-version
request authorizes a receipt. Conflicting evidence is refused rather than
replaced. Same-evidence signed re-assertion refreshes a two-minute local
readiness window only with a NEW request-authentication nonce. Receipt nonces
are recorded transactionally in SQLite and survive daemon restart; a captured,
previously accepted signed request cannot be replayed after restart to refresh
the readiness timestamp. A reboot preserves the receipt but does not make it
fresh. Ordinary sync's in-memory request nonce cache remains unchanged.

Before target-side block writes, the source generation tuple learned from the
first-copy manifest is persisted on the held target; later retries cannot
change that tuple. The target must finish with the exact full-root digest
before it can activate. The source must finish against the pinned archive.

## Explicit orchestration sequence

1. Source and target each Begin with the other's **paired node ID**, held.
2. Target pulls and verifies the source generation. Each side independently
   calls Finish and Activate, keeping AutoSync disabled and the named-peer
   fence active. Never enable AutoSync as part of this procedure.
3. Explicitly invoke `POST /api/games/{gameId}/first-copy/receipt/send` on the
   activated **target**, with `{"txId":"<local-target-transaction>"}`. The
   target sends a pairing-key signed statement to the source. The source
   verifies its own activated state and records the target's transaction ID.
4. Invoke the same local endpoint on the activated **source** with its own
   transaction ID. It now knows the target's transaction from authenticated
   evidence and sends its own statement to the target.
5. On BOTH devices inspect `GET /api/games/{gameId}/first-copy/receipt`.
   `peerReceiptVerified` must be true and `remoteReceipt` must identify the
   counterpart's role, both transaction IDs and one exact source generation.
6. **Stop here for synthetic acceptance.** Sending or receiving a receipt
   never automatically opens a fence or turns on AutoSync.

`POST /api/games/{gameId}/first-copy/open` is still a separate permission
expansion. Its local database gate now requires a fresh, authenticated remote
receipt naming the exact transaction pair, content hash and source generation.
It rechecks current local bytes, refuses concurrent sync and leaves AutoSync
off. This gate is stricter, but local Open is **not an atomic two-device
commit**. An authenticated receipt demonstrates the other device's declared
state only at that point in time. A third peer and a mid-handover peer outage
remain risks. The orchestration layer must NOT automate Open or claim normal
bidirectional sync is safe until a separately reviewed distributed handover
and real-device evidence are complete.

## Failure handling

- Lost LAN/WAN connection before a receipt: no remote evidence, both fenced.
- Signed receipt delivered but response lost: the sender retries explicitly;
  destination accepts an identical receipt and refreshes its readiness time.
- Process reboot: first-copy row and received evidence persist, no automatic
  exchange, Open, provisioning release or AutoSync enablement.
- Source save changed or target save divergent: sending/receiving the receipt
  is blocked by a fresh local content-hash check.
- Older peer: no Phase B endpoint or capability; fail closed with the existing
  hold/direction fence. Do not automatically downgrade to Phase A receipts.
- Unauthorized/unpaired sender: never accepted even from localhost; a
  request's peer ID must be proven by the existing signed transport.
- Stale receipt: explicit Open refuses after two minutes unless the sender
  explicitly reasserts the exact unchanged evidence with a fresh signed
  request.

## Validation and remaining work

The rewritten Phase B feature commit passed the FULL software gate on Go
1.26.4: focused Go tests, first-copy E2E (LAN/WAN), race detection, vet and the
full backend/E2E suite all passed. The desktop-app build was excluded because
`frontend/dist` was unavailable. This documentation-only status amendment does
not alter executable or test sources. The migration deliberately invalidates
existing experimental remote receipt rows: their nonce history cannot be
reconstructed safely, so new signed statements must be exchanged. Hardware
acceptance remains outstanding. Do not run these protocol experiments on real
saves yet.

Phase C or follow-up required before automatic Open: signed two-peer
preparation/commit, coordinated rollback/recovery semantics, explicit operator
permission, and mixed-version/three-peer fault tests. Independent remote
attestation remains a trusted assertion, not remote filesystem measurement.
