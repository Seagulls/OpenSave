package e2e

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// A completion event through the local HTTP API must not be allowed to
// impersonate a signed paired peer, even on loopback. This is independent
// of whether the game is currently held.
func TestTranche1UnsignedLANSyncEventCannotRecordLineage(t *testing.T) {
	d := testutil.NewTestDaemon(t, "Metadata-LAN")
	dir := sideDir(t, d, "metadata-held")
	createHeld(t, d, "metadata-held", "Metadata Held", dir)
	for _, eventType := range []string{"sync-complete", "in-sync"} {
		rc := d.APIStatus(http.MethodPost, "/api/p2p/sync-event/metadata-held",
			map[string]any{"eventType": eventType, "data": map[string]any{
				"pulledFiles": []string{"forged.sav"}, "manifestHash": "fake"}}, nil)
		if rc != http.StatusUnauthorized {
			t.Fatalf("unsigned LAN %s: rc=%d", eventType, rc)
		}
	}
	files, _, err := d.Daemon.Store.GetSyncState("metadata-held", "forged-peer")
	if err != nil || len(files) != 0 {
		t.Fatalf("unsigned event changed lineage: %v, %v", files, err)
	}
}

// A room member can invent a relay 'sync-event' with somebody else's From
// peer ID. Those legacy bare events are display-only now; a completion must
// instead be sent by OpenSave's existing nonce/MAC-authenticated WAN request.
func TestTranche1BareWANCompletionCannotInventLineage(t *testing.T) {
	relayURL := startRelay(t)
	const room = "tranche1-bare-event"
	a := testutil.NewTestDaemon(t, "Metadata-A")
	b := testutil.NewTestDaemon(t, "Metadata-B")
	pairOverRelay(t, a, b, relayURL, room)
	gameID := "metadata-wan"
	bdir := sideDir(t, b, "metadata-b")
	createHeld(t, b, gameID, "Metadata", bdir)
	beforeFiles, _, err := b.Daemon.Store.GetSyncState(gameID, a.NodeID())
	if err != nil {
		t.Fatal(err)
	}
	sendAs(t, relayURL, room, map[string]any{
		"type": "sync-event", "to": b.NodeID(), "from": a.NodeID(),
		"gameId": gameID, "eventType": "sync-complete",
		"data": map[string]any{"pulledFiles": []string{"invented.sav"}},
	})
	// Wait past the old immediate mutation path and its asynchronous refresh.
	time.Sleep(800 * time.Millisecond)
	afterFiles, _, err := b.Daemon.Store.GetSyncState(gameID, a.NodeID())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeFiles, afterFiles) {
		t.Fatalf("bare WAN frame invented lineage: before %v, after %v", beforeFiles, afterFiles)
	}
}
