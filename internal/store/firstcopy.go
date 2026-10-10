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
	FirstCopySource    = "source"
	FirstCopyTarget    = "target"
	FirstCopyCopying   = "copying"
	FirstCopyVerified  = "verified"
	FirstCopyActivated = "activated"
)

// FirstCopy is a durable, game-scoped permission for one paired peer.
// Role source: that peer may read this game while it is still held, and this
// device accepts no changes to it. Role target: this device may pull from
// that peer only, and it does not serve the game back.
//
// Phase copying is the transfer. Phase verified means this device has checked
// its own digest. Neither phase releases the hold. Activate does that, in the
// same transaction that deletes the lease.
type FirstCopy struct {
	GameID      string `db:"game_id" json:"gameId"`
	TxID        string `db:"tx_id" json:"txId"`
	Role        string `db:"role" json:"role"`
	PeerID      string `db:"peer_id" json:"peerId"`
	Phase       string `db:"phase" json:"phase"`
	ContentHash string `db:"content_hash" json:"contentHash"`
	CreatedAt   string `db:"created_at" json:"createdAt"`
	ExpiresAt   string `db:"expires_at" json:"expiresAt"`
}

func (f FirstCopy) expired(now time.Time) bool {
	exp, err := time.Parse(time.RFC3339Nano, f.ExpiresAt)
	return err != nil || !exp.After(now)
}

type rowQuery interface {
	Get(any, string, ...any) error
}

func (s *Store) readFirstCopy(q rowQuery, gameID string) (*FirstCopy, error) {
	var row FirstCopy
	err := q.Get(&row, `SELECT game_id, tx_id, role, peer_id, phase, content_hash, created_at, expires_at FROM game_first_copies WHERE game_id = ?`, gameID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("first-copy %s: %w", gameID, err)
	}
	return &row, nil
}

// ActiveFirstCopy is the unexpired lease, or nil. A database error is
// returned. An expired row is not a permission, and it is not deleted here.
func (s *Store) ActiveFirstCopy(gameID string) (*FirstCopy, error) {
	if s == nil || gameID == "" {
		return nil, nil
	}
	if err := s.provisioningFault(); err != nil {
		return nil, err
	}
	row, err := s.readFirstCopy(s.db, gameID)
	if err != nil || row == nil || row.expired(time.Now()) {
		return nil, err
	}
	return row, nil
}

// BoundFirstCopy is the direction fence. An activated lease does not expire
// into open access: other peers stay refused until OpenFirstCopy.
func (s *Store) BoundFirstCopy(gameID string) (*FirstCopy, error) {
	if s == nil || gameID == "" {
		return nil, nil
	}
	if err := s.provisioningFault(); err != nil {
		return nil, err
	}
	row, err := s.readFirstCopy(s.db, gameID)
	if err != nil || row == nil {
		return nil, err
	}
	if row.Phase == FirstCopyActivated || !row.expired(time.Now()) {
		return row, nil
	}
	return nil, nil
}

