package p2p

import (
	"fmt"
	"os"
	"time"

	"github.com/opensave/opensave/internal/p2p/syncengine"
	"github.com/opensave/opensave/internal/snapshot"
	"github.com/opensave/opensave/internal/store"
)

type sourceGenerationView struct {
	snapshot store.Snapshot
	value    snapshot.Generation
	identity syncengine.FirstCopyGeneration
	size     int64
	modTime  time.Time
}

func (v sourceGenerationView) matches(expected *syncengine.FirstCopyGeneration) bool {
	return expected != nil &&
		expected.Protocol == v.identity.Protocol &&
		expected.TxID == v.identity.TxID &&
		expected.ID == v.identity.ID &&
		expected.Digest == v.identity.Digest &&
		expected.Branch == v.identity.Branch
}

// sourceGeneration is the pinned snapshot a source first-copy must serve.
// The first read after creation or restart validates the whole archive. Later
// block requests reuse that certified manifest while the archive's size and
// modification time are unchanged, and still verify every returned block.
// There is never a fallback to live files.
func (e *Engine) sourceGeneration(gameID string) (sourceGenerationView, bool, error) {
	lease, err := e.Store.BoundFirstCopy(gameID)
	if err != nil {
		return sourceGenerationView{}, false, err
	}
	if lease == nil || lease.Role != store.FirstCopySource {
		return sourceGenerationView{}, false, nil
	}
	if lease.GenerationID == "" || lease.ContentHash == "" {
		return sourceGenerationView{}, false, fmt.Errorf("first-copy generation identity is incomplete")
	}
	snap, err := e.Store.GetSnapshot(lease.GenerationID)
	if err != nil {
		return sourceGenerationView{}, false, err
	}
	game, err := e.Store.GetGame(gameID)
	if err != nil {
		return sourceGenerationView{}, false, err
	}
	if snap.GameID != gameID || snap.BranchName != lease.GenerationBranch ||
		game.ActiveBranch != lease.GenerationBranch || !snap.Pinned {
		return sourceGenerationView{}, false, fmt.Errorf("first-copy generation ownership is inconsistent")
	}
	info, err := os.Stat(snap.ZipPath)
	if err != nil || !info.Mode().IsRegular() {
		return sourceGenerationView{}, false, fmt.Errorf("first-copy generation archive is missing")
	}

	e.generationMu.Lock()
	if cached, ok := e.generationCache[lease.GenerationID]; ok &&
		cached.snapshot.ZipPath == snap.ZipPath &&
		cached.size == info.Size() && cached.modTime.Equal(info.ModTime()) &&
		cached.identity.TxID == lease.TxID &&
		cached.identity.Digest == lease.ContentHash &&
		cached.identity.Branch == lease.GenerationBranch {
		e.generationMu.Unlock()
		return cached, true, nil
	}
	e.generationMu.Unlock()

	gen, err := snapshot.GenerationManifest(snap.ZipPath)
	if err != nil {
		return sourceGenerationView{}, false, err
	}
	gen = snapshot.TransferableGeneration(gen)
	if gen.Digest != lease.ContentHash {
		return sourceGenerationView{}, false, fmt.Errorf("first-copy generation digest does not match its transaction")
	}
	view := sourceGenerationView{
		snapshot: snap,
		value:    gen,
		identity: syncengine.FirstCopyGeneration{
			Protocol: syncengine.FirstCopyGenerationProtocol,
			TxID:     lease.TxID, ID: lease.GenerationID,
			Digest: lease.ContentHash, Branch: lease.GenerationBranch,
		},
		size: info.Size(), modTime: info.ModTime(),
	}
	e.generationMu.Lock()
	if e.generationCache == nil {
		e.generationCache = map[string]sourceGenerationView{}
	}
	e.generationCache[lease.GenerationID] = view
	e.generationMu.Unlock()
	return view, true, nil
}
