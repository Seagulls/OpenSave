-- Phase and the digest taken when the lease was armed. Finish records
-- verification without releasing the hold. Activate is a later, separate
-- step. An expired row stays until the same peer rearms it or it is aborted,
-- so a retry is not a primary-key failure and a different peer cannot steal it.
ALTER TABLE game_first_copies ADD COLUMN phase TEXT NOT NULL DEFAULT 'copying';
ALTER TABLE game_first_copies ADD COLUMN content_hash TEXT NOT NULL DEFAULT '';
