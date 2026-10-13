-- Tranche 2 foundation: a permission-neutral, signed pair-live preparation journal.
-- This table does not authorize native sync, metadata writes, watcher, AutoSync,
-- snapshot unpin, lease deletion or a third peer. See pair-live design brief.
CREATE TABLE game_first_copy_pair_live_prepares (
  game_id TEXT PRIMARY KEY,
  local_tx_id TEXT NOT NULL,
  peer_id TEXT NOT NULL,
  source_tx_id TEXT NOT NULL,
  target_tx_id TEXT NOT NULL,
  generation_id TEXT NOT NULL,
  branch TEXT NOT NULL,
  digest TEXT NOT NULL,
  decision_id TEXT NOT NULL,
  role TEXT NOT NULL CHECK(role IN ('source','target')),
  peer_ready INTEGER NOT NULL DEFAULT 0 CHECK(peer_ready IN (0,1)),
  created_at TEXT NOT NULL,
  peer_ready_at TEXT NOT NULL DEFAULT '',
  local_active INTEGER NOT NULL DEFAULT 0 CHECK(local_active IN(0,1)),
  peer_active INTEGER NOT NULL DEFAULT 0 CHECK(peer_active IN(0,1)),
  root_commitment TEXT NOT NULL DEFAULT '',
  peer_root_commitment TEXT NOT NULL DEFAULT ''
);
CREATE TABLE game_first_copy_pair_live_nonces (
  game_id TEXT NOT NULL,
  decision_id TEXT NOT NULL,
  peer_id TEXT NOT NULL,
  nonce TEXT NOT NULL,
  PRIMARY KEY(game_id,decision_id,peer_id,nonce)
);
-- A replay ledger cannot be wiped to revive a signed stale readiness.
CREATE TRIGGER pair_live_preserve_nonce
BEFORE DELETE ON game_first_copy_pair_live_nonces
BEGIN SELECT RAISE(ABORT,'pair-live nonce history is append-only'); END;
-- An old daemon cannot delete or rebind the protection while a pair-live
-- preparation exists. Retain 0047 and 0048 protections unmodified.
CREATE TRIGGER pair_live_preserve_guard
BEFORE DELETE ON game_first_copy_pair_guards
WHEN EXISTS(SELECT 1 FROM game_first_copy_pair_live_prepares WHERE game_id=OLD.game_id)
BEGIN SELECT RAISE(ABORT,'pair-live prepare retains pair guard'); END;
CREATE TRIGGER pair_live_preserve_prepare
BEFORE UPDATE ON game_first_copy_pair_live_prepares
WHEN NEW.game_id IS NOT OLD.game_id OR NEW.local_tx_id IS NOT OLD.local_tx_id
 OR NEW.peer_id IS NOT OLD.peer_id OR NEW.source_tx_id IS NOT OLD.source_tx_id
 OR NEW.target_tx_id IS NOT OLD.target_tx_id OR NEW.generation_id IS NOT OLD.generation_id
 OR NEW.branch IS NOT OLD.branch OR NEW.digest IS NOT OLD.digest
 OR NEW.decision_id IS NOT OLD.decision_id OR NEW.role IS NOT OLD.role
 OR NEW.created_at IS NOT OLD.created_at
 OR (OLD.peer_ready=1 AND NEW.peer_ready!=1)
 OR (OLD.local_active=1 AND NEW.local_active!=1)
 OR (OLD.peer_active=1 AND NEW.peer_active!=1)
 OR (OLD.root_commitment!='' AND NEW.root_commitment!=OLD.root_commitment)
 OR (OLD.peer_root_commitment!='' AND NEW.peer_root_commitment!=OLD.peer_root_commitment)
BEGIN SELECT RAISE(ABORT,'pair-live preparation identity is immutable'); END;
-- The predecessor C.3 lease-identity trigger does not cover the target's
-- source-generation reference fields. Preserve them as soon as preparation
-- is durably created; otherwise a mixed-version writer could rebind the
-- target while leaving the source-side archive identity unchanged.
CREATE TRIGGER pair_live_preserve_source_reference
BEFORE UPDATE ON game_first_copies
WHEN EXISTS(SELECT 1 FROM game_first_copy_pair_live_prepares WHERE game_id=OLD.game_id)
 AND (NEW.game_id IS NOT OLD.game_id
   OR NEW.source_tx_id IS NOT OLD.source_tx_id
   OR NEW.source_generation_id IS NOT OLD.source_generation_id
   OR NEW.source_generation_branch IS NOT OLD.source_generation_branch)
