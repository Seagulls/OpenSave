package daemon

import (
	"context"
	"errors"
	"sync"
	"time"
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
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	if err := d.LinkGamesContext(ctx, "canonical", "alias"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected bounded admission refusal, got %v", err)
	}
	if _,err:=d.Store.GetGame("alias");err!=nil{t.Fatalf("lost alias: %v",err)}
	roots,err:=d.Store.GameRootPaths("alias")
	if err!=nil||roots["extra"]!=extra{t.Fatalf("lost tracked root %v %v",roots,err)}
}

// A short native writer should drain inside one user request. The complete
// link must carry mapped roots while keeping their physical bytes untouched.
func TestLinkGamesWaitsForWriterDrain(t *testing.T) {
	d, err := New(Options{HomeOverride: t.TempDir(), DisableDiscovery: true})
	if err != nil { t.Fatal(err) }
	t.Cleanup(d.Stop)
	base := t.TempDir()
	for _, id := range []string{"canonical", "alias"} {
		path := filepath.Join(base, id)
		if err := os.Mkdir(path, 0700); err != nil { t.Fatal(err) }
		if err := d.Store.CreateGame(store.Game{ID: id, Name: id, SavePath: path}); err != nil { t.Fatal(err) }
	}
	extra := filepath.Join(base, "extra")
	if err := os.Mkdir(extra, 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(extra, "slot.sav"), []byte("ORIGINAL"), 0600); err != nil { t.Fatal(err) }
	if err := d.Store.AddGameRoot("alias", "extra", extra); err != nil { t.Fatal(err) }
	writer := d.P2P.Sync.Writing("canonical")
	var once sync.Once
	release := func() { once.Do(writer) }
	defer release()
	go func() { time.Sleep(100*time.Millisecond); release() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.LinkGamesContext(ctx, "canonical", "alias"); err != nil { t.Fatal(err) }
	if _, err := d.Store.GetGame("alias"); err == nil { t.Fatal("alias remains independently tracked") }
	roots, err := d.Store.GameRootPaths("canonical")
	if err != nil || roots["extra"] != extra { t.Fatalf("root not carried: %v %v", roots, err) }
	bytes, err := os.ReadFile(filepath.Join(extra, "slot.sav"))
	if err != nil || string(bytes) != "ORIGINAL" { t.Fatalf("save changed: %q %v", bytes, err) }
}
