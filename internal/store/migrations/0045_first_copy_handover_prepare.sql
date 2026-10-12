-- Phase C.1: explicit, restart-durable handover PREPARATION ONLY.
-- None of these tables confer permission to Open or turn on AutoSync.
-- A separate signed remote prepare and local operator intent are required.
CREATE TABLE game_first_copy_handover_local (
    game_id TEXT PRIMARY KEY REFERENCES game_first_copies(game_id) ON DELETE CASCADE,
    local_tx_id TEXT NOT NULL,
    peer_id TEXT NOT NULL,
    source_tx_id TEXT NOT NULL,
    target_tx_id TEXT NOT NULL,
    generation_id TEXT NOT NULL,
    branch TEXT NOT NULL,
    digest TEXT NOT NULL,
    role TEXT NOT NULL CHECK(role IN ('source', 'target')),
    prepared_at TEXT NOT NULL
);

CREATE TABLE game_first_copy_handover_remote (
    game_id TEXT PRIMARY KEY REFERENCES game_first_copies(game_id) ON DELETE CASCADE,
    local_tx_id TEXT NOT NULL,
    peer_id TEXT NOT NULL,
    source_tx_id TEXT NOT NULL,
    target_tx_id TEXT NOT NULL,
    generation_id TEXT NOT NULL,
    branch TEXT NOT NULL,
    digest TEXT NOT NULL,
    role TEXT NOT NULL CHECK(role IN ('source', 'target')),
    received_at TEXT NOT NULL
);

-- Signed request nonces survive a cancellation and daemon restart. Clearing a
-- local preparation must NOT make an old captured signed request reusable.
CREATE TABLE game_first_copy_handover_nonces (
    game_id TEXT NOT NULL REFERENCES game_first_copies(game_id) ON DELETE CASCADE,
    local_tx_id TEXT NOT NULL,
    peer_id TEXT NOT NULL,
    nonce TEXT NOT NULL,
    PRIMARY KEY (game_id, local_tx_id, peer_id, nonce)
);
