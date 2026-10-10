package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/testutil"
)

type firstCopyRow struct {
	TxID   string `json:"txId"`
	Role   string `json:"role"`
	PeerID string `json:"peerId"`
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
	writeHoldFile(t, tgt, "other.sav", "TARGET-ONLY")
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
		AutoSync bool `json:"autoSync"`
		Released bool `json:"released"`
	}
	source.API(http.MethodPost, "/api/games/"+id+"/first-copy/finish", map[string]string{"txId": first.TxID}, &finished)
	if finished.AutoSync || !finished.Released {
		t.Fatalf("finish = %+v; want released and AutoSync still off", finished)
	}
	held, err = source.Daemon.Store.ProvisioningHeld(id)
	if err != nil || held {
		t.Fatal("finish left the hold in place")
	}
	if lease, err = source.Daemon.Store.ActiveFirstCopy(id); err != nil || lease != nil {
		t.Fatalf("finish left a lease: %+v %v", lease, err)
	}
}
