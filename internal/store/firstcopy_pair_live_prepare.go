package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Tranche 2 preparation is not a pair-live permission. It is only a durable
// authenticated handshake, retained across restart for a later safe release.
const PairLivePrepareVersion = 1

type PairLiveReadyMessage struct {
	Version        int    `json:"version"`
	GameID         string `json:"gameId"`
	SourceTxID     string `json:"sourceTxId"`
	TargetTxID     string `json:"targetTxId"`
	GenerationID   string `json:"generationId"`
	Branch         string `json:"branch"`
	Digest         string `json:"digest"`
	DecisionID     string `json:"decisionId"`
	Role           string `json:"role"`
	Kind           string `json:"kind,omitempty"`
	RootCommitment string `json:"rootCommitment,omitempty"`
}

type PairLivePrepareStatus struct {
	GameID             string `db:"game_id" json:"gameId"`
	LocalTxID          string `db:"local_tx_id" json:"localTxId"`
	PeerID             string `db:"peer_id" json:"peerId"`
	SourceTxID         string `db:"source_tx_id" json:"sourceTxId"`
	TargetTxID         string `db:"target_tx_id" json:"targetTxId"`
	GenerationID       string `db:"generation_id" json:"generationId"`
	Branch             string `db:"branch" json:"branch"`
	Digest             string `db:"digest" json:"digest"`
	DecisionID         string `db:"decision_id" json:"decisionId"`
	Role               string `db:"role" json:"role"`
	PeerReady          bool   `db:"peer_ready" json:"peerReady"`
	CreatedAt          string `db:"created_at" json:"createdAt"`
	PeerReadyAt        string `db:"peer_ready_at" json:"peerReadyAt"`
	LocalActive        bool   `db:"local_active" json:"localActive"`
	PeerActive         bool   `db:"peer_active" json:"peerActive"`
	RootCommitment     string `db:"root_commitment" json:"rootCommitment"`
	PeerRootCommitment string `db:"peer_root_commitment" json:"peerRootCommitment"`
	// Intentionally false: no source/target native admission is implemented
	// in the preparation-only build. Do not mistake readiness for release.
	NativePairSyncEnabled bool `json:"nativePairSyncEnabled"`
	GeneralPeerAdmission  bool `json:"generalPeerAdmission"`
	AutoSync              bool `json:"autoSync"`
	FenceActive           bool `json:"fenceActive"`
}

func readPairLivePrepare(q rowQuery, gameID string) (*PairLivePrepareStatus, error) {
	var p PairLivePrepareStatus
	err := q.Get(&p, `SELECT game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,generation_id,
      branch,digest,decision_id,role,peer_ready,created_at,peer_ready_at,
      local_active,peer_active,root_commitment,peer_root_commitment
      FROM game_first_copy_pair_live_prepares WHERE game_id=?`, gameID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pair-live preparation: %w", err)
	}
	p.FenceActive = true
	p.NativePairSyncEnabled = p.LocalActive && p.PeerActive && p.RootCommitment != "" && p.RootCommitment == p.PeerRootCommitment
	return &p, nil
}

func pairLiveMessage(g *FirstCopyPairGuard) PairLiveReadyMessage {
	return PairLiveReadyMessage{Version: PairLivePrepareVersion, GameID: g.GameID,
		SourceTxID: g.SourceTxID, TargetTxID: g.TargetTxID, GenerationID: g.GenerationID,
		Branch: g.Branch, Digest: g.Digest, DecisionID: g.DecisionID, Role: g.Role}
}

