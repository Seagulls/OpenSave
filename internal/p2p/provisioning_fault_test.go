package p2p

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/internal/syncpause"
)

// SyncAllGames used to ignore a failed hold-set read and sync every AutoSync
// game. With a peer online and the game row still readable, that is a transfer.
// Sync is nil here: reaching it panics, which is the failure this test wants
// if the early return is removed.
func TestSyncAllGamesStopsWhenHoldSetUnreadable(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "opensave.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.EnsureDefaultSettings(t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateGame(store.Game{
		ID: "live", Name: "Live", SavePath: t.TempDir(), ActiveBranch: "main",
		AutoSync: true, MaxSnapshots: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPeer(store.Peer{
		ID: "peer", Name: "Peer", Address: "127.0.0.1", Port: 9, Status: "online",
	}); err != nil {
		t.Fatal(err)
	}
	s.SetProvisioningReadFault(errors.New("injected hold read failure"))
	e := &Engine{Store: s, Pause: syncpause.New(), Log: func(string, string) {}}
	e.SyncAllGames(context.Background())
}

// Staged release is distinct from hold: ordinary automatic retries must
// respect AutoSync=false even once the durable provisioning hold is gone.
// A present online relay peer ensures the retry loop reaches its per-game
// filter without needing a real LAN peer or any transfer. Sync is nil, so
// an attempted retry fails the test instead of moving data.
func TestPendingResyncRemainsQueuedWhileAutoSyncDisabled(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "opensave.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.EnsureDefaultSettings(t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	const id = "staged-retry"
	if err := s.CreateHeldGame(store.Game{ID: id, Name: "Staged Retry", SavePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	released, err := s.ReleaseProvisioningMode(id, false)
	if err != nil || !released {
		t.Fatalf("staged release: released=%v err=%v", released, err)
	}
	if err := s.UpsertPeer(store.Peer{
		ID: "peer", Name: "Peer", Address: "relay", Status: "online",
	}); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Store: s, Pause: syncpause.New(), Log: func(string, string) {},
		pendingResync: map[string]bool{id: true}}
	e.retryPendingResyncs(context.Background())
	if !e.pendingResync[id] {
		t.Fatal("disabled AutoSync must retain interrupted-sync retry for later activation")
	}
}
