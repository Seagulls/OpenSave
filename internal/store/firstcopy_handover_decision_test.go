package store

import (
	"strings"
	"testing"
)

func decisionTargetFixture(t *testing.T) (*Store, FirstCopy, FirstCopyDecisionMessage, string) {
	t.Helper()
	s, row, receipt, path := handoverTargetFixture(t)
	if err := s.AcceptFirstCopyRemoteReceipt(row.GameID, row.PeerID, "phase-b-nonce", receipt); err != nil {
		t.Fatal(err)
	}
	prep, err := s.PrepareFirstCopyHandover(row.GameID, row.TxID)
	if err != nil {
		t.Fatal(err)
	}
	peer := prep
	peer.Role = FirstCopySource
	if err := s.AcceptFirstCopyHandoverPrepare(row.GameID, row.PeerID, "phase-c-prepare-nonce", peer); err != nil {
		t.Fatal(err)
	}
	msg := decisionMessage(prep, strings.Repeat("a", 32), DecisionOffer, FirstCopySource)
	return s, row, msg, path
}

func TestFirstCopyDecision_RequiresSignedOfferAndSeparateOperatorApproval(t *testing.T) {
	s, row, offer, _ := decisionTargetFixture(t)
	if err := s.AuthorizeFirstCopyDecision(row.GameID, row.TxID, offer.DecisionID); err == nil {
		t.Fatal("approved without signed offer")
	}
	if err := s.AcceptFirstCopyDecision(row.GameID, "wrong-peer", "signed-one", offer); err == nil {
		t.Fatal("wrong peer accepted")
	}
	wrong := offer
	wrong.Digest = "primary:another"
	if err := s.AcceptFirstCopyDecision(row.GameID, row.PeerID, "signed-two", wrong); err == nil {
		t.Fatal("wrong generation accepted")
	}
	if err := s.AcceptFirstCopyDecision(row.GameID, row.PeerID, "signed-three", offer); err != nil {
		t.Fatal(err)
	}
	status, err := s.ReadFirstCopyDecisionStatus(row.GameID, row.TxID)
	if err != nil || status.LocalAuthorized || status.DecisionRecorded || !status.FenceActive || status.OpenAuthorized || status.AutoSync {
		t.Fatalf("unapproved offer: %+v %v", status, err)
	}
	if _, _, err := s.OutboundFirstCopyDecision(row.GameID, row.TxID, DecisionApproval); err == nil {
		t.Fatal("sent approval before operator consent")
	}
	if err := s.AuthorizeFirstCopyDecision(row.GameID, row.TxID, offer.DecisionID); err != nil {
		t.Fatal(err)
	}
	approved, peer, err := s.OutboundFirstCopyDecision(row.GameID, row.TxID, DecisionApproval)
	if err != nil || peer != row.PeerID || approved.Role != FirstCopyTarget {
		t.Fatalf("wrong approved statement: %+v %q %v", approved, peer, err)
	}
	if err := s.AcceptFirstCopyDecision(row.GameID, row.PeerID, "signed-four", offer); err != nil {
		t.Fatalf("fresh signed duplicate offer should be idempotent: %v", err)
	}
	commit := offer
	commit.Stage = DecisionCommit
	if err := s.AcceptFirstCopyDecision(row.GameID, row.PeerID, "signed-five", commit); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptFirstCopyDecision(row.GameID, row.PeerID, "signed-five", commit); err == nil {
		t.Fatal("replayed nonce accepted")
	}
	if err := s.AcceptFirstCopyDecision(row.GameID, row.PeerID, "signed-six", commit); err != nil {
		t.Fatalf("fresh nonce retry refused: %v", err)
	}
	confirmed, _, err := s.OutboundFirstCopyDecision(row.GameID, row.TxID, DecisionConfirm)
	if err != nil || confirmed.Stage != DecisionConfirm {
		t.Fatalf("missing confirmation: %+v %v", confirmed, err)
	}
	status, err = s.ReadFirstCopyDecisionStatus(row.GameID, row.TxID)
	if err != nil || !status.DecisionRecorded || status.OpenAuthorized || !status.FenceActive || status.AutoSync {
		t.Fatalf("commit expanded permissions: %+v %v", status, err)
	}
	firstCopy, err := s.RecordedFirstCopy(row.GameID)
	if err != nil || firstCopy == nil || firstCopy.Phase != FirstCopyActivated {
		t.Fatalf("commit removed fence: %+v %v", firstCopy, err)
	}
	game, err := s.GetGame(row.GameID)
	if err != nil || game.AutoSync {
		t.Fatalf("commit enabled AutoSync: %+v %v", game, err)
	}
	if err := s.CancelFirstCopyDecision(row.GameID, row.TxID); err == nil {
		t.Fatal("unilateral cancel of committed decision accepted")
	}
}

func TestFirstCopyDecision_CancelTombstoneAndRestartReplay(t *testing.T) {
	s, row, offer, path := decisionTargetFixture(t)
	if err := s.AcceptFirstCopyDecision(row.GameID, row.PeerID, "offer-nonce", offer); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelFirstCopyDecision(row.GameID, row.TxID); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptFirstCopyDecision(row.GameID, row.PeerID, "fresh-signed-old-offer", offer); err == nil {
		t.Fatal("reused cancelled decision accepted")
	}
	replacement := offer
	replacement.DecisionID = strings.Repeat("b", 32)
	if err := s.AcceptFirstCopyDecision(row.GameID, row.PeerID, "new-offer-nonce", replacement); err != nil {
		t.Fatalf("fresh decision refused: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resumed.Close() })
	if err := resumed.AcceptFirstCopyDecision(row.GameID, row.PeerID, "offer-nonce", offer); err == nil {
		t.Fatal("restart admitted reused signed request")
	}
	if err := resumed.AcceptFirstCopyDecision(row.GameID, row.PeerID, "fresh-after-restart", offer); err == nil {
		t.Fatal("restart forgot cancelled decision")
	}
	// A stale Phase B receipt must prevent new votes without erasing the
	// durable state needed to recover after restart.
	if _, err := resumed.db.Exec(`UPDATE game_first_copy_remote_receipts SET received_at=? WHERE game_id=?`, "2000-01-01T00:00:00Z", row.GameID); err != nil {
		t.Fatal(err)
	}
	if err := resumed.AcceptFirstCopyDecision(row.GameID, row.PeerID, "other-nonce", replacement); err == nil {
		t.Fatal("stale receipt allowed signed decision")
	}
	status, err := resumed.ReadFirstCopyDecisionStatus(row.GameID, row.TxID)
	if err != nil || status.DecisionID != replacement.DecisionID || status.EvidenceFresh || !status.FenceActive || status.OpenAuthorized {
		t.Fatalf("recovery journal hidden or exposed: %+v %v", status, err)
	}
}
