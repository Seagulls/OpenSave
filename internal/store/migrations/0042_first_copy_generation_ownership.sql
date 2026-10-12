-- A first-copy generation is a live protocol dependency, not merely a
-- user-pinned backup. Keep it while any durable transaction references it,
-- including after expiry and across restart.
CREATE TRIGGER snapshots_refuse_first_copy_delete
BEFORE DELETE ON snapshots
WHEN EXISTS (
  SELECT 1 FROM game_first_copies
  WHERE generation_id = OLD.id
)
BEGIN
  SELECT RAISE(ABORT, 'snapshot is required by a first-copy transaction');
END;

CREATE TRIGGER snapshots_refuse_first_copy_unpin
BEFORE UPDATE OF pinned ON snapshots
WHEN NEW.pinned = 0 AND EXISTS (
  SELECT 1 FROM game_first_copies
  WHERE generation_id = OLD.id
)
BEGIN
  SELECT RAISE(ABORT, 'snapshot is required by a first-copy transaction');
END;