BEGIN SELECT RAISE(ABORT,'pair-live prepare protects source generation reference'); END;
CREATE TRIGGER pair_live_preserve_guard_update
BEFORE UPDATE ON game_first_copy_pair_guards
WHEN EXISTS(SELECT 1 FROM game_first_copy_pair_live_prepares WHERE game_id=OLD.game_id)
BEGIN SELECT RAISE(ABORT,'pair-live prepare protects C.3 guard identity'); END;
CREATE TRIGGER pair_live_preserve_preparation
BEFORE DELETE ON game_first_copy_pair_live_prepares
BEGIN SELECT RAISE(ABORT,'pair-live preparation is durable until explicit future recovery'); END;

-- A write scope is confined to a single BEGIN/COMMIT transaction. Every
-- first-copy protected metadata trigger checks it; callers must DELETE their
-- scope before commit. No durable permission flag authorizes old callbacks.
CREATE TABLE pair_live_write_scopes (
 game_id TEXT NOT NULL,peer_id TEXT NOT NULL,decision_id TEXT NOT NULL,
 purpose TEXT NOT NULL CHECK(purpose IN('seed','native')),
 PRIMARY KEY(game_id,peer_id,decision_id)
);

-- The predecessor 0048 triggers stay fail-closed unless a current transactional
-- scope matching the immutable decision is established in the same connection.
-- SQLite serializes writers and isolates uncommitted scope rows from others.
DROP TRIGGER IF EXISTS first_copy_metadata_primary_insert;
CREATE TRIGGER first_copy_metadata_primary_insert BEFORE INSERT ON game_peer_sync_state
 WHEN (EXISTS(SELECT 1 FROM game_provisioning_holds WHERE game_id IN(NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id))) OR
 EXISTS(SELECT 1 FROM game_first_copies WHERE game_id IN(NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id))) OR
 EXISTS(SELECT 1 FROM game_first_copy_pair_guards WHERE game_id IN(NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id)))) AND NOT (EXISTS(SELECT 1 FROM pair_live_write_scopes sc JOIN
 game_first_copy_pair_live_prepares pl ON pl.game_id=sc.game_id
 WHERE sc.game_id=NEW.game_id AND sc.peer_id=NEW.peer_id
 AND sc.decision_id=pl.decision_id AND pl.peer_id=NEW.peer_id
 AND pl.local_active=1 AND (sc.purpose='seed' OR (sc.purpose='native' AND pl.peer_active=1))
 AND pl.root_commitment!='' AND pl.local_tx_id!=''
 AND EXISTS(SELECT 1 FROM game_first_copy_pair_guards pg WHERE pg.game_id=sc.game_id AND pg.peer_id=sc.peer_id AND pg.decision_id=sc.decision_id AND pg.phase='pair_finalized')
 AND NOT EXISTS(SELECT 1 FROM game_provisioning_holds h WHERE h.game_id=sc.game_id)))
 BEGIN SELECT RAISE(ABORT,'first-copy protected sync metadata'); END;
DROP TRIGGER IF EXISTS first_copy_metadata_primary_update;
CREATE TRIGGER first_copy_metadata_primary_update BEFORE UPDATE ON game_peer_sync_state
 WHEN (EXISTS(SELECT 1 FROM game_provisioning_holds WHERE game_id IN(OLD.game_id,NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=OLD.game_id),(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id))) OR
 EXISTS(SELECT 1 FROM game_first_copies WHERE game_id IN(OLD.game_id,NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=OLD.game_id),(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id))) OR
 EXISTS(SELECT 1 FROM game_first_copy_pair_guards WHERE game_id IN(OLD.game_id,NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=OLD.game_id),(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id)))) AND NOT (EXISTS(SELECT 1 FROM pair_live_write_scopes sc JOIN
 game_first_copy_pair_live_prepares pl ON pl.game_id=sc.game_id
 WHERE sc.game_id=NEW.game_id AND sc.peer_id=NEW.peer_id
 AND sc.decision_id=pl.decision_id AND pl.peer_id=NEW.peer_id
 AND pl.local_active=1 AND (sc.purpose='seed' OR (sc.purpose='native' AND pl.peer_active=1))
 AND pl.root_commitment!='' AND pl.local_tx_id!=''
 AND EXISTS(SELECT 1 FROM game_first_copy_pair_guards pg WHERE pg.game_id=sc.game_id AND pg.peer_id=sc.peer_id AND pg.decision_id=sc.decision_id AND pg.phase='pair_finalized')
 AND NOT EXISTS(SELECT 1 FROM game_provisioning_holds h WHERE h.game_id=sc.game_id)) AND OLD.game_id=NEW.game_id AND OLD.peer_id=NEW.peer_id)
 BEGIN SELECT RAISE(ABORT,'first-copy protected sync metadata'); END;
