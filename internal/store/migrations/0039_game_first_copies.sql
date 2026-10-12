-- Opt-in, one game, one paired peer, one direction.
--
-- A provisioning hold stops every transfer. This row is the only exception:
-- the named peer may read a source, or this device may pull from the named
-- peer. It is not AutoSync, and it is not a release. No row means the feature
-- is off. Older builds ignore the table. A device that does not understand it
-- must be kept held; this table cannot make an old binary enforce the rule.
CREATE TABLE game_first_copies (
    game_id TEXT PRIMARY KEY REFERENCES games(id) ON DELETE CASCADE,
    tx_id TEXT NOT NULL,
    role TEXT NOT NULL,
    peer_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
