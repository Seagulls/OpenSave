package store

import (
	"testing"
)

func TestCreateHeldGameIsAtomicAndDefaultsExistingGamesClear(t *testing.T) {
	s := openTestStore(t)

	plain := Game{ID: "already-live", Name: "Already Live", SavePath: "/tmp/live", ActiveBranch: "main", AutoSync: true, MaxSnapshots: 20}
	if err := s.CreateGame(plain); err != nil {
		t.Fatal(err)
	}
	held, err := s.ProvisioningHeld("already-live")
	if err != nil {
		t.Fatal(err)
	}
	if held {
		t.Fatal("a game created without the hold was held; existing libraries must not change")
	}

	if err := s.CreateHeldGame(Game{
		ID: "configuring", Name: "Configuring", SavePath: "/tmp/cfg", MaxSnapshots: 20,
		AutoSync: true, // caller cannot override the forced off
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetGame("configuring")
	if err != nil {
		t.Fatal(err)
	}
	if got.AutoSync {
		t.Fatal("held game was stored with AutoSync on")
	}
	held, err = s.ProvisioningHeld("configuring")
	if err != nil || !held {
		t.Fatalf("hold missing after commit: held=%v err=%v", held, err)
	}

	set, err := s.ProvisioningHeldSet()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := set["configuring"]; !ok {
		t.Fatal("held set omitted the new game")
	}
	if _, ok := set["already-live"]; ok {
		t.Fatal("held set included a game that was not held")
	}

	released, err := s.ReleaseProvisioning("already-live")
	if err != nil {
		t.Fatal(err)
	}
	if released {
		t.Fatal("releasing a game that was not held reported a release")
	}
	plain, err = s.GetGame("already-live")
	if err != nil {
		t.Fatal(err)
	}
	if !plain.AutoSync {
		t.Fatal("a no-op release changed AutoSync on a game that was not held")
	}

	released, err = s.ReleaseProvisioning("configuring")
	if err != nil || !released {
		t.Fatalf("release = %v, %v", released, err)
	}
	got, err = s.GetGame("configuring")
	if err != nil {
		t.Fatal(err)
	}
	if !got.AutoSync {
		t.Fatal("release left AutoSync off")
	}
	held, err = s.ProvisioningHeld("configuring")
	if err != nil || held {
		t.Fatalf("hold survived release: held=%v err=%v", held, err)
	}

	released, err = s.ReleaseProvisioning("configuring")
	if err != nil || released {
		t.Fatalf("second release = %v, %v; want a no-op", released, err)
	}
}

func TestValidExplicitGameID(t *testing.T) {
	ok := []string{"hades", "crash-bandicoot-4", "a1"}
	bad := []string{"", "Hades", "../x", "a/b", "a b", "a--b", "-a", "a-"}
	for _, id := range ok {
		if !ValidExplicitGameID(id) {
			t.Errorf("rejected %q", id)
		}
	}
	for _, id := range bad {
		if ValidExplicitGameID(id) {
			t.Errorf("accepted %q", id)
		}
	}
}
