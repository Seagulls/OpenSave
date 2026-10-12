package store

import (
	"strings"
	"testing"
	"time"
)

// The fixture first establishes the exact C.2 committed decision on a target.
// No test touches the user's live game directories or databases.
func finalizedTargetFixture(t *testing.T) (*Store, FirstCopy, FirstCopyFinalizeMessage, string) {
	t.Helper()
	s, row, offer, path := decisionTargetFixture(t)
	if err := s.AcceptFirstCopyDecision(row.GameID, row.PeerID, "c3-offer-nonce", offer); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeFirstCopyDecision(row.GameID, row.TxID, offer.DecisionID); err != nil {
		t.Fatal(err)
	}
	commit := offer
	commit.Stage = DecisionCommit
	if err := s.AcceptFirstCopyDecision(row.GameID, row.PeerID, "c3-commit-nonce", commit); err != nil {
		t.Fatal(err)
	}
	msg := FirstCopyFinalizeMessage{
		Version: FirstCopyFinalizeVersion, GameID: row.GameID, SourceTxID: commit.SourceTxID,
		TargetTxID: commit.TargetTxID, GenerationID: commit.GenerationID, Branch: commit.Branch,
		Digest: commit.Digest, DecisionID: commit.DecisionID, Role: FirstCopySource, Stage: FinalizeOffer,
	}
	return s, row, msg, path
}

func TestFirstCopyPairFinalize_GuardVetoAndRetainedLease(t *testing.T) {
	s, row, msg, _ := finalizedTargetFixture(t)
	status, err := s.BeginFirstCopyPairFinalization(row.GameID, row.TxID)
	if err != nil || status.Phase != FinalizeIntent || !status.FenceActive || status.OpenAuthorized || status.AutoSync {
		t.Fatalf("intent %+v %v", status, err)
	}
	if err := s.OpenFirstCopy(row.GameID, row.TxID); err == nil {
		t.Fatal("legacy Open bypassed committed C.2 and C.3 guard")
	}
	if _, err := s.db.Exec(`DELETE FROM game_first_copies WHERE game_id=?`, row.GameID); err == nil {
		t.Fatal("pair guard allowed original lease deletion")
	}
	if _, err := s.db.Exec(`DELETE FROM games WHERE id=?`, row.GameID); err == nil {
		t.Fatal("pair guard allowed game deletion")
	}
	msg.Digest = "primary:tampered"
	if err := s.AcceptFirstCopyFinalizeMessage(row.GameID, row.PeerID, "bad-digest", msg); err == nil {
		t.Fatal("wrong digest accepted")
	}
	msg.Digest = "primary:verified-first-copy"
	if err := s.AcceptFirstCopyFinalizeMessage(row.GameID, "impostor", "wrong-peer", msg); err == nil {
		t.Fatal("unbound peer accepted")
	}
	if err := s.AcceptFirstCopyFinalizeMessage(row.GameID, row.PeerID, "source-offer", msg); err != nil {
		t.Fatal(err)
	}
	status, err = s.ReadFirstCopyPairFinalization(row.GameID, row.TxID)
	if err != nil || status.Phase != FinalizeOffered || status.PairFinalized || !status.FenceActive {
		t.Fatalf("offer status %+v %v", status, err)
	}
	if err := s.AcceptFirstCopyFinalizeMessage(row.GameID, row.PeerID, "source-offer", msg); err == nil {
		t.Fatal("reused signed nonce")
	}
	guard, err := s.PairGuardForServe(row.GameID)
	if err != nil || guard == nil || guard.Role != FirstCopyTarget || guard.PeerID != row.PeerID {
		t.Fatalf("pair guard %+v %v", guard, err)
	}
	game, err := s.GetGame(row.GameID)
	if err != nil || game.AutoSync {
		t.Fatalf("AutoSync changed %+v %v", game, err)
	}
}

