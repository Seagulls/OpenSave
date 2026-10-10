package store

import (
	"testing"
	"time"
)

func TestFirstCopyFinishDoesNotReleaseAndExpiredRowRearms(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertPeer(Peer{ID: "peer-a", Name: "A", Address: "127.0.0.1", Port: 1, Status: "online"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateHeldGame(Game{ID: "g", Name: "G", SavePath: t.TempDir(), MaxSnapshots: 5}); err != nil {
		t.Fatal(err)
	}
	row, err := s.BeginFirstCopy("g", FirstCopySource, "peer-a", "hash-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReleaseProvisioningMode("g", false); err == nil {
		t.Fatal("release succeeded while a first-copy row exists")
	}
	verified, err := s.VerifyFirstCopy("g", row.TxID, "hash-1")
	if err != nil || verified.Phase != FirstCopyVerified {
		t.Fatalf("verify = %+v %v", verified, err)
	}
	held, err := s.ProvisioningHeld("g")
	if err != nil || !held {
		t.Fatal("verify released the hold")
	}
	if _, err := s.VerifyFirstCopy("g", row.TxID, "hash-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActivateFirstCopy("g", "wrong", false); err == nil {
		t.Fatal("wrong transaction activated")
	}
	released, err := s.ActivateFirstCopy("g", row.TxID, false)
	if err != nil || !released {
		t.Fatalf("activate = %v %v", released, err)
	}
	held, err = s.ProvisioningHeld("g")
	if err != nil || held {
		t.Fatal("activate left the hold")
	}
	game, err := s.GetGame("g")
	if err != nil || game.AutoSync {
		t.Fatal("activate turned AutoSync on")
	}

	if err := s.CreateHeldGame(Game{ID: "expired", Name: "E", SavePath: t.TempDir(), MaxSnapshots: 5}); err != nil {
		t.Fatal(err)
	}
	old, err := s.BeginFirstCopy("expired", FirstCopySource, "peer-a", "h", time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if active, err := s.ActiveFirstCopy("expired"); err != nil || active != nil {
		t.Fatalf("expired lease still active: %+v %v", active, err)
	}
	if _, err := s.BeginFirstCopy("expired", FirstCopyTarget, "peer-a", "h", time.Hour); err == nil {
		t.Fatal("a different role stole an expired lease")
	}
	fresh, err := s.BeginFirstCopy("expired", FirstCopySource, "peer-a", "h2", time.Hour)
	if err != nil || fresh.TxID == old.TxID {
		t.Fatalf("same-peer rearm = %+v %v", fresh, err)
	}
	held, err = s.ProvisioningHeld("expired")
	if err != nil || !held {
		t.Fatal("rearm released the hold")
	}
}