// pairLiveBasisTx never trusts a local ready row as authority: the original
// activated lease and finalized C.3 guard must both still match.
func (s *Store) pairLiveBasisTx(q rowQuery, gameID, txID string) (*FirstCopyPairGuard, error) {
	if err := s.provisioningFault(); err != nil {
		return nil, err
	}
	g, err := readFirstCopyPairGuard(q, gameID)
	if err != nil {
		return nil, err
	}
	if g == nil || g.Phase != FinalizePairFinalized || g.LocalTxID != txID ||
		g.DecisionID == "" || g.Digest == "" || g.PeerID == "" ||
		(g.Role == FirstCopySource && !g.PeerAck) ||
		(g.Role != FirstCopySource && g.Role != FirstCopyTarget) {
		return nil, fmt.Errorf("pair-live requires finalized C.3 guard")
	}
	lease, err := s.readFirstCopy(q, gameID)
	if err != nil {
		return nil, err
	}
	if lease == nil || lease.TxID != txID || lease.Phase != FirstCopyActivated ||
		lease.Role != g.Role || lease.PeerID != g.PeerID || lease.ContentHash != g.Digest ||
		(lease.Role == FirstCopySource && (lease.TxID != g.SourceTxID || lease.GenerationID != g.GenerationID || lease.GenerationBranch != g.Branch)) ||
		(lease.Role == FirstCopyTarget && (lease.TxID != g.TargetTxID || lease.SourceTxID != g.SourceTxID || lease.SourceGenerationID != g.GenerationID || lease.SourceGenerationBranch != g.Branch)) {
		return nil, fmt.Errorf("pair-live lease does not match finalized guard")
	}
	var autoSync int
	if err := q.Get(&autoSync, `SELECT auto_sync FROM games WHERE id=?`, gameID); err != nil {
		return nil, err
	}
	if autoSync != 0 {
		return nil, fmt.Errorf("pair-live requires AutoSync disabled")
	}
	held, err := txHeld(q, gameID)
	if err != nil {
		return nil, err
	}
	if held {
		return nil, fmt.Errorf("pair-live requires activated hold release")
	}
	return g, nil
}

func (s *Store) PreparePairLive(gameID, txID string) (PairLivePrepareStatus, error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return PairLivePrepareStatus{}, err
	}
	defer tx.Rollback()
	guard, err := s.pairLiveBasisTx(tx, gameID, txID)
	if err != nil {
		return PairLivePrepareStatus{}, err
	}
	old, err := readPairLivePrepare(tx, gameID)
	if err != nil {
		return PairLivePrepareStatus{}, err
	}
	if old != nil {
		if !pairLiveStatusMatches(*old, guard) {
			return PairLivePrepareStatus{}, fmt.Errorf("existing pair-live preparation has different identity")
		}
		return *old, nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = tx.Exec(`INSERT INTO game_first_copy_pair_live_prepares
      (game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,generation_id,branch,digest,decision_id,role,created_at)
      VALUES(?,?,?,?,?,?,?,?,?,?,?)`, gameID, txID, guard.PeerID, guard.SourceTxID, guard.TargetTxID,
		guard.GenerationID, guard.Branch, guard.Digest, guard.DecisionID, guard.Role, now)
	if err != nil {
		return PairLivePrepareStatus{}, err
	}
	if err = tx.Commit(); err != nil {
		return PairLivePrepareStatus{}, err
	}
	return PairLivePrepareStatus{GameID: gameID, LocalTxID: txID, PeerID: guard.PeerID,
		SourceTxID: guard.SourceTxID, TargetTxID: guard.TargetTxID, GenerationID: guard.GenerationID,
		Branch: guard.Branch, Digest: guard.Digest, DecisionID: guard.DecisionID, Role: guard.Role,
		CreatedAt: now, FenceActive: true}, nil
}
func pairLiveStatusMatches(p PairLivePrepareStatus, g *FirstCopyPairGuard) bool {
	return p.GameID == g.GameID && p.LocalTxID == g.LocalTxID && p.PeerID == g.PeerID &&
		p.SourceTxID == g.SourceTxID && p.TargetTxID == g.TargetTxID &&
		p.GenerationID == g.GenerationID && p.Branch == g.Branch && p.Digest == g.Digest &&
		p.DecisionID == g.DecisionID && p.Role == g.Role
}

