package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Phase C.1 prepares an explicit two-peer handover but never opens a fence.
// A successfully exchanged preparation is only evidence for operator review.
const FirstCopyHandoverVersion = 1
const firstCopyHandoverFreshness = 2 * time.Minute

// FirstCopyHandoverPrepare is transmitted ONLY using signed paired-peer LAN/WAN
// requests. The sender identity and request nonce come from that transport.
type FirstCopyHandoverPrepare struct {
	Version      int    `json:"version"`
	GameID       string `json:"gameId"`
	SourceTxID   string `json:"sourceTxId"`
	TargetTxID   string `json:"targetTxId"`
	GenerationID string `json:"generationId"`
	Branch       string `json:"branch"`
	Digest       string `json:"digest"`
	Role         string `json:"role"`
	Stage        string `json:"stage"`
}

type firstCopyHandoverRow struct {
	GameID       string `db:"game_id"`
	LocalTxID    string `db:"local_tx_id"`
	PeerID       string `db:"peer_id"`
	SourceTxID   string `db:"source_tx_id"`
	TargetTxID   string `db:"target_tx_id"`
	GenerationID string `db:"generation_id"`
	Branch       string `db:"branch"`
	Digest       string `db:"digest"`
	Role         string `db:"role"`
	PreparedAt   string `db:"prepared_at"`
	ReceivedAt   string `db:"received_at"`
}

// FirstCopyHandoverStatus is explicitly not a safe-to-open certificate.
type FirstCopyHandoverStatus struct {
	GameID                 string `json:"gameId"`
	TxID                   string `json:"txId"`
	Phase                  string `json:"phase"`
	LocalPrepared          bool   `json:"localPrepared"`
	PeerPrepared           bool   `json:"peerPrepared"`
	PeerPrepareFresh       bool   `json:"peerPrepareFresh"`
	ReadyForOperatorReview bool   `json:"readyForOperatorReview"`
	FenceActive            bool   `json:"fenceActive"`
	AutoSync               bool   `json:"autoSync"`
	OpenAuthorized         bool   `json:"openAuthorized"`
}

func firstCopyHandoverFresh(stamp string, now time.Time) bool {
	then, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return false
	}
	age := now.Sub(then)
	return age >= -5*time.Second && age <= firstCopyHandoverFreshness
}

func firstCopyHandoverFromReceipt(r FirstCopyReceipt) FirstCopyHandoverPrepare {
	return FirstCopyHandoverPrepare{
		Version: FirstCopyHandoverVersion, GameID: r.GameID,
		SourceTxID: r.SourceTxID, TargetTxID: r.TargetTxID,
		GenerationID: r.GenerationID, Branch: r.Branch,
		Digest: r.Digest, Role: r.Role, Stage: "prepared",
	}
}

func validFirstCopyHandover(p FirstCopyHandoverPrepare) bool {
	r := FirstCopyReceipt{Version: FirstCopyReceiptV1, GameID: p.GameID,
		SourceTxID: p.SourceTxID, TargetTxID: p.TargetTxID,
		GenerationID: p.GenerationID, Branch: p.Branch,
		Digest: p.Digest, Role: p.Role, Phase: FirstCopyActivated}
	return p.Version == FirstCopyHandoverVersion && p.Stage == "prepared" && validRemoteReceipt(r)
}

func firstCopyHandoverMatches(row firstCopyHandoverRow, p FirstCopyHandoverPrepare, peer, localTx string) bool {
	return row.GameID == p.GameID && row.LocalTxID == localTx &&
		row.PeerID == peer && row.SourceTxID == p.SourceTxID &&
		row.TargetTxID == p.TargetTxID && row.GenerationID == p.GenerationID &&
		row.Branch == p.Branch && row.Digest == p.Digest && row.Role == p.Role
}

