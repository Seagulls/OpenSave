package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Phase C.2 persists an independently authenticated two-peer decision. It
// NEVER removes the first-copy fence, releases a hold or enables AutoSync.
// Distributed fence removal is a separate, unimplemented protocol.
const FirstCopyDecisionVersion = 1

const (
	DecisionOffered   = "offered"
	DecisionApproved  = "approved"
	DecisionCommitted = "committed"
)

const (
	DecisionOffer    = "offer"
	DecisionApproval = "approval"
	DecisionCommit   = "commit"
	DecisionConfirm  = "confirm"
)

// FirstCopyDecisionMessage always describes the SENDER's role. Authentication
// of that sender and the nonce comes from the paired LAN/WAN envelope.
type FirstCopyDecisionMessage struct {
	Version      int    `json:"version"`
	GameID       string `json:"gameId"`
	SourceTxID   string `json:"sourceTxId"`
	TargetTxID   string `json:"targetTxId"`
	GenerationID string `json:"generationId"`
	Branch       string `json:"branch"`
	Digest       string `json:"digest"`
	Role         string `json:"role"`
	DecisionID   string `json:"decisionId"`
	Stage        string `json:"stage"`
}

type firstCopyDecisionRow struct {
	GameID          string `db:"game_id"`
	LocalTxID       string `db:"local_tx_id"`
	PeerID          string `db:"peer_id"`
	SourceTxID      string `db:"source_tx_id"`
	TargetTxID      string `db:"target_tx_id"`
	GenerationID    string `db:"generation_id"`
	Branch          string `db:"branch"`
	Digest          string `db:"digest"`
	DecisionID      string `db:"decision_id"`
	Role            string `db:"role"`
	Phase           string `db:"phase"`
	LocalAuthorized bool   `db:"local_authorized"`
	PeerAuthorized  bool   `db:"peer_authorized"`
	PeerCommitted   bool   `db:"peer_committed"`
	CreatedAt       string `db:"created_at"`
}

type FirstCopyDecisionStatus struct {
	GameID              string `json:"gameId"`
	TxID                string `json:"txId"`
	DecisionID          string `json:"decisionId,omitempty"`
	Phase               string `json:"phase,omitempty"`
	LocalAuthorized     bool   `json:"localAuthorized"`
	PeerAuthorized      bool   `json:"peerAuthorized"`
	DecisionRecorded    bool   `json:"decisionRecorded"`
	PeerCommitConfirmed bool   `json:"peerCommitConfirmed"`
	FenceActive         bool   `json:"fenceActive"`
	AutoSync            bool   `json:"autoSync"`
	OpenAuthorized      bool   `json:"openAuthorized"`
	EvidenceFresh       bool   `json:"evidenceFresh"`
}

