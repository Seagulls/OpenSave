package store

import (
	"fmt"
	"time"
)

// verifyDecisionEvidenceInTransaction closes the C.2 cancellation race:
// no signed decision or new vote can rely only on preparation/receipt evidence
// sampled BEFORE its SQLite write transaction. C.2 commits already persisted
// may be retransmitted without fresh two-minute readiness, since they do not
// change any native save permissions.
func (s *Store) verifyDecisionEvidenceInTransaction(q rowQuery, gameID string, lease *FirstCopy,
	basis FirstCopyHandoverPrepare, now time.Time) error {
	fence, err := s.readFirstCopy(q, gameID)
	if err != nil {
		return err
	}
	if fence == nil || fence.TxID != lease.TxID || fence.Phase != FirstCopyActivated || fence.PeerID != lease.PeerID ||
		fence.Role != lease.Role || fence.ContentHash != basis.Digest {
		return fmt.Errorf("decision fence changed during authorization")
	}
	var local firstCopyHandoverRow
	if err := q.Get(&local, `SELECT game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,
        generation_id,branch,digest,role,prepared_at FROM game_first_copy_handover_local WHERE game_id=?`, gameID); err != nil {
		return fmt.Errorf("local preparation cancelled: %w", err)
	}
	if !firstCopyHandoverMatches(local, basis, lease.PeerID, lease.TxID) || !firstCopyHandoverFresh(local.PreparedAt, now) {
		return fmt.Errorf("local handover preparation expired or changed")
	}
	remoteBasis := basis
	if lease.Role == FirstCopySource {
		remoteBasis.Role = FirstCopyTarget
	} else {
		remoteBasis.Role = FirstCopySource
	}
	var remote firstCopyHandoverRow
	if err := q.Get(&remote, `SELECT game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,
        generation_id,branch,digest,role,received_at FROM game_first_copy_handover_remote WHERE game_id=?`, gameID); err != nil {
		return fmt.Errorf("peer preparation cancelled: %w", err)
	}
	if !firstCopyHandoverMatches(remote, remoteBasis, lease.PeerID, lease.TxID) || !firstCopyHandoverFresh(remote.ReceivedAt, now) {
		return fmt.Errorf("signed remote preparation expired or changed")
	}
	var receipt remoteReceiptRow
	if err := q.Get(&receipt, `SELECT game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,
        generation_id,branch,digest,role,phase,received_at FROM game_first_copy_remote_receipts
        WHERE game_id=? AND local_tx_id=?`, gameID, lease.TxID); err != nil {
		return fmt.Errorf("signed Phase B receipt absent: %w", err)
	}
	if receipt.PeerID != lease.PeerID || receipt.SourceTxID != basis.SourceTxID ||
		receipt.TargetTxID != basis.TargetTxID || receipt.GenerationID != basis.GenerationID ||
		receipt.Branch != basis.Branch || receipt.Digest != basis.Digest || receipt.Role != remoteBasis.Role ||
		receipt.Phase != FirstCopyActivated || !firstCopyHandoverFresh(receipt.ReceivedAt, now) {
		return fmt.Errorf("signed Phase B evidence expired or changed")
	}
	return nil
}

// The already committed decision is durable recovery evidence, NOT an Open
// authorization. Reassertion after timeout must use fresh signed transport
// nonce and pass the C.3 local filesystem checks; it cannot create a new vote.
func (s *Store) committedDecisionBasis(gameID, txID string) (FirstCopyHandoverPrepare, *FirstCopy, error) {
	lease, err := s.RecordedFirstCopy(gameID)
	if err != nil {
		return FirstCopyHandoverPrepare{}, nil, err
	}
	if lease == nil || lease.TxID != txID || lease.Phase != FirstCopyActivated {
		return FirstCopyHandoverPrepare{}, nil, fmt.Errorf("activated transaction required")
	}
	receipt, peer, err := s.OutboundFirstCopyReceipt(gameID, txID)
	if err != nil {
		return FirstCopyHandoverPrepare{}, nil, err
	}
	if peer != lease.PeerID || receipt.Digest != lease.ContentHash {
		return FirstCopyHandoverPrepare{}, nil, fmt.Errorf("local receipt does not match fence")
	}
	return firstCopyHandoverFromReceipt(receipt), lease, nil
}
