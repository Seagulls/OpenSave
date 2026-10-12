# Phase C.1: signed, durable two-peer handover preparation (prototype)

**Status:** opt-in synthetic candidate only. No automatic Open, AutoSync, release,
rollback, retry, or live-device acceptance. Requires the Phase B first-copy
receipt protocol and the entire preceding stacked first-copy branch history.

## Problem

An authenticated Phase B receipt demonstrates that a paired peer **reported**
an activated copy at a point in time. It is not a two-phase commit, evidence of
ongoing filesystem quiescence, or permission to remove a game-scoped direction
fence. A simple controller that calls Open on each device in sequence risks
one device becoming accessible to a third peer before the other device opens.

Phase C.1 records explicit **local intent** on both devices, exchanges matching
**signed remote preparation** statements on LAN or WAN, and provides an
operator-visible status. Every step retains the first-copy fence and keeps
AutoSync disabled. It deliberately does **not** commit or open either device.

## Operator contract

Both sides must already be in `activated`, with no local sync running, matching
save contents, and fresh Phase B authenticated receipts naming the same source
and target transaction IDs and pinned archive. All calls below are local
operator/dashboard calls, not peer-facing endpoints.

| Endpoint | Effect |
| --- | --- |
| `POST /api/games/{id}/first-copy/handover/prepare` with `{ "txId": "<local-tx>" }` | Explicit operator intent on THIS device after digest + Phase B receipt checks. Durable intent, no permission expansion. |
| Same call on the counterpart | Independently records its operator intent. Neither device can attest for the other. |
| `POST /api/games/{id}/first-copy/handover/send` with `{ "txId": "<local-tx>" }` | Sends this device's locally derived preparation to its named paired peer using signed P2P LAN or WAN. Both sides must have prepared locally first. |
| `GET /api/games/{id}/first-copy/handover` | Computes current local/peer preparation evidence and readiness for **operator review only**, never permission to Open. |
| `DELETE /api/games/{id}/first-copy/handover` with `{ "txId": "<local-tx>" }` | Cancels the local intent and locally recorded peer preparation. The underlying first-copy fence and nonce history remain. It does not remotely revoke the counterpart's previous observation; it expires there. |

For both devices, call Prepare, then Send on each, then inspect Status on each.
Only `readyForOperatorReview=true` on both may justify further **manual review**;
it is **not** a safe-to-open certificate. The Phase B two-minute freshness window
applies to both the authenticated remote receipt and local/remote preparation.
A process restart keeps evidence but cannot refresh its age or open the fence.

## Signed peer protocol

`POST /api/p2p/first-copy/handover/prepare/{gameId}` on LAN, or signed WAN
`/first-copy/handover/prepare/{gameId}`, receives a versioned assertion naming
`gameId`, `sourceTxId`, `targetTxId`, `generationId`, `branch`, `digest`, sender
`role=source|target`, and `stage=prepared`. The authenticated sender ID and
nonce come from the existing pairing MAC, not JSON or a source IP. Before
writing anything, the receiver checks its activated fence, current local
digest, nonbusy sync state, recent authenticated Phase B counterpart receipt,
**its own current local operator intent**, complementary role, bound generation
and transaction IDs. Reuse of any signed prepare request nonce is permanently
refused for that transaction, including after restart and local cancellation.

Conflicting remote evidence cannot replace an existing preparation. Identical
evidence may be reasserted using a fresh authenticated nonce and renewed local
operator intent. Old or unsigned peers fail closed. A network outage leaves
both devices' native first-copy fences intact; lost responses are safe to
retry with a new signed request nonce. No background resend is enabled.

**Capability:** `firstCopy: 4`. Older peers lacking the signed prepare endpoint
cannot satisfy Phase C.1; never downgrade to local claims or bypass the hold.

## Persistence and safety

Migration `0045_first_copy_handover_prepare.sql` adds a durable operator-intent
row, a separately authenticated peer-prepare row, and a durable replay-nonce
ledger per game/first-copy transaction. None of these tables are consulted by
ordinary sync or native Open; they cannot modify a hold, fence, snapshot pin,
game identity, branch or AutoSync setting.

> [!WARNING]
> `readyForOperatorReview` is **not** a distributed commit decision. Current
> Open remains a separate local permission-expanding operation, and two calls
> to Open cannot be made atomically across devices. The peer may disconnect,
> restart, or mutate save files between preparation and Open, and another
> paired peer could access the first opened side. This implementation MUST NOT
> automatically Open or enable AutoSync, nor be used to test production saves.
>
> A follow-up phase must define a **durable commit authorization/abort protocol**
> and explicitly handle a crash after the first fence has opened, recovery,
> freshness refresh, peer replacement, third-peer access and exact operator
> consent. A distributed database transaction is not assumed available.

## Tests and acceptance

The focused store tests exercise absent or stale receipt, local intent,
wrong peer, wrong digest, durable nonce replay, preparation cancel/renew, and
fence/AutoSync invariants. The paired-daemon E2E test exercises both signed
LAN and relay/WAN preparations, one-sided prepare refusal, unsigned local
forgery rejection, explicit cancellation, and non-opening lifecycle checks.

Run the local software gate on the exact isolated branch HEAD before
considering merge; preserve the existing Phase B full software gate as
separate evidence. No hardware acceptance is claimed.

## Explicitly deferred

No automatically coordinated Open; no phase-C commit; no remote filesystem
measurement; no automatic retry/rollback; no enabling background sync; no
NAS, third-device restoration or mixed-version migration acceptance.
