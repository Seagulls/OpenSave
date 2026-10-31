package store

import "testing"

// An injected failure of the second SQL statement must roll back the first.
func TestRebaseAgreedHashesRollsBackOnNamedRootSQLFailure(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateGame(Game{ID: "atomic", Name: "Atomic", SavePath: "/old"}); err != nil { t.Fatal(err) }
	if err := s.SetAgreedHash("atomic", "peer", "OLD"); err != nil { t.Fatal(err) }
	if err := s.SetAgreedHashForRoot("atomic", "peer", "config", "ROOT"); err != nil { t.Fatal(err) }
	if _, err := s.db.Exec(`CREATE TRIGGER fail_root_update BEFORE UPDATE ON game_root_sync_state BEGIN SELECT RAISE(ABORT, 'injected second statement failure'); END`); err != nil { t.Fatal(err) }
	if err := s.RebaseAgreedHashesForGame("atomic", "NEW"); err == nil { t.Fatal("injected failure not reported") }
	if got := s.GetAgreedHash("atomic", "peer"); got != "OLD" { t.Fatalf("primary agreement committed despite second failure: %q", got) }
	if got := s.GetAgreedHashForRoot("atomic", "peer", "config"); got != "ROOT" { t.Fatalf("named-root agreement changed: %q", got) }
}