func (s *Store) ReadPairLivePrepare(gameID, txID string) (PairLivePrepareStatus, error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return PairLivePrepareStatus{}, err
	}
	defer tx.Rollback()
	guard, err := s.pairLiveBasisTx(tx, gameID, txID)
	if err != nil {
		return PairLivePrepareStatus{}, err
	}
	status, err := readPairLivePrepare(tx, gameID)
	if err != nil {
		return PairLivePrepareStatus{}, err
	}
	if status == nil || !pairLiveStatusMatches(*status, guard) {
		return PairLivePrepareStatus{}, fmt.Errorf("pair-live preparation is missing or inconsistent")
	}
	return *status, nil
}
func (s *Store) OutboundPairLiveReady(gameID, txID string) (PairLiveReadyMessage, string, error) {
	status, err := s.ReadPairLivePrepare(gameID, txID)
	if err != nil {
		return PairLiveReadyMessage{}, "", err
	}
	return PairLiveReadyMessage{Version: PairLivePrepareVersion, GameID: status.GameID,
		SourceTxID: status.SourceTxID, TargetTxID: status.TargetTxID,
		GenerationID: status.GenerationID, Branch: status.Branch, Digest: status.Digest,
		DecisionID: status.DecisionID, Role: status.Role}, status.PeerID, nil
}

// AcceptPairLiveReady trusts peer/nonce ONLY from existing verified signed
// request middleware. All data changes and permission expansions remain
// prohibited. The nonce is durable even if the connection then drops.
func (s *Store) AcceptPairLiveReady(gameID, peerID, nonce string, m PairLiveReadyMessage) error {
	if gameID == "" || peerID == "" || nonce == "" || (m.Kind != "" && m.Kind != "active") || m.Version != PairLivePrepareVersion || m.GameID != gameID ||
		m.DecisionID == "" || m.GenerationID == "" || m.Digest == "" || m.Branch == "" ||
		(m.Role != FirstCopySource && m.Role != FirstCopyTarget) {
		return fmt.Errorf("invalid authenticated pair-live readiness")
	}
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	status, err := readPairLivePrepare(tx, gameID)
	if err != nil {
		return err
	}
	if status == nil {
		return fmt.Errorf("local preparation must precede peer readiness")
	}
	guard, err := s.pairLiveBasisTx(tx, gameID, status.LocalTxID)
	if err != nil {
		return err
	}
	expected := pairLiveMessage(guard)
	if !pairLiveStatusMatches(*status, guard) || peerID != status.PeerID ||
		m.Role == status.Role || m.SourceTxID != expected.SourceTxID ||
		m.TargetTxID != expected.TargetTxID || m.GenerationID != expected.GenerationID ||
		m.Branch != expected.Branch || m.Digest != expected.Digest || m.DecisionID != expected.DecisionID {
		return fmt.Errorf("peer readiness does not match finalized local pair")
	}
	if _, err = tx.Exec(`INSERT INTO game_first_copy_pair_live_nonces(game_id,decision_id,peer_id,nonce)
       VALUES(?,?,?,?)`, gameID, m.DecisionID, peerID, nonce); err != nil {
		return fmt.Errorf("pair-live readiness replay refused: %w", err)
	}
	if m.Kind == "active" {
		if len(m.RootCommitment) != 64 || (status.RootCommitment != "" && status.RootCommitment != m.RootCommitment) ||
			(status.PeerRootCommitment != "" && status.PeerRootCommitment != m.RootCommitment) {
			return fmt.Errorf("peer-live commitment mismatch")
		}
		if _, err = tx.Exec(`UPDATE game_first_copy_pair_live_prepares SET peer_active=1,peer_root_commitment=? WHERE game_id=?`, m.RootCommitment, gameID); err != nil {
			return err
		}
	} else {
		if _, err = tx.Exec(`UPDATE game_first_copy_pair_live_prepares SET peer_ready=1,peer_ready_at=? WHERE game_id=?`,
			time.Now().UTC().Format(time.RFC3339Nano), gameID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
