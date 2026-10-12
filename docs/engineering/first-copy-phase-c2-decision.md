# Phase C.2: durable, signed two-peer decision ledger

**Status: synthetic-only candidate, no hardware acceptance.** This builds on
Phase C.1 preparation and Phase B authenticated receipts. The source controls
the decision identity, but cannot record a decision until the target has
explicitly approved and transmitted a signed approval. The target never
records a decision without the signed source commit. Confirmation is separately
signed back to the source. Every transition is durable and retryable by an
explicit operator request.

**A commit decision is NOT an atomic two-device Open.** Both devices retain
first-copy direction fences and AutoSync off at every stage. Existing local
Open is refused while Phase C preparation or decision/tombstone state exists.

## Explicit operator flow

1. Complete Phase B and Phase C.1 on both devices. Both must be `activated`,
   have matching full-root digests, fresh authenticated receipts and signed
   counterpart preparation, with no active sync or pause.
2. On the **source**, `POST /api/games/{id}/first-copy/handover/decision/offer`
   with `{ "txId": "<source-tx>" }`. It returns a random, durable 128-bit
   `decisionId`. Repeating it returns the same decision, never an unrelated one.
3. Source sends `{ "txId": "<source-tx>", "stage": "offer" }` to
   `/api/games/{id}/first-copy/handover/decision/send`. This uses the signed
   LAN/WAN P2P decision route. Target persists it but is not authorized.
4. On **target**, `POST /api/games/{id}/first-copy/handover/decision/authorize`
   with `{ "txId": "<target-tx>", "decisionId": "<id>" }`. Only then can the
   target explicitly `send` stage `approval` to source.
5. Source receives target's signed approval. Source operator independently
   invokes local `authorize` for the same decision ID. This records a durable
   source commit decision, **but does not change the first-copy fence**.
6. Source `send`s stage `commit`. Target receives it only if its own signed
   source-offered decision was explicitly authorized. Target records `committed`.
7. Target `send`s stage `confirm`. Source durably records the target confirmation.
8. Inspect `GET /api/games/{id}/first-copy/handover/decision` on BOTH sides.
   `decisionRecorded` must be true on both; source also reports
   `peerCommitConfirmed=true`. **`openAuthorized` remains false** in all states.

The receipt, preparation and content checks are repeated at each operation.
Their two-minute windows still apply. Stale evidence must be refreshed
explicitly. The decision ID never changes during a retry of the same decision.

## Security and failure contracts

- Every peer message names the exact game, both transaction IDs, generation,
  branch, digest, signed sender role, decision ID and stage. No arbitrary
  client-selected peer or JSON peer identity is accepted.
- LAN and relay/WAN use pairing-key authenticated request/response transport.
  Receipt MAC nonces, preparation nonces and decision nonces have independent,
  persistent SQLite replay protections.
- Wrong peer, role, game, generation, digest, stale preparation, unsigned local
  HTTP, malformed payload and duplicate signed nonce fail closed.
- A lost response can be explicitly retried using a new signed transport nonce.
  The receiving peer may already have applied the same statement; valid
  idempotent reassertion does not create a second decision.
- Before local commitment, cancellation creates a durable tombstone for that
  decision ID. A delayed old signed offer/approval cannot revive it.
- Cancelling Phase C.1 preparation vetoes any uncommitted decision transaction;
  committed decisions instead require coordinated recovery, NOT unilateral
  deletion or rollback.
- A restarted daemon retains committed-but-unconfirmed state even when
  receipts have expired. A status query is diagnostic, never permission.
- Legacy Open cannot bypass local preparation, active decision or cancellation
  tombstone. No API implemented here removes either first-copy fence.

> [!WARNING]
> **No atomic distributed Open or safe third-peer admission is implemented.**
> A source decision can be committed while target delivery remains pending.
> Neither side may use that durable fact as permission to expand serving.
> This deliberately leaves the game fenced until a separately audited
> recovery/finalization protocol proves how to avoid one-sided access.
>
> A cancellation is local; it is not yet a signed remote revocation message.
> A peer may temporarily retain its previous preparation until it expires.
> Do not use these experiments on real save payloads or mix with stock daemons.

## Tests

Source+target LAN/WAN end-to-end protocol with independent approvals, unsigned
forgery refusal, confirmation, no AutoSync and both fences retained; SQLite
negative cases for missing target consent, replay after restart, stale receipt,
cancellation tombstone and legacy Open refusal. Exact-head focused/race/vet
and full backend E2E gates must pass before opening a draft PR.

## Deferred

No automatic Open, coordinated permission expansion, signed remote revocation,
atomic multi-peer admission fence, outage-safe finalization, mixed-version
fallback, game-data installation or hardware acceptance. Phase C.3 must also
address recovery if the source committed but target is unreachable or rejected
the decision, and distinguish *recorded decision* from *globally open*.
