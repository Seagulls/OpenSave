package p2p

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/opensave/opensave/internal/store"
)

func TestSyncEventRequiresTrackedUnprotectedGameAndKnownPeer(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.UpsertPeer(store.Peer{ID: "source", Name: "Source", Status: "online"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateGame(store.Game{ID: "plain", Name: "Plain", SavePath: filepath.Join(t.TempDir(), "plain")}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateHeldGame(store.Game{ID: "held", Name: "Held", SavePath: filepath.Join(t.TempDir(), "held")}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGameAlias("held-alias", "held"); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Store: s}
	if got, err := e.syncEventGame("plain", "source"); err != nil || got != "plain" {
		t.Fatalf("plain event: %q %v", got, err)
	}
	for _, tc := range []struct{ game, peer string }{{"held", "source"}, {"held-alias", "source"}, {"plain", ""}, {"plain", "not-paired"}, {"unknown", "source"}} {
		if _, err := e.syncEventGame(tc.game, tc.peer); err == nil {
			t.Errorf("unsafe event accepted: %+v", tc)
		}
	}
	if _, err := s.BeginFirstCopy("held", store.FirstCopyTarget, "source", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.syncEventGame("held", "source"); err == nil {
		t.Fatal("held first-copy event accepted")
	}
	s.SetProvisioningReadFault(errors.New("injected unreadable state"))
	if _, err := e.syncEventGame("plain", "source"); err == nil {
		t.Fatal("unreadable guard allowed event")
	}
}
