package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const (
	FirstCopySource = "source"
	FirstCopyTarget = "target"
)

// FirstCopy is a durable, game-scoped permission for one paired peer.
// Role source: that peer may read this game, and this device accepts no
// changes to it. Role target: this device may pull from that peer only, and
// it does not serve the game back.
type FirstCopy struct {
	GameID    string `db:"game_id" json:"gameId"`
	TxID      string `db:"tx_id" json:"txId"`
	Role      string `db:"role" json:"role"`
	PeerID    string `db:"peer_id" json:"peerId"`
	CreatedAt string `db:"created_at" json:"createdAt"`
	ExpiresAt string `db:"expires_at" json:"expiresAt"`
}

func (f FirstCopy) expired(now time.Time) bool {
	exp, err := time.Parse(time.RFC3339Nano, f.ExpiresAt)
	return err != nil || !exp.After(now)
}

// ActiveFirstCopy is the unexpired lease, or nil. A database error is
// returned. An expired row is not a permission.
func (s *Store) ActiveFirstCopy(gameID string) (*FirstCopy, error) {
	if s == nil || gameID == "" {
		return nil, nil
	}
	if err := s.provisioningFault(); err != nil {
		return nil, err
	}
	var row FirstCopy
	err := s.db.Get(&row, `SELECT game_id, tx_id, role, peer_id, created_at, expires_at FROM game_first_copies WHERE game_id = ?`, gameID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("first-copy %s: %w", gameID, err)
	}
	if row.expired(time.Now()) {
		return nil, nil
	}
	return &row, nil
}

// BeginFirstCopy records a lease for a game that is still held. The same
// role and peer returns the existing transaction. Any other parameter is
// rejected and the existing row is left alone.
func (s *Store) BeginFirstCopy(gameID, role, peerID string, ttl time.Duration) (FirstCopy, error) {
	if role != FirstCopySource && role != FirstCopyTarget {
		return FirstCopy{}, fmt.Errorf("first-copy role must be source or target")
	}
	if gameID == "" || peerID == "" {
		return FirstCopy{}, fmt.Errorf("first-copy requires a game and a peer")
	}
	if _, err := s.GetPeer(peerID); err != nil {
		return FirstCopy{}, fmt.Errorf("first-copy peer %s: %w", peerID, err)
	}
	held, err := s.ProvisioningHeld(gameID)
	if err != nil {
		return FirstCopy{}, err
	}
	if !held {
		return FirstCopy{}, fmt.Errorf("first-copy requires the game to still be held")
	}
	if existing, err := s.ActiveFirstCopy(gameID); err != nil {
		return FirstCopy{}, err
	} else if existing != nil {
		if existing.Role != role || existing.PeerID != peerID {
			return FirstCopy{}, fmt.Errorf("first-copy for %s is already bound to %s as %s", gameID, existing.PeerID, existing.Role)
		}
		return *existing, nil
	}
	if ttl <= 0 || ttl > 2*time.Hour {
		ttl = 30 * time.Minute
	}
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return FirstCopy{}, err
	}
	now := time.Now().UTC()
	row := FirstCopy{
		GameID: gameID, TxID: hex.EncodeToString(buf[:]), Role: role, PeerID: peerID,
		CreatedAt: now.Format(time.RFC3339Nano),
		ExpiresAt: now.Add(ttl).Format(time.RFC3339Nano),
	}
	if _, err := s.db.Exec(
		`INSERT INTO game_first_copies (game_id, tx_id, role, peer_id, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		row.GameID, row.TxID, row.Role, row.PeerID, row.CreatedAt, row.ExpiresAt,
	); err != nil {
		return FirstCopy{}, fmt.Errorf("insert first-copy: %w", err)
	}
	return row, nil
}

// AbortFirstCopy removes the lease when the transaction matches. The hold
// stays. A mismatch does not delete the row.
func (s *Store) AbortFirstCopy(gameID, txID string) error {
	res, err := s.db.Exec(`DELETE FROM game_first_copies WHERE game_id = ? AND tx_id = ?`, gameID, txID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no first-copy %s for %s", txID, gameID)
	}
	return nil
}
