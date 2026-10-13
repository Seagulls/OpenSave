package store

import (
	"path/filepath"
	"testing"
	"time"
)

// Covers ordinary, held, activated, finalized and alias-addressed writes in
// the actual migrated SQLite store. This is the persistence barrier that a
// network request check alone cannot provide against async callbacks.
func TestFirstCopyBlocksLateSyncMetadataAtSQLBoundary(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "protected-metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	g := Game{ID: "game", Name: "Game", SavePath: filepath.Join(t.TempDir(), "save"), ActiveBranch: "main"}
	if err := s.CreateGame(g); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateGame(Game{ID: "other", Name: "Other", SavePath: filepath.Join(t.TempDir(), "other"), ActiveBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPeer(Peer{ID: "peer", Name: "Peer", Status: "online"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSyncState("game", "peer", []string{"existing.sav"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSyncStateForRoot("game", "peer", "profile", []string{"existing.cfg"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordGamePeerSynced("game", "peer", "2026-01-01T00:00:00.000Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO game_aliases(alias_id,game_id) VALUES('old-id','game')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO game_peer_sync_state(game_id,peer_id,last_synced_files,last_synced_dirs) VALUES('old-id','peer','[]','[]')`); err != nil {
		t.Fatal(err)
	}

	assertBlocked := func(stage string) {
		t.Helper()
		calls := []struct {
			name string
			fn   func() error
		}{
			{"primary replace", func() error { return s.SetSyncState("game", "peer", []string{"invented.sav"}, nil) }},
			{"primary agreed hash", func() error { return s.SetAgreedHash("game", "peer", "invented-base") }},
			{"root replace", func() error { return s.SetSyncStateForRoot("game", "peer", "profile", []string{"invented.cfg"}, nil) }},
			{"root agreed hash", func() error { return s.SetAgreedHashForRoot("game", "peer", "profile", "invented-root-base") }},
			{"game/peer stamp", func() error { return s.RecordGamePeerSynced("game", "peer", "2026-01-02T00:00:00.000Z") }},
			{"alias update", func() error {
				_, err := s.db.Exec(`UPDATE game_peer_sync_state SET agreed_hash='invented' WHERE game_id='old-id'`)
				return err
			}},
		}
		for _, c := range calls {
			if err := c.fn(); err == nil {
				t.Errorf("%s: %s unexpectedly allowed", stage, c.name)
			}
		}
		files, _, err := s.GetSyncState("game", "peer")
		if err != nil || len(files) != 1 || files[0] != "existing.sav" {
			t.Fatalf("%s: protected lineage changed: %v, %v", stage, files, err)
		}
		root, _, err := s.GetSyncStateForRoot("game", "peer", "profile")
		if err != nil || len(root) != 1 || root[0] != "existing.cfg" {
			t.Fatalf("%s: protected root lineage changed: %v, %v", stage, root, err)
		}
		stamp, err := s.GetPeer("peer")
		if err != nil || stamp.LastSynced.String != "2026-01-01T00:00:00.000Z" {
			t.Fatalf("%s: global peer stamp was incorrectly advanced: %+v %v", stage, stamp.LastSynced, err)
		}
		if err := s.SetSyncState("other", "peer", []string{"other.sav"}, nil); err != nil {
			t.Fatalf("%s: unrelated game blocked: %v", stage, err)
		}
	}

	if _, err := s.db.Exec(`INSERT INTO game_provisioning_holds(game_id,held_at) VALUES('game','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	assertBlocked("held")
	if _, err := s.db.Exec(`DELETE FROM game_provisioning_holds WHERE game_id='game'`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordGamePeerSynced("game", "peer", "2026-01-01T00:00:00.000Z"); err != nil {
		t.Fatalf("ordinary release was still blocked: %v", err)
	}

	if _, err := s.db.Exec(`INSERT INTO game_first_copies
      (game_id,tx_id,role,peer_id,created_at,expires_at,phase,content_hash)
      VALUES('game','tx','source','peer',?,?,'activated','digest')`,
		time.Now().UTC().Format(time.RFC3339Nano), time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	assertBlocked("activated")
	if _, err := s.db.Exec(`INSERT INTO game_first_copy_pair_guards
      (game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,generation_id,branch,
       digest,decision_id,role,phase,updated_at)
       VALUES('game','tx','peer','tx','target-tx','gen','main',
              'digest','decision','source','pair_finalized','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	assertBlocked("pair-finalized")
}
