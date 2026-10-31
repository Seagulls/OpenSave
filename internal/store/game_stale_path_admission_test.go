package store

import (
	"errors"
	"testing"
)

func TestMetadataCannotResurrectPriorSavePath(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateGame(Game{ID: "stale", Name: "Original", SavePath: "/old"}); err != nil { t.Fatal(err) }
	stale, err := s.GetGame("stale")
	if err != nil { t.Fatal(err) }
	moved := stale
	moved.SavePath = "/new"
	if err := s.UpdateGamePathIf(moved, "/old"); err != nil { t.Fatal(err) }
	stale.Name = "Renamed"
	if err := s.UpdateGame(stale); !errors.Is(err, ErrGamePathChanged) { t.Fatalf("stale metadata mutation: %v", err) }
	actual, err := s.GetGame("stale")
	if err != nil { t.Fatal(err) }
	if actual.SavePath != "/new" || actual.Name != "Original" { t.Fatalf("stale metadata changed record: %#v", actual) }
}

func TestIntentionalPathMoveNeedsExpectedPreviousPath(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateGame(Game{ID: "stale", Name: "Original", SavePath: "/old"}); err != nil { t.Fatal(err) }
	g, err := s.GetGame("stale")
	if err != nil { t.Fatal(err) }
	g.SavePath = "/new"
	if err := s.UpdateGame(g); !errors.Is(err, ErrGamePathChanged) { t.Fatalf("unadmitted path change accepted: %v", err) }
	if err := s.UpdateGamePathIf(g, "/other"); !errors.Is(err, ErrGamePathChanged) { t.Fatalf("stale expected old accepted: %v", err) }
	if err := s.UpdateGamePathIf(g, "/old"); err != nil { t.Fatal(err) }
}
