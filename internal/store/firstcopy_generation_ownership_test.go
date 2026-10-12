package store

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func firstCopyGenerationStore(t *testing.T) (*Store, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "opensave.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPeer(Peer{ID: "peer-a", Name: "A", Address: "127.0.0.1", Port: 1, Status: "online"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateHeldGame(Game{ID: "g", Name: "G", SavePath: t.TempDir(), ActiveBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	return s, dbPath
}

func createGenerationRow(t *testing.T, s *Store, id string) Snapshot {
	t.Helper()
	snap := Snapshot{
		ID: id, GameID: "g", BranchName: "main",
		Timestamp: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		ZipPath:   filepath.Join(t.TempDir(), id+".zip"),
	}
	if err := s.CreateSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestFirstCopyGenerationBindingIsAtomicAndRetryKeepsWinner(t *testing.T) {
	s, _ := firstCopyGenerationStore(t)
	defer s.Close()
	a := createGenerationRow(t, s, "snap_a")
	b := createGenerationRow(t, s, "snap_b")

	type result struct {
		row  FirstCopy
		used bool
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, candidate := range []Snapshot{a, b} {
		wg.Add(1)
		go func(snap Snapshot) {
			defer wg.Done()
			<-start
			row, used, err := s.BeginFirstCopySource("g", "peer-a", time.Hour, snap, "digest-"+snap.ID)
			results <- result{row: row, used: used, err: err}
		}(candidate)
	}
	close(start)
	wg.Wait()
	close(results)

	var winner FirstCopy
	used := 0
	for got := range results {
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.used {
			used++
			winner = got.row
		}
	}
	if used != 1 {
		t.Fatalf("bound candidates = %d, want one", used)
	}
	recorded, err := s.RecordedFirstCopy("g")
	if err != nil || recorded == nil || recorded.GenerationID != winner.GenerationID || recorded.TxID != winner.TxID {
		t.Fatalf("recorded winner = %+v, err=%v; call winner=%+v", recorded, err, winner)
	}
	for _, snap := range []Snapshot{a, b} {
		got, err := s.GetSnapshot(snap.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Pinned != (snap.ID == winner.GenerationID) {
			t.Fatalf("snapshot %s pinned=%v", snap.ID, got.Pinned)
		}
	}

	renewed, err := s.RearmFirstCopy("g", FirstCopySource, "peer-a", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.TxID != winner.TxID || renewed.GenerationID != winner.GenerationID {
		t.Fatalf("retry substituted generation: winner=%+v retry=%+v", winner, renewed)
	}
}

func TestFirstCopyGenerationSurvivesRestartAndCannotBeRemoved(t *testing.T) {
	s, dbPath := firstCopyGenerationStore(t)
	snap := createGenerationRow(t, s, "snap_restart")
	row, used, err := s.BeginFirstCopySource("g", "peer-a", time.Hour, snap, "digest")
	if err != nil || !used {
		t.Fatalf("bind = %+v used=%v err=%v", row, used, err)
	}
	if err := s.SetSnapshotPinned(snap.ID, false); err == nil {
		t.Fatal("manual unpin removed a live first-copy generation")
	}
	if err := s.DeleteSnapshot(snap.ID); err == nil {
		t.Fatal("manual deletion removed a live first-copy generation")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	recorded, err := s.RecordedFirstCopy("g")
	if err != nil || recorded == nil || recorded.GenerationID != snap.ID || recorded.TxID != row.TxID {
		t.Fatalf("restart ownership = %+v err=%v", recorded, err)
	}
	got, err := s.GetSnapshot(snap.ID)
	if err != nil || !got.Pinned {
		t.Fatalf("restart snapshot = %+v err=%v", got, err)
	}
	if err := s.AbortFirstCopy("g", row.TxID); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetSnapshot(snap.ID)
	if err != nil || got.Pinned {
		t.Fatalf("abort did not release generation: %+v err=%v", got, err)
	}
	if err := s.DeleteSnapshot(snap.ID); err != nil {
		t.Fatalf("released generation cannot be deleted: %v", err)
	}
}
