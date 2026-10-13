package store

import (
	"testing"
	"time"
)

// Preparation cannot grant native pair sync or change its predecessor fence.
// Fixture uses disposable SQLite only. It does not require a real save folder.
func TestPairLivePrepareFailsClosedAndAuthenticatesPeer(t *testing.T) {
	s := peerAccessFixture(t)
	if _, err := s.PreparePairLive("peer-access", "local-tx"); err == nil {
		t.Fatal("prepared without C.3")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`INSERT INTO game_first_copies
       (game_id,tx_id,role,peer_id,created_at,expires_at,phase,content_hash,generation_id,generation_branch)
       VALUES('peer-access','local-tx','source','named-peer',?,?,'activated','digest','generation','main')`, now,
		time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO game_first_copy_pair_guards
       (game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,generation_id,branch,digest,decision_id,role,phase,peer_ack,updated_at)
       VALUES('peer-access','local-tx','named-peer','local-tx','remote-tx','generation','main','digest','decision','source','pair_finalized',0,?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreparePairLive("peer-access", "local-tx"); err == nil {
		t.Fatal("source prepared before authenticated finalization ack")
	}
	if _, err := s.db.Exec(`UPDATE game_first_copy_pair_guards SET peer_ack=1 WHERE game_id='peer-access'`); err != nil {
		t.Fatal(err)
	}
	first, err := s.PreparePairLive("peer-access", "local-tx")
	if err != nil {
		t.Fatal(err)
	}
	if !first.FenceActive || first.PeerReady || first.NativePairSyncEnabled || first.AutoSync || first.GeneralPeerAdmission {
		t.Fatalf("incorrect initial status: %+v", first)
	}
	second, err := s.PreparePairLive("peer-access", "local-tx")
	if err != nil || second.DecisionID != first.DecisionID || second.CreatedAt != first.CreatedAt {
		t.Fatalf("non-idempotent prepare: %+v %v", second, err)
	}
	outbound, peer, err := s.OutboundPairLiveReady("peer-access", "local-tx")
	if err != nil || peer != "named-peer" || outbound.Role != "source" {
		t.Fatalf("outbound: %+v %s %v", outbound, peer, err)
	}
	remote := outbound
	remote.Role = "target"
	if err := s.AcceptPairLiveReady("peer-access", "wrong-peer", "nonce-A", remote); err == nil {
		t.Fatal("forged peer accepted")
	}
	if err := s.AcceptPairLiveReady("peer-access", "named-peer", "nonce-A", outbound); err == nil {
		t.Fatal("same-role accepted")
	}
	altered := remote
	altered.Digest = "wrong"
	if err := s.AcceptPairLiveReady("peer-access", "named-peer", "nonce-A", altered); err == nil {
		t.Fatal("different generation accepted")
	}
	if err := s.AcceptPairLiveReady("peer-access", "named-peer", "nonce-A", remote); err != nil {
		t.Fatalf("authenticated readiness refused: %v", err)
	}
	if err := s.AcceptPairLiveReady("peer-access", "named-peer", "nonce-A", remote); err == nil {
		t.Fatal("replayed nonce accepted")
	}
	ready, err := s.ReadPairLivePrepare("peer-access", "local-tx")
	if err != nil || !ready.PeerReady || ready.NativePairSyncEnabled {
		t.Fatalf("readiness granted permissions: %+v %v", ready, err)
	}
	if _, err := s.db.Exec(`DELETE FROM game_first_copy_pair_guards WHERE game_id='peer-access'`); err == nil {
		t.Fatal("removed predecessor guard")
	}
	state, err := s.PeerTransferAccess("peer-access")
	if err != nil || state.Guard == nil || state.Lease == nil {
		t.Fatalf("lost protection: %+v %v", state, err)
	}
	if hash := s.GetAgreedHash("peer-access", "named-peer"); hash != "" {
		t.Fatalf("invented merge base: %q", hash)
	}
	if _, err := s.db.Exec(`UPDATE games SET auto_sync=1 WHERE id='peer-access'`); err == nil {
		t.Fatal("enabled AutoSync")
	}
	if _, err := s.db.Exec(`UPDATE game_first_copy_pair_live_prepares SET peer_id='other' WHERE game_id='peer-access'`); err == nil {
		t.Fatal("rebound peer")
	}
	if _, err := s.db.Exec(`DELETE FROM game_first_copy_pair_live_nonces WHERE game_id='peer-access'`); err == nil {
		t.Fatal("deleted replay history")
	}
}

func TestPairLiveTargetPreparationRequiresBoundSourceGeneration(t *testing.T) {
	s := peerAccessFixture(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`INSERT INTO game_first_copies
 (game_id,tx_id,role,peer_id,created_at,expires_at,phase,content_hash,
 source_tx_id,source_generation_id,source_generation_branch)
 VALUES('peer-access','target-tx','target','named-peer',?,?,'activated','digest','source-tx','archive','main')`,
		now, time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO game_first_copy_pair_guards
 (game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,generation_id,branch,digest,
 decision_id,role,phase,peer_ack,updated_at)
 VALUES('peer-access','target-tx','named-peer','source-tx','target-tx','archive','main','digest',
 'decision','target','pair_finalized',0,?)`, now); err != nil {
		t.Fatal(err)
	}
	state, err := s.PreparePairLive("peer-access", "target-tx")
	if err != nil || state.Role != "target" || state.NativePairSyncEnabled {
		t.Fatalf("target preparation: %+v %v", state, err)
	}
	msg, peer, err := s.OutboundPairLiveReady("peer-access", "target-tx")
	if err != nil || peer != "named-peer" || msg.SourceTxID != "source-tx" {
		t.Fatalf("target ready %+v %q %v", msg, peer, err)
	}
	source := msg
	source.Role = "source"
	if err := s.AcceptPairLiveReady("peer-access", "named-peer", "target-nonce", source); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE game_first_copies SET source_generation_id='tampered' WHERE game_id='peer-access'`); err == nil {
		t.Fatal("changed source generation while guarded")
	}
	after, err := s.ReadPairLivePrepare("peer-access", "target-tx")
	if err != nil || !after.PeerReady || after.NativePairSyncEnabled {
		t.Fatalf("target status: %+v %v", after, err)
	}
}

// An old callback cannot reuse a native writer's decision scope, and a
// unilateral activation cannot write convergence history or admit a peer.
func TestPairLiveScopedNativeMetadataNoStaleCallback(t *testing.T) {
	s := peerAccessFixture(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`INSERT INTO peers(id,name,address,port) VALUES('named-peer','Named','127.0.0.1',9999)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO game_first_copies(game_id,tx_id,role,peer_id,created_at,expires_at,phase,content_hash,generation_id,generation_branch) VALUES('peer-access','source-tx','source','named-peer',?,?,'activated','digest','generation','main')`, now, future); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO game_first_copy_pair_guards(game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,generation_id,branch,digest,decision_id,role,phase,peer_ack,updated_at) VALUES('peer-access','source-tx','named-peer','source-tx','target-tx','generation','main','digest','decision','source','pair_finalized',1,?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreparePairLive("peer-access", "source-tx"); err != nil {
		t.Fatal(err)
	}
	ready, peer, err := s.OutboundPairLiveReady("peer-access", "source-tx")
	if err != nil || peer != "named-peer" {
		t.Fatal(err)
	}
	ready.Role = FirstCopyTarget
	if err := s.AcceptPairLiveReady("peer-access", peer, "fresh-ready-1", ready); err != nil {
		t.Fatal(err)
	}
	rootHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := s.PairLiveWrite("peer-access", peer, "decision", "agreed", "", rootHash, nil, nil); err == nil {
		t.Fatal("unilateral native metadata accepted")
	}
	if err := s.ActivatePairLive("peer-access", "source-tx", rootHash, []PairLiveRootSeed{{Root: "", Hash: rootHash, Files: []string{"slot.sav"}, Dirs: []string{}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.PairLiveWrite("peer-access", peer, "decision", "agreed", "", rootHash, nil, nil); err == nil {
		t.Fatal("metadata accepted before peer active")
	}
	if err := s.SetAgreedHash("peer-access", peer, "forged-old"); err == nil {
		t.Fatal("old setter bypassed PR10 guard")
	}
	active := ready
	active.Kind = "active"
	active.RootCommitment = rootHash
	if err := s.AcceptPairLiveReady("peer-access", peer, "fresh-active-1", active); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptPairLiveReady("peer-access", peer, "fresh-active-1", active); err == nil {
		t.Fatal("replayed live activation accepted")
	}
	if _, err := s.PairLiveModeForPeer("peer-access", "other"); err == nil {
		t.Fatal("third peer admitted")
	}
	mode, err := s.PairLiveModeForPeer("peer-access", peer)
	if err != nil || mode.DecisionID != "decision" {
		t.Fatalf("mode %+v %v", mode, err)
	}
	if err := s.PairLiveWrite("peer-access", peer, "wrong-decision", "agreed", "", rootHash, nil, nil); err == nil {
		t.Fatal("wrong decision accepted")
	}
	if err := s.PairLiveWrite("peer-access", peer, "decision", "agreed", "", rootHash, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.PairLiveWrite("peer-access", peer, "decision", "lineage", "", "", []string{"slot.sav", "another.sav"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.PairLiveWrite("peer-access", peer, "decision", "synced", "", "2026-10-13T19:00:00Z", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAgreedHash("peer-access", peer, "forged-old"); err == nil {
		t.Fatal("late callback gained native metadata scope")
	}
	if got := s.GetAgreedHash("peer-access", peer); got != rootHash {
		t.Fatalf("corrupt native baseline %q", got)
	}
	if n, err := s.PairLiveLocalActivated("peer-access"); err != nil || !n {
		t.Fatalf("lost local activation %v %v", n, err)
	}
	if err := s.PairLiveValidateIncoming("peer-access", peer, "wrong"); err == nil {
		t.Fatal("wrong wire epoch accepted")
	}
	if err := s.PairLiveValidateIncoming("peer-access", peer, "decision"); err != nil {
		t.Fatal(err)
	}
}
