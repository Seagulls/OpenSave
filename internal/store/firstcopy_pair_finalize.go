package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Phase C.3 deliberately finalizes ONLY a guarded source/target pair. The
// original first-copy row and pinned generation remain present as a second
// layer of protection; no ordinary bidirectional sync is enabled here.
const FirstCopyFinalizeVersion = 1

const (
	FinalizeOffer  = "offer"
	FinalizeReady  = "ready"
	FinalizeCommit = "commit"
	FinalizeAck    = "ack"
	FinalizeAbort  = "abort"

	FinalizeIntent        = "intent"
	FinalizeOffered       = "offered"
	FinalizeReadyPhase    = "ready"
	FinalizePairFinalized = "pair_finalized"
	FinalizeCancelled     = "cancelled"
)

type FirstCopyFinalizeMessage struct {
	Version      int    `json:"version"`
	GameID       string `json:"gameId"`
	SourceTxID   string `json:"sourceTxId"`
	TargetTxID   string `json:"targetTxId"`
	GenerationID string `json:"generationId"`
	Branch       string `json:"branch"`
	Digest       string `json:"digest"`
	DecisionID   string `json:"decisionId"`
	Role         string `json:"role"`
	Stage        string `json:"stage"`
}

// FirstCopyPairGuard is a durable admission restriction, independent of the
// original first-copy row. It only permits the source to serve immutable
// pinned-generation reads to the named target; no writes or third-peer reads.
type FirstCopyPairGuard struct {
	GameID       string `db:"game_id" json:"gameId"`
	LocalTxID    string `db:"local_tx_id" json:"localTxId"`
	PeerID       string `db:"peer_id" json:"peerId"`
	SourceTxID   string `db:"source_tx_id" json:"sourceTxId"`
	TargetTxID   string `db:"target_tx_id" json:"targetTxId"`
	GenerationID string `db:"generation_id" json:"generationId"`
	Branch       string `db:"branch" json:"branch"`
	Digest       string `db:"digest" json:"digest"`
	DecisionID   string `db:"decision_id" json:"decisionId"`
	Role         string `db:"role" json:"role"`
	Phase        string `db:"phase" json:"phase"`
	PeerAck      bool   `db:"peer_ack" json:"peerAck"`
	UpdatedAt    string `db:"updated_at" json:"updatedAt"`
}

type FirstCopyFinalizationStatus struct {
	ContentValid      bool   `json:"contentValid"`
	GameID            string `json:"gameId"`
	TxID              string `json:"txId"`
	DecisionID        string `json:"decisionId"`
	Phase             string `json:"phase"`
	PeerAck           bool   `json:"peerAck"`
	PairFinalized     bool   `json:"pairFinalized"`
	RecoveryRequired  bool   `json:"recoveryRequired"`
	FenceActive       bool   `json:"fenceActive"`
	ThirdPeerAdmitted bool   `json:"thirdPeerAdmitted"`
	AutoSync          bool   `json:"autoSync"`
	OpenAuthorized    bool   `json:"openAuthorized"`
}

func validFinalizeMessage(m FirstCopyFinalizeMessage) bool {
	r := FirstCopyDecisionMessage{Version: FirstCopyDecisionVersion,
		GameID: m.GameID, SourceTxID: m.SourceTxID, TargetTxID: m.TargetTxID,
		GenerationID: m.GenerationID, Branch: m.Branch, Digest: m.Digest,
		Role: m.Role, DecisionID: m.DecisionID, Stage: DecisionCommit}
	if m.Version != FirstCopyFinalizeVersion || !validDecisionMessage(r) {
		return false
	}
	switch m.Stage {
	case FinalizeOffer, FinalizeReady, FinalizeCommit, FinalizeAck, FinalizeAbort:
		return true
	}
	return false
}

func readFirstCopyPairGuard(q rowQuery, gameID string) (*FirstCopyPairGuard, error) {
	var row FirstCopyPairGuard
	err := q.Get(&row, `SELECT game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,
        generation_id,branch,digest,decision_id,role,phase,peer_ack,updated_at
        FROM game_first_copy_pair_guards WHERE game_id=?`, gameID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read first-copy pair guard: %w", err)
	}
	return &row, nil
}

