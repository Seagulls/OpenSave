package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// PairLiveRootSeed is a native per-root convergence record, NOT the composite
// first-copy archive digest. All paths are relative to this root.
type PairLiveRootSeed struct {
	Root  string   `json:"root"`
	Hash  string   `json:"hash"`
	Files []string `json:"files"`
	Dirs  []string `json:"dirs"`
}

// PairLiveMode is an immutable decision-bound game-scoped access token. It
// is only accepted from a pairing-key authenticated peer on the wire.
type PairLiveMode struct {
	GameID         string `json:"gameId"`
	PeerID         string `json:"peerId"`
	DecisionID     string `json:"decisionId"`
	RootCommitment string `json:"rootCommitment"`
	LocalActive    bool   `json:"localActive"`
	PeerActive     bool   `json:"peerActive"`
}

func (s *Store) PairLiveModeForPeer(gameID, peerID string) (PairLiveMode, error) {
	if s == nil || gameID == "" || peerID == "" {
		return PairLiveMode{}, fmt.Errorf("invalid pair-live peer")
	}
	if err := s.provisioningFault(); err != nil {
		return PairLiveMode{}, err
	}
	var p struct {
		PeerID             string `db:"peer_id"`
		DecisionID         string `db:"decision_id"`
		RootCommitment     string `db:"root_commitment"`
		PeerRootCommitment string `db:"peer_root_commitment"`
		LocalActive        bool   `db:"local_active"`
		PeerActive         bool   `db:"peer_active"`
		LocalTxID          string `db:"local_tx_id"`
	}
	err := s.db.Get(&p, `SELECT peer_id,decision_id,root_commitment,peer_root_commitment,local_active,peer_active,local_tx_id FROM game_first_copy_pair_live_prepares WHERE game_id=?`, gameID)
	if errors.Is(err, sql.ErrNoRows) {
		return PairLiveMode{}, nil
	}
	if err != nil {
		return PairLiveMode{}, err
	}
	if p.PeerID != peerID || p.DecisionID == "" || p.RootCommitment == "" || p.RootCommitment != p.PeerRootCommitment || !p.LocalActive || !p.PeerActive {
		return PairLiveMode{}, fmt.Errorf("pair-live is not jointly active for named peer")
	}
	guard, err := s.pairLiveBasisTx(s.db, gameID, p.LocalTxID)
	if err != nil || guard.DecisionID != p.DecisionID || guard.PeerID != peerID {
		return PairLiveMode{}, fmt.Errorf("pair-live guard mismatch: %w", err)
	}
	return PairLiveMode{GameID: gameID, PeerID: peerID, DecisionID: p.DecisionID, RootCommitment: p.RootCommitment, LocalActive: true, PeerActive: true}, nil
}

func (s *Store) PairLiveIsActive(gameID string) (bool, error) {
	var p int
	if err := s.db.Get(&p, `SELECT COUNT(*) FROM game_first_copy_pair_live_prepares WHERE game_id=? AND local_active=1 AND peer_active=1 AND root_commitment != '' AND root_commitment=peer_root_commitment`, gameID); err != nil {
		return false, err
	}
	return p != 0, nil
}

func validPairLiveRoots(seeds []PairLiveRootSeed) bool {
	if len(seeds) == 0 || len(seeds) > 64 || seeds[0].Root != "" {
		return false
	}
	seen := map[string]bool{}
	for _, r := range seeds {
		if seen[r.Root] || len(r.Hash) != 64 || len(r.Root) > 128 {
			return false
		}
		seen[r.Root] = true
		for _, name := range append(append([]string{}, r.Files...), r.Dirs...) {
			if name == "" || name == ".." || len(name) > 4096 {
				return false
			}
		}
	}
	return true
}

