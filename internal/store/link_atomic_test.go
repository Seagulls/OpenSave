package store

import "testing"

func TestLinkGamesAtomicRollsBackAfterEarlierSuccessfulRootCarry(t *testing.T) {
	s := openTestStore(t)
	for _, id := range []string{"canonical", "alias"} {
		if err := s.CreateGame(Game{ID:id, Name:id, SavePath:"/tmp/"+id}); err != nil { t.Fatal(err) }
	}
	if err:=s.AddGameRoot("alias","first","/tmp/first");err!=nil{t.Fatal(err)}
	if err:=s.AddGameRoot("alias","second","/tmp/second");err!=nil{t.Fatal(err)}
	if _,err:=s.db.Exec(`CREATE TRIGGER reject_second_root BEFORE INSERT ON game_roots WHEN NEW.game_id = 'canonical' AND NEW.name = 'second' BEGIN SELECT RAISE(ABORT,'inject second root failure'); END`);err!=nil{t.Fatal(err)}
	if _,_,err:=s.LinkGamesAtomic("canonical","alias","alias");err==nil{t.Fatal("expected injected failure")}
	if _,err:=s.GetGame("alias");err!=nil{t.Fatalf("alias registration deleted on failure: %v",err)}
	if _,ok:=s.GetGameAlias("alias");ok{t.Fatal("alias created despite failure")}
	aliasRoots,err:=s.GameRootPaths("alias");if err!=nil{t.Fatal(err)}
	if aliasRoots["first"]!="/tmp/first"||aliasRoots["second"]!="/tmp/second"{t.Fatalf("source root coverage changed: %v",aliasRoots)}
	canonRoots,err:=s.GameRootPaths("canonical");if err!=nil{t.Fatal(err)}
	if len(canonRoots)!=0{t.Fatalf("partial canonical roots left behind: %v",canonRoots)}
}

func TestLinkGamesAtomicSuccessCarriesRootsAndAlias(t *testing.T) {
	s:=openTestStore(t)
	for _,id:=range []string{"canonical","alias"}{if err:=s.CreateGame(Game{ID:id,Name:id,SavePath:"/tmp/"+id});err!=nil{t.Fatal(err)}}
	if err:=s.AddGameRoot("alias","config","/tmp/config");err!=nil{t.Fatal(err)}
	_,merged,err:=s.LinkGamesAtomic("canonical","alias","alias");if err!=nil{t.Fatal(err)}
	if !merged{t.Fatal("expected merged alias")}
	if _,err:=s.GetGame("alias");err==nil{t.Fatal("alias remains tracked")}
	roots,err:=s.GameRootPaths("canonical");if err!=nil||roots["config"]!="/tmp/config"{t.Fatalf("missing carried root: %v, %v",roots,err)}
	if id,ok:=s.ResolveGameAlias("alias");!ok||id!="canonical"{t.Fatalf("missing alias redirect: %q %v",id,ok)}
}