// BeginFirstCopy records a lease for a game that is still held. The hold
// check and the insert are one transaction. The same role and peer returns
// the existing transaction, or replaces an expired row with a new one. A
// different peer is rejected, including when the old row has expired.
func (s *Store) BeginFirstCopy(gameID, role, peerID, contentHash string, ttl time.Duration) (FirstCopy, error) {
	if role != FirstCopySource && role != FirstCopyTarget {
		return FirstCopy{}, fmt.Errorf("first-copy role must be source or target")
	}
	if gameID == "" || peerID == "" {
		return FirstCopy{}, fmt.Errorf("first-copy requires a game and a peer")
	}
	if _, err := s.GetPeer(peerID); err != nil {
		return FirstCopy{}, fmt.Errorf("first-copy peer %s: %w", peerID, err)
	}
	if ttl <= 0 || ttl > 2*time.Hour {
		ttl = 30 * time.Minute
	}
	tx, err := s.db.Beginx()
	if err != nil {
		return FirstCopy{}, err
	}
	defer tx.Rollback()

	held, err := txHeld(tx, gameID)
	if err != nil {
		return FirstCopy{}, err
	}
	if !held {
		return FirstCopy{}, fmt.Errorf("first-copy requires the game to still be held")
	}
	existing, err := s.readFirstCopy(tx, gameID)
	if err != nil {
		return FirstCopy{}, err
	}
	now := time.Now().UTC()
	if existing != nil && !existing.expired(now) {
		if existing.Role != role || existing.PeerID != peerID {
			return FirstCopy{}, fmt.Errorf("first-copy for %s is already bound to %s as %s", gameID, existing.PeerID, existing.Role)
		}
		return *existing, nil
	}
	if existing != nil && (existing.Role != role || existing.PeerID != peerID) {
		return FirstCopy{}, fmt.Errorf("expired first-copy for %s is still bound to %s; abort %s before choosing another peer", gameID, existing.PeerID, existing.TxID)
	}
	if existing != nil {
		if _, err := tx.Exec(`DELETE FROM game_first_copies WHERE game_id = ? AND tx_id = ?`, gameID, existing.TxID); err != nil {
			return FirstCopy{}, err
		}
	}
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return FirstCopy{}, err
	}
	row := FirstCopy{
		GameID: gameID, TxID: hex.EncodeToString(buf[:]), Role: role, PeerID: peerID,
		Phase: FirstCopyCopying, ContentHash: contentHash,
		CreatedAt: now.Format(time.RFC3339Nano),
		ExpiresAt: now.Add(ttl).Format(time.RFC3339Nano),
	}
	if _, err := tx.Exec(
		`INSERT INTO game_first_copies (game_id, tx_id, role, peer_id, phase, content_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		row.GameID, row.TxID, row.Role, row.PeerID, row.Phase, row.ContentHash, row.CreatedAt, row.ExpiresAt,
	); err != nil {
		return FirstCopy{}, fmt.Errorf("insert first-copy: %w", err)
	}
	return row, tx.Commit()
}

func txHeld(tx rowQuery, gameID string) (bool, error) {
	var one int
	err := tx.Get(&one, `SELECT 1 FROM game_provisioning_holds WHERE game_id = ?`, gameID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("provisioning hold %s: %w", gameID, err)
	}
	return true, nil
}

// AbortFirstCopy removes the lease when the transaction matches, including
// an expired row. The hold stays. A mismatch does not delete the row.
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

// VerifyFirstCopy records that this device's digest still matches. It does
// not delete the lease or the hold. A repeat with the same transaction and
// digest is a no-op. A mismatch or a missing lease leaves both in place.
func (s *Store) VerifyFirstCopy(gameID, txID, digest string) (FirstCopy, error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return FirstCopy{}, err
	}
	defer tx.Rollback()
	row, err := s.readFirstCopy(tx, gameID)
	if err != nil {
		return FirstCopy{}, err
	}
	if row == nil || row.expired(time.Now()) || row.TxID != txID {
		return FirstCopy{}, fmt.Errorf("first-copy transaction does not match")
	}
	if row.Phase == FirstCopyVerified && row.ContentHash == digest {
		return *row, nil
	}
	if row.Role == FirstCopySource && row.ContentHash != digest {
		return FirstCopy{}, fmt.Errorf("source changed since the first copy was armed")
	}
	if row.Role == FirstCopyTarget && digest == "" {
		return FirstCopy{}, fmt.Errorf("target verification requires the source digest")
	}
	if _, err := tx.Exec(`UPDATE game_first_copies SET phase = ?, content_hash = ? WHERE game_id = ? AND tx_id = ?`,
		FirstCopyVerified, digest, gameID, txID); err != nil {
		return FirstCopy{}, err
	}
	row.Phase = FirstCopyVerified
	row.ContentHash = digest
	return *row, tx.Commit()
}

// ActivateFirstCopy releases the hold and marks the lease activated, in one
// transaction. The lease stays, so only the named peer may read. OpenFirstCopy
// is what removes that fence. An expired verified lease cannot activate.
func (s *Store) ActivateFirstCopy(gameID, txID string, enableAutoSync bool) (bool, error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	row, err := s.readFirstCopy(tx, gameID)
	if err != nil {
		return false, err
	}
	if row == nil || row.TxID != txID || row.Phase != FirstCopyVerified || row.expired(time.Now()) {
		return false, fmt.Errorf("first-copy is not verified or has expired")
	}
	held, err := txHeld(tx, gameID)
	if err != nil {
		return false, err
	}
	if !held {
		return false, fmt.Errorf("first-copy cannot activate without its provisioning hold")
	}
	if _, err := tx.Exec(`UPDATE game_first_copies SET phase = ? WHERE game_id = ? AND tx_id = ?`, FirstCopyActivated, gameID, txID); err != nil {
		return false, err
	}
	res, err := tx.Exec(`DELETE FROM game_provisioning_holds WHERE game_id = ?`, gameID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	auto := 0
	if enableAutoSync {
		auto = 1
	}
	if _, err := tx.Exec(`UPDATE games SET auto_sync = ? WHERE id = ?`, auto, gameID); err != nil {
		return false, err
	}
	return n > 0, tx.Commit()
}

// OpenFirstCopy removes an activated direction fence. Until this call, a
// paired third peer cannot read the game. It does not change AutoSync.
func (s *Store) OpenFirstCopy(gameID, txID string) error {
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := s.readFirstCopy(tx, gameID)
	if err != nil {
		return err
	}
	if row == nil || row.TxID != txID || row.Phase != FirstCopyActivated {
		return fmt.Errorf("first-copy is not activated")
	}
	if _, err := tx.Exec(`DELETE FROM game_first_copies WHERE game_id = ? AND tx_id = ?`, gameID, txID); err != nil {
		return err
	}
	return tx.Commit()
}