func validDecisionMessage(m FirstCopyDecisionMessage) bool {
	r := FirstCopyReceipt{Version: FirstCopyReceiptV1, GameID: m.GameID,
		SourceTxID: m.SourceTxID, TargetTxID: m.TargetTxID, GenerationID: m.GenerationID,
		Branch: m.Branch, Digest: m.Digest, Role: m.Role, Phase: FirstCopyActivated}
	if m.Version != FirstCopyDecisionVersion || !validRemoteReceipt(r) ||
		len(m.DecisionID) != 32 {
		return false
	}
	for _, c := range m.DecisionID {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	switch m.Stage {
	case DecisionOffer, DecisionApproval, DecisionCommit, DecisionConfirm:
		return true
	}
	return false
}

func (s *Store) decisionBasis(gameID, txID string) (FirstCopyHandoverPrepare, *FirstCopy, error) {
	status, err := s.ReadFirstCopyHandoverStatus(gameID, txID)
	if err != nil {
		return FirstCopyHandoverPrepare{}, nil, err
	}
	if !status.ReadyForOperatorReview || !status.FenceActive || status.OpenAuthorized {
		return FirstCopyHandoverPrepare{}, nil, fmt.Errorf("fresh signed two-peer preparation is required")
	}
	return s.firstCopyHandoverBasis(gameID, txID, time.Now().UTC())
}

func (s *Store) readDecision(q rowQuery, gameID string) (*firstCopyDecisionRow, error) {
	var row firstCopyDecisionRow
	err := q.Get(&row, `SELECT game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,
        generation_id,branch,digest,decision_id,role,phase,local_authorized,
        peer_authorized,peer_committed,created_at FROM game_first_copy_handover_decisions WHERE game_id=?`, gameID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func decisionMatches(row *firstCopyDecisionRow, basis FirstCopyHandoverPrepare, lease *FirstCopy, id string) bool {
	return row != nil && row.GameID == basis.GameID && row.LocalTxID == lease.TxID &&
		row.PeerID == lease.PeerID && row.SourceTxID == basis.SourceTxID &&
		row.TargetTxID == basis.TargetTxID && row.GenerationID == basis.GenerationID &&
		row.Branch == basis.Branch && row.Digest == basis.Digest &&
		row.DecisionID == id && row.Role == lease.Role
}

func decisionMessage(p FirstCopyHandoverPrepare, id, stage, role string) FirstCopyDecisionMessage {
	return FirstCopyDecisionMessage{Version: FirstCopyDecisionVersion,
		GameID: p.GameID, SourceTxID: p.SourceTxID, TargetTxID: p.TargetTxID,
		GenerationID: p.GenerationID, Branch: p.Branch, Digest: p.Digest,
		Role: role, DecisionID: id, Stage: stage}
}

// OfferFirstCopyDecision starts a new source-owned decision with a random
// identity. Repeated offers return the SAME durable identity, not a new vote.
func (s *Store) OfferFirstCopyDecision(gameID, txID string) (FirstCopyDecisionMessage, error) {
	p, lease, err := s.decisionBasis(gameID, txID)
	if err != nil {
		return FirstCopyDecisionMessage{}, err
	}
	if lease.Role != FirstCopySource {
		return FirstCopyDecisionMessage{}, fmt.Errorf("only the source offers a decision")
	}
	tx, err := s.db.Beginx()
	if err != nil {
		return FirstCopyDecisionMessage{}, err
	}
	defer tx.Rollback()
	existing, err := s.readDecision(tx, gameID)
	if err != nil {
		return FirstCopyDecisionMessage{}, err
	}
	if existing != nil {
		if !decisionMatches(existing, p, lease, existing.DecisionID) {
			return FirstCopyDecisionMessage{}, fmt.Errorf("conflicting existing decision")
		}
		return decisionMessage(p, existing.DecisionID, DecisionOffer, lease.Role), nil
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return FirstCopyDecisionMessage{}, err
	}
	id := hex.EncodeToString(random[:])
	_, err = tx.Exec(`INSERT INTO game_first_copy_handover_decisions
      (game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,generation_id,branch,digest,decision_id,role,phase,created_at)
      VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, gameID, lease.TxID, lease.PeerID, p.SourceTxID,
		p.TargetTxID, p.GenerationID, p.Branch, p.Digest, id, lease.Role,
		DecisionOffered, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return FirstCopyDecisionMessage{}, err
	}
	if err := tx.Commit(); err != nil {
		return FirstCopyDecisionMessage{}, err
	}
	return decisionMessage(p, id, DecisionOffer, lease.Role), nil
}

// AuthorizeFirstCopyDecision is a separate, local operator action on EACH
// device. Target authorizes the received offer; source authorizes only after
// a signed target approval has been persisted. No call removes the fence.
func (s *Store) AuthorizeFirstCopyDecision(gameID, txID, id string) error {
	p, lease, err := s.decisionBasis(gameID, txID)
	if err != nil {
		return err
	}
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := s.readDecision(tx, gameID)
	if err != nil {
		return err
	}
	if !decisionMatches(row, p, lease, id) {
		return fmt.Errorf("decision identity does not match")
	}
	if row.Role == FirstCopySource && !row.PeerAuthorized {
		return fmt.Errorf("source requires signed target approval first")
	}
	if row.Phase == DecisionCommitted {
		return tx.Commit()
	}
	if row.Phase != DecisionOffered && row.Phase != DecisionApproved {
		return fmt.Errorf("invalid decision phase")
	}
	phase := DecisionApproved
	if row.Role == FirstCopySource {
		phase = DecisionCommitted
	}
	if _, err := tx.Exec(`UPDATE game_first_copy_handover_decisions SET phase=?,local_authorized=1
        WHERE game_id=? AND local_tx_id=? AND decision_id=?`, phase, gameID, txID, id); err != nil {
		return err
	}
	return tx.Commit()
}

// AcceptFirstCopyDecision verifies an already signed paired-peer statement.
// Nonces and state transitions commit atomically. Any malformed, stale or
// cancelled decision fails closed; no direct write to the game's permissions.
func (s *Store) AcceptFirstCopyDecision(gameID, peer, nonce string, m FirstCopyDecisionMessage) error {
	if !validDecisionMessage(m) || m.GameID != gameID || peer == "" || nonce == "" || len(nonce) > 256 {
		return fmt.Errorf("invalid signed handover decision")
	}
	lease, err := s.RecordedFirstCopy(gameID)
	if err != nil || lease == nil {
		return fmt.Errorf("no first-copy transaction: %w", err)
	}
	p, lease, err := s.decisionBasis(gameID, lease.TxID)
	if err != nil {
		return err
	}
	if peer != lease.PeerID || m.Role == lease.Role || p.SourceTxID != m.SourceTxID ||
		p.TargetTxID != m.TargetTxID || p.GenerationID != m.GenerationID ||
		p.Branch != m.Branch || p.Digest != m.Digest || p.GameID != m.GameID {
		return fmt.Errorf("signed decision does not match the activated pair")
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
		current.PeerID != peer || current.ContentHash != m.Digest {
		return fmt.Errorf("first-copy fence changed")
	}
	var cancelled int
	if err := tx.Get(&cancelled, `SELECT COUNT(*) FROM game_first_copy_handover_cancelled_decisions
        WHERE game_id=? AND decision_id=?`, gameID, m.DecisionID); err != nil {
		return err
	}
	if cancelled != 0 {
		return fmt.Errorf("hand-over decision was cancelled")
	}
	row, err := s.readDecision(tx, gameID)
	if err != nil {
		return err
	}
	if row != nil && !decisionMatches(row, p, lease, m.DecisionID) {
		return fmt.Errorf("different handover decision already recorded")
	}
	switch m.Stage {
	case DecisionOffer:
		if lease.Role != FirstCopyTarget || m.Role != FirstCopySource {
			return fmt.Errorf("only the target accepts a source offer")
		}
		if row == nil {
			_, err = tx.Exec(`INSERT INTO game_first_copy_handover_decisions
                (game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,generation_id,branch,digest,decision_id,role,phase,created_at)
                VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, gameID, lease.TxID, lease.PeerID, p.SourceTxID, p.TargetTxID,
				p.GenerationID, p.Branch, p.Digest, m.DecisionID, lease.Role, DecisionOffered, time.Now().UTC().Format(time.RFC3339Nano))
			if err != nil {
				return err
			}
		}
	case DecisionApproval:
		if lease.Role != FirstCopySource || m.Role != FirstCopyTarget || row == nil || (row.Phase != DecisionOffered && row.Phase != DecisionCommitted) {
			return fmt.Errorf("source requires its offer before signed approval")
		}
		if _, err = tx.Exec(`UPDATE game_first_copy_handover_decisions SET peer_authorized=1
            WHERE game_id=? AND decision_id=?`, gameID, m.DecisionID); err != nil {
			return err
		}
	case DecisionCommit:
		if lease.Role != FirstCopyTarget || m.Role != FirstCopySource || row == nil ||
			!row.LocalAuthorized || (row.Phase != DecisionApproved && row.Phase != DecisionCommitted) {
			return fmt.Errorf("target must explicitly approve before signed commit")
		}
		if _, err = tx.Exec(`UPDATE game_first_copy_handover_decisions
            SET phase=?,peer_authorized=1 WHERE game_id=? AND decision_id=?`,
			DecisionCommitted, gameID, m.DecisionID); err != nil {
			return err
		}
	case DecisionConfirm:
		if lease.Role != FirstCopySource || m.Role != FirstCopyTarget || row == nil ||
			row.Phase != DecisionCommitted || !row.LocalAuthorized || !row.PeerAuthorized {
			return fmt.Errorf("source has not committed this decision")
		}
		if _, err = tx.Exec(`UPDATE game_first_copy_handover_decisions SET peer_committed=1
            WHERE game_id=? AND decision_id=?`, gameID, m.DecisionID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO game_first_copy_handover_decision_nonces
        (game_id,local_tx_id,peer_id,nonce) VALUES (?,?,?,?)`, gameID, lease.TxID, peer, nonce); err != nil {
		return fmt.Errorf("signed decision request nonce reused or not persisted: %w", err)
	}
	return tx.Commit()
}

// OutboundFirstCopyDecision returns only a stage substantiated by local durable
// evidence; caller must validate current digest, pause and active sync.
func (s *Store) OutboundFirstCopyDecision(gameID, txID, stage string) (FirstCopyDecisionMessage, string, error) {
	p, lease, err := s.decisionBasis(gameID, txID)
	if err != nil {
		return FirstCopyDecisionMessage{}, "", err
	}
	row, err := s.readDecision(s.db, gameID)
	if err != nil {
		return FirstCopyDecisionMessage{}, "", err
	}
	if row == nil || !decisionMatches(row, p, lease, row.DecisionID) {
		return FirstCopyDecisionMessage{}, "", fmt.Errorf("missing or conflicting decision")
	}
	switch stage {
	case DecisionOffer:
		if lease.Role != FirstCopySource {
			return FirstCopyDecisionMessage{}, "", fmt.Errorf("only source sends offer")
		}
	case DecisionApproval:
		if lease.Role != FirstCopyTarget || !row.LocalAuthorized || (row.Phase != DecisionApproved && row.Phase != DecisionCommitted) {
			return FirstCopyDecisionMessage{}, "", fmt.Errorf("target must approve first")
		}
	case DecisionCommit:
		if lease.Role != FirstCopySource || row.Phase != DecisionCommitted || !row.LocalAuthorized || !row.PeerAuthorized {
			return FirstCopyDecisionMessage{}, "", fmt.Errorf("source requires durable operator and peer approval")
		}
	case DecisionConfirm:
		if lease.Role != FirstCopyTarget || row.Phase != DecisionCommitted || !row.PeerAuthorized || !row.LocalAuthorized {
			return FirstCopyDecisionMessage{}, "", fmt.Errorf("target has not recorded committed decision")
		}
	default:
		return FirstCopyDecisionMessage{}, "", fmt.Errorf("unsupported decision stage")
	}
	return decisionMessage(p, row.DecisionID, stage, lease.Role), lease.PeerID, nil
}

// CancelFirstCopyDecision is an irreversible local veto before this device
// commits. A durable tombstone blocks reusing the old ID after cancellation.
// Once committed it requires separately reviewed coordinated recovery.
func (s *Store) CancelFirstCopyDecision(gameID, txID string) error {
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	fence, err := s.readFirstCopy(tx, gameID)
	if err != nil {
		return err
	}
	if fence == nil || fence.TxID != txID || fence.Phase != FirstCopyActivated {
		return fmt.Errorf("activated fence required")
	}
	row, err := s.readDecision(tx, gameID)
	if err != nil {
		return err
	}
	if row == nil {
		return tx.Commit()
	}
	if row.Phase == DecisionCommitted {
		return fmt.Errorf("committed decision requires coordinated recovery")
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO game_first_copy_handover_cancelled_decisions
        (game_id,decision_id) VALUES (?,?)`, gameID, row.DecisionID); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM game_first_copy_handover_decisions WHERE game_id=? AND local_tx_id=?`, gameID, txID); err != nil {
		return err
	}
	return tx.Commit()
}

// ReadFirstCopyDecisionStatus is a recovery journal, not an authorization gate.
// Expired preparation evidence does not erase a durable decision.
func (s *Store) ReadFirstCopyDecisionStatus(gameID, txID string) (FirstCopyDecisionStatus, error) {
	lease, err := s.RecordedFirstCopy(gameID)
	if err != nil {
		return FirstCopyDecisionStatus{}, err
	}
	if lease == nil || lease.TxID != txID || lease.Phase != FirstCopyActivated {
		return FirstCopyDecisionStatus{}, fmt.Errorf("matching activated fence required")
	}
	result := FirstCopyDecisionStatus{GameID: gameID, TxID: txID, FenceActive: true, AutoSync: false, OpenAuthorized: false}
	row, err := s.readDecision(s.db, gameID)
	if err != nil {
		return result, err
	}
	if row != nil {
		if row.GameID != gameID || row.LocalTxID != txID || row.PeerID != lease.PeerID ||
			row.Role != lease.Role || row.Digest != lease.ContentHash {
			return result, fmt.Errorf("decision journal does not match the activated fence")
		}
		result.DecisionID = row.DecisionID
		result.Phase = row.Phase
		result.LocalAuthorized = row.LocalAuthorized
		result.PeerAuthorized = row.PeerAuthorized
		result.DecisionRecorded = row.Phase == DecisionCommitted
		result.PeerCommitConfirmed = row.PeerCommitted
	}
	if status, err := s.ReadFirstCopyHandoverStatus(gameID, txID); err == nil {
		result.EvidenceFresh = status.ReadyForOperatorReview
	}
	return result, nil
}