// PairGuardForServe must fail closed on an unreadable/invalid row. The caller
// consults it BEFORE falling through to ordinary serving or sync paths.
func (s *Store) PairGuardForServe(gameID string) (*FirstCopyPairGuard, error) {
	if gameID == "" {
		return nil, nil
	}
	if s == nil {
		return nil, fmt.Errorf("pair guard store unavailable")
	}
	if err := s.provisioningFault(); err != nil {
		return nil, err
	}
	g, err := readFirstCopyPairGuard(s.db, gameID)
	if err != nil || g == nil {
		return g, err
	}
	if g.PeerID == "" || g.DecisionID == "" || g.Digest == "" || g.GameID != gameID ||
		(g.Role != FirstCopySource && g.Role != FirstCopyTarget) ||
		(g.Phase != FinalizeIntent && g.Phase != FinalizeOffered && g.Phase != FinalizeReadyPhase &&
			g.Phase != FinalizePairFinalized && g.Phase != FinalizeCancelled) {
		return nil, fmt.Errorf("invalid durable pair guard; refusing access")
	}
	return g, nil
}

// finalizeBasisTx checks all durable C.2 consent and source-generation facts
// *inside the write transaction*. Fresh Phase B receipts are NOT treated as
// repeatable votes after the C.2 decision has already been committed.
func (s *Store) finalizeBasisTx(q rowQuery, gameID, txID string) (*FirstCopy, *firstCopyDecisionRow, error) {
	lease, err := s.readFirstCopy(q, gameID)
	if err != nil {
		return nil, nil, err
	}
	if lease == nil || lease.TxID != txID || lease.Phase != FirstCopyActivated ||
		lease.PeerID == "" {
		return nil, nil, fmt.Errorf("matching activated first-copy fence required")
	}
	decision, err := s.readDecision(q, gameID)
	if err != nil {
		return nil, nil, err
	}
	if decision == nil || decision.Phase != DecisionCommitted ||
		!decision.LocalAuthorized || !decision.PeerAuthorized ||
		decision.LocalTxID != txID || decision.PeerID != lease.PeerID ||
		decision.Role != lease.Role || decision.Digest != lease.ContentHash ||
		decision.GameID != gameID || decision.DecisionID == "" {
		return nil, nil, fmt.Errorf("locally committed C.2 decision and two operator approvals required")
	}
	if lease.Role == FirstCopySource {
		if !decision.PeerCommitted || decision.SourceTxID != lease.TxID ||
			decision.GenerationID != lease.GenerationID || decision.Branch != lease.GenerationBranch {
			return nil, nil, fmt.Errorf("source requires signed target commit confirmation")
		}
	} else if decision.TargetTxID != lease.TxID || decision.SourceTxID != lease.SourceTxID ||
		decision.GenerationID != lease.SourceGenerationID || decision.Branch != lease.SourceGenerationBranch ||
		lease.SourceTxID == "" {
		return nil, nil, fmt.Errorf("target source-generation binding changed")
	}
	var held int
	if err := q.Get(&held, `SELECT COUNT(*) FROM game_provisioning_holds WHERE game_id=?`, gameID); err != nil {
		return nil, nil, err
	}
	if held != 0 {
		return nil, nil, fmt.Errorf("provisioning hold unexpectedly remains")
	}
	var auto int
	if err := q.Get(&auto, `SELECT auto_sync FROM games WHERE id=?`, gameID); err != nil {
		return nil, nil, err
	}
	if auto != 0 {
		return nil, nil, fmt.Errorf("AutoSync must remain off")
	}
	var veto int
	if err := q.Get(&veto, `SELECT COUNT(*) FROM game_first_copy_handover_cancelled_decisions WHERE game_id=? AND decision_id=?`, gameID, decision.DecisionID); err != nil {
		return nil, nil, err
	}
	if veto != 0 {
		return nil, nil, fmt.Errorf("C.2 decision was cancelled")
	}
	return lease, decision, nil
}

