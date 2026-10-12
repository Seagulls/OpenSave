-- Phase C.2: durable, pairing-authenticated handover decision protocol.
-- No record in these tables removes first-copy fences or authorises Open.
CREATE TABLE game_first_copy_handover_decisions (
    game_id TEXT PRIMARY KEY REFERENCES game_first_copies(game_id) ON DELETE CASCADE,
    local_tx_id TEXT NOT NULL,
    peer_id TEXT NOT NULL,
    source_tx_id TEXT NOT NULL,
    target_tx_id TEXT NOT NULL,
    generation_id TEXT NOT NULL,
    branch TEXT NOT NULL,
    digest TEXT NOT NULL,
    decision_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK(role IN ('source', 'target')),
    phase TEXT NOT NULL CHECK(phase IN ('offered', 'approved', 'committed')),
    local_authorized INTEGER NOT NULL DEFAULT 0 CHECK(local_authorized IN (0, 1)),
    peer_authorized INTEGER NOT NULL DEFAULT 0 CHECK(peer_authorized IN (0, 1)),
    peer_committed INTEGER NOT NULL DEFAULT 0 CHECK(peer_committed IN (0, 1)),
    created_at TEXT NOT NULL
);
CREATE TABLE game_first_copy_handover_decision_nonces (
    game_id TEXT NOT NULL REFERENCES game_first_copies(game_id) ON DELETE CASCADE,
    local_tx_id TEXT NOT NULL,
    peer_id TEXT NOT NULL,
    nonce TEXT NOT NULL,
    PRIMARY KEY(game_id, local_tx_id, peer_id, nonce)
);
CREATE TABLE game_first_copy_handover_cancelled_decisions (
    game_id TEXT NOT NULL REFERENCES game_first_copies(game_id) ON DELETE CASCADE,
    decision_id TEXT NOT NULL,
    PRIMARY KEY(game_id, decision_id)
);
