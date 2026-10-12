package store

import "testing"

func TestFirstCopyDecision_CancelPreparationVetoesOffer(t *testing.T) {
	s, row, offer, _ := decisionTargetFixture(t)
	if err := s.AcceptFirstCopyDecision(row.GameID, row.PeerID, "offer-nonce", offer); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelFirstCopyHandover(row.GameID, row.TxID); err != nil {
		t.Fatalf("cancel preparation must veto uncommitted decision: %v", err)
	}
	got, err := s.readDecision(s.db, row.GameID)
	if err != nil || got != nil {
		t.Fatalf("cancel left active decision: %+v %v", got, err)
	}
	if err := s.AcceptFirstCopyDecision(row.GameID, row.PeerID, "other-nonce", offer); err == nil {
		t.Fatal("old offer revived after cancel")
	}
	lease, err := s.RecordedFirstCopy(row.GameID)
	if err != nil || lease == nil || lease.Phase != FirstCopyActivated {
		t.Fatalf("cancel removed protection: %+v %v", lease, err)
	}
	if err := s.OpenFirstCopy(row.GameID, row.TxID); err == nil {
		t.Fatal("legacy Open bypassed cancelled handover")
	}
}

func TestFirstCopyDecision_LegacyOpenBlockedByPreparation(t *testing.T) {
	s, row, _, _ := decisionTargetFixture(t)
	// Preparation is a safety-critical boundary even BEFORE an offer arrives.
	if err := s.OpenFirstCopy(row.GameID, row.TxID); err == nil {
		t.Fatal("legacy Open bypassed Phase C preparation")
	}
	lease, err := s.RecordedFirstCopy(row.GameID)
	if err != nil || lease == nil || lease.Phase != FirstCopyActivated {
		t.Fatalf("Open refusal mutated lease: %+v %v", lease, err)
	}
}
