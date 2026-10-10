package store

import (
	"testing"
	"time"
)

func newFirstCopyLifecycleStore(t *testing.T, gameID string) (*Store, FirstCopy) {
	t.Helper()
	s := openTestStore(t)
	if err := s.UpsertPeer(Peer{ID: "firstcopy-source-peer", Name: "Source", Address: "127.0.0.1", Port: 1, Status: "online"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateHeldGame(Game{ID: gameID, Name: "Lifecycle", SavePath: t.TempDir(), MaxSnapshots: 5}); err != nil {
		t.Fatal(err)
	}
	row, err := s.BeginFirstCopy(gameID, FirstCopySource, "firstcopy-source-peer", "hash-a", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyFirstCopy(gameID, row.TxID, "hash-a"); err != nil {
		t.Fatal(err)
	}
	return s, row
}

// Regression: neither a delayed Abort nor a retry of Finish may remove or
// downgrade the durable fence after this device's hold was released.
func TestFirstCopyActivatedFenceRejectsAbortAndFinish(t *testing.T) {
	const id = "lifecycle-regression"
	s, row := newFirstCopyLifecycleStore(t, id)
	if _, err := s.ActivateFirstCopy(id, row.TxID, false); err != nil {
		t.Fatal(err)
	}
	held, err := s.ProvisioningHeld(id)
	if err != nil || held {
		t.Fatalf("expected activated, unheld: held=%v err=%v", held, err)
	}

	if err := s.AbortFirstCopy(id, row.TxID); err == nil {
		t.Fatal("Abort deleted an activated fence")
	}
	if _, err := s.VerifyFirstCopy(id, row.TxID, "hash-a"); err == nil {
		t.Fatal("Finish regressed activated to verified")
	}
	lease, err := s.BoundFirstCopy(id)
	if err != nil || lease == nil || lease.Phase != FirstCopyActivated {
		t.Fatalf("activated fence lost: lease=%+v err=%v", lease, err)
	}
	if err := s.OpenFirstCopy(id, "wrong"); err == nil {
		t.Fatal("wrong tx opened an activated fence")
	}
	if err := s.OpenFirstCopy(id, row.TxID); err != nil {
		t.Fatal(err)
	}
	lease, err = s.BoundFirstCopy(id)
	if err != nil || lease != nil {
		t.Fatalf("open did not release the fence: %+v %v", lease, err)
	}
}

func TestFirstCopyActivatedFenceSurvivesOriginalExpiry(t *testing.T) {
	const id = "lifecycle-expiry"
	s, row := newFirstCopyLifecycleStore(t, id)
	if _, err := s.ActivateFirstCopy(id, row.TxID, false); err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`UPDATE game_first_copies SET expires_at = ? WHERE game_id = ?`, past, id); err != nil {
		t.Fatal(err)
	}
	lease, err := s.BoundFirstCopy(id)
	if err != nil || lease == nil || lease.Phase != FirstCopyActivated {
		t.Fatalf("expired activated fence stopped protecting the game: %+v %v", lease, err)
	}
	if err := s.AbortFirstCopy(id, row.TxID); err == nil {
		t.Fatal("expired activated fence was aborted")
	}
	if _, err := s.VerifyFirstCopy(id, row.TxID, "hash-a"); err == nil {
		t.Fatal("expired activated fence was demoted")
	}
	lease, err = s.BoundFirstCopy(id)
	if err != nil || lease == nil || lease.Phase != FirstCopyActivated {
		t.Fatalf("fence lost: %+v %v", lease, err)
	}
}

func TestFirstCopyActivationDoesNotClaimAutoSync(t *testing.T) {
	const id = "lifecycle-autosync"
	s, row := newFirstCopyLifecycleStore(t, id)
	if _, err := s.ActivateFirstCopy(id, row.TxID, true); err == nil {
		t.Fatal("activation accepted ineffective AutoSync")
	}
	held, err := s.ProvisioningHeld(id)
	if err != nil || !held {
		t.Fatalf("rejecting AutoSync released hold: %v %v", held, err)
	}
	lease, err := s.ActiveFirstCopy(id)
	if err != nil || lease == nil || lease.Phase != FirstCopyVerified {
		t.Fatalf("rejecting AutoSync mutated lease: %+v %v", lease, err)
	}
	if _, err := s.ActivateFirstCopy(id, row.TxID, false); err != nil {
		t.Fatal(err)
	}
}

func TestFirstCopyVerifiedDigestCannotChangeOnRepeat(t *testing.T) {
	const id = "lifecycle-verified"
	s, row := newFirstCopyLifecycleStore(t, id)
	if _, err := s.VerifyFirstCopy(id, row.TxID, "different-digest"); err == nil {
		t.Fatal("verified digest changed on repeated Finish")
	}
	lease, err := s.ActiveFirstCopy(id)
	if err != nil || lease == nil || lease.Phase != FirstCopyVerified || lease.ContentHash != "hash-a" {
		t.Fatalf("verified digest mutated: %+v %v", lease, err)
	}
}
