package daemon

import (
	"context"
	"fmt"
	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/snapshot"
	"github.com/opensave/opensave/internal/store"
	"sort"
)

func (d *Daemon) PreparePairLive(gameID, txID string) (store.PairLivePrepareStatus, error) {
	if err := d.handoverPreflight(gameID, txID); err != nil {
		return store.PairLivePrepareStatus{}, err
	}
	status, err := d.Store.ReadFirstCopyPairFinalization(gameID, txID)
	if err != nil {
		return store.PairLivePrepareStatus{}, err
	}
	if !status.PairFinalized || (status.Phase != store.FinalizePairFinalized) {
		return store.PairLivePrepareStatus{}, fmt.Errorf("pair-live requires finalized C.3")
	}
	return d.Store.PreparePairLive(gameID, txID)
}
func (d *Daemon) SendPairLiveReady(ctx context.Context, gameID, txID string) (store.PairLiveReadyMessage, error) {
	if err := d.handoverPreflight(gameID, txID); err != nil {
		return store.PairLiveReadyMessage{}, err
	}
	msg, peer, err := d.Store.OutboundPairLiveReady(gameID, txID)
	if err != nil {
		return store.PairLiveReadyMessage{}, err
	}
	if d.P2P == nil {
		return store.PairLiveReadyMessage{}, fmt.Errorf("pair-live transport not available")
	}
	if err = d.P2P.SendPairLiveReady(ctx, peer, msg); err != nil {
		return store.PairLiveReadyMessage{}, err
	}
	return msg, nil
}
func (d *Daemon) ReadPairLivePrepare(gameID, txID string) (store.PairLivePrepareStatus, error) {
	return d.Store.ReadPairLivePrepare(gameID, txID)
}

// ActivatePairLive locally seeds the primary and named-root native baseline;
// source/target serving remains fenced until signed remote activation arrives.
func (d *Daemon) ActivatePairLive(gameID, txID string) (store.PairLivePrepareStatus, error) {
	if err := d.handoverPreflight(gameID, txID); err != nil {
		return store.PairLivePrepareStatus{}, err
	}
	p, err := d.Store.ReadPairLivePrepare(gameID, txID)
	if err != nil {
		return store.PairLivePrepareStatus{}, err
	}
	if !p.PeerReady || p.LocalActive {
		return store.PairLivePrepareStatus{}, fmt.Errorf("fresh signed readiness required and no previous activation")
	}
	game, err := d.Store.GetGame(gameID)
	if err != nil {
		return store.PairLivePrepareStatus{}, err
	}
	if game.SyncIgnore != "" || game.ActiveBranch != p.Branch {
		return store.PairLivePrepareStatus{}, fmt.Errorf("pair-live branch or ignore rules differ")
	}
	extra, err := d.Store.GameRootPaths(gameID)
	if err != nil {
		return store.PairLivePrepareStatus{}, err
	}
	m, failures, err := delta.BuildMultiManifest(game.SavePath, extra)
	if err != nil || len(failures) != 0 || len(m.Extra) != len(extra) {
		return store.PairLivePrepareStatus{}, fmt.Errorf("pair-live requires every save location readable: %v / %v", err, failures)
	}
	digest, err := snapshot.FirstCopySourceDigest(game.SavePath, extra)
	if err != nil || digest != p.Digest {
		return store.PairLivePrepareStatus{}, fmt.Errorf("verified generation content changed: %v", err)
	}
	names := m.RootNames()
	sort.Strings(names)
	// Primary root MUST be first for ActivatePairLive's defensive validation.
	ordered := append([]string{delta.PrimaryRoot}, []string{}...)
	for _, n := range names {
		if n != delta.PrimaryRoot {
			ordered = append(ordered, n)
		}
	}
	seeds := make([]store.PairLiveRootSeed, 0, len(ordered))
	for _, root := range ordered {
		v := m.Root(root)
		files := make([]string, 0, len(v.Files))
		for path := range v.Files {
			files = append(files, path)
		}
		sort.Strings(files)
		dirs := append([]string{}, v.Dirs...)
		sort.Strings(dirs)
		seeds = append(seeds, store.PairLiveRootSeed{Root: root, Hash: m.RootHash(root), Files: files, Dirs: dirs})
	}
	if err := d.Store.ActivatePairLive(gameID, txID, m.ContentHash(), seeds); err != nil {
		return store.PairLivePrepareStatus{}, err
	}
	d.ResyncWatchers()
	return d.Store.ReadPairLivePrepare(gameID, txID)
}

func (d *Daemon) SendPairLiveActive(ctx context.Context, gameID, txID string) error {
	p, err := d.Store.ReadPairLivePrepare(gameID, txID)
	if err != nil {
		return err
	}
	if !p.LocalActive || len(p.RootCommitment) != 64 {
		return fmt.Errorf("local activation required")
	}
	// Before the peer is live, recheck that the source/target still holds the
	// exact certified generation. Never authorize a changed unshared copy.
	// Once BOTH peers are active, normal native edits may legitimately differ
	// from the pinned snapshot and must not disable recovery retransmission.
	if !p.PeerActive {
		game, err := d.Store.GetGame(gameID)
		if err != nil {
			return err
		}
		digest, err := d.firstCopyDigest(game)
		if err != nil || digest != p.Digest {
			return fmt.Errorf("pair-live activation evidence no longer matches current save: %v", err)
		}
	}
	msg, peer, err := d.Store.OutboundPairLiveReady(gameID, txID)
	if err != nil {
		return err
	}
	msg.Kind = "active"
	msg.RootCommitment = p.RootCommitment
	if d.P2P == nil {
		return fmt.Errorf("pair-live signed transport unavailable")
	}
	return d.P2P.SendPairLiveReady(ctx, peer, msg)
}