func TestFirstCopyPairFinalize_TargetCommitAckRestartAndReplay(t *testing.T) {
	s, row, msg, path := finalizedTargetFixture(t)
	if _, err := s.BeginFirstCopyPairFinalization(row.GameID, row.TxID); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptFirstCopyFinalizeMessage(row.GameID, row.PeerID, "new-offer", msg); err != nil {
		t.Fatal(err)
	}
	out, peer, err := s.StageFirstCopyFinalizeSend(row.GameID, row.TxID, FinalizeReady)
	if err != nil || peer != row.PeerID || out.Role != FirstCopyTarget || out.Stage != FinalizeReady {
		t.Fatalf("Ready %+v %s %v", out, peer, err)
	}
	msg.Stage = FinalizeCommit
	if err := s.AcceptFirstCopyFinalizeMessage(row.GameID, row.PeerID, "signed-commit", msg); err != nil {
		t.Fatal(err)
	}
	status, err := s.ReadFirstCopyPairFinalization(row.GameID, row.TxID)
	if err != nil || !status.PairFinalized || status.ThirdPeerAdmitted || status.AutoSync || status.OpenAuthorized {
		t.Fatalf("pair-finalized %+v %v", status, err)
	}
	if _, _, err := s.StageFirstCopyFinalizeSend(row.GameID, row.TxID, FinalizeAck); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM game_first_copies WHERE game_id=?`, row.GameID); err == nil {
		t.Fatal("finalized guard lost legacy fence")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	status, err = next.ReadFirstCopyPairFinalization(row.GameID, row.TxID)
	if err != nil || !status.PairFinalized || !status.FenceActive {
		t.Fatalf("restart lost guard %+v %v", status, err)
	}
	if err := next.AcceptFirstCopyFinalizeMessage(row.GameID, row.PeerID, "signed-commit", msg); err == nil {
		t.Fatal("restart accepted replayed signed commit")
	}
	if err := next.AcceptFirstCopyFinalizeMessage(row.GameID, row.PeerID, "fresh-retransmission", msg); err != nil {
		t.Fatalf("fresh signed idempotent retry refused: %v", err)
	}
	if err := next.CancelFirstCopyPairFinalization(row.GameID, row.TxID); err == nil {
		t.Fatal("unilateral cancel of finalized pair succeeded")
	}
}

func TestFirstCopyPairFinalize_CancelRetainsPermanentVeto(t *testing.T) {
	s, row, msg, path := finalizedTargetFixture(t)
	if _, err := s.BeginFirstCopyPairFinalization(row.GameID, row.TxID); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelFirstCopyPairFinalization(row.GameID, row.TxID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StageFirstCopyFinalizeSend(row.GameID, row.TxID, FinalizeOffer); err == nil {
		t.Fatal("cancelled intent sent offer")
	}
	if err := s.AcceptFirstCopyFinalizeMessage(row.GameID, row.PeerID, "late-offer", msg); err == nil {
		t.Fatal("cancelled finalization accepted late offer")
	}
	if _, _, err := s.StageFirstCopyFinalizeSend(row.GameID, row.TxID, FinalizeAbort); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginFirstCopyPairFinalization(row.GameID, row.TxID); err == nil {
		t.Fatal("cancelled decision reused")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resumed.Close() })
	status, err := resumed.ReadFirstCopyPairFinalization(row.GameID, row.TxID)
	if err != nil || status.Phase != FinalizeCancelled || !status.FenceActive || !status.RecoveryRequired {
		t.Fatalf("cancel reboot %+v %v", status, err)
	}
	if err := resumed.AcceptFirstCopyFinalizeMessage(row.GameID, row.PeerID, "new-late-offer", msg); err == nil {
		t.Fatal("restart forgot veto")
	}
}

func TestFirstCopyDecision_AtomicRecheckOfCancelledPreparations(t *testing.T) {
	s, row, offer, _ := decisionTargetFixture(t)
	basis, lease, err := s.decisionBasis(row.GameID, row.TxID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM game_first_copy_handover_local WHERE game_id=?`, row.GameID); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.verifyDecisionEvidenceInTransaction(tx, row.GameID, lease, basis, nowForDecisionTest()); err == nil {
		t.Fatal("missing intent passed transactional recheck")
	}
	_ = tx.Rollback()
	if err := s.AcceptFirstCopyDecision(row.GameID, row.PeerID, "late-decision", offer); err == nil {
		t.Fatal("decision accepted after cancellation")
	}
}

func nowForDecisionTest() time.Time { return time.Now().UTC() }

func TestFirstCopyPairFinalize_PersistedDecisionDoesNotDependOnStaleReceipt(t *testing.T) {
	s, row, msg, _ := finalizedTargetFixture(t)
	if _, err := s.db.Exec(`UPDATE game_first_copy_remote_receipts SET received_at='2000-01-01T00:00:00Z' WHERE game_id=?`, row.GameID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginFirstCopyPairFinalization(row.GameID, row.TxID); err != nil {
		t.Fatalf("durable signed C.2 decision could not recover: %v", err)
	}
	if err := s.AcceptFirstCopyFinalizeMessage(row.GameID, row.PeerID, "recover-offer", msg); err != nil {
		t.Fatalf("recovery message refused: %v", err)
	}
	if _, _, err := s.StageFirstCopyFinalizeSend(row.GameID, row.TxID, FinalizeReady); err != nil {
		t.Fatal(err)
	}
	status, err := s.ReadFirstCopyPairFinalization(row.GameID, row.TxID)
	if err != nil || status.Phase != FinalizeReadyPhase || !status.FenceActive {
		t.Fatalf("unsafe recovery %+v %v", status, err)
	}
	if !strings.HasPrefix(status.DecisionID, "aaaa") {
		t.Fatalf("unexpected decision %q", status.DecisionID)
	}
}

func TestFirstCopyPairFinalize_RemoteAbortIsDurableVeto(t *testing.T) {
	s, row, msg, path := finalizedTargetFixture(t)
	if _, err := s.BeginFirstCopyPairFinalization(row.GameID, row.TxID); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptFirstCopyFinalizeMessage(row.GameID, row.PeerID, "initial-offer", msg); err != nil {
		t.Fatal(err)
	}
	msg.Stage = FinalizeAbort
	if err := s.AcceptFirstCopyFinalizeMessage(row.GameID, row.PeerID, "signed-abort", msg); err != nil {
		t.Fatal(err)
	}
	msg.Stage = FinalizeOffer
	if err := s.AcceptFirstCopyFinalizeMessage(row.GameID, row.PeerID, "late-post-abort", msg); err == nil {
		t.Fatal("stale offer revived vetoed decision")
	}
	guard, err := s.PairGuardForServe(row.GameID)
	if err != nil || guard == nil || guard.Phase != FinalizeCancelled {
		t.Fatalf("missing quarantine %+v %v", guard, err)
	}
	if _, err := s.db.Exec(`DELETE FROM game_first_copies WHERE game_id=?`, row.GameID); err == nil {
		t.Fatal("abort removed durable fence")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.AcceptFirstCopyFinalizeMessage(row.GameID, row.PeerID, "new-nonce-same-offer", msg); err == nil {
		t.Fatal("veto not persisted across restart")
	}
}
