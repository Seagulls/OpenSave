-- The snapshot that a source first-copy must serve. Live files may change
-- afterwards; they are not this generation. Empty means the lease has no
-- pinned generation, and a source serve must fail closed rather than read live.
ALTER TABLE game_first_copies ADD COLUMN generation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE game_first_copies ADD COLUMN generation_branch TEXT NOT NULL DEFAULT '';

-- A named peer's statement that it holds this generation. Open refuses
-- without a matching row. A local Open is not that statement.
CREATE TABLE game_first_copy_receipts (
    game_id TEXT NOT NULL,
    tx_id TEXT NOT NULL,
    peer_id TEXT NOT NULL,
    generation_id TEXT NOT NULL,
    digest TEXT NOT NULL,
    PRIMARY KEY (game_id, tx_id, peer_id)
);
