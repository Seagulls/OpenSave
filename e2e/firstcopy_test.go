package e2e

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/testutil"
)

type firstCopyRow struct {
	TxID         string `json:"txId"`
	Role         string `json:"role"`
	PeerID       string `json:"peerId"`
	GenerationID string `json:"generationId"`
}

func beginCopy(t *testing.T, node *testutil.TestDaemon, gameID, role, peerID string) firstCopyRow {
	t.Helper()
	var row firstCopyRow
	node.API(http.MethodPost, "/api/games/"+gameID+"/first-copy", map[string]any{
		"role": role, "peerId": peerID,
	}, &row)
	if row.TxID == "" || row.Role != role || row.PeerID != peerID {
		t.Fatalf("first-copy = %+v", row)
	}
	return row
}

func TestFirstCopy_TwoPeerActivate(t *testing.T) {
	source := testutil.NewTestDaemon(t, "Two-Source")
	target := testutil.NewTestDaemon(t, "Two-Target")
	source.PairWith(target)
	const id = "two-peer-game"
	src := sideDir(t, source, "two")
	tgt := sideDir(t, target, "two")
	writeHoldFile(t, src, "slot.sav", "SOURCE-BYTES")
	createHeld(t, source, id, "Two Peer", src)
	createHeld(t, target, id, "Two Peer", tgt)
	srcLease := beginCopy(t, source, id, "source", target.NodeID())
	beginCopy(t, target, id, "target", source.NodeID())
	if !testutil.WaitFor(45*time.Second, func() bool {
		target.API(http.MethodPost, "/api/games/"+id+"/sync", nil, nil)
		return readHoldFile(t, tgt, "slot.sav") == "SOURCE-BYTES" && fileHash(src, "slot.sav") == fileHash(tgt, "slot.sav")
	}) {
		t.Fatal("two-peer copy did not match")
	}
	source.API(http.MethodPost, "/api/games/"+id+"/first-copy/finish", map[string]string{"txId": srcLease.TxID}, nil)
	var activated struct {
		Released bool `json:"released"`
		AutoSync bool `json:"autoSync"`
	}
	source.API(http.MethodPost, "/api/games/"+id+"/first-copy/activate", map[string]string{"txId": srcLease.TxID}, &activated)
	if !activated.Released || activated.AutoSync {
		t.Fatalf("two-peer activate = %+v", activated)
	}
	if readHoldFile(t, src, "slot.sav") != "SOURCE-BYTES" {
		t.Fatal("two-peer activate changed the source")
	}
}

func TestFirstCopy_LiveMutationDoesNotChangeServedGeneration(t *testing.T) {
	source := testutil.NewTestDaemon(t, "Gen-Source")
	target := testutil.NewTestDaemon(t, "Gen-Target")
	source.PairWith(target)
	const id = "generation-game"
	src := sideDir(t, source, "gen")
	tgt := sideDir(t, target, "gen")
	writeHoldFile(t, src, "slot.sav", "GENERATION")
	createHeld(t, source, id, "Generation", src)
	createHeld(t, target, id, "Generation", tgt)
	lease := beginCopy(t, source, id, "source", target.NodeID())
	if lease.TxID == "" || lease.GenerationID == "" {
		t.Fatal("source begin did not pin a generation")
	}
	again := beginCopy(t, source, id, "source", target.NodeID())
	if again.TxID != lease.TxID || again.GenerationID != lease.GenerationID {
		t.Fatalf("repeat begin substituted a generation: %+v %+v", lease, again)
	}
	writeHoldFile(t, src, "slot.sav", "MUTATED-LIVE")
	beginCopy(t, target, id, "target", source.NodeID())
	if !testutil.WaitFor(45*time.Second, func() bool {
		target.APIStatus(http.MethodPost, "/api/games/"+id+"/sync", nil, nil)
		return readHoldFile(t, tgt, "slot.sav") == "GENERATION"
	}) {
		t.Fatalf("target received %q, want the pinned generation", readHoldFile(t, tgt, "slot.sav"))
	}
	if readHoldFile(t, src, "slot.sav") != "MUTATED-LIVE" {
		t.Fatal("pinning the generation altered the live save")
	}
	if status := source.APIStatus(http.MethodPost, "/api/games/"+id+"/first-copy/open", map[string]string{"txId": lease.TxID}, nil); status < 400 {
		t.Fatal("open succeeded without the other peer's receipt")
	}
}

