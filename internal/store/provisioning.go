package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Provisioning hold: a game that has been recorded but is not ready to sync.
//
// The row is written in the same transaction as the game, so a crash cannot
// leave a tracked game that the next launch will sync. Existing games have
// no row and are left alone. See migrations/0038_game_provisioning_holds.sql.

// ProvisioningHeld reports whether gameID is still being configured.
// An unknown id is not held.
func (s *Store) ProvisioningHeld(gameID string) (bool, error) {
	if gameID == "" {
		return false, nil
	}
	var one int
	err := s.db.Get(&one, `SELECT 1 FROM game_provisioning_holds WHERE game_id = ?`, gameID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("provisioning hold %s: %w", gameID, err)
	}
	return true, nil
}

// ProvisioningHeldSet is every game currently held, for callers that walk
// the library and must not query once per game.
func (s *Store) ProvisioningHeldSet() (map[string]struct{}, error) {
	var ids []string
	if err := s.db.Select(&ids, `SELECT game_id FROM game_provisioning_holds`); err != nil {
		return nil, fmt.Errorf("list provisioning holds: %w", err)
	}
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out, nil
}

// CreateHeldGame inserts a game and its provisioning hold in one transaction.
// AutoSync is forced off. The local untrack tombstone is cleared in the same
// transaction: re-tracking is explicit, but it must not become visible to a
// peer before the hold exists.
func (s *Store) CreateHeldGame(g Game) error {
	if g.ID == "" {
		return fmt.Errorf("held game requires an id")
	}
	if g.ActiveBranch == "" {
		g.ActiveBranch = "main"
	}
	if g.MaxSnapshots == 0 {
		g.MaxSnapshots = 20
	}
	g.AutoSync = false

	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.NamedExec(`
		INSERT INTO games (id, name, save_path, active_branch, auto_sync, max_snapshots, max_manual_snapshots, app_id, exe_path, cover_url)
		VALUES (:id, :name, :save_path, :active_branch, :auto_sync, :max_snapshots, :max_manual_snapshots, :app_id, :exe_path, :cover_url)`,
		g); err != nil {
		return fmt.Errorf("insert game: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO branches (game_id, name) VALUES (?, ?)`, g.ID, g.ActiveBranch); err != nil {
		return fmt.Errorf("insert default branch: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO game_provisioning_holds (game_id, held_at) VALUES (?, ?)`,
		g.ID, time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("insert provisioning hold: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM untracked_games WHERE game_id = ?`, g.ID); err != nil {
		return fmt.Errorf("clear tombstone: %w", err)
	}
	return tx.Commit()
}

// ReleaseProvisioning clears the hold and turns AutoSync on, in one
// transaction. A game that is not held is left unchanged, including a game
// whose AutoSync the user turned off on purpose. The bool is whether this
// call released a hold.
func (s *Store) ReleaseProvisioning(gameID string) (bool, error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`DELETE FROM game_provisioning_holds WHERE game_id = ?`, gameID)
	if err != nil {
		return false, fmt.Errorf("release provisioning hold %s: %w", gameID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, tx.Commit()
	}
	if _, err := tx.Exec(`UPDATE games SET auto_sync = 1 WHERE id = ?`, gameID); err != nil {
		return false, fmt.Errorf("enable auto-sync %s: %w", gameID, err)
	}
	return true, tx.Commit()
}
