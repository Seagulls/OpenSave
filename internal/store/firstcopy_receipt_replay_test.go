package store

import (
	"path/filepath"
	"testing"
	"time"
)

// Test first-copy replay safety across restart, rather than testing the
// process-local transport nonce cache, which intentionally does not survive.
func TestFirstCopyAuthenticatedReceiptNoncePersistsAcrossRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "receipt-replay.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	const gameID = "durable-receipt-replay"
	const peerID = "paired-source"
	const digest = "primary:receipt-replay-generation"
	if err := s.UpsertPeer(Peer{ID: peerID, Name: "Source", Address: "127.0.0.1", Port: 1, Status: "online"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateHeldGame(Game{ID: gameID, Name: "Durable Receipt", SavePath: t.TempDir(), MaxSnapshots: 5}); err != nil {
		t.Fatal(err)
	}
	target, err := s.BeginFirstCopy(gameID, FirstCopyTarget, peerID, digest, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BindFirstCopyTargetGeneration(gameID, target.TxID, peerID, "source-tx", "source-generation", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyFirstCopy(gameID, target.TxID, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActivateFirstCopy(gameID, target.TxID, false); err != nil {
		t.Fatal(err)
	}

	receipt := FirstCopyReceipt{Version: FirstCopyReceiptV1, GameID: gameID,
		SourceTxID: "source-tx", TargetTxID: target.TxID,
		GenerationID: "source-generation", Branch: "main", Digest: digest,
		Role: FirstCopySource, Phase: FirstCopyActivated}
	if err := s.AcceptFirstCopyRemoteReceipt(gameID, peerID, "signed-nonce-one", receipt); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptFirstCopyRemoteReceipt(gameID, peerID, "signed-nonce-two", receipt); err != nil {
		t.Fatalf("fresh signed nonce cannot reassert identical receipt: %v", err)
	}
	// Simulate a stale authenticated statement and a daemon restart. If an
	// old nonce can be replayed, the receipt falsely becomes fresh for Open.
	const stale = "2000-01-01T00:00:00Z"
	if _, err := s.db.Exec(`UPDATE game_first_copy_remote_receipts SET received_at = ? WHERE game_id = ?`, stale, gameID); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	for _, replayed := range []string{"signed-nonce-one", "signed-nonce-two"} {
		if err := s.AcceptFirstCopyRemoteReceipt(gameID, peerID, replayed, receipt); err == nil {
			t.Fatalf("replayed authenticated nonce %q accepted after restart", replayed)
		}
		var stamp string
		if err := s.db.Get(&stamp, `SELECT received_at FROM game_first_copy_remote_receipts WHERE game_id = ?`, gameID); err != nil {
			t.Fatal(err)
		}
		if stamp != stale {
			t.Fatalf("replay refreshed remote readiness timestamp: %q", stamp)
		}
	}
	if err := s.AcceptFirstCopyRemoteReceipt(gameID, peerID, "signed-nonce-three", receipt); err != nil {
		t.Fatalf("fresh signed retry refused after restart: %v", err)
	}
	var stamp string
	if err := s.db.Get(&stamp, `SELECT received_at FROM game_first_copy_remote_receipts WHERE game_id = ?`, gameID); err != nil {
		t.Fatal(err)
	}
	if stamp == stale {
		t.Fatal("fresh authenticated reassertion did not renew readiness")
	}
}
