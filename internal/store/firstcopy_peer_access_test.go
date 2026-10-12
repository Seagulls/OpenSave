package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func peerAccessFixture(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "peer-access.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.db.Exec(`INSERT INTO games (id,name,save_path,auto_sync)
		VALUES ('peer-access', 'Peer Access', '/tmp/peer-access-test-save', 0)`); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPeerTransferAccessOrdinaryAndHeld(t *testing.T) {
	s := peerAccessFixture(t)
	state, err := s.PeerTransferAccess("peer-access")
	if err != nil || state.Held || state.Lease != nil || state.Guard != nil {
		t.Fatalf("ordinary state: %+v, %v", state, err)
	}
	if _, err := s.db.Exec(`INSERT INTO game_provisioning_holds(game_id,held_at)
		VALUES ('peer-access','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	state, err = s.PeerTransferAccess("peer-access")
	if err != nil || !state.Held || state.Lease != nil {
		t.Fatalf("held state: %+v, %v", state, err)
	}
	if _, err := s.db.Exec(`INSERT INTO game_aliases(alias_id,game_id)
        VALUES('peer-alias','peer-access')`); err != nil {
		t.Fatal(err)
	}
	alias, err := s.PeerTransferAccess("peer-alias")
	if err != nil || !alias.Held || alias.Guard != nil || alias.Lease != nil {
		t.Fatalf("alias bypassed provisioning hold: %+v %v", alias, err)
	}
	s.SetProvisioningReadFault(errors.New("synthetic hold lookup failure"))
	if _, err := s.PeerTransferAccess("peer-access"); err == nil {
		t.Fatal("unreadable protection state was treated as ordinary access")
	}
	s.SetProvisioningReadFault(nil)
}

func TestPeerTransferAccessExpiredAndActivated(t *testing.T) {
	s := peerAccessFixture(t)
	if _, err := s.db.Exec(`INSERT INTO game_first_copies
		(game_id,tx_id,role,peer_id,created_at,expires_at,phase,content_hash)
		VALUES ('peer-access','tx-1','source','named-peer',?,?,'copying','digest')`,
		time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339Nano),
		time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	state, err := s.PeerTransferAccess("peer-access")
	if err != nil || state.Lease != nil {
		t.Fatalf("expired copying lease granted access: %+v, %v", state, err)
	}
	if _, err := s.db.Exec(`UPDATE game_first_copies SET phase='activated' WHERE game_id='peer-access'`); err != nil {
		t.Fatal(err)
	}
	state, err = s.PeerTransferAccess("peer-access")
	if err != nil || state.Lease == nil || state.Lease.PeerID != "named-peer" || state.Lease.Phase != FirstCopyActivated {
		t.Fatalf("activated fence lost: %+v, %v", state, err)
	}
}

func TestPeerTransferAccessIndependentGuardAndCorruption(t *testing.T) {
	s := peerAccessFixture(t)
	if _, err := s.db.Exec(`INSERT INTO game_first_copies
		(game_id,tx_id,role,peer_id,created_at,expires_at,phase,content_hash)
		VALUES ('peer-access','tx-1','source','named-peer',?,?,'activated','digest')`,
		time.Now().UTC().Format(time.RFC3339Nano),
		time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO game_first_copy_pair_guards
		(game_id,local_tx_id,peer_id,source_tx_id,target_tx_id,generation_id,branch,
		digest,decision_id,role,phase,updated_at)
		VALUES('peer-access','tx-1','named-peer','tx-1','tx-target','generation','main',
		'digest','decision','source','pair_finalized','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	state, err := s.PeerTransferAccess("peer-access")
	if err != nil || state.Guard == nil || state.Lease == nil || state.Guard.PeerID != state.Lease.PeerID {
		t.Fatalf("pair guard missing: %+v, %v", state, err)
	}
	if _, err := s.db.Exec(`UPDATE game_first_copy_pair_guards SET peer_id='' WHERE game_id='peer-access'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PeerTransferAccess("peer-access"); err == nil {
		t.Fatal("invalid pair guard was treated as open access")
	}
}

func BenchmarkPeerAccessLegacyOrdinary(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "legacy.sqlite"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`INSERT INTO games(id,name,save_path) VALUES('benchmark','Benchmark','/tmp/test')`); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.PairGuardForServe("benchmark"); err != nil {
			b.Fatal(err)
		}
		if _, err := s.BoundFirstCopy("benchmark"); err != nil {
			b.Fatal(err)
		}
		if _, err := s.ProvisioningBlocks("benchmark"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPeerAccessUnifiedOrdinary(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "unified.sqlite"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`INSERT INTO games(id,name,save_path) VALUES('benchmark','Benchmark','/tmp/test')`); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		state, err := s.PeerTransferAccess("benchmark")
		if err != nil || state.Held || state.Lease != nil || state.Guard != nil {
			b.Fatalf("unexpected state: %+v %v", state, err)
		}
	}
}
