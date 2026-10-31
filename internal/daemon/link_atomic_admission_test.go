package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opensave/opensave/internal/store"
)

func TestLinkGamesRefusesBusyCanonicalWithoutLosingCoverage(t *testing.T) {
	d, err := New(Options{HomeOverride:t.TempDir(),DisableDiscovery:true})
	if err!=nil{t.Fatal(err)}
	t.Cleanup(d.Stop)
	base:=t.TempDir()
	for _,id:=range []string{"canonical","alias"}{
		dir:=filepath.Join(base,id)
		if err:=os.Mkdir(dir,0700);err!=nil{t.Fatal(err)}
		if err:=d.Store.CreateGame(store.Game{ID:id,Name:id,SavePath:dir});err!=nil{t.Fatal(err)}
	}
	extra:=filepath.Join(base,"extra")
	if err:=os.Mkdir(extra,0700);err!=nil{t.Fatal(err)}
	if err:=d.Store.AddGameRoot("alias","extra",extra);err!=nil{t.Fatal(err)}
	writer:=d.P2P.Sync.Writing("canonical")
	defer writer()
	if err:=d.LinkGames("canonical","alias");err==nil{t.Fatal("expected busy refusal")}
	if _,err:=d.Store.GetGame("alias");err!=nil{t.Fatalf("lost alias: %v",err)}
	roots,err:=d.Store.GameRootPaths("alias")
	if err!=nil||roots["extra"]!=extra{t.Fatalf("lost tracked root %v %v",roots,err)}
}