// firstCopyHandoverBasis requires a current, pairing-authenticated Phase B
// receipt already persisted on THIS device. A missing/stale/contradictory
// receipt cannot be promoted into handover preparation.
func (s *Store) firstCopyHandoverBasis(gameID, txID string, now time.Time) (FirstCopyHandoverPrepare, *FirstCopy, error) {
	lease, err := s.RecordedFirstCopy(gameID)
	if err != nil {
		return FirstCopyHandoverPrepare{}, nil, err
	}
	if lease == nil || lease.TxID != txID || lease.Phase != FirstCopyActivated {
		return FirstCopyHandoverPrepare{}, nil, fmt.Errorf("handover requires an activated, matching first-copy fence")
	}
	receipt, peerID, err := s.OutboundFirstCopyReceipt(gameID, txID)
	if err != nil {
		return FirstCopyHandoverPrepare{}, nil, err
	}
	if peerID != lease.PeerID || receipt.Digest != lease.ContentHash {
		return FirstCopyHandoverPrepare{}, nil, fmt.Errorf("handover receipt identity does not match local fence")
	}
	var remote remoteReceiptRow
	err = s.db.Get(&remote, `SELECT game_id, local_tx_id, peer_id, source_tx_id, target_tx_id,
        generation_id, branch, digest, role, phase, received_at
        FROM game_first_copy_remote_receipts WHERE game_id = ? AND local_tx_id = ?`, gameID, txID)
	if err != nil {
		return FirstCopyHandoverPrepare{}, nil, fmt.Errorf("handover requires an authenticated remote receipt: %w", err)
	}
	if remote.PeerID != lease.PeerID || remote.Role == lease.Role ||
		remote.SourceTxID != receipt.SourceTxID || remote.TargetTxID != receipt.TargetTxID ||
		remote.GenerationID != receipt.GenerationID || remote.Branch != receipt.Branch ||
		remote.Digest != receipt.Digest || remote.Phase != FirstCopyActivated ||
		!firstCopyHandoverFresh(remote.ReceivedAt, now) {
		return FirstCopyHandoverPrepare{}, nil, fmt.Errorf("handover requires a fresh matching authenticated remote receipt")
	}
	return firstCopyHandoverFromReceipt(receipt), lease, nil
}

