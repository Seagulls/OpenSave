package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/testutil"
)

// Signed requests, not local DB inserts, create the durable remote receipt.
// Both sides are already activated and remain isolated through the exchange.
// Neither Finish, Activate, nor receipt send automatically invokes Open.
func TestFirstCopy_PhaseBAuthenticatedReceiptExchange(t *testing.T) {
	for _, mode := range []string{"lan", "wan"} {
		t.Run(mode, func(t *testing.T) {
			source := testutil.NewTestDaemon(t, "Receipt-Source")
			target := testutil.NewTestDaemon(t, "Receipt-Target")
			if mode == "wan" {
				pairOverRelay(t, source, target, startRelay(t), "firstcopy-receipt-wan")
			} else {
				source.PairWith(target)
			}
			gameID := "firstcopy-receipt-exchange-" + mode
			src := sideDir(t, source, "receipt-source")
			tgt := sideDir(t, target, "receipt-target")
			writeHoldFile(t, src, "slot.sav", "RECEIPT-GENERATION")
			createHeld(t, source, gameID, "Receipts", src)
			createHeld(t, target, gameID, "Receipts", tgt)
			a := beginCopy(t, source, gameID, store.FirstCopySource, target.NodeID())
			b := beginCopy(t, target, gameID, store.FirstCopyTarget, source.NodeID())
			if !testutil.WaitFor(45*time.Second, func() bool {
				target.APIStatus(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
				return readHoldFile(t, tgt, "slot.sav") == "RECEIPT-GENERATION"
			}) {
				t.Fatal("target did not copy immutable source generation")
			}
			srcRow, err := source.Daemon.Store.RecordedFirstCopy(gameID)
			if err != nil || srcRow == nil {
				t.Fatalf("source lease: %v", err)
			}
			if status := source.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/finish",
				map[string]string{"txId": a.TxID, "expectHash": srcRow.ContentHash}, nil); status != 200 {
				t.Fatalf("source Finish status=%d", status)
			}
			if status := target.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/finish",
				map[string]string{"txId": b.TxID, "expectHash": srcRow.ContentHash}, nil); status != 200 {
				t.Fatalf("target Finish status=%d", status)
			}
			for _, pair := range []struct {
				node *testutil.TestDaemon
				tx   string
			}{{source, a.TxID}, {target, b.TxID}} {
				if status := pair.node.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/activate",
					map[string]string{"txId": pair.tx}, nil); status != 200 {
					t.Fatalf("Activate status=%d", status)
				}
			}
			// A locally forged receipt and an Open before signed proof are refused.
			fake := store.FirstCopyReceipt{Version: 1, GameID: gameID, SourceTxID: a.TxID,
				TargetTxID: b.TxID, GenerationID: srcRow.GenerationID,
				Branch: srcRow.GenerationBranch, Digest: srcRow.ContentHash,
				Role: store.FirstCopyTarget, Phase: store.FirstCopyActivated}
			if status := source.APIStatus(http.MethodPost, "/api/p2p/first-copy/receipt/"+gameID, fake, nil); status < 400 {
				t.Fatalf("unsigned local receipt unexpectedly accepted: %d", status)
			}
			if status := source.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/open",
				map[string]string{"txId": a.TxID, "expectHash": srcRow.ContentHash}, nil); status < 400 {
				t.Fatalf("Open without remote authenticated receipt accepted: %d", status)
			}
			// Each side sends its OWN locally derived attestation through the
			// pairing-signed transport. Target goes first because it knows source
			// identity from the manifest, source learns target tx from that receipt.
			for _, pair := range []struct {
				node *testutil.TestDaemon
				tx   string
			}{{target, b.TxID}, {source, a.TxID}} {
				if status := pair.node.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/receipt/send",
					map[string]string{"txId": pair.tx}, nil); status != 200 {
					t.Fatalf("signed receipt send status=%d", status)
				}
			}
			for _, pair := range []struct {
				node  *testutil.TestDaemon
				tx    string
				other string
			}{
				{source, a.TxID, store.FirstCopyTarget}, {target, b.TxID, store.FirstCopySource},
			} {
				remote, err := pair.node.Daemon.Store.RemoteFirstCopyReceipt(gameID, pair.tx)
				if err != nil || remote == nil || remote.Role != pair.other || remote.SourceTxID != a.TxID ||
					remote.TargetTxID != b.TxID || remote.Digest != srcRow.ContentHash {
					t.Fatalf("authenticated receipt invalid: %+v err=%v", remote, err)
				}
			}
			// Even the correctly signed counterpart cannot replace one transaction's
			// immutable first receipt with a different target transaction ID.
			conflicting := fake
			conflicting.TargetTxID = "another-transaction"
			if err := source.Daemon.Store.AcceptFirstCopyRemoteReceipt(gameID, target.NodeID(), "simulated-signed-conflict", conflicting); err == nil {
				t.Fatal("authenticated receipt could be overwritten by another transaction")
			}
			// The bridge/controller may decide when to issue explicit Open AFTER
			// separately examining both activated receipt records. This test does
			// not open them: no automatic permission expansion is allowed.
			for _, pair := range []struct {
				node *testutil.TestDaemon
				tx   string
			}{{source, a.TxID}, {target, b.TxID}} {
				row, err := pair.node.Daemon.Store.RecordedFirstCopy(gameID)
				if err != nil || row == nil || row.Phase != store.FirstCopyActivated {
					t.Fatalf("receipt exchange removed an activated fence: %+v err=%v", row, err)
				}
			}
			// Now explicitly authorize Open on each synthetic device, after
			// confirmed signed receipts on BOTH sides. Open remains a separate
			// operation and must leave native AutoSync disabled.
			for _, pair := range []struct {
				node *testutil.TestDaemon
				tx   string
			}{{target, b.TxID}, {source, a.TxID}} {
				if status := pair.node.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/open",
					map[string]string{"txId": pair.tx, "expectHash": srcRow.ContentHash}, nil); status != http.StatusOK {
					t.Fatalf("explicit Open rejected valid signed receipts: %d", status)
				}
				fence, err := pair.node.Daemon.Store.RecordedFirstCopy(gameID)
				if err != nil || fence != nil {
					t.Fatalf("explicit Open left a first-copy fence: %+v err=%v", fence, err)
				}
				game, err := pair.node.Daemon.Store.GetGame(gameID)
				if err != nil || game.AutoSync {
					t.Fatalf("Open unexpectedly changed AutoSync: game=%+v err=%v", game, err)
				}
			}
			// Once opened on both peers, the ordinary game lifecycle applies.
			if err := target.Daemon.Store.DeleteGame(gameID); err != nil {
				t.Fatalf("DeleteGame after authenticated Open refused: %v", err)
			}
		})
	}
}
func TestFirstCopy_PhaseBLegacyReceiptIsNotAuthority(t *testing.T) {
	source := testutil.NewTestDaemon(t, "Receipt-Legacy-Source")
	target := testutil.NewTestDaemon(t, "Receipt-Legacy-Target")
	source.PairWith(target)
	const gameID = "firstcopy-old-receipt"
	src := sideDir(t, source, "legacy-receipt-source")
	writeHoldFile(t, src, "slot.sav", "OLD-RECEIPT")
	createHeld(t, source, gameID, "Legacy Receipt", src)
	lease := beginCopy(t, source, gameID, store.FirstCopySource, target.NodeID())
	if err := source.Daemon.Store.RecordFirstCopyReceipt(gameID, lease.TxID, target.NodeID(), lease.GenerationID, "abc"); err == nil {
		t.Fatal("unauthenticated legacy receipt writer remains enabled")
	}
}