func TestFirstCopy_MultiRootGenerationParityLANAndWAN(t *testing.T) {
	for _, mode := range []string{"lan", "wan"} {
		t.Run(mode, func(t *testing.T) {
			source := testutil.NewTestDaemon(t, "Roots-Source-"+mode)
			target := testutil.NewTestDaemon(t, "Roots-Target-"+mode)
			if mode == "wan" {
				pairOverRelay(t, source, target, startRelay(t), "first-copy-roots")
			} else {
				source.PairWith(target)
			}

			id := "first-copy-roots-" + mode
			src := sideDir(t, source, "roots-primary")
			tgt := sideDir(t, target, "roots-primary")
			srcConfig := sideDir(t, source, "roots-config")
			tgtConfig := sideDir(t, target, "roots-config")
			writeHoldFile(t, src, "same.sav", "PRIMARY-"+mode)
			writeHoldFile(t, srcConfig, "same.sav", "CONFIG-"+mode)
			createHeld(t, source, id, "Roots", src)
			createHeld(t, target, id, "Roots", tgt)
			if err := source.Daemon.Store.AddGameRoot(id, "config", srcConfig); err != nil {
				t.Fatal(err)
			}
			if err := target.Daemon.Store.AddGameRoot(id, "config", tgtConfig); err != nil {
				t.Fatal(err)
			}
			beginCopy(t, source, id, "source", target.NodeID())
			beginCopy(t, target, id, "target", source.NodeID())
			if !testutil.WaitFor(45*time.Second, func() bool {
				target.APIStatus(http.MethodPost, "/api/games/"+id+"/sync", nil, nil)
				return readHoldFile(t, tgt, "same.sav") == "PRIMARY-"+mode &&
					readHoldFile(t, tgtConfig, "same.sav") == "CONFIG-"+mode
			}) {
				t.Fatalf("multi-root first copy failed: primary=%q config=%q",
					readHoldFile(t, tgt, "same.sav"), readHoldFile(t, tgtConfig, "same.sav"))
			}
		})
	}
}

