package store

import (
	"path/filepath"
	"testing"
)

// Supplemental experiment-only regression: common SQL must preserve the
// ordinary path and the atomically held path without changing CLI/API surface.
func TestSharedCreateAtomicHeldAndOrdinary(t *testing.T) {
	s := openTestStore(t)
	dir := t.TempDir()
	ordinary := Game{ID: "ordinary-1", Name: "ordinary", SavePath: filepath.Join(dir, "one"), AutoSync: true}
	if err := s.CreateGame(ordinary); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetGame(ordinary.ID)
	if err != nil || !got.AutoSync {
		t.Fatalf("ordinary creation failed: %+v, %v", got, err)
	}
	var heldCount int
	if err := s.db.Get(&heldCount, "SELECT count(*) FROM game_provisioning_holds WHERE game_id = ?", ordinary.ID); err != nil || heldCount != 0 {
		t.Fatalf("ordinary got hold: %d, %v", heldCount, err)
	}
	held := Game{ID: "held-1", Name: "held", SavePath: filepath.Join(dir, "two"), AutoSync: true}
	if _, err := s.db.Exec("INSERT INTO untracked_games(game_id,untracked_at_ms) VALUES (?,?)", held.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateHeldGame(held); err != nil {
		t.Fatal(err)
	}
	h, err := s.GetGame(held.ID)
	if err != nil || h.AutoSync {
		t.Fatalf("held create stored auto sync: %+v, %v", h, err)
	}
	if err := s.db.Get(&heldCount, "SELECT count(*) FROM game_provisioning_holds WHERE game_id = ?", held.ID); err != nil || heldCount != 1 {
		t.Fatalf("missing durable hold: %d, %v", heldCount, err)
	}
	var remaining int
	if err := s.db.Get(&remaining, "SELECT count(*) FROM untracked_games WHERE game_id = ?", held.ID); err != nil || remaining != 0 {
		t.Fatalf("tombstone not cleared: %d, %v", remaining, err)
	}
	if err := s.CreateHeldGame(Game{ID: "held-1", Name: "duplicate", SavePath: filepath.Join(dir, "three")}); err == nil {
		t.Fatal("duplicate id accepted")
	}
	if err := s.CreateHeldGame(Game{Name: "invalid", SavePath: filepath.Join(dir, "four")}); err == nil {
		t.Fatal("empty held id accepted")
	}
	var count int
	if err := s.db.Get(&count, "SELECT count(*) FROM games"); err != nil || count != 2 {
		t.Fatalf("partial rows found: %d, %v", count, err)
	}
	if err := s.db.Get(&count, "SELECT count(*) FROM branches WHERE game_id=?", "held-1"); err != nil || count != 1 {
		t.Fatalf("branch missing: %d, %v", count, err)
	}
}

func TestSharedCreateRollbackOnHoldInsertFailure(t *testing.T) {
	s := openTestStore(t)
	// Failure after the games/branches insert must roll back everything.
	if _, err := s.db.Exec("DROP TABLE game_provisioning_holds"); err != nil {
		t.Fatal(err)
	}
	err := s.CreateHeldGame(Game{ID: "must-rollback", Name: "held", SavePath: filepath.Join(t.TempDir(), "path")})
	if err == nil {
		t.Fatal("expected hold insert failure")
	}
	if _, err := s.GetGame("must-rollback"); err != ErrNotFound {
		t.Fatalf("partial game committed: %v", err)
	}
	var branches int
	if err := s.db.Get(&branches, "SELECT count(*) FROM branches WHERE game_id=?", "must-rollback"); err != nil || branches != 0 {
		t.Fatalf("partial branch committed: %d, %v", branches, err)
	}
}
