package e2e

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/p2p/syncengine"
	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/testutil"
)

// Authenticated decision exchange is a recovery ledger, not distributed Open.
// Both signed LAN and relay/WAN paths remain fenced before and after commit.
func TestFirstCopy_PhaseC3GuardedFinalization(t *testing.T) {
	for _, mode := range []string{"lan", "wan"} {
		t.Run(mode, func(t *testing.T) {
			source := testutil.NewTestDaemon(t, "Decision-Source")
			target := testutil.NewTestDaemon(t, "Decision-Target")
			third := testutil.NewTestDaemon(t, "Finalization-Third")
			if mode == "wan" {
				pairOverRelay(t, source, target, startRelay(t), "handover-decision-wan")
			} else {
				source.PairWith(target)
			}
			source.PairWith(third)
			gameID := "firstcopy-finalize-" + mode
			src := sideDir(t, source, "decision-source")
			tgt := sideDir(t, target, "decision-target")
			thirdDir := sideDir(t, third, "finalization-third")
			writeHoldFile(t, thirdDir, "third.sav", "THIRD-UNTOUCHED")
			writeHoldFile(t, src, "slot.sav", "DECISION-IMMUTABLE")
			createHeld(t, source, gameID, "Decision", src)
			createHeld(t, target, gameID, "Decision", tgt)
			createHeld(t, third, gameID, "Decision", thirdDir)
			a := beginCopy(t, source, gameID, store.FirstCopySource, target.NodeID())
			b := beginCopy(t, target, gameID, store.FirstCopyTarget, source.NodeID())
			if !testutil.WaitFor(45*time.Second, func() bool {
				target.APIStatus(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
				return readHoldFile(t, tgt, "slot.sav") == "DECISION-IMMUTABLE"
			}) {
				t.Fatal("target did not receive source generation")
			}
			srcRow, err := source.Daemon.Store.RecordedFirstCopy(gameID)
			if err != nil || srcRow == nil {
				t.Fatalf("source lease: %v", err)
			}
			for _, p := range []struct {
				node *testutil.TestDaemon
				tx   string
			}{{source, a.TxID}, {target, b.TxID}} {
				if rc := p.node.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/finish", map[string]string{"txId": p.tx, "expectHash": srcRow.ContentHash}, nil); rc != 200 {
					t.Fatalf("finish %d", rc)
				}
				if rc := p.node.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/activate", map[string]string{"txId": p.tx}, nil); rc != 200 {
					t.Fatalf("activate %d", rc)
				}
			}
			for _, p := range []struct {
				node *testutil.TestDaemon
				tx   string
			}{{target, b.TxID}, {source, a.TxID}} {
				if rc := p.node.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/receipt/send", map[string]string{"txId": p.tx}, nil); rc != 200 {
					t.Fatalf("Phase B receipt %d", rc)
				}
			}
			for _, p := range []struct {
				node *testutil.TestDaemon
				tx   string
			}{{source, a.TxID}, {target, b.TxID}} {
				if rc := p.node.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/prepare", map[string]string{"txId": p.tx}, nil); rc != 200 {
					t.Fatalf("Phase C prepare %d", rc)
				}
			}
			for _, p := range []struct {
				node *testutil.TestDaemon
				tx   string
			}{{target, b.TxID}, {source, a.TxID}} {
				if rc := p.node.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/send", map[string]string{"txId": p.tx}, nil); rc != 200 {
					t.Fatalf("Phase C signed prepare %d", rc)
				}
			}
			// Explicit source operator creates an immutable decision identity.
			offer, err := source.Daemon.OfferFirstCopyDecision(gameID, a.TxID)
			if err != nil {
				t.Fatal(err)
			}
			if err := source.Daemon.AuthorizeFirstCopyDecision(gameID, a.TxID, offer.DecisionID); err == nil {
				t.Fatal("source authorized before target consent")
			}
			if rc := source.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/decision/send", map[string]string{"txId": a.TxID, "stage": store.DecisionOffer}, nil); rc != 200 {
				t.Fatalf("signed decision offer %d", rc)
			}
			// Unsigned loopback callers cannot forge a peer's decision.
			fake := offer
			fake.Stage = store.DecisionApproval
			fake.Role = store.FirstCopyTarget
			if rc := source.APIStatus(http.MethodPost, "/api/p2p/first-copy/handover/decision/"+gameID, fake, nil); rc < 400 {
				t.Fatalf("unsigned approval accepted %d", rc)
			}
			if rc := target.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/decision/authorize", map[string]string{"txId": b.TxID, "decisionId": offer.DecisionID}, nil); rc != 200 {
				t.Fatalf("target operator approval %d", rc)
			}
			if rc := target.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/decision/send", map[string]string{"txId": b.TxID, "stage": store.DecisionApproval}, nil); rc != 200 {
				t.Fatalf("target signed approval %d", rc)
			}
			if rc := source.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/decision/authorize", map[string]string{"txId": a.TxID, "decisionId": offer.DecisionID}, nil); rc != 200 {
				t.Fatalf("source operator decision %d", rc)
			}
			// A committed source may reboot before target sees the decision;
			// the caller must explicitly retry the identical signed decision.
			if rc := source.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/decision/send", map[string]string{"txId": a.TxID, "stage": store.DecisionCommit}, nil); rc != 200 {
				t.Fatalf("signed commit %d", rc)
			}
			if rc := target.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/decision/send", map[string]string{"txId": b.TxID, "stage": store.DecisionConfirm}, nil); rc != 200 {
				t.Fatalf("signed confirm %d", rc)
			}
			for _, p := range []struct {
				node   *testutil.TestDaemon
				tx     string
				source bool
			}{{source, a.TxID, true}, {target, b.TxID, false}} {
				status, err := p.node.Daemon.Store.ReadFirstCopyDecisionStatus(gameID, p.tx)
				if err != nil || !status.DecisionRecorded || !status.FenceActive || status.AutoSync || status.OpenAuthorized {
					t.Fatalf("decision exposed game: %+v %v", status, err)
				}
				if p.source && !status.PeerCommitConfirmed {
					t.Fatalf("source missing remote confirm: %+v", status)
				}
				lease, err := p.node.Daemon.Store.RecordedFirstCopy(gameID)
				if err != nil || lease == nil || lease.Phase != store.FirstCopyActivated {
					t.Fatalf("decision lost fence: %+v %v", lease, err)
				}
				game, err := p.node.Daemon.Store.GetGame(gameID)
				if err != nil || game.AutoSync {
					t.Fatalf("decision enabled AutoSync: %+v %v", game, err)
				}
			}
			// Both local operators separately request the durable guard.
			for _, operator := range []struct {
				node *testutil.TestDaemon
				tx   string
			}{
				{source, a.TxID}, {target, b.TxID},
			} {
				if rc := operator.node.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/finalize",
					map[string]string{"txId": operator.tx}, nil); rc != 200 {
					t.Fatalf("finalize intent %d", rc)
				}
			}
			for _, operator := range []struct {
				node *testutil.TestDaemon
				tx   string
			}{{source, a.TxID}, {target, b.TxID}} {
				var light store.FirstCopyFinalizationStatus
				if rc := operator.node.APIStatus(http.MethodGet, "/api/games/"+gameID+"/first-copy/handover/finalize?verifyContent=0", nil, &light); rc != 200 || light.ContentChecked || light.ContentValid || !light.FenceActive || light.OpenAuthorized {
					t.Fatalf("lightweight poll claimed content verification: rc=%d %+v", rc, light)
				}
				var full store.FirstCopyFinalizationStatus
				if rc := operator.node.APIStatus(http.MethodGet, "/api/games/"+gameID+"/first-copy/handover/finalize", nil, &full); rc != 200 || !full.ContentChecked || !full.ContentValid || !full.FenceActive || full.OpenAuthorized {
					t.Fatalf("default poll changed safety semantics: rc=%d %+v", rc, full)
				}
			}
			// Source/target/third are all paired with a signed transport,
			// but the durable admission guard allows no third-peer bytes.
			if rc := third.APIStatus(http.MethodPost, "/api/games/"+gameID+"/release-provisioning",
				map[string]any{"autoSync": false}, nil); rc != 200 {
				t.Fatalf("third release-provisioning %d", rc)
			}
			verifyThird := func(where string) {
				t.Helper()
				paired, err := third.Daemon.Store.GetPeer(source.NodeID())
				if err != nil {
					t.Fatal(err)
				}
				counterpart := syncengine.Peer{ID: paired.ID, Name: paired.Name, Address: paired.Address, Port: paired.Port}
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				transport := third.Daemon.P2P.Sync.Transport
				manifest, manifestErr := transport.FetchManifest(ctx, counterpart, gameID, syncengine.ManifestQuery{Name: "Decision", SavePath: thirdDir})
				if manifestErr == nil {
					t.Fatalf("signed third peer read manifest during %s: %+v", where, manifest)
				}
				if errors.Is(manifestErr, context.DeadlineExceeded) || errors.Is(manifestErr, context.Canceled) {
					t.Fatalf("third peer manifest probe timed out at %s, refusal not proven: %v", where, manifestErr)
				}
				generation := &syncengine.FirstCopyGeneration{Protocol: syncengine.FirstCopyGenerationProtocol,
					TxID: a.TxID, ID: srcRow.GenerationID, Digest: srcRow.ContentHash, Branch: srcRow.GenerationBranch}
				ref := syncengine.FileRef{GameID: gameID, RelPath: "slot.sav", Generation: generation}
				blocks, blockErr := transport.FetchBlocks(ctx, counterpart, ref, []int{0}, delta.BlockSizeFor(int64(len("DECISION-IMMUTABLE"))))
				if blockErr == nil || len(blocks) != 0 {
					t.Fatalf("signed third peer received protected block during %s: blocks=%d err=%v", where, len(blocks), blockErr)
				}
				if errors.Is(blockErr, context.DeadlineExceeded) || errors.Is(blockErr, context.Canceled) {
					t.Fatalf("third peer block probe timed out at %s, refusal not proven: %v", where, blockErr)
				}
				deleteErr := transport.DeleteRemote(ctx, counterpart, ref)
				if deleteErr == nil {
					t.Fatalf("signed third peer deleted protected source during %s", where)
				}
				if errors.Is(deleteErr, context.DeadlineExceeded) || errors.Is(deleteErr, context.Canceled) {
					t.Fatalf("third peer delete probe timed out at %s, refusal not proven: %v", where, deleteErr)
				}
				rc := third.APIStatus(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
				if rc >= 500 {
					t.Fatalf("third sync at %s failed rc=%d", where, rc)
				}
				if readHoldFile(t, thirdDir, "slot.sav") != "" || readHoldFile(t, src, "third.sav") != "" ||
					readHoldFile(t, thirdDir, "third.sav") != "THIRD-UNTOUCHED" ||
					readHoldFile(t, src, "slot.sav") != "DECISION-IMMUTABLE" ||
					readHoldFile(t, tgt, "slot.sav") != "DECISION-IMMUTABLE" {
					t.Fatalf("third peer transferred unauthorized saves during %s", where)
				}
			}
			verifyThird("intent")
			for _, step := range []struct {
				node      *testutil.TestDaemon
				tx, stage string
			}{
				{source, a.TxID, store.FinalizeOffer},
				{target, b.TxID, store.FinalizeReady},
				{source, a.TxID, store.FinalizeCommit},
			} {
				if rc := step.node.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/finalize/send",
					map[string]string{"txId": step.tx, "stage": step.stage}, nil); rc != 200 {
					t.Fatalf("signed finalize stage %s rc=%d", step.stage, rc)
				}
				verifyThird(step.stage)
			}
			// Simulate a lost final acknowledgement: source is locally
			// finalized but missing the target's signed Ack. No timeout Open.
			sourceStatus, err := source.Daemon.Store.ReadFirstCopyPairFinalization(gameID, a.TxID)
			if err != nil || !sourceStatus.PairFinalized || sourceStatus.PeerAck || !sourceStatus.RecoveryRequired {
				t.Fatalf("missing acknowledgement was not recoverable: %+v %v", sourceStatus, err)
			}
			target.Restart()
			verifyThird("target-restart-before-ack")
			// A lost response to commit is not a new decision: the same
			// durable source stage is resent with a new signed nonce.
			if rc := source.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/finalize/send",
				map[string]string{"txId": a.TxID, "stage": store.FinalizeCommit}, nil); rc != 200 {
				t.Fatalf("idempotent signed commit recovery %d", rc)
			}
			if rc := target.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/finalize/send",
				map[string]string{"txId": b.TxID, "stage": store.FinalizeAck}, nil); rc != 200 {
				t.Fatalf("signed finalization Ack after restart %d", rc)
			}
			// Deliver the same logical Ack again with a distinct transport
			// nonce; it must be idempotent without changing permissions.
			if rc := target.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/finalize/send",
				map[string]string{"txId": b.TxID, "stage": store.FinalizeAck}, nil); rc != 200 {
				t.Fatalf("idempotent Ack after lost reply %d", rc)
			}
			for _, p := range []struct {
				node *testutil.TestDaemon
				tx   string
			}{
				{source, a.TxID}, {target, b.TxID},
			} {
				status, err := p.node.Daemon.Store.ReadFirstCopyPairFinalization(gameID, p.tx)
				if err != nil || !status.PairFinalized || status.OpenAuthorized || status.AutoSync || !status.FenceActive {
					t.Fatalf("unsafe pair-finalized status %+v %v", status, err)
				}
				if rc := p.node.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/open",
					map[string]string{"txId": p.tx, "expectHash": srcRow.ContentHash}, nil); rc < 400 {
					t.Fatalf("legacy Open bypassed guard %d", rc)
				}
			}
			source.Restart()
			verifyThird("completed-restart")
		})
	}
}
