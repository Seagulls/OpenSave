-- Phase C.3: durable pair-only handover fence, independent of the original
-- first-copy lease. Deliberately retain the original lease and pinned source
-- generation as a backwards-compatible second defence for C.1/C.2 daemons.
-- Neither committed nor finalized state means global admission/AutoSync.
CREATE TABLE game_first_copy_pair_guards (
    game_id TEXT PRIMARY KEY,
    local_tx_id TEXT NOT NULL,
    peer_id TEXT NOT NULL,
    source_tx_id TEXT NOT NULL,
    target_tx_id TEXT NOT NULL,
    generation_id TEXT NOT NULL,
    branch TEXT NOT NULL,
    digest TEXT NOT NULL,
    decision_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('source','target')),
    phase TEXT NOT NULL CHECK (phase IN ('intent','offered','ready','pair_finalized','cancelled')),
    peer_ack INTEGER NOT NULL DEFAULT 0 CHECK (peer_ack IN (0,1)),
    updated_at TEXT NOT NULL
);
CREATE TABLE game_first_copy_finalize_nonces (
    game_id TEXT NOT NULL,
    decision_id TEXT NOT NULL,
    peer_id TEXT NOT NULL,
    nonce TEXT NOT NULL,
    PRIMARY KEY (game_id,decision_id,peer_id,nonce)
);
CREATE TABLE game_first_copy_finalize_vetoes (
    game_id TEXT NOT NULL,
    decision_id TEXT NOT NULL,
    PRIMARY KEY (game_id,decision_id)
);
-- In a partial finalization, losing either game or original first-copy lease
-- would make older C.1/C.2 implementations ignore the new guard. Refuse it.
CREATE TRIGGER first_copy_pair_guard_preserve_lease
BEFORE DELETE ON game_first_copies
WHEN EXISTS (SELECT 1 FROM game_first_copy_pair_guards WHERE game_id=OLD.game_id)
BEGIN SELECT RAISE(ABORT, 'pair guard requires first-copy lease'); END;
CREATE TRIGGER first_copy_pair_guard_preserve_game
BEFORE DELETE ON games
WHEN EXISTS (SELECT 1 FROM game_first_copy_pair_guards WHERE game_id=OLD.id)
BEGIN SELECT RAISE(ABORT, 'pair guard prevents game deletion'); END;
CREATE TRIGGER first_copy_pair_guard_preserve_generation
BEFORE DELETE ON snapshots
WHEN EXISTS (SELECT 1 FROM game_first_copy_pair_guards WHERE generation_id=OLD.id)
BEGIN SELECT RAISE(ABORT, 'pair guard preserves source generation'); END;
CREATE TRIGGER first_copy_pair_guard_preserve_pin
BEFORE UPDATE OF pinned ON snapshots
WHEN OLD.pinned=1 AND NEW.pinned=0 AND
 EXISTS (SELECT 1 FROM game_first_copy_pair_guards WHERE generation_id=OLD.id)
BEGIN SELECT RAISE(ABORT, 'pair guard preserves pinned source generation'); END;
-- Protect the original fence against older daemon lifecycle mutations even
-- when its Go version does not know this extra table. Only user-controlled
-- third-peer admission may intentionally remove this database guard later.
CREATE TRIGGER first_copy_pair_guard_preserve_lease_identity
BEFORE UPDATE ON game_first_copies
WHEN EXISTS (SELECT 1 FROM game_first_copy_pair_guards WHERE game_id=OLD.game_id)
 AND (NEW.tx_id IS NOT OLD.tx_id OR NEW.peer_id IS NOT OLD.peer_id OR
      NEW.role IS NOT OLD.role OR NEW.phase IS NOT OLD.phase OR
      NEW.content_hash IS NOT OLD.content_hash OR
      NEW.generation_id IS NOT OLD.generation_id)
BEGIN SELECT RAISE(ABORT, 'pair guard protects original first-copy identity'); END;
CREATE TRIGGER first_copy_pair_guard_preserve_game_identity
BEFORE UPDATE OF id,save_path,active_branch,auto_sync ON games
WHEN EXISTS (SELECT 1 FROM game_first_copy_pair_guards WHERE game_id=OLD.id)
 AND (NEW.id IS NOT OLD.id OR NEW.save_path IS NOT OLD.save_path OR
      NEW.active_branch IS NOT OLD.active_branch OR NEW.auto_sync IS NOT 0)
BEGIN SELECT RAISE(ABORT, 'pair guard protects save location, branch and AutoSync'); END;
