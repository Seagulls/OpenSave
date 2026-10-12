package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// FirstCopyReceiptV1 binds a signed peer's statement to precisely one pair of
// independently allocated local transactions and the immutable source archive.
// The HTTP or relay authentication, NOT any user-supplied JSON field, supplies
// the sender's peer ID. No receipt changes the hold, fence or AutoSync setting.
const FirstCopyReceiptV1 = 1

type FirstCopyReceipt struct {
	Version      int    `json:"version"`
	GameID       string `json:"gameId"`
	SourceTxID   string `json:"sourceTxId"`
	TargetTxID   string `json:"targetTxId"`
	GenerationID string `json:"generationId"`
	Branch       string `json:"branch"`
	Digest       string `json:"digest"`
	Role         string `json:"role"`
	Phase        string `json:"phase"`
}

type remoteReceiptRow struct {
	GameID       string `db:"game_id"`
	LocalTxID    string `db:"local_tx_id"`
	PeerID       string `db:"peer_id"`
	SourceTxID   string `db:"source_tx_id"`
	TargetTxID   string `db:"target_tx_id"`
	GenerationID string `db:"generation_id"`
	Branch       string `db:"branch"`
	Digest       string `db:"digest"`
	Role         string `db:"role"`
	Phase        string `db:"phase"`
	ReceivedAt   string `db:"received_at"`
}

func validRemoteReceipt(r FirstCopyReceipt) bool {
	if r.Version != FirstCopyReceiptV1 || r.GameID == "" ||
		r.SourceTxID == "" || r.TargetTxID == "" || r.GenerationID == "" ||
		r.Branch == "" || len(r.Digest) > 16384 || !strings.HasPrefix(r.Digest, "primary:") || r.Phase != FirstCopyActivated ||
		(r.Role != FirstCopySource && r.Role != FirstCopyTarget) {
		return false
	}
	for _, value := range []string{r.GameID, r.SourceTxID, r.TargetTxID, r.GenerationID, r.Branch} {
		if len(value) > 256 {
			return false
		}
	}
	return true
}

// BindFirstCopyTargetGeneration stores the pinned source identity learned
// from a verified multi-root manifest BEFORE any target writes. A retry may
// reuse, but may never replace, that identity. The target's own tx ID remains
// independent of the source tx ID, so collisions cannot silently succeed.
func (s *Store) BindFirstCopyTargetGeneration(gameID, targetTxID, peerID, sourceTxID, generationID, branch string) error {
	if gameID == "" || targetTxID == "" || peerID == "" || sourceTxID == "" ||
		generationID == "" || branch == "" {
		return fmt.Errorf("first-copy source generation identity is incomplete")
	}
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := s.readFirstCopy(tx, gameID)
	if err != nil {
		return err
	}
	if row == nil || row.Role != FirstCopyTarget || row.TxID != targetTxID ||
		row.PeerID != peerID || row.Phase != FirstCopyCopying || row.expired(time.Now()) {
		return fmt.Errorf("first-copy target transaction is not an active copying lease")
	}
	held, err := txHeld(tx, gameID)
	if err != nil {
		return err
	}
	if !held {
		return fmt.Errorf("first-copy target provisioning hold is missing")
	}
	if row.SourceTxID != "" || row.SourceGenerationID != "" || row.SourceGenerationBranch != "" {
		if row.SourceTxID != sourceTxID || row.SourceGenerationID != generationID ||
			row.SourceGenerationBranch != branch {
			return fmt.Errorf("first-copy source generation changed on retry")
		}
		return nil
	}
	res, err := tx.Exec(`UPDATE game_first_copies
        SET source_tx_id = ?, source_generation_id = ?, source_generation_branch = ?
        WHERE game_id = ? AND tx_id = ? AND role = ? AND phase = ?
          AND source_tx_id = '' AND source_generation_id = '' AND source_generation_branch = ''`,
		sourceTxID, generationID, branch, gameID, targetTxID, FirstCopyTarget, FirstCopyCopying)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("first-copy source generation was concurrently bound")
	}
	return tx.Commit()
}

