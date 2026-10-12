-- Phase B security hardening: authenticated receipt replay protection must
-- survive daemon restart. A per-process nonce cache does not suffice when a
-- signed receipt re-assertion refreshes a remote readiness timestamp.
-- The row belongs to one local first-copy transaction; it is removed with
-- the fence on explicit Open/Abort when foreign keys are enforced.
CREATE TABLE game_first_copy_authenticated_nonces (
    game_id TEXT NOT NULL REFERENCES game_first_copies(game_id) ON DELETE CASCADE,
    local_tx_id TEXT NOT NULL,
    peer_id TEXT NOT NULL,
    nonce TEXT NOT NULL,
    PRIMARY KEY (game_id, local_tx_id, peer_id, nonce)
);
-- Previously accepted Phase B receipts have no durable nonce provenance.
-- Fail closed on upgrade; require both peers to issue fresh statements.
DELETE FROM game_first_copy_remote_receipts;