func (s *Store) readHandoverRow(table, gameID string) (*firstCopyHandoverRow, error) {
	// Table is always a constant at the caller, never caller-supplied.
	var timestamp string
	switch table {
	case "game_first_copy_handover_local":
		timestamp = "prepared_at"
	case "game_first_copy_handover_remote":
		timestamp = "received_at"
	default:
		return nil, fmt.Errorf("unsupported handover table")
	}
	var row firstCopyHandoverRow
	query := `SELECT game_id, local_tx_id, peer_id, source_tx_id, target_tx_id,
        generation_id, branch, digest, role, ` + timestamp + ` FROM ` + table + ` WHERE game_id = ?`
	err := s.db.Get(&row, query, gameID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// PrepareFirstCopyHandover stores EXPLICIT local operator intent. The caller
// must first check live local save content and any active sync. This writes
// only the preparation table; it cannot modify the game or first-copy fence.
func (s *Store) PrepareFirstCopyHandover(gameID, txID string) (FirstCopyHandoverPrepare, error) {
	now := time.Now().UTC()
	p, lease, err := s.firstCopyHandoverBasis(gameID, txID, now)
	if err != nil {
		return FirstCopyHandoverPrepare{}, err
	}
	if !validFirstCopyHandover(p) {
		return FirstCopyHandoverPrepare{}, fmt.Errorf("handover identity incomplete")
	}
	tx, err := s.db.Beginx()
	if err != nil {
		return FirstCopyHandoverPrepare{}, err
	}
	defer tx.Rollback()
	current, err := s.readFirstCopy(tx, gameID)
	if err != nil {
		return FirstCopyHandoverPrepare{}, err
	}
	if current == nil || current.TxID != txID || current.PeerID != lease.PeerID ||
		current.Phase != FirstCopyActivated || current.ContentHash != p.Digest {
		return FirstCopyHandoverPrepare{}, fmt.Errorf("first-copy fence changed during preparation")
	}
	stamp := now.Format(time.RFC3339Nano)
	_, err = tx.Exec(`INSERT OR IGNORE INTO game_first_copy_handover_local
        (game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,generation_id,branch,digest,role,prepared_at)
        VALUES (?,?,?,?,?,?,?,?,?,?)`, gameID, txID, lease.PeerID,
		p.SourceTxID, p.TargetTxID, p.GenerationID, p.Branch, p.Digest, p.Role, stamp)
	if err != nil {
		return FirstCopyHandoverPrepare{}, err
	}
	var existing firstCopyHandoverRow
	if err := tx.Get(&existing, `SELECT game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,
        generation_id,branch,digest,role,prepared_at FROM game_first_copy_handover_local WHERE game_id=?`, gameID); err != nil {
		return FirstCopyHandoverPrepare{}, err
	}
	if !firstCopyHandoverMatches(existing, p, lease.PeerID, txID) {
		return FirstCopyHandoverPrepare{}, fmt.Errorf("conflicting local handover preparation cannot be replaced")
	}
	if _, err := tx.Exec(`UPDATE game_first_copy_handover_local SET prepared_at=? WHERE game_id=? AND local_tx_id=?`, stamp, gameID, txID); err != nil {
		return FirstCopyHandoverPrepare{}, err
	}
	if err := tx.Commit(); err != nil {
		return FirstCopyHandoverPrepare{}, err
	}
	return p, nil
}

// OutboundFirstCopyHandoverPrepare refuses a stale local intent and requires
// the Phase B peer receipt to remain fresh. There is no automatic retransmit.
func (s *Store) OutboundFirstCopyHandoverPrepare(gameID, txID string) (FirstCopyHandoverPrepare, string, error) {
	now := time.Now().UTC()
	p, lease, err := s.firstCopyHandoverBasis(gameID, txID, now)
	if err != nil {
		return FirstCopyHandoverPrepare{}, "", err
	}
	local, err := s.readHandoverRow("game_first_copy_handover_local", gameID)
	if err != nil {
		return FirstCopyHandoverPrepare{}, "", err
	}
	if local == nil || !firstCopyHandoverMatches(*local, p, lease.PeerID, txID) || !firstCopyHandoverFresh(local.PreparedAt, now) {
		return FirstCopyHandoverPrepare{}, "", fmt.Errorf("fresh local handover preparation required")
	}
	return p, lease.PeerID, nil
}

// AcceptFirstCopyHandoverPrepare is called only after signed LAN/WAN request
// authentication. The nonce comes from the authenticated transport envelope.
// Both local operator intent and matching Phase B receipts are prerequisites.
func (s *Store) AcceptFirstCopyHandoverPrepare(gameID, signedPeer, nonce string, p FirstCopyHandoverPrepare) error {
	if !validFirstCopyHandover(p) || p.GameID != gameID || signedPeer == "" || nonce == "" || len(nonce) > 256 {
		return fmt.Errorf("invalid authenticated handover preparation")
	}
	now := time.Now().UTC()
	currentLease, err := s.RecordedFirstCopy(gameID)
	if err != nil {
		return err
	}
	if currentLease == nil {
		return fmt.Errorf("no activated first-copy for handover")
	}
	localBasis, lease, err := s.firstCopyHandoverBasis(gameID, currentLease.TxID, now)
	if err != nil {
		return err
	}
	if lease.PeerID != signedPeer || localBasis.Role == p.Role ||
		localBasis.GameID != p.GameID || localBasis.SourceTxID != p.SourceTxID ||
		localBasis.TargetTxID != p.TargetTxID || localBasis.GenerationID != p.GenerationID ||
		localBasis.Branch != p.Branch || localBasis.Digest != p.Digest {
		return fmt.Errorf("remote handover preparation does not match the activated pair")
	}
	local, err := s.readHandoverRow("game_first_copy_handover_local", gameID)
	if err != nil {
		return err
	}
	if local == nil || !firstCopyHandoverMatches(*local, localBasis, lease.PeerID, lease.TxID) ||
		!firstCopyHandoverFresh(local.PreparedAt, now) {
		return fmt.Errorf("remote handover preparation refused until this device explicitly prepares")
	}
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := s.readFirstCopy(tx, gameID)
	if err != nil {
		return err
	}
	if current == nil || current.Phase != FirstCopyActivated || current.TxID != lease.TxID ||
		current.PeerID != signedPeer || current.ContentHash != p.Digest {
		return fmt.Errorf("first-copy fence changed during remote preparation")
	}
	// Recheck the operator's intent IN the same transaction as the accepted
	// nonce: concurrent cancel cannot turn an old prepare into fresh evidence.
	var txLocal firstCopyHandoverRow
	if err := tx.Get(&txLocal, `SELECT game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,
        generation_id,branch,digest,role,prepared_at FROM game_first_copy_handover_local WHERE game_id=?`, gameID); err != nil {
		return fmt.Errorf("local preparation was cancelled: %w", err)
	}
	if !firstCopyHandoverMatches(txLocal, localBasis, lease.PeerID, lease.TxID) || !firstCopyHandoverFresh(txLocal.PreparedAt, now) {
		return fmt.Errorf("local preparation changed or expired")
	}
	if _, err := tx.Exec(`INSERT INTO game_first_copy_handover_nonces
        (game_id,local_tx_id,peer_id,nonce) VALUES (?,?,?,?)`, gameID, lease.TxID, signedPeer, nonce); err != nil {
		return fmt.Errorf("signed handover prepare nonce reused or not durable: %w", err)
	}
	stamp := now.Format(time.RFC3339Nano)
	if _, err := tx.Exec(`INSERT OR IGNORE INTO game_first_copy_handover_remote
        (game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,generation_id,branch,digest,role,received_at)
        VALUES (?,?,?,?,?,?,?,?,?,?)`, gameID, lease.TxID, signedPeer,
		p.SourceTxID, p.TargetTxID, p.GenerationID, p.Branch, p.Digest, p.Role, stamp); err != nil {
		return err
	}
	var existing firstCopyHandoverRow
	if err := tx.Get(&existing, `SELECT game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,
        generation_id,branch,digest,role,received_at FROM game_first_copy_handover_remote WHERE game_id=?`, gameID); err != nil {
		return err
	}
	if !firstCopyHandoverMatches(existing, p, signedPeer, lease.TxID) {
		return fmt.Errorf("conflicting signed remote preparation cannot replace existing evidence")
	}
	if _, err := tx.Exec(`UPDATE game_first_copy_handover_remote SET received_at=? WHERE game_id=? AND local_tx_id=?`, stamp, gameID, lease.TxID); err != nil {
		return err
	}
	return tx.Commit()
}

// ReadFirstCopyHandoverStatus does not expose any new permission. The caller
// also MUST verify live content/no active sync before presenting readiness.
func (s *Store) ReadFirstCopyHandoverStatus(gameID, txID string) (FirstCopyHandoverStatus, error) {
	now := time.Now().UTC()
	p, lease, err := s.firstCopyHandoverBasis(gameID, txID, now)
	if err != nil {
		return FirstCopyHandoverStatus{}, err
	}
	status := FirstCopyHandoverStatus{GameID: gameID, TxID: txID, Phase: lease.Phase,
		FenceActive: true, AutoSync: false, OpenAuthorized: false}
	local, err := s.readHandoverRow("game_first_copy_handover_local", gameID)
	if err != nil {
		return status, err
	}
	remote, err := s.readHandoverRow("game_first_copy_handover_remote", gameID)
	if err != nil {
		return status, err
	}
	if local != nil && firstCopyHandoverMatches(*local, p, lease.PeerID, txID) {
		status.LocalPrepared = firstCopyHandoverFresh(local.PreparedAt, now)
	}
	peerP := p
	if lease.Role == FirstCopySource {
		peerP.Role = FirstCopyTarget
	} else {
		peerP.Role = FirstCopySource
	}
	if remote != nil && firstCopyHandoverMatches(*remote, peerP, lease.PeerID, txID) {
		status.PeerPrepared = true
		status.PeerPrepareFresh = firstCopyHandoverFresh(remote.ReceivedAt, now)
	}
	status.ReadyForOperatorReview = status.LocalPrepared && status.PeerPrepareFresh
	return status, nil
}

// CancelFirstCopyHandover drops local/remote preparation observations only.
// Old signed request nonces remain durable; the first-copy fence survives.
func (s *Store) CancelFirstCopyHandover(gameID, txID string) error {
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
		return fmt.Errorf("handover cancellation requires matching activated fence")
	}
	if _, err := tx.Exec(`DELETE FROM game_first_copy_handover_local WHERE game_id=? AND local_tx_id=?`, gameID, txID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM game_first_copy_handover_remote WHERE game_id=? AND local_tx_id=?`, gameID, txID); err != nil {
		return err
	}
	return tx.Commit()
}