// AcceptFirstCopyRemoteReceipt is ONLY callable after request authentication
// succeeded. Peer identity comes from the signed LAN/WAN transport context.
// Conflicting, stale, wrong-generation and wrong-role receipts never overwrite
// previously accepted evidence, including after a reboot or lost reply.
// authenticatedNonce is the nonce in the already-verified pairing MAC.
// The caller MUST obtain it from signed LAN headers or authenticated WAN
// envelope, never from the receipt JSON. Every accepted nonce is durable
// before a receipt can refresh readiness.
func (s *Store) AcceptFirstCopyRemoteReceipt(gameID, signedPeerID, authenticatedNonce string, receipt FirstCopyReceipt) error {
	if !validRemoteReceipt(receipt) || receipt.GameID != gameID || signedPeerID == "" ||
		authenticatedNonce == "" || len(authenticatedNonce) > 256 {
		return fmt.Errorf("invalid first-copy remote receipt")
	}
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := s.readFirstCopy(tx, gameID)
	if err != nil {
		return err
	}
	if row == nil || row.Phase != FirstCopyActivated || row.PeerID != signedPeerID ||
		row.ContentHash != receipt.Digest || row.Role == receipt.Role {
		return fmt.Errorf("receipt does not match the activated local first-copy fence")
	}
	if row.Role == FirstCopySource {
		if receipt.SourceTxID != row.TxID || receipt.GenerationID != row.GenerationID ||
			receipt.Branch != row.GenerationBranch {
			return fmt.Errorf("target receipt does not identify the pinned source generation")
		}
	} else {
		if receipt.TargetTxID != row.TxID || receipt.SourceTxID != row.SourceTxID ||
			receipt.GenerationID != row.SourceGenerationID ||
			receipt.Branch != row.SourceGenerationBranch || row.SourceTxID == "" {
			return fmt.Errorf("source receipt does not match the target's immutable source binding")
		}
	}
	// The ordinary request nonce cache is in memory. Persist receipt nonces
	// in the SAME transaction as the receipt so a captured valid MAC cannot
	// be replayed after a daemon restart to renew readiness. A re-send with
	// a fresh authenticated nonce is still idempotent and refreshes readiness.
	if _, err := tx.Exec(`INSERT INTO game_first_copy_authenticated_nonces
        (game_id, local_tx_id, peer_id, nonce) VALUES (?, ?, ?, ?)`,
		gameID, row.TxID, signedPeerID, authenticatedNonce); err != nil {
		return fmt.Errorf("first-copy receipt authentication nonce was already used or could not be recorded: %w", err)
	}
	_, err = tx.Exec(`INSERT OR IGNORE INTO game_first_copy_remote_receipts
        (game_id, local_tx_id, peer_id, source_tx_id, target_tx_id, generation_id,
         branch, digest, role, phase, received_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, gameID, row.TxID, signedPeerID,
		receipt.SourceTxID, receipt.TargetTxID, receipt.GenerationID,
		receipt.Branch, receipt.Digest, receipt.Role, receipt.Phase,
		time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	var got remoteReceiptRow
	err = tx.Get(&got, `SELECT game_id, local_tx_id, peer_id, source_tx_id,
         target_tx_id, generation_id, branch, digest, role, phase, received_at
         FROM game_first_copy_remote_receipts WHERE game_id = ? AND local_tx_id = ?`,
		gameID, row.TxID)
	if err != nil {
		return err
	}
	if got.PeerID != signedPeerID || got.SourceTxID != receipt.SourceTxID ||
		got.TargetTxID != receipt.TargetTxID || got.GenerationID != receipt.GenerationID ||
		got.Branch != receipt.Branch || got.Digest != receipt.Digest ||
		got.Role != receipt.Role || got.Phase != receipt.Phase {
		return fmt.Errorf("different authenticated receipt already recorded for transaction")
	}
	// A fresh, signed re-assertion of identical evidence renews its readiness
	// timestamp. The identity can NEVER be overwritten by a conflicting one.
	_, err = tx.Exec(`UPDATE game_first_copy_remote_receipts SET received_at = ?
	    WHERE game_id = ? AND local_tx_id = ?`,
		time.Now().UTC().Format(time.RFC3339Nano), gameID, row.TxID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// RemoteFirstCopyReceipt returns authenticated evidence only. The old
// game_first_copy_receipts table is ignored for all Phase B decisions.
func (s *Store) RemoteFirstCopyReceipt(gameID, txID string) (*FirstCopyReceipt, error) {
	if s == nil || gameID == "" || txID == "" {
		return nil, fmt.Errorf("first-copy receipt query is incomplete")
	}
	var row remoteReceiptRow
	err := s.db.Get(&row, `SELECT game_id, local_tx_id, peer_id, source_tx_id,
         target_tx_id, generation_id, branch, digest, role, phase, received_at
         FROM game_first_copy_remote_receipts WHERE game_id = ? AND local_tx_id = ?`, gameID, txID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &FirstCopyReceipt{
		Version: FirstCopyReceiptV1, GameID: row.GameID, SourceTxID: row.SourceTxID,
		TargetTxID: row.TargetTxID, GenerationID: row.GenerationID,
		Branch: row.Branch, Digest: row.Digest, Role: row.Role, Phase: row.Phase,
	}, nil
}

// OutboundFirstCopyReceipt derives all facts from local durable state. The
// target can speak first after finishing and activation. The source can only
// sign a receipt once the target's authenticated acknowledgement has pinned
// the target transaction ID. This makes retries deterministic.
func (s *Store) OutboundFirstCopyReceipt(gameID, txID string) (FirstCopyReceipt, string, error) {
	row, err := s.RecordedFirstCopy(gameID)
	if err != nil {
		return FirstCopyReceipt{}, "", err
	}
	if row == nil || row.TxID != txID || row.Phase != FirstCopyActivated {
		return FirstCopyReceipt{}, "", fmt.Errorf("receipt requires an activated first-copy transaction")
	}
	receipt := FirstCopyReceipt{
		Version: FirstCopyReceiptV1, GameID: gameID, Digest: row.ContentHash,
		Role: row.Role, Phase: FirstCopyActivated,
	}
	if row.Role == FirstCopyTarget {
		receipt.SourceTxID = row.SourceTxID
		receipt.TargetTxID = row.TxID
		receipt.GenerationID = row.SourceGenerationID
		receipt.Branch = row.SourceGenerationBranch
	} else {
		remote, err := s.RemoteFirstCopyReceipt(gameID, txID)
		if err != nil {
			return FirstCopyReceipt{}, "", err
		}
		if remote == nil || remote.Role != FirstCopyTarget {
			return FirstCopyReceipt{}, "", fmt.Errorf("source cannot attest before authenticated target receipt")
		}
		receipt.SourceTxID = row.TxID
		receipt.TargetTxID = remote.TargetTxID
		receipt.GenerationID = row.GenerationID
		receipt.Branch = row.GenerationBranch
	}
	if !validRemoteReceipt(receipt) {
		return FirstCopyReceipt{}, "", fmt.Errorf("first-copy receipt identity is incomplete")
	}
	return receipt, row.PeerID, nil
}
