package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// LinkGamesAtomic carries all mapped roots, aliases, snapshots and collection
// coverage in one transaction. No partial link can delete the final tracked
// owner of a root if a later carry or history adoption fails.
func (s *Store) LinkGamesAtomic(canonicalID, aliasID, base string) (names []string, merged bool, err error) {
	if aliasID == "" || canonicalID == "" || aliasID == canonicalID {
		return nil, false, fmt.Errorf("invalid game link")
	}
	tx, err := s.db.Beginx()
	if err != nil { return nil, false, err }
	defer tx.Rollback()

	var canonical, alias Game
	if err = tx.Get(&canonical, `SELECT * FROM games WHERE id = ?`, canonicalID); err != nil {
		return nil, false, fmt.Errorf("canonical game: %w", err)
	}
	aliasErr := tx.Get(&alias, `SELECT * FROM games WHERE id = ?`, aliasID)
	if aliasErr != nil && !errors.Is(aliasErr, sql.ErrNoRows) { return nil, false, aliasErr }
	merged = aliasErr == nil

	var carries []GameRoot
	if merged {
		var roots, existing []GameRoot
		if err = tx.Select(&roots, `SELECT * FROM game_roots WHERE game_id = ? ORDER BY ordinal, name`, aliasID); err != nil { return nil, false, err }
		if err = tx.Select(&existing, `SELECT * FROM game_roots WHERE game_id = ? ORDER BY ordinal, name`, canonicalID); err != nil { return nil, false, err }
		existingNames := make(map[string]bool)
		paths := []string{canonical.SavePath}
		for _, root := range existing {
			existingNames[root.Name] = true
			if root.Mapped() { paths = append(paths, root.Path) }
		}
		for _, root := range roots {
			if existingNames[root.Name] { continue } // canonical wins on same name
			if root.Mapped() {
				for _, path := range paths {
					if overlaps(root.Path, path) {
						return nil, false, fmt.Errorf("cannot link: %s overlaps an existing save location", root.Path)
					}
				}
				paths = append(paths, root.Path)
			}
			carries = append(carries, root)
			existingNames[root.Name] = true
		}
	}

	if _, err = tx.Exec(`INSERT INTO game_aliases (alias_id, game_id, created_at_ms) VALUES (?, ?, ?)
		ON CONFLICT(alias_id) DO UPDATE SET game_id = excluded.game_id`, aliasID, canonicalID, time.Now().UnixMilli()); err != nil {
		return nil, false, err
	}
	if merged {
		if _, err = tx.Exec(`UPDATE game_aliases SET alias_name = ?, alias_save_path = ?, alias_app_id = ? WHERE alias_id = ?`,
			alias.Name, alias.SavePath, alias.AppID, aliasID); err != nil {
			return nil, false, err
		}
		for _, root := range carries {
			if _, err = tx.Exec(`INSERT INTO game_roots (game_id, name, path, ordinal)
				VALUES (?, ?, ?, COALESCE((SELECT MAX(ordinal) + 1 FROM game_roots WHERE game_id = ?), 0))`,
				canonicalID, root.Name, root.Path, canonicalID); err != nil {
				return nil, false, fmt.Errorf("carry root %q: %w", root.Name, err)
			}
			if root.Mapped() {
				if _, err = tx.Exec(`INSERT INTO game_root_mapped (game_id, name, mapped_ms) VALUES (?, ?, ?)`,
					canonicalID, root.Name, time.Now().UnixMilli()); err != nil {
					return nil, false, fmt.Errorf("map root %q: %w", root.Name, err)
				}
			}
		}
		if names, err = s.adoptHistoryTx(tx, aliasID, canonicalID, base); err != nil {
			return nil, false, err
		}
		if _, err = tx.Exec(`INSERT OR IGNORE INTO collection_games (collection_id, game_id, added_ms)
			SELECT collection_id, ?, ? FROM collection_games WHERE game_id = ?`,
			canonicalID, time.Now().UnixMilli(), aliasID); err != nil {
			return nil, false, err
		}
		if _, err = tx.Exec(`DELETE FROM games WHERE id = ?`, aliasID); err != nil {
			return nil, false, err
		}
	}
	if err = tx.Commit(); err != nil { return nil, false, err }
	return names, merged, nil
}