func guardMatchesBasis(g *FirstCopyPairGuard, lease *FirstCopy, d *firstCopyDecisionRow) bool {
	return g != nil && g.GameID == lease.GameID && g.LocalTxID == lease.TxID &&
		g.PeerID == lease.PeerID && g.DecisionID == d.DecisionID &&
		g.SourceTxID == d.SourceTxID && g.TargetTxID == d.TargetTxID &&
		g.GenerationID == d.GenerationID && g.Branch == d.Branch &&
		g.Digest == d.Digest && g.Role == lease.Role
}

func finalizeMessage(g *FirstCopyPairGuard, stage string) FirstCopyFinalizeMessage {
	return FirstCopyFinalizeMessage{Version: FirstCopyFinalizeVersion, GameID: g.GameID,
		SourceTxID: g.SourceTxID, TargetTxID: g.TargetTxID,
		GenerationID: g.GenerationID, Branch: g.Branch, Digest: g.Digest,
		DecisionID: g.DecisionID, Role: g.Role, Stage: stage}
}

func (s *Store) BeginFirstCopyPairFinalization(gameID, txID string) (FirstCopyFinalizationStatus, error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return FirstCopyFinalizationStatus{}, err
	}
	defer tx.Rollback()
	lease, decision, err := s.finalizeBasisTx(tx, gameID, txID)
	if err != nil {
		return FirstCopyFinalizationStatus{}, err
	}
	var veto int
	if err := tx.Get(&veto, `SELECT COUNT(*) FROM game_first_copy_finalize_vetoes WHERE game_id=? AND decision_id=?`, gameID, decision.DecisionID); err != nil {
		return FirstCopyFinalizationStatus{}, err
	}
	if veto != 0 {
		return FirstCopyFinalizationStatus{}, fmt.Errorf("finalization was vetoed; cannot restart same decision")
	}
	guard, err := readFirstCopyPairGuard(tx, gameID)
	if err != nil {
		return FirstCopyFinalizationStatus{}, err
	}
	if guard != nil {
		if !guardMatchesBasis(guard, lease, decision) || guard.Phase == FinalizeCancelled {
			return FirstCopyFinalizationStatus{}, fmt.Errorf("existing finalization differs or was cancelled")
		}
	} else {
		_, err = tx.Exec(`INSERT INTO game_first_copy_pair_guards
          (game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,generation_id,branch,digest,decision_id,role,phase,updated_at)
          VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, gameID, txID, lease.PeerID, decision.SourceTxID,
			decision.TargetTxID, decision.GenerationID, decision.Branch, decision.Digest,
			decision.DecisionID, lease.Role, FinalizeIntent, time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return FirstCopyFinalizationStatus{}, err
		}
		guard, err = readFirstCopyPairGuard(tx, gameID)
		if err != nil {
			return FirstCopyFinalizationStatus{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return FirstCopyFinalizationStatus{}, err
	}
	return finalizationStatus(guard), nil
}

// StageFirstCopyFinalizeSend saves the next local state BEFORE a signed LAN/WAN
// message is sent. If the network response is lost, retry uses the same
// decision ID with a fresh authenticated request nonce.
func (s *Store) StageFirstCopyFinalizeSend(gameID, txID, stage string) (FirstCopyFinalizeMessage, string, error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return FirstCopyFinalizeMessage{}, "", err
	}
	defer tx.Rollback()
	lease, decision, err := s.finalizeBasisTx(tx, gameID, txID)
	if err != nil {
		return FirstCopyFinalizeMessage{}, "", err
	}
	guard, err := readFirstCopyPairGuard(tx, gameID)
	if err != nil {
		return FirstCopyFinalizeMessage{}, "", err
	}
	if !guardMatchesBasis(guard, lease, decision) {
		return FirstCopyFinalizeMessage{}, "", fmt.Errorf("first finalize locally on this device")
	}
	next := guard.Phase
	switch stage {
	case FinalizeOffer:
		if lease.Role != FirstCopySource || (next != FinalizeIntent && next != FinalizeOffered) {
			return FirstCopyFinalizeMessage{}, "", fmt.Errorf("only source offers from local intent")
		}
		next = FinalizeOffered
	case FinalizeReady:
		if lease.Role != FirstCopyTarget || (next != FinalizeOffered && next != FinalizeReadyPhase) {
			return FirstCopyFinalizeMessage{}, "", fmt.Errorf("target must receive source offer before Ready")
		}
		next = FinalizeReadyPhase
	case FinalizeCommit:
		if lease.Role != FirstCopySource || (next != FinalizeReadyPhase && next != FinalizePairFinalized) {
			return FirstCopyFinalizeMessage{}, "", fmt.Errorf("source requires authenticated target Ready")
		}
		next = FinalizePairFinalized
	case FinalizeAck:
		if lease.Role != FirstCopyTarget || next != FinalizePairFinalized {
			return FirstCopyFinalizeMessage{}, "", fmt.Errorf("target must persist pair finalization before Ack")
		}
	case FinalizeAbort:
		if next != FinalizeCancelled {
			return FirstCopyFinalizeMessage{}, "", fmt.Errorf("cancel locally before sending signed abort")
		}
	default:
		return FirstCopyFinalizeMessage{}, "", fmt.Errorf("unsupported signed finalize stage")
	}
	if next != guard.Phase {
		if _, err := tx.Exec(`UPDATE game_first_copy_pair_guards SET phase=?,updated_at=? WHERE game_id=? AND local_tx_id=? AND phase=?`,
			next, time.Now().UTC().Format(time.RFC3339Nano), gameID, txID, guard.Phase); err != nil {
			return FirstCopyFinalizeMessage{}, "", err
		}
	}
	msg := finalizeMessage(guard, stage)
	if err = tx.Commit(); err != nil {
		return FirstCopyFinalizeMessage{}, "", err
	}
	return msg, lease.PeerID, nil
}

// AcceptFirstCopyFinalizeMessage is only for an already pairing-MAC-authenticated
// sender. The signed nonce is stored with the state change in one transaction.
func (s *Store) AcceptFirstCopyFinalizeMessage(gameID, peer, nonce string, m FirstCopyFinalizeMessage) error {
	if !validFinalizeMessage(m) || m.GameID != gameID || peer == "" || nonce == "" || len(nonce) > 256 {
		return fmt.Errorf("invalid signed pair finalization")
	}
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Never derive the local transaction identifier from untrusted JSON.
	local, err := s.readFirstCopy(tx, gameID)
	if err != nil {
		return err
	}
	if local == nil {
		return fmt.Errorf("local first-copy fence missing")
	}
	lease, decision, err := s.finalizeBasisTx(tx, gameID, local.TxID)
	if err != nil {
		return err
	}
	if m.Role == lease.Role || peer != lease.PeerID || m.DecisionID != decision.DecisionID ||
		m.SourceTxID != decision.SourceTxID || m.TargetTxID != decision.TargetTxID ||
		m.GenerationID != decision.GenerationID || m.Branch != decision.Branch || m.Digest != decision.Digest {
		return fmt.Errorf("signed finalization does not match committed named pair")
	}
	guard, err := readFirstCopyPairGuard(tx, gameID)
	if err != nil {
		return err
	}
	if guard == nil {
		return fmt.Errorf("explicit local finalization intent is required")
	}
	if !guardMatchesBasis(guard, lease, decision) {
		return fmt.Errorf("local pair guard identity changed")
	}
	var veto int
	if err := tx.Get(&veto, `SELECT COUNT(*) FROM game_first_copy_finalize_vetoes WHERE game_id=? AND decision_id=?`, gameID, m.DecisionID); err != nil {
		return err
	}
	if veto != 0 && m.Stage != FinalizeAbort {
		return fmt.Errorf("finalization was vetoed")
	}
	next := guard.Phase
	switch m.Stage {
	case FinalizeOffer:
		if lease.Role != FirstCopyTarget || (next != FinalizeIntent && next != FinalizeOffered) {
			return fmt.Errorf("target cannot accept source offer in state %s", next)
		}
		next = FinalizeOffered
	case FinalizeReady:
		if lease.Role != FirstCopySource || (next != FinalizeOffered && next != FinalizeReadyPhase) {
			return fmt.Errorf("source cannot accept target Ready in state %s", next)
		}
		next = FinalizeReadyPhase
	case FinalizeCommit:
		if lease.Role != FirstCopyTarget || (next != FinalizeReadyPhase && next != FinalizePairFinalized) {
			return fmt.Errorf("target cannot accept commit in state %s", next)
		}
		next = FinalizePairFinalized
	case FinalizeAck:
		if lease.Role != FirstCopySource || next != FinalizePairFinalized {
			return fmt.Errorf("source requires committed guard before Ack")
		}
		guard.PeerAck = true
	case FinalizeAbort:
		if next == FinalizePairFinalized {
			return fmt.Errorf("committed pair finalization cannot be unilaterally aborted")
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO game_first_copy_finalize_vetoes (game_id,decision_id) VALUES (?,?)`, gameID, m.DecisionID); err != nil {
			return err
		}
		next = FinalizeCancelled
	}
	if _, err := tx.Exec(`INSERT INTO game_first_copy_finalize_nonces (game_id,decision_id,peer_id,nonce)
        VALUES (?,?,?,?)`, gameID, m.DecisionID, peer, nonce); err != nil {
		return fmt.Errorf("signed finalize nonce replayed: %w", err)
	}
	if _, err := tx.Exec(`UPDATE game_first_copy_pair_guards SET phase=?,peer_ack=?,updated_at=? WHERE game_id=? AND local_tx_id=? AND phase=?`,
		next, guard.PeerAck, time.Now().UTC().Format(time.RFC3339Nano), gameID, lease.TxID, guard.Phase); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CancelFirstCopyPairFinalization(gameID, txID string) error {
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	lease, decision, err := s.finalizeBasisTx(tx, gameID, txID)
	if err != nil {
		return err
	}
	guard, err := readFirstCopyPairGuard(tx, gameID)
	if err != nil {
		return err
	}
	if !guardMatchesBasis(guard, lease, decision) {
		return fmt.Errorf("local finalization not present")
	}
	if guard.Phase == FinalizePairFinalized {
		return fmt.Errorf("pair-finalized state requires coordinated recovery")
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO game_first_copy_finalize_vetoes(game_id,decision_id) VALUES (?,?)`, gameID, decision.DecisionID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE game_first_copy_pair_guards SET phase=?,updated_at=? WHERE game_id=? AND local_tx_id=?`,
		FinalizeCancelled, time.Now().UTC().Format(time.RFC3339Nano), gameID, txID); err != nil {
		return err
	}
	return tx.Commit()
}

