package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/testutil"
)

// The signed preparation barrier never removes the Phase B direction fence,
// even when the other peer responds over LAN or authenticated relay/WAN.
func TestFirstCopy_PhaseCPrepareOnly(t *testing.T) {
	for _, mode := range []string{"lan", "wan"} {
		t.Run(mode, func(t *testing.T) {
			source := testutil.NewTestDaemon(t, "Prepare-Source")
			target := testutil.NewTestDaemon(t, "Prepare-Target")
			if mode == "wan" {
				pairOverRelay(t, source, target, startRelay(t), "firstcopy-handover-wan")
			} else {
				source.PairWith(target)
			}
			gameID := "firstcopy-handover-" + mode
			src := sideDir(t, source, "handover-source")
			tgt := sideDir(t, target, "handover-target")
			writeHoldFile(t, src, "slot.sav", "PHASE-C-IMMUTABLE")
			createHeld(t, source, gameID, "Handover", src)
			createHeld(t, target, gameID, "Handover", tgt)
			a := beginCopy(t, source, gameID, store.FirstCopySource, target.NodeID())
			b := beginCopy(t, target, gameID, store.FirstCopyTarget, source.NodeID())
			if !testutil.WaitFor(45*time.Second, func() bool {
				target.APIStatus(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
				return readHoldFile(t, tgt, "slot.sav") == "PHASE-C-IMMUTABLE"
			}) {
				t.Fatal("target did not receive source generation")
			}
			srcRow, err := source.Daemon.Store.RecordedFirstCopy(gameID)
			if err != nil || srcRow == nil {
				t.Fatalf("source first-copy: %v", err)
			}
			for _, pair := range []struct {
				node *testutil.TestDaemon
				tx   string
			}{{source, a.TxID}, {target, b.TxID}} {
				if rc := pair.node.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/finish",
					map[string]string{"txId": pair.tx, "expectHash": srcRow.ContentHash}, nil); rc != 200 {
					t.Fatalf("Finish %d", rc)
				}
				if rc := pair.node.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/activate",
					map[string]string{"txId": pair.tx}, nil); rc != 200 {
					t.Fatalf("Activate %d", rc)
				}
			}
			for _, pair := range []struct {
				node *testutil.TestDaemon
				tx   string
			}{{target, b.TxID}, {source, a.TxID}} {
				if rc := pair.node.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/receipt/send",
					map[string]string{"txId": pair.tx}, nil); rc != 200 {
					t.Fatalf("Phase B receipt %d", rc)
				}
			}
			// A prepare from only one operator is insufficient. Each side
			// must explicitly prepare locally before either signed send.
			if rc := target.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/prepare",
				map[string]string{"txId": b.TxID}, nil); rc != 200 {
				t.Fatalf("target prepare %d", rc)
			}
			if rc := target.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/send",
				map[string]string{"txId": b.TxID}, nil); rc < 400 {
				t.Fatalf("one-sided preparation unexpectedly accepted remotely: %d", rc)
			}
			if rc := source.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/prepare",
				map[string]string{"txId": a.TxID}, nil); rc != 200 {
				t.Fatalf("source prepare %d", rc)
			}
			for _, pair := range []struct {
				node *testutil.TestDaemon
				tx   string
			}{{target, b.TxID}, {source, a.TxID}} {
				if rc := pair.node.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/send",
					map[string]string{"txId": pair.tx}, nil); rc != 200 {
					t.Fatalf("signed prepare %d", rc)
				}
			}
			for _, pair := range []struct {
				node *testutil.TestDaemon
				tx   string
			}{{target, b.TxID}, {source, a.TxID}} {
				status, err := pair.node.Daemon.Store.ReadFirstCopyHandoverStatus(gameID, pair.tx)
				if err != nil || !status.ReadyForOperatorReview || !status.FenceActive || status.OpenAuthorized || status.AutoSync {
					t.Fatalf("preparation exposed game: %+v err=%v", status, err)
				}
				row, err := pair.node.Daemon.Store.RecordedFirstCopy(gameID)
				if err != nil || row == nil || row.Phase != store.FirstCopyActivated {
					t.Fatalf("prepare removed fence: %+v err=%v", row, err)
				}
				game, err := pair.node.Daemon.Store.GetGame(gameID)
				if err != nil || game.AutoSync {
					t.Fatalf("prepare enabled AutoSync: %+v err=%v", game, err)
				}
			}
			// An unsigned local HTTP call may not fake a peer preparation.
			fake := store.FirstCopyHandoverPrepare{Version: store.FirstCopyHandoverVersion, GameID: gameID,
				SourceTxID: a.TxID, TargetTxID: b.TxID, GenerationID: srcRow.GenerationID,
				Branch: srcRow.GenerationBranch, Digest: srcRow.ContentHash, Role: store.FirstCopyTarget, Stage: "prepared"}
			if rc := source.APIStatus(http.MethodPost, "/api/p2p/first-copy/handover/prepare/"+gameID, fake, nil); rc < 400 {
				t.Fatalf("unsigned handover prepare accepted: %d", rc)
			}
			if rc := source.APIStatus(http.MethodDelete, "/api/games/"+gameID+"/first-copy/handover",
				map[string]string{"txId": a.TxID}, nil); rc != 200 {
				t.Fatalf("cancel preparation %d", rc)
			}
			status, err := source.Daemon.Store.ReadFirstCopyHandoverStatus(gameID, a.TxID)
			if err != nil || status.ReadyForOperatorReview || !status.FenceActive {
				t.Fatalf("cancel exposed game: %+v err=%v", status, err)
			}
		})
	}
}
