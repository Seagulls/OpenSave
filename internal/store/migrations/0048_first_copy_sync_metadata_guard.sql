-- Tranche 1: neither held first copy nor an activated/pair-finalized fence may
-- silently acquire per-peer merge-base, deletion lineage, pushed hash or
-- per-game last-synced evidence from a late peer event or queued callback.
--
-- This is a store-level fence: checking at HTTP arrival alone races a guard
-- installed after the handler read and before an async writer persists.
-- These triggers also intentionally withhold NORMAL target first-copy sync
-- lineage until the explicitly verified future release establishes a real
-- baseline. They do not block the saved payload or authenticated receipts.
-- DELETE remains available for unpair/untrack cleanup.
--
-- The alias subquery protects the canonical tracked game even if a callback
-- carries a linked game ID. A missing/unreadable table fails the SQL write.
CREATE TRIGGER first_copy_metadata_primary_insert
BEFORE INSERT ON game_peer_sync_state
WHEN EXISTS (SELECT 1 FROM game_provisioning_holds WHERE game_id IN
        (NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id)))
  OR EXISTS (SELECT 1 FROM game_first_copies WHERE game_id IN
        (NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id)))
  OR EXISTS (SELECT 1 FROM game_first_copy_pair_guards WHERE game_id IN
        (NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id)))
BEGIN SELECT RAISE(ABORT, 'first-copy protected sync metadata'); END;

CREATE TRIGGER first_copy_metadata_primary_update
BEFORE UPDATE ON game_peer_sync_state
WHEN EXISTS (SELECT 1 FROM game_provisioning_holds WHERE game_id IN
        (OLD.game_id,NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=OLD.game_id),
         (SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id)))
  OR EXISTS (SELECT 1 FROM game_first_copies WHERE game_id IN
        (OLD.game_id,NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=OLD.game_id),
         (SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id)))
  OR EXISTS (SELECT 1 FROM game_first_copy_pair_guards WHERE game_id IN
        (OLD.game_id,NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=OLD.game_id),
         (SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id)))
BEGIN SELECT RAISE(ABORT, 'first-copy protected sync metadata'); END;

CREATE TRIGGER first_copy_metadata_root_insert
BEFORE INSERT ON game_root_sync_state
WHEN EXISTS (SELECT 1 FROM game_provisioning_holds WHERE game_id IN
        (NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id)))
  OR EXISTS (SELECT 1 FROM game_first_copies WHERE game_id IN
        (NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id)))
  OR EXISTS (SELECT 1 FROM game_first_copy_pair_guards WHERE game_id IN
        (NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id)))
BEGIN SELECT RAISE(ABORT, 'first-copy protected sync metadata'); END;

CREATE TRIGGER first_copy_metadata_root_update
BEFORE UPDATE ON game_root_sync_state
WHEN EXISTS (SELECT 1 FROM game_provisioning_holds WHERE game_id IN
        (OLD.game_id,NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=OLD.game_id),
         (SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id)))
  OR EXISTS (SELECT 1 FROM game_first_copies WHERE game_id IN
        (OLD.game_id,NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=OLD.game_id),
         (SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id)))
  OR EXISTS (SELECT 1 FROM game_first_copy_pair_guards WHERE game_id IN
        (OLD.game_id,NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=OLD.game_id),
         (SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id)))
BEGIN SELECT RAISE(ABORT, 'first-copy protected sync metadata'); END;
