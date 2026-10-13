package e2e

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/p2p/syncengine"
	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/testutil"
)

func TestFirstCopy_PairLiveNativeConvergence(t *testing.T) {
	for _, mode := range []string{"lan", "wan"} {
		t.Run(mode, func(t *testing.T) {
			source := testutil.NewTestDaemon(t, "PairLive-Source")
			target := testutil.NewTestDaemon(t, "PairLive-Target")
			third := testutil.NewTestDaemon(t, "PairLive-Third")
			if mode == "wan" {
				pairOverRelay(t, source, target, startRelay(t), "handover-decision-wan")
			} else {
				source.PairWith(target)
			}
			source.PairWith(third)
			gameID := "firstcopy-pair-live-" + mode
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

			// C3 must be finalized on both sides, with the source's authenticated Ack.
			for _, node := range []struct {
				d  *testutil.TestDaemon
				tx string
			}{{source, a.TxID}, {target, b.TxID}} {
				if rc := node.d.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/finalize", map[string]string{"txId": node.tx}, nil); rc != 200 {
					t.Fatalf("C3 begin rc=%d", rc)
				}
			}
			for _, step := range []struct {
				d         *testutil.TestDaemon
				tx, phase string
			}{
				{source, a.TxID, store.FinalizeOffer},
				{target, b.TxID, store.FinalizeReady},
				{source, a.TxID, store.FinalizeCommit},
				{target, b.TxID, store.FinalizeAck},
			} {
				if rc := step.d.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/handover/finalize/send", map[string]string{"txId": step.tx, "stage": step.phase}, nil); rc != 200 {
					t.Fatalf("C3 send %s rc=%d", step.phase, rc)
				}
			}
			// Prepared is permission-neutral even after signed readiness.
			for _, node := range []struct {
				d  *testutil.TestDaemon
				tx string
			}{{source, a.TxID}, {target, b.TxID}} {
				if rc := node.d.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/pair-live/prepare", map[string]string{"txId": node.tx}, nil); rc != 200 {
					t.Fatalf("prepare rc=%d", rc)
				}
			}
			for _, node := range []struct {
				d  *testutil.TestDaemon
				tx string
			}{{source, a.TxID}, {target, b.TxID}} {
				if rc := node.d.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/pair-live/send", map[string]string{"txId": node.tx}, nil); rc != 200 {
					t.Fatalf("ready rc=%d", rc)
				}
			}
			for _, node := range []struct {
				d  *testutil.TestDaemon
				tx string
			}{{source, a.TxID}, {target, b.TxID}} {
				p, err := node.d.Daemon.Store.ReadPairLivePrepare(gameID, node.tx)
				if err != nil || !p.PeerReady || p.NativePairSyncEnabled {
					t.Fatalf("preactive permission %+v %v", p, err)
				}
			}
			// Activate target first: one-sided activation still cannot read live files.
			if rc := target.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/pair-live/activate", map[string]string{"txId": b.TxID}, nil); rc != 200 {
				t.Fatalf("target activation %d", rc)
			}
			if _, err := target.Daemon.Store.PairLiveModeForPeer(gameID, source.NodeID()); err == nil {
				t.Fatal("one-sided target opened native sync")
			}
			if rc := target.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/pair-live/active/send", map[string]string{"txId": b.TxID}, nil); rc != 200 {
				t.Fatalf("target signed active %d", rc)
			}
			if rc := source.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/pair-live/activate", map[string]string{"txId": a.TxID}, nil); rc != 200 {
				t.Fatalf("source activation %d", rc)
			}
			if rc := source.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/pair-live/active/send", map[string]string{"txId": a.TxID}, nil); rc != 200 {
				t.Fatalf("source signed active %d", rc)
			}
			for _, node := range []struct {
				d        *testutil.TestDaemon
				tx, peer string
			}{{source, a.TxID, target.NodeID()}, {target, b.TxID, source.NodeID()}} {
				mode, err := node.d.Daemon.Store.PairLiveModeForPeer(gameID, node.peer)
				if err != nil || !mode.LocalActive || !mode.PeerActive {
					t.Fatalf("pair did not enter live state %+v %v", mode, err)
				}
				saved, err := node.d.Daemon.Store.RecordedFirstCopy(gameID)
				if err != nil || saved == nil || saved.Phase != store.FirstCopyActivated {
					t.Fatalf("pair lost original lease %+v %v", saved, err)
				}
				g, err := node.d.Daemon.Store.GetGame(gameID)
				if err != nil || g.AutoSync {
					t.Fatalf("ordinary AutoSync enabled %+v %v", g, err)
				}
				if _, err := node.d.Daemon.Store.PairLiveModeForPeer(gameID, third.NodeID()); err == nil {
					t.Fatal("third peer inherited native permission")
				}
			}
			if base := source.Daemon.Store.GetAgreedHash(gameID, target.NodeID()); base == "" || base != target.Daemon.Store.GetAgreedHash(gameID, source.NodeID()) {
				t.Fatalf("native pair baseline not seeded source=%q", base)
			}
			// Both directions must use OpenSave's own sync engine and stored lineage.
			initialBase := source.Daemon.Store.GetAgreedHash(gameID, target.NodeID())
			writeHoldFile(t, src, "slot.sav", "PAIR-LIVE-EDIT-FROM-SOURCE")
			if !testutil.WaitFor(35*time.Second, func() bool {
				target.APIStatus(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
				return readHoldFile(t, tgt, "slot.sav") == "PAIR-LIVE-EDIT-FROM-SOURCE"
			}) {
				t.Fatal("source change did not travel through native sync")
			}
			// Completion is async; require authenticated shared-base convergence
			// before the next edit. A stranded base must never silently pass.
			if !testutil.WaitFor(15*time.Second, func() bool {
				a := source.Daemon.Store.GetAgreedHash(gameID, target.NodeID())
				b := target.Daemon.Store.GetAgreedHash(gameID, source.NodeID())
				return a != "" && a == b && a != initialBase
			}) {
				t.Fatalf("source merge base not ratcheted after signed pair-live completion: source=%s target=%s",
					source.Daemon.Store.GetAgreedHash(gameID, target.NodeID()),
					target.Daemon.Store.GetAgreedHash(gameID, source.NodeID()))
			}
			writeHoldFile(t, tgt, "slot.sav", "PAIR-LIVE-EDIT-FROM-BRIDGE")
			if !testutil.WaitFor(35*time.Second, func() bool {
				source.APIStatus(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
				return readHoldFile(t, src, "slot.sav") == "PAIR-LIVE-EDIT-FROM-BRIDGE"
			}) {
				t.Fatal("target change did not travel through native sync")
			}
			// A third signed peer must not receive source bytes, even though
			// the source/target pair is live and can exchange with each other.
			paired, err := third.Daemon.Store.GetPeer(source.NodeID())
			if err != nil {
				t.Fatal(err)
			}
			remote := syncengine.Peer{ID: paired.ID, Name: paired.Name, Address: paired.Address, Port: paired.Port}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, readErr := third.Daemon.P2P.Sync.Transport.FetchManifest(ctx, remote, gameID, syncengine.ManifestQuery{Name: "Pair Live", SavePath: thirdDir})
			if readErr == nil || errors.Is(readErr, context.DeadlineExceeded) {
				t.Fatalf("third peer manifest not safely refused: %v", readErr)
			}
			liveProof, err := source.Daemon.Store.ReadPairLivePrepare(gameID, a.TxID)
			if err != nil {
				t.Fatal(err)
			}
			_, forgedReadErr := third.Daemon.P2P.Sync.Transport.FetchManifest(ctx, remote, gameID, syncengine.ManifestQuery{Name: "Pair Live", SavePath: thirdDir, PairLiveEpoch: liveProof.DecisionID})
			if forgedReadErr == nil || errors.Is(forgedReadErr, context.DeadlineExceeded) {
				t.Fatalf("signed third peer forged live decision: %v", forgedReadErr)
			}
			wrongRef := syncengine.FileRef{GameID: gameID, RelPath: "slot.sav", PairLiveEpoch: liveProof.DecisionID}
			_, blockErr := third.Daemon.P2P.Sync.Transport.FetchBlocks(ctx, remote, wrongRef, []int{0}, 4096)
			if blockErr == nil || errors.Is(blockErr, context.DeadlineExceeded) {
				t.Fatalf("third peer fetched live blocks: %v", blockErr)
			}
			deleteErr := third.Daemon.P2P.Sync.Transport.DeleteRemote(ctx, remote, wrongRef)
			if deleteErr == nil || errors.Is(deleteErr, context.DeadlineExceeded) {
				t.Fatalf("third peer deleted live source: %v", deleteErr)
			}

			if rc := source.APIStatus(http.MethodPost, "/api/games/"+gameID+"/first-copy/open", map[string]string{"txId": a.TxID, "expectHash": srcRow.ContentHash}, nil); rc < 400 {
				t.Fatalf("legacy unrestricted Open accepted: %d", rc)
			}
			if readHoldFile(t, thirdDir, "third.sav") != "THIRD-UNTOUCHED" {
				t.Fatal("third peer save mutated")
			}
		})
	}
}