DROP TRIGGER IF EXISTS first_copy_metadata_root_insert;
CREATE TRIGGER first_copy_metadata_root_insert BEFORE INSERT ON game_root_sync_state
 WHEN (EXISTS(SELECT 1 FROM game_provisioning_holds WHERE game_id IN(NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id))) OR
 EXISTS(SELECT 1 FROM game_first_copies WHERE game_id IN(NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id))) OR
 EXISTS(SELECT 1 FROM game_first_copy_pair_guards WHERE game_id IN(NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id)))) AND NOT (EXISTS(SELECT 1 FROM pair_live_write_scopes sc JOIN
 game_first_copy_pair_live_prepares pl ON pl.game_id=sc.game_id
 WHERE sc.game_id=NEW.game_id AND sc.peer_id=NEW.peer_id
 AND sc.decision_id=pl.decision_id AND pl.peer_id=NEW.peer_id
 AND pl.local_active=1 AND (sc.purpose='seed' OR (sc.purpose='native' AND pl.peer_active=1))
 AND pl.root_commitment!='' AND pl.local_tx_id!=''
 AND EXISTS(SELECT 1 FROM game_first_copy_pair_guards pg WHERE pg.game_id=sc.game_id AND pg.peer_id=sc.peer_id AND pg.decision_id=sc.decision_id AND pg.phase='pair_finalized')
 AND NOT EXISTS(SELECT 1 FROM game_provisioning_holds h WHERE h.game_id=sc.game_id)))
 BEGIN SELECT RAISE(ABORT,'first-copy protected sync metadata'); END;
DROP TRIGGER IF EXISTS first_copy_metadata_root_update;
CREATE TRIGGER first_copy_metadata_root_update BEFORE UPDATE ON game_root_sync_state
 WHEN (EXISTS(SELECT 1 FROM game_provisioning_holds WHERE game_id IN(OLD.game_id,NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=OLD.game_id),(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id))) OR
 EXISTS(SELECT 1 FROM game_first_copies WHERE game_id IN(OLD.game_id,NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=OLD.game_id),(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id))) OR
 EXISTS(SELECT 1 FROM game_first_copy_pair_guards WHERE game_id IN(OLD.game_id,NEW.game_id,(SELECT game_id FROM game_aliases WHERE alias_id=OLD.game_id),(SELECT game_id FROM game_aliases WHERE alias_id=NEW.game_id)))) AND NOT (EXISTS(SELECT 1 FROM pair_live_write_scopes sc JOIN
 game_first_copy_pair_live_prepares pl ON pl.game_id=sc.game_id
 WHERE sc.game_id=NEW.game_id AND sc.peer_id=NEW.peer_id
 AND sc.decision_id=pl.decision_id AND pl.peer_id=NEW.peer_id
 AND pl.local_active=1 AND (sc.purpose='seed' OR (sc.purpose='native' AND pl.peer_active=1))
 AND pl.root_commitment!='' AND pl.local_tx_id!=''
 AND EXISTS(SELECT 1 FROM game_first_copy_pair_guards pg WHERE pg.game_id=sc.game_id AND pg.peer_id=sc.peer_id AND pg.decision_id=sc.decision_id AND pg.phase='pair_finalized')
 AND NOT EXISTS(SELECT 1 FROM game_provisioning_holds h WHERE h.game_id=sc.game_id)) AND OLD.game_id=NEW.game_id AND OLD.peer_id=NEW.peer_id)
 BEGIN SELECT RAISE(ABORT,'first-copy protected sync metadata'); END;

-- The native merge base describes an exact root map and ignore-rule policy.
-- A legacy/downgraded writer cannot silently change either after preparation.
CREATE TRIGGER pair_live_lock_ignore_rules
BEFORE UPDATE OF sync_ignore ON games
WHEN EXISTS(SELECT 1 FROM game_first_copy_pair_live_prepares WHERE game_id=OLD.id)
 AND NEW.sync_ignore IS NOT OLD.sync_ignore
BEGIN SELECT RAISE(ABORT,'pair-live root comparison policy immutable'); END;
CREATE TRIGGER pair_live_lock_root_insert
BEFORE INSERT ON game_roots
WHEN EXISTS(SELECT 1 FROM game_first_copy_pair_live_prepares WHERE game_id=NEW.game_id)
BEGIN SELECT RAISE(ABORT,'pair-live root map immutable'); END;
CREATE TRIGGER pair_live_lock_root_update
BEFORE UPDATE ON game_roots
WHEN EXISTS(SELECT 1 FROM game_first_copy_pair_live_prepares WHERE game_id IN(OLD.game_id,NEW.game_id))
BEGIN SELECT RAISE(ABORT,'pair-live root map immutable'); END;
CREATE TRIGGER pair_live_lock_root_delete
BEFORE DELETE ON game_roots
WHEN EXISTS(SELECT 1 FROM game_first_copy_pair_live_prepares WHERE game_id=OLD.game_id)
BEGIN SELECT RAISE(ABORT,'pair-live root map immutable'); END;