func finalizationStatus(g *FirstCopyPairGuard) FirstCopyFinalizationStatus {
	s := FirstCopyFinalizationStatus{GameID: g.GameID, TxID: g.LocalTxID, DecisionID: g.DecisionID,
		Phase: g.Phase, PeerAck: g.PeerAck, FenceActive: true, ThirdPeerAdmitted: false, AutoSync: false, OpenAuthorized: false}
	s.PairFinalized = g.Phase == FinalizePairFinalized
	s.RecoveryRequired = g.Phase == FinalizeCancelled || (g.Phase != FinalizeIntent && (!s.PairFinalized || (g.Role == FirstCopySource && !g.PeerAck)))
	return s
}

func (s *Store) ReadFirstCopyPairFinalization(gameID, txID string) (FirstCopyFinalizationStatus, error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return FirstCopyFinalizationStatus{}, err
	}
	defer tx.Rollback()
	lease, decision, err := s.finalizeBasisTx(tx, gameID, txID)
	if err != nil {
		return FirstCopyFinalizationStatus{}, err
	}
	g, err := readFirstCopyPairGuard(tx, gameID)
	if err != nil {
		return FirstCopyFinalizationStatus{}, err
	}
	if g == nil {
		return FirstCopyFinalizationStatus{GameID: gameID, TxID: txID, DecisionID: decision.DecisionID, Phase: "not_started", FenceActive: true}, nil
	}
	if !guardMatchesBasis(g, lease, decision) {
		return FirstCopyFinalizationStatus{}, fmt.Errorf("pair guard is inconsistent with C.2 decision")
	}
	return finalizationStatus(g), nil
}