func TestFirstCopy_ConcurrentBeginBindsOneGeneration(t *testing.T) {
	source := testutil.NewTestDaemon(t, "Concurrent-Source")
	target := testutil.NewTestDaemon(t, "Concurrent-Target")
	source.PairWith(target)
	const id = "concurrent-generation"
	src := sideDir(t, source, "concurrent")
	writeHoldFile(t, src, "slot.sav", "ONE-GENERATION")
	createHeld(t, source, id, "Concurrent", src)

	const callers = 8
	rows := make(chan store.FirstCopy, callers)
	errs := make(chan error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			row, err := source.Daemon.BeginFirstCopy(id, store.FirstCopySource, target.NodeID(), time.Hour)
			rows <- row
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(rows)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var txID, generationID string
	for row := range rows {
		if txID == "" {
			txID, generationID = row.TxID, row.GenerationID
		}
		if row.TxID != txID || row.GenerationID != generationID {
			t.Fatalf("concurrent Begin substituted content: first=%s/%s got=%s/%s",
				txID, generationID, row.TxID, row.GenerationID)
		}
	}
	snaps, err := source.Daemon.Store.ListSnapshots(id, "main")
	if err != nil {
		t.Fatal(err)
	}
	pinned := 0
	for _, snap := range snaps {
		if snap.Pinned {
			pinned++
			if snap.ID != generationID {
				t.Fatalf("wrong snapshot pinned: %+v", snap)
			}
		}
	}
	if pinned != 1 {
		t.Fatalf("pinned generations = %d, want one", pinned)
	}
}

func TestFirstCopy_MissingGenerationFailsClosed(t *testing.T) {
	source := testutil.NewTestDaemon(t, "Missing-Source")
	target := testutil.NewTestDaemon(t, "Missing-Target")
	source.PairWith(target)
	const id = "missing-generation"
	src := sideDir(t, source, "missing")
	tgt := sideDir(t, target, "missing")
	writeHoldFile(t, src, "slot.sav", "MUST-NOT-FALL-BACK")
	createHeld(t, source, id, "Missing", src)
	createHeld(t, target, id, "Missing", tgt)
	lease := beginCopy(t, source, id, "source", target.NodeID())
	snap, err := source.Daemon.Store.GetSnapshot(lease.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(snap.ZipPath); err != nil {
		t.Fatal(err)
	}
	beginCopy(t, target, id, "target", source.NodeID())
	for i := 0; i < 3; i++ {
		target.APIStatus(http.MethodPost, "/api/games/"+id+"/sync", nil, nil)
	}
	if got := readHoldFile(t, tgt, "slot.sav"); got != "" {
		t.Fatalf("missing generation fell back to live source: %q", got)
	}
	recorded, err := source.Daemon.Store.RecordedFirstCopy(id)
	if err != nil || recorded == nil || recorded.GenerationID != lease.GenerationID {
		t.Fatalf("missing archive destroyed ownership: %+v %v", recorded, err)
	}
}

func TestFirstCopy_RollbackCannotOverwriteActivatedSource(t *testing.T) {
	source := testutil.NewTestDaemon(t, "Restore-Source")
	target := testutil.NewTestDaemon(t, "Restore-Target")
	source.PairWith(target)
	const id = "restore-fence"
	src := sideDir(t, source, "restore")
	tgt := sideDir(t, target, "restore")
	writeHoldFile(t, src, "slot.sav", "BEFORE")
	createHeld(t, source, id, "Restore Fence", src)
	createHeld(t, target, id, "Restore Fence", tgt)
	if !testutil.WaitFor(20*time.Second, func() bool {
		snaps, err := source.Daemon.Store.ListSnapshots(id, "main")
		return err == nil && len(snaps) > 0
	}) {
		t.Fatal("no snapshot to restore")
	}
	snaps, err := source.Daemon.Store.ListSnapshots(id, "main")
	if err != nil || len(snaps) == 0 {
		t.Fatal(err)
	}
	writeHoldFile(t, src, "slot.sav", "AFTER")
	lease := beginCopy(t, source, id, "source", target.NodeID())
	source.API(http.MethodPost, "/api/games/"+id+"/first-copy/finish", map[string]string{"txId": lease.TxID}, nil)
	source.API(http.MethodPost, "/api/games/"+id+"/first-copy/activate", map[string]string{"txId": lease.TxID}, nil)
	status := source.APIStatus(http.MethodPost, "/api/games/"+id+"/rollback", map[string]string{"snapshotId": snaps[0].ID}, nil)
	if status < 400 {
		t.Fatalf("rollback of an activated first-copy returned %d", status)
	}
	if readHoldFile(t, src, "slot.sav") != "AFTER" {
		t.Fatal("rollback changed an activated source")
	}
	if err := source.Daemon.AcceptCloudOffer(id, "missing"); err == nil || !strings.Contains(err.Error(), "first-copy") {
		t.Fatalf("cloud offer acceptance was not refused: %v", err)
	}
}

func TestFirstCopy_DivergentTargetIsRefused(t *testing.T) {
	source := testutil.NewTestDaemon(t, "Diverge-Source")
	target := testutil.NewTestDaemon(t, "Diverge-Target")
	source.PairWith(target)
	const id = "diverge-game"
	src := sideDir(t, source, "div")
	tgt := sideDir(t, target, "div")
	writeHoldFile(t, src, "slot.sav", "SOURCE-BYTES")
	writeHoldFile(t, tgt, "other.sav", "TARGET-ONLY")
	createHeld(t, source, id, "Diverge", src)
	createHeld(t, target, id, "Diverge", tgt)
	beginCopy(t, source, id, "source", target.NodeID())
	beginCopy(t, target, id, "target", source.NodeID())
	target.APIStatus(http.MethodPost, "/api/games/"+id+"/sync", nil, nil)
	if readHoldFile(t, tgt, "other.sav") != "TARGET-ONLY" || readHoldFile(t, tgt, "slot.sav") != "" {
		t.Fatal("divergent target was modified")
	}
	if readHoldFile(t, src, "slot.sav") != "SOURCE-BYTES" || readHoldFile(t, src, "other.sav") != "" {
		t.Fatal("source changed while a divergent target was refused")
	}
	held, err := target.Daemon.Store.ProvisioningHeld(id)
	if err != nil || !held {
		t.Fatal("refusal released the target hold")
	}
}

func TestFirstCopy_OnlyNamedTargetReceivesAndSourceIsNotWritten(t *testing.T) {
	source := testutil.NewTestDaemon(t, "Copy-Source")
	target := testutil.NewTestDaemon(t, "Copy-Target")
	other := testutil.NewTestDaemon(t, "Copy-Other")
	source.PairWith(target)
	source.PairWith(other)

	liveSrc := sideDir(t, source, "live")
	liveTgt := sideDir(t, target, "live")
	writeHoldFile(t, liveSrc, "live.sav", "LIVE")
	source.API(http.MethodPost, "/api/games", map[string]string{"name": "Copy Live", "savePath": liveSrc}, nil)
	target.API(http.MethodPost, "/api/games", map[string]string{"name": "Copy Live", "savePath": liveTgt}, nil)

	const id = "first-copy-game"
	src := sideDir(t, source, "copy")
	tgt := sideDir(t, target, "copy")
	oth := sideDir(t, other, "copy")
	writeHoldFile(t, src, "slot.sav", "SOURCE-BYTES")
	writeHoldFile(t, oth, "third.sav", "THIRD-ONLY")
	createHeld(t, source, id, "First Copy", src)
	createHeld(t, target, id, "First Copy", tgt)
	createHeld(t, other, id, "First Copy", oth)

	again := beginCopy(t, source, id, "source", target.NodeID())
	first := beginCopy(t, source, id, "source", target.NodeID())
	if first.TxID != again.TxID {
		t.Fatalf("repeat begin changed the transaction: %s %s", first.TxID, again.TxID)
	}
	var rejected struct {
		Error string `json:"error"`
	}
	if status := source.APIStatus(http.MethodPost, "/api/games/"+id+"/first-copy", map[string]any{
		"role": "source", "peerId": other.NodeID(),
	}, &rejected); status < 400 {
		t.Fatal("a second peer was accepted onto an existing first-copy")
	}
	beginCopy(t, target, id, "target", source.NodeID())

	if !testutil.WaitFor(45*time.Second, func() bool {
		target.API(http.MethodPost, "/api/games/"+id+"/sync", nil, nil)
		return readHoldFile(t, tgt, "slot.sav") == "SOURCE-BYTES" &&
			fileHash(src, "slot.sav") == fileHash(tgt, "slot.sav")
	}) {
		t.Fatalf("named target did not receive the source bytes: %q", readHoldFile(t, tgt, "slot.sav"))
	}
	if readHoldFile(t, src, "slot.sav") != "SOURCE-BYTES" || readHoldFile(t, src, "other.sav") != "" || readHoldFile(t, src, "third.sav") != "" {
		t.Fatal("source bytes changed during the first copy")
	}
	var manifest struct {
		Error string `json:"error"`
	}
	if status := source.APIStatus(http.MethodGet, "/api/p2p/manifest/"+id, nil, &manifest); status < 400 || manifest.Error == "" {
		t.Fatalf("unsigned manifest read = %d %q", status, manifest.Error)
	}
	if status := source.APIStatus(http.MethodPost, "/api/p2p/delete-file/"+id, map[string]string{"relPath": "slot.sav"}, &manifest); status < 400 {
		t.Fatal("delete was accepted during a source first-copy")
	}
	if readHoldFile(t, src, "slot.sav") != "SOURCE-BYTES" {
		t.Fatal("delete changed the source")
	}
	other.API(http.MethodPost, "/api/games/"+id+"/release-provisioning", map[string]any{"autoSync": false}, nil)
	if status := other.APIStatus(http.MethodPost, "/api/games/"+id+"/sync", nil, nil); status >= 500 {
		t.Fatalf("third peer sync failed closed unexpectedly: %d", status)
	}
	if readHoldFile(t, oth, "slot.sav") != "" || readHoldFile(t, src, "third.sav") != "" || readHoldFile(t, oth, "third.sav") != "THIRD-ONLY" {
		t.Fatal("a third peer exchanged bytes")
	}
	if !testutil.WaitFor(30*time.Second, func() bool {
		source.API(http.MethodPost, "/api/games/"+store.SlugifyGameID("Copy Live")+"/sync", nil, nil)
		return readHoldFile(t, liveTgt, "live.sav") == "LIVE"
	}) {
		t.Fatal("an unrelated game stopped syncing during a first copy")
	}
	if status := source.APIStatus(http.MethodPost, "/api/games/"+id+"/sync", nil, nil); status < 400 {
		t.Fatalf("source sync during a first copy returned %d", status)
	}
	if readHoldFile(t, src, "other.sav") != "" {
		t.Fatal("source sync pulled the target")
	}
	source.Restart()
	held, err := source.Daemon.Store.ProvisioningHeld(id)
	if err != nil || !held {
		t.Fatal("restart released the hold")
	}
	lease, err := source.Daemon.Store.ActiveFirstCopy(id)
	if err != nil || lease == nil || lease.TxID != first.TxID {
		t.Fatalf("restart dropped the lease: %+v %v", lease, err)
	}
	if readHoldFile(t, src, "slot.sav") != "SOURCE-BYTES" {
		t.Fatal("restart changed the source")
	}
	var finished struct {
		Released bool   `json:"released"`
		Phase    string `json:"phase"`
	}
	source.API(http.MethodPost, "/api/games/"+id+"/first-copy/finish", map[string]string{"txId": first.TxID}, &finished)
	if finished.Released || finished.Phase != "verified" {
		t.Fatalf("finish = %+v; verification must not lift the hold", finished)
	}
	held, err = source.Daemon.Store.ProvisioningHeld(id)
	if err != nil || !held {
		t.Fatal("finish released the hold")
	}
	if status := other.APIStatus(http.MethodPost, "/api/games/"+id+"/sync", nil, nil); status >= 500 {
		t.Fatalf("third peer sync after finish: %d", status)
	}
	if readHoldFile(t, oth, "slot.sav") != "" || readHoldFile(t, src, "slot.sav") != "SOURCE-BYTES" {
		t.Fatal("finish let a third peer read or write the source")
	}
	var activated struct {
		Released  bool `json:"released"`
		AutoSync  bool `json:"autoSync"`
		LocalOnly bool `json:"localOnly"`
	}
	source.API(http.MethodPost, "/api/games/"+id+"/first-copy/activate", map[string]string{"txId": first.TxID}, &activated)
	if !activated.Released || activated.AutoSync || !activated.LocalOnly {
		t.Fatalf("activate = %+v", activated)
	}
	held, err = source.Daemon.Store.ProvisioningHeld(id)
	if err != nil || held {
		t.Fatal("activate left the hold")
	}
	lease, err = source.Daemon.Store.BoundFirstCopy(id)
	if err != nil || lease == nil || lease.Phase != "activated" || lease.PeerID != target.NodeID() {
		t.Fatalf("activate dropped the named-peer fence: %+v %v", lease, err)
	}
	if status := other.APIStatus(http.MethodPost, "/api/games/"+id+"/sync", nil, nil); status >= 500 {
		t.Fatalf("third peer sync after activate: %d", status)
	}
	if readHoldFile(t, oth, "slot.sav") != "" || readHoldFile(t, src, "third.sav") != "" || readHoldFile(t, src, "slot.sav") != "SOURCE-BYTES" {
		t.Fatal("activation exposed the source to a third peer")
	}
	// An out-of-order Abort, Finish or Open must not weaken an activated fence.
	if status := source.APIStatus(http.MethodDelete, "/api/games/"+id+"/first-copy", map[string]string{"txId": first.TxID}, nil); status < 400 {
		t.Fatalf("delayed abort unexpectedly returned %d after activation", status)
	}
	if status := source.APIStatus(http.MethodPost, "/api/games/"+id+"/first-copy/finish", map[string]string{"txId": first.TxID}, nil); status < 400 {
		t.Fatalf("late finish unexpectedly returned %d after activation", status)
	}
	if status := source.APIStatus(http.MethodPost, "/api/games/"+id+"/first-copy/open", map[string]string{"txId": first.TxID, "expectHash": "bogus"}, nil); status < 400 {
		t.Fatalf("open with an unverified digest unexpectedly returned %d", status)
	}
	source.Restart()
	lease, err = source.Daemon.Store.BoundFirstCopy(id)
	if err != nil || lease == nil || lease.Phase != "activated" || lease.PeerID != target.NodeID() {
		t.Fatalf("late lifecycle operation or restart removed fence: %+v %v", lease, err)
	}
	if status := other.APIStatus(http.MethodPost, "/api/games/"+id+"/sync", nil, nil); status >= 500 {
		t.Fatalf("third peer sync after restart returned %d", status)
	}
	if readHoldFile(t, oth, "slot.sav") != "" || readHoldFile(t, src, "third.sav") != "" || readHoldFile(t, src, "slot.sav") != "SOURCE-BYTES" {
		t.Fatal("third peer exchanged files after delayed abort/finish and restart")
	}
}

func TestFirstCopy_ActivatedPeerGameOpsCannotRemoveFence(t *testing.T) {
	source := testutil.NewTestDaemon(t, "GameOp-Source")
	target := testutil.NewTestDaemon(t, "GameOp-Target")
	other := testutil.NewTestDaemon(t, "GameOp-Other")
	source.PairWith(target)
	source.PairWith(other)
	const id = "game-op-fence"
	src := sideDir(t, source, "game-op")
	tgt := sideDir(t, target, "game-op")
	third := sideDir(t, other, "game-op")
	writeHoldFile(t, src, "slot.sav", "AUTHORITATIVE")
	writeHoldFile(t, third, "other.sav", "THIRD")
	createHeld(t, source, id, "Game Op Fence", src)
	createHeld(t, target, id, "Game Op Fence", tgt)
	createHeld(t, other, id, "Game Op Fence", third)
	if err := source.Daemon.Store.AddGameAlias("old-alias", id); err != nil {
		t.Fatal(err)
	}
	lease := beginCopy(t, source, id, "source", target.NodeID())
	source.API(http.MethodPost, "/api/games/"+id+"/first-copy/finish", map[string]string{"txId": lease.TxID}, nil)
	source.API(http.MethodPost, "/api/games/"+id+"/first-copy/activate", map[string]string{"txId": lease.TxID}, nil)

	// Exercise the same daemon callbacks used by authenticated LAN and WAN
	// untrack/retrack notifications, without relying on asynchronous timing.
	if source.Daemon.P2P.OnUntrackRequest == nil || source.Daemon.P2P.OnRetrackRequest == nil {
		t.Fatal("peer game-op callbacks missing")
	}
	source.Daemon.P2P.OnUntrackRequest(id)
	source.Daemon.P2P.OnRetrackRequest(id)
	if _, err := source.Daemon.Store.GetGame(id); err != nil {
		t.Fatalf("peer game op removed authoritative game: %v", err)
	}
	if err := source.Daemon.UntrackGame(id); err == nil {
		t.Fatal("local untrack removed activated game")
	}
	if err := source.Daemon.LinkGames(id, "new-alias"); err == nil {
		t.Fatal("game link changed activated identity")
	}
	if _, ok := source.Daemon.Store.GetGameAlias("new-alias"); ok {
		t.Fatal("refused link still installed alias")
	}
	if err := source.Daemon.UnlinkGame("old-alias"); err == nil {
		t.Fatal("unlink changed activated identity")
	}
	if _, ok := source.Daemon.Store.GetGameAlias("old-alias"); !ok {
		t.Fatal("refused unlink removed alias")
	}

	if status := source.APIStatus(http.MethodPatch, "/api/games/"+id, map[string]any{"autoSync": true}, nil); status < 400 {
		t.Fatalf("settings API enabled AutoSync while fenced: %d", status)
	}
	fence, err := source.Daemon.Store.BoundFirstCopy(id)
	if err != nil || fence == nil || fence.Phase != store.FirstCopyActivated || fence.PeerID != target.NodeID() {
		t.Fatalf("game lifecycle removed fence: %+v %v", fence, err)
	}
	if readHoldFile(t, src, "slot.sav") != "AUTHORITATIVE" {
		t.Fatal("game lifecycle modified source data")
	}
	other.API(http.MethodPost, "/api/games/"+id+"/release-provisioning", map[string]any{"autoSync": false}, nil)
	other.APIStatus(http.MethodPost, "/api/games/"+id+"/sync", nil, nil)
	if readHoldFile(t, third, "slot.sav") != "" || readHoldFile(t, src, "other.sav") != "" {
		t.Fatal("third peer exchanged first-copy data")
	}
}
