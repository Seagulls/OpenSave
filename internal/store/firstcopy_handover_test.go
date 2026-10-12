package store

import (
	"path/filepath"
	"testing"
)

func handoverTargetFixture(t *testing.T) (*Store, FirstCopy, FirstCopyReceipt, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "handover.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	const gameID = "handover-fixture"
	const peerID = "paired-source"
	const digest = "primary:verified-first-copy"
	if err := s.UpsertPeer(Peer{ID: peerID, Name: "Source", Address: "127.0.0.1", Port: 1, Status: "online"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateHeldGame(Game{ID: gameID, Name: "Handover", SavePath: t.TempDir(), MaxSnapshots: 5}); err != nil {
		t.Fatal(err)
	}
	row, err := s.BeginFirstCopy(gameID, FirstCopyTarget, peerID, digest, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BindFirstCopyTargetGeneration(gameID, row.TxID, peerID, "source-tx", "source-generation", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyFirstCopy(gameID, row.TxID, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActivateFirstCopy(gameID, row.TxID, false); err != nil {
		t.Fatal(err)
	}
	receipt := FirstCopyReceipt{Version: FirstCopyReceiptV1, GameID: gameID, SourceTxID: "source-tx", TargetTxID: row.TxID,
		GenerationID: "source-generation", Branch: "main", Digest: digest, Role: FirstCopySource, Phase: FirstCopyActivated}
	return s, row, receipt, path
}

func TestFirstCopyHandover_RequiresFreshAuthenticatedReceiptAndLocalIntent(t *testing.T) {
	s, row, receipt, _ := handoverTargetFixture(t)
	if _, err := s.PrepareFirstCopyHandover(row.GameID, row.TxID); err == nil {
		t.Fatal("prepared without remote receipt")
	}
	if err := s.AcceptFirstCopyRemoteReceipt(row.GameID, row.PeerID, "receipt-nonce", receipt); err != nil {
		t.Fatal(err)
	}
	p, err := s.PrepareFirstCopyHandover(row.GameID, row.TxID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Role != FirstCopyTarget || p.Stage != "prepared" {
		t.Fatalf("incorrect local prepare %+v", p)
	}
	status, err := s.ReadFirstCopyHandoverStatus(row.GameID, row.TxID)
	if err != nil {
		t.Fatal(err)
	}
	if !status.LocalPrepared || status.PeerPrepared || status.ReadyForOperatorReview || status.OpenAuthorized || !status.FenceActive || status.AutoSync {
		t.Fatalf("unsafe initial status %+v", status)
	}
	mismatch := p
	mismatch.Role = FirstCopySource
	mismatch.Digest = "primary:different"
	if err := s.AcceptFirstCopyHandoverPrepare(row.GameID, row.PeerID, "wrong-evidence", mismatch); err == nil {
		t.Fatal("accepted contradictory remote prepare")
	}
	other := p
	other.Role = FirstCopySource
	if err := s.AcceptFirstCopyHandoverPrepare(row.GameID, "wrong-paired-node", "nonce-wrong-peer", other); err == nil {
		t.Fatal("accepted unbound peer")
	}
	if err := s.AcceptFirstCopyHandoverPrepare(row.GameID, row.PeerID, "prepare-nonce-one", other); err != nil {
		t.Fatal(err)
	}
	status, err = s.ReadFirstCopyHandoverStatus(row.GameID, row.TxID)
	if err != nil {
		t.Fatal(err)
	}
	if !status.LocalPrepared || !status.PeerPrepared || !status.PeerPrepareFresh || !status.ReadyForOperatorReview || status.OpenAuthorized || !status.FenceActive {
		t.Fatalf("incorrect prepared status %+v", status)
	}
	lease, err := s.RecordedFirstCopy(row.GameID)
	if err != nil || lease == nil || lease.Phase != FirstCopyActivated {
		t.Fatalf("handover removed first-copy fence: %+v %v", lease, err)
	}
	game, err := s.GetGame(row.GameID)
	if err != nil || game.AutoSync {
		t.Fatalf("handover enabled AutoSync: %+v %v", game, err)
	}
	if err := s.AcceptFirstCopyHandoverPrepare(row.GameID, row.PeerID, "prepare-nonce-one", other); err == nil {
		t.Fatal("replayed signed nonce")
	}
	if err := s.CancelFirstCopyHandover(row.GameID, row.TxID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareFirstCopyHandover(row.GameID, row.TxID); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptFirstCopyHandoverPrepare(row.GameID, row.PeerID, "prepare-nonce-one", other); err == nil {
		t.Fatal("cancel allowed reuse of signed nonce")
	}
	if err := s.AcceptFirstCopyHandoverPrepare(row.GameID, row.PeerID, "prepare-nonce-two", other); err != nil {
		t.Fatal(err)
	}
}

func TestFirstCopyHandover_ExpiredOrUnpreparedFailsClosed(t *testing.T) {
	s, row, receipt, _ := handoverTargetFixture(t)
	if err := s.AcceptFirstCopyRemoteReceipt(row.GameID, row.PeerID, "receipt-nonce", receipt); err != nil {
		t.Fatal(err)
	}
	p, err := s.PrepareFirstCopyHandover(row.GameID, row.TxID)
	if err != nil {
		t.Fatal(err)
	}
	other := p
	other.Role = FirstCopySource
	if _, err := s.db.Exec(`UPDATE game_first_copy_handover_local SET prepared_at='2000-01-01T00:00:00Z' WHERE game_id=?`, row.GameID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.OutboundFirstCopyHandoverPrepare(row.GameID, row.TxID); err == nil {
		t.Fatal("sent stale local intent")
	}
	if err := s.AcceptFirstCopyHandoverPrepare(row.GameID, row.PeerID, "fresh-auth", other); err == nil {
		t.Fatal("accepted remote when local intent expired")
	}
	if _, err := s.db.Exec(`UPDATE game_first_copy_remote_receipts SET received_at='2000-01-01T00:00:00Z' WHERE game_id=?`, row.GameID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareFirstCopyHandover(row.GameID, row.TxID); err == nil {
		t.Fatal("accepted stale signed Phase B receipt")
	}
	if err := s.CancelFirstCopyHandover(row.GameID, row.TxID); err != nil {
		t.Fatal(err)
	}
	lease, err := s.RecordedFirstCopy(row.GameID)
	if err != nil || lease == nil || lease.Phase != FirstCopyActivated {
		t.Fatalf("cancel lost fence: %+v %v", lease, err)
	}
}

// Phase C preparation evidence is persistent, but a daemon reboot does not
// refresh readiness or erase the durable signed request replay ledger.
func TestFirstCopyHandover_RestartPreservesFenceAndNonceHistory(t *testing.T) {
	s, row, receipt, path := handoverTargetFixture(t)
	if err := s.AcceptFirstCopyRemoteReceipt(row.GameID, row.PeerID, "phase-b-nonce", receipt); err != nil {
		t.Fatal(err)
	}
	p, err := s.PrepareFirstCopyHandover(row.GameID, row.TxID)
	if err != nil {
		t.Fatal(err)
	}
	peer := p
	peer.Role = FirstCopySource
	if err := s.AcceptFirstCopyHandoverPrepare(row.GameID, row.PeerID, "phase-c-nonce", peer); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	status, err := reopened.ReadFirstCopyHandoverStatus(row.GameID, row.TxID)
	if err != nil || !status.LocalPrepared || !status.PeerPrepared || !status.FenceActive || status.OpenAuthorized {
		t.Fatalf("restart changed handover safety: %+v err=%v", status, err)
	}
	if err := reopened.AcceptFirstCopyHandoverPrepare(row.GameID, row.PeerID, "phase-c-nonce", peer); err == nil {
		t.Fatal("restart admitted a previously signed prepare request")
	}
	lease, err := reopened.RecordedFirstCopy(row.GameID)
	if err != nil || lease == nil || lease.Phase != FirstCopyActivated {
		t.Fatalf("restart lost fence: %+v %v", lease, err)
	}
	game, err := reopened.GetGame(row.GameID)
	if err != nil || game.AutoSync {
		t.Fatalf("restart enabled AutoSync: %+v %v", game, err)
	}
}