// ActivatePairLive records verified native lineage in the SAME SQLite
// transaction as local activation. No ordinary AutoSync bit is changed.
// The ephemeral scope row is inserted and removed before COMMIT; a different
// connection/callback never sees this permission and cannot borrow it.
func (s *Store) ActivatePairLive(gameID, txID, commitment string, seeds []PairLiveRootSeed) error {
	if commitment == "" || !validPairLiveRoots(seeds) {
		return fmt.Errorf("invalid pair-live native baseline")
	}
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	guard, err := s.pairLiveBasisTx(tx, gameID, txID)
	if err != nil {
		return err
	}
	p, err := readPairLivePrepare(tx, gameID)
	if err != nil {
		return err
	}
	if p == nil || !pairLiveStatusMatches(*p, guard) || !p.PeerReady {
		return fmt.Errorf("matching authenticated pair-live preparation required")
	}
	if p.PeerActive && p.PeerRootCommitment != commitment {
		return fmt.Errorf("remote live root commitment mismatch")
	}
	if p.LocalActive {
		return fmt.Errorf("pair-live already activated; do not silently reseed history")
	}
	at, err := time.Parse(time.RFC3339Nano, p.PeerReadyAt)
	if err != nil || time.Since(at) > 2*time.Minute || time.Until(at) > 5*time.Second {
		return fmt.Errorf("pair-live readiness stale; send fresh signed readiness")
	}
	old := map[string]bool{}
	for _, r := range seeds {
		if old[r.Root] {
			return fmt.Errorf("duplicate native location")
		}
		old[r.Root] = true
	}
	_, err = tx.Exec(`UPDATE game_first_copy_pair_live_prepares SET local_active=1,root_commitment=? WHERE game_id=? AND local_active=0`, commitment, gameID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO pair_live_write_scopes(game_id,peer_id,decision_id,purpose) VALUES(?,?,?,'seed')`, gameID, guard.PeerID, guard.DecisionID)
	if err != nil {
		return err
	}
	for _, r := range seeds {
		files, err := json.Marshal(r.Files)
		if err != nil {
			return err
		}
		dirs, err := json.Marshal(r.Dirs)
		if err != nil {
			return err
		}
		if r.Root == "" {
			_, err = tx.Exec(`INSERT INTO game_peer_sync_state(game_id,peer_id,last_synced_files,last_synced_dirs,agreed_hash,pushed_hash) VALUES(?,?,?,?,?,'') ON CONFLICT(game_id,peer_id) DO UPDATE SET last_synced_files=excluded.last_synced_files,last_synced_dirs=excluded.last_synced_dirs,agreed_hash=excluded.agreed_hash,pushed_hash=''`, gameID, guard.PeerID, string(files), string(dirs), r.Hash)
		} else {
			_, err = tx.Exec(`INSERT INTO game_root_sync_state(game_id,peer_id,root,last_synced_files,last_synced_dirs,agreed_hash,pushed_hash) VALUES(?,?,?,?,?,?,'') ON CONFLICT(game_id,peer_id,root) DO UPDATE SET last_synced_files=excluded.last_synced_files,last_synced_dirs=excluded.last_synced_dirs,agreed_hash=excluded.agreed_hash,pushed_hash=''`, gameID, guard.PeerID, r.Root, string(files), string(dirs), r.Hash)
		}
		if err != nil {
			return err
		}
	}
	res, err := tx.Exec(`DELETE FROM pair_live_write_scopes WHERE game_id=? AND peer_id=? AND decision_id=?`, gameID, guard.PeerID, guard.DecisionID)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err != nil || count != 1 {
		return fmt.Errorf("pair-live scoped write cleanup failed")
	}
	return tx.Commit()
}

// PairLiveWrite is the only protected-game metadata entry point. Caller must
// supply a decision ID received from the current native SyncGame context;
// delayed legacy callbacks have no context token and remain refused by 0048.
// All state writers are invoked inside this transaction (never after it).
func (s *Store) PairLiveWrite(gameID, peerID, decisionID, kind, root, value string, files, dirs []string) error {
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p, err := readPairLivePrepare(tx, gameID)
	if err != nil {
		return err
	}
	if p == nil || p.PeerID != peerID || p.DecisionID != decisionID || !p.LocalActive || !p.PeerActive || p.RootCommitment == "" || p.RootCommitment != p.PeerRootCommitment {
		return fmt.Errorf("pair-live metadata scope not active")
	}
	guard, err := s.pairLiveBasisTx(tx, gameID, p.LocalTxID)
	if err != nil || !pairLiveStatusMatches(*p, guard) {
		return fmt.Errorf("pair-live metadata guard inconsistent")
	}
	_, err = tx.Exec(`INSERT INTO pair_live_write_scopes(game_id,peer_id,decision_id,purpose) VALUES(?,?,?,'native')`, gameID, peerID, decisionID)
	if err != nil {
		return err
	}
	switch kind {
	case "agreed", "pushed":
		col := "agreed_hash"
		if kind == "pushed" {
			col = "pushed_hash"
		}
		if root == "" {
			if kind == "agreed" {
				_, err = tx.Exec(`INSERT INTO game_peer_sync_state(game_id,peer_id,last_synced_files,last_synced_dirs,agreed_hash,pushed_hash) VALUES(?,?,'[]','[]',?,'') ON CONFLICT(game_id,peer_id) DO UPDATE SET agreed_hash=excluded.agreed_hash,pushed_hash=''`, gameID, peerID, value)
			} else {
				_, err = tx.Exec(`INSERT INTO game_peer_sync_state(game_id,peer_id,last_synced_files,last_synced_dirs,pushed_hash) VALUES(?,?,'[]','[]',?) ON CONFLICT(game_id,peer_id) DO UPDATE SET pushed_hash=excluded.pushed_hash`, gameID, peerID, value)
			}
		} else {
			// Column identifier is from a hard-coded enumeration, never user input.
			query := `INSERT INTO game_root_sync_state(game_id,peer_id,root,` + col + `) VALUES(?,?,?,?) ON CONFLICT(game_id,peer_id,root) DO UPDATE SET ` + col + `=excluded.` + col
			if kind == "agreed" {
				query += `,pushed_hash=''`
			}
			_, err = tx.Exec(query, gameID, peerID, root, value)
		}
	case "lineage":
		sort.Strings(files)
		sort.Strings(dirs)
		f, fe := json.Marshal(files)
		d, de := json.Marshal(dirs)
		if fe != nil || de != nil {
			return fmt.Errorf("encode lineage: %v / %v", fe, de)
		}
		if root == "" {
			_, err = tx.Exec(`INSERT INTO game_peer_sync_state(game_id,peer_id,last_synced_files,last_synced_dirs) VALUES(?,?,?,?) ON CONFLICT(game_id,peer_id) DO UPDATE SET last_synced_files=excluded.last_synced_files,last_synced_dirs=excluded.last_synced_dirs`, gameID, peerID, string(f), string(d))
		} else {
			_, err = tx.Exec(`INSERT INTO game_root_sync_state(game_id,peer_id,root,last_synced_files,last_synced_dirs) VALUES(?,?,?,?,?) ON CONFLICT(game_id,peer_id,root) DO UPDATE SET last_synced_files=excluded.last_synced_files,last_synced_dirs=excluded.last_synced_dirs`, gameID, peerID, root, string(f), string(d))
		}
	case "synced":
		_, err = tx.Exec(`INSERT INTO game_peer_sync_state(game_id,peer_id,last_synced_files,last_synced_dirs,last_synced) VALUES(?,?,'[]','[]',?) ON CONFLICT(game_id,peer_id) DO UPDATE SET last_synced=excluded.last_synced`, gameID, peerID, value)
		if err == nil {
			_, err = tx.Exec(`UPDATE peers SET last_synced=? WHERE id=?`, value, peerID)
		}
	default:
		return fmt.Errorf("unsupported scoped metadata operation")
	}
	if err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM pair_live_write_scopes WHERE game_id=? AND peer_id=? AND decision_id=?`, gameID, peerID, decisionID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return fmt.Errorf("pair-live scoped write cleanup failed")
	}
	return tx.Commit()
}

func (s *Store) PairLiveLocalActivated(gameID string) (bool, error) {
	var active int
	err := s.db.Get(&active, `SELECT COALESCE((SELECT local_active FROM game_first_copy_pair_live_prepares WHERE game_id=?),0)`, gameID)
	return active != 0, err
}
func (s *Store) PairLiveValidateIncoming(gameID, peerID, epoch string) error {
	mode, err := s.PairLiveModeForPeer(gameID, peerID)
	if err != nil {
		return err
	}
	if !mode.LocalActive || !mode.PeerActive || mode.DecisionID == "" || epoch != mode.DecisionID {
		return fmt.Errorf("authenticated current pair-live session required")
	}
	return nil
}
