package store

import (
	"fmt"
	"time"
)

// PeerAccessState is a request-scoped, consistent view of the three durable
// restrictions that govern a peer transfer. It is never cached between
// requests or reused to authorize a state-changing operation.
//
// In particular, nil Lease means an expired, unactivated lease is not a
// transfer authorization. An activated lease remains in effect indefinitely.
type PeerAccessState struct {
	Held  bool
	Lease *FirstCopy
	Guard *FirstCopyPairGuard
}

// validatePeerAccessGuard is shared by the normal guard reader and the
// combined hot-path query so their fail-closed validation cannot drift.
func validatePeerAccessGuard(g *FirstCopyPairGuard, gameID string) error {
	if g == nil || g.PeerID == "" || g.DecisionID == "" || g.Digest == "" || g.GameID != gameID ||
		(g.Role != FirstCopySource && g.Role != FirstCopyTarget) ||
		(g.Phase != FinalizeIntent && g.Phase != FinalizeOffered && g.Phase != FinalizeReadyPhase &&
			g.Phase != FinalizePairFinalized && g.Phase != FinalizeCancelled) {
		return fmt.Errorf("invalid durable pair guard; refusing access")
	}
	return nil
}

// PeerTransferAccess reads provisioning, the first-copy direction fence and
// the Phase C pair-only guard with ONE indexed SQL statement. The query takes
// a single SQLite statement snapshot instead of interleaving three SELECTs.
// Its read never grants long-lived permission and does not replace the
// transaction-time checks used when a peer changes or writes save content.
func (s *Store) PeerTransferAccess(gameID string) (PeerAccessState, error) {
	if s == nil {
		return PeerAccessState{}, fmt.Errorf("peer access store unavailable")
	}
	if gameID == "" {
		return PeerAccessState{}, nil
	}
	if err := s.provisioningFault(); err != nil {
		return PeerAccessState{}, err
	}
	var row struct {
		Held         int    `db:"held"`
		LeasePresent int    `db:"lease_present"`
		LeaseTxID    string `db:"lease_tx_id"`
		LeaseRole    string `db:"lease_role"`
		LeasePeer    string `db:"lease_peer"`
		LeasePhase   string `db:"lease_phase"`
		LeaseDigest  string `db:"lease_digest"`
		LeaseExpiry  string `db:"lease_expiry"`
		GuardPresent int    `db:"guard_present"`
		GuardLocalTx string `db:"guard_local_tx"`
		GuardPeer    string `db:"guard_peer"`
		GuardRole    string `db:"guard_role"`
		GuardPhase   string `db:"guard_phase"`
		GuardDigest  string `db:"guard_digest"`
		GuardID      string `db:"guard_id"`
	}
	// The alias probe preserves ProvisioningBlocks semantics. Callers of
	// peerGameAccess normally pass a canonical tracked game ID, but an alias
	// must not turn a held game into an apparently ordinary game.
	err := s.db.Get(&row, `SELECT
		CASE WHEN EXISTS (SELECT 1 FROM game_provisioning_holds WHERE game_id=?)
		     OR EXISTS (SELECT 1 FROM game_aliases AS a
		       JOIN game_provisioning_holds AS h ON h.game_id=a.game_id
		       WHERE a.alias_id=?) THEN 1 ELSE 0 END AS held,
		CASE WHEN f.game_id IS NULL THEN 0 ELSE 1 END AS lease_present,
		COALESCE(f.tx_id,'') AS lease_tx_id,
		COALESCE(f.role,'') AS lease_role,
		COALESCE(f.peer_id,'') AS lease_peer,
		COALESCE(f.phase,'') AS lease_phase,
		COALESCE(f.content_hash,'') AS lease_digest,
		COALESCE(f.expires_at,'') AS lease_expiry,
		CASE WHEN g.game_id IS NULL THEN 0 ELSE 1 END AS guard_present,
		COALESCE(g.local_tx_id,'') AS guard_local_tx,
		COALESCE(g.peer_id,'') AS guard_peer,
		COALESCE(g.role,'') AS guard_role,
		COALESCE(g.phase,'') AS guard_phase,
		COALESCE(g.digest,'') AS guard_digest,
		COALESCE(g.decision_id,'') AS guard_id
		FROM (SELECT 1) AS singleton
		LEFT JOIN game_first_copies AS f ON f.game_id=?
		LEFT JOIN game_first_copy_pair_guards AS g ON g.game_id=?`,
		gameID, gameID, gameID, gameID)
	if err != nil {
		return PeerAccessState{}, fmt.Errorf("read peer transfer access: %w", err)
	}
	out := PeerAccessState{Held: row.Held != 0}
	if row.LeasePresent != 0 {
		lease := &FirstCopy{
			GameID: gameID, TxID: row.LeaseTxID, Role: row.LeaseRole,
			PeerID: row.LeasePeer, Phase: row.LeasePhase,
			ContentHash: row.LeaseDigest, ExpiresAt: row.LeaseExpiry,
		}
		if lease.Phase == FirstCopyActivated || !lease.expired(time.Now()) {
			out.Lease = lease
		}
	}
	if row.GuardPresent != 0 {
		guard := &FirstCopyPairGuard{
			GameID: gameID, LocalTxID: row.GuardLocalTx,
			PeerID: row.GuardPeer, Role: row.GuardRole,
			Phase: row.GuardPhase, Digest: row.GuardDigest,
			DecisionID: row.GuardID,
		}
		if err := validatePeerAccessGuard(guard, gameID); err != nil {
			return PeerAccessState{}, err
		}
		out.Guard = guard
	}
	return out, nil
}
