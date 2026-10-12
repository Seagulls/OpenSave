-- Phase B: the legacy game_first_copy_receipts rows were not authenticated.
-- They must never confer Open permission. This table is populated only after
-- authentication with the pairing key on the P2P request.
-- The target binds the source generation before the first save-file write.
ALTER TABLE game_first_copies ADD COLUMN source_tx_id TEXT NOT NULL DEFAULT '';
ALTER TABLE game_first_copies ADD COLUMN source_generation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE game_first_copies ADD COLUMN source_generation_branch TEXT NOT NULL DEFAULT '';

CREATE TABLE game_first_copy_remote_receipts (
    game_id TEXT NOT NULL,
    local_tx_id TEXT NOT NULL,
    peer_id TEXT NOT NULL,
    source_tx_id TEXT NOT NULL,
    target_tx_id TEXT NOT NULL,
    generation_id TEXT NOT NULL,
    branch TEXT NOT NULL,
    digest TEXT NOT NULL,
    role TEXT NOT NULL CHECK(role IN ('source', 'target')),
    phase TEXT NOT NULL CHECK(phase = 'activated'),
    received_at TEXT NOT NULL,
    PRIMARY KEY(game_id, local_tx_id)
);
