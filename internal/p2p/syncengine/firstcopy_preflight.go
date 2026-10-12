package syncengine

import (
	"context"
	"fmt"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/store"
)

// preflightFirstCopyTarget is intentionally separate from normal sync. It
// examines all existing target paths BEFORE branch alignment or any writes.
// A missing/mismatched root is never silently treated as an empty save.
func (e *Engine) preflightFirstCopyTarget(ctx context.Context, gameID string, game store.Game, peer Peer, remote ManifestResponse) error {
	fence, fenced := firstCopyFenceFrom(ctx)
	if !fenced {
		return nil
	}
	if fence.Role != store.FirstCopyTarget || fence.PeerID != peer.ID {
		return fmt.Errorf("%w: unexpected first-copy peer or direction", ErrFirstCopyDirection)
	}
	lease, err := e.Store.ActiveFirstCopy(gameID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProvisioningUnreadable, err)
	}
	if lease == nil || lease.TxID != fence.TxID || lease.Role != fence.Role || lease.PeerID != peer.ID || lease.Phase != store.FirstCopyCopying {
		return fmt.Errorf("%w: first-copy lease changed or is no longer copying", ErrFirstCopyDirection)
	}
	if remote.ActiveBranch != "" && remote.ActiveBranch != game.ActiveBranch {
		return fmt.Errorf("first copy refused: branch mismatch (%q / %q)", game.ActiveBranch, remote.ActiveBranch)
	}
	primary, err := e.ReadManifest(ctx, gameID, game.SavePath)
	if err != nil {
		return fmt.Errorf("first-copy preflight primary: %w", err)
	}
	if err := firstCopyTreesCompatible(primary, remote.Manifest); err != nil {
		return fmt.Errorf("first-copy preflight primary: %w", err)
	}
	localRoots, err := e.Store.GameRootPaths(gameID)
	if err != nil {
		return fmt.Errorf("first-copy preflight roots: %w", err)
	}
	if len(localRoots) != len(remote.Manifest.Extra) || (len(localRoots) != 0 && remote.Proto < ProtoMultiRoot) {
		return fmt.Errorf("first copy refused: save location sets differ between source and target")
	}
	for name, path := range localRoots {
		root, ok := remote.Manifest.Extra[name]
		if !ok {
			return fmt.Errorf("first copy refused: source has no save location %q", name)
		}
		local, err := e.ReadManifest(ctx, gameID, path)
		if err != nil {
			return fmt.Errorf("first-copy preflight %q: %w", name, err)
		}
		if err := firstCopyTreesCompatible(local, delta.Manifest{Files: root.Files, Dirs: root.Dirs}); err != nil {
			return fmt.Errorf("first-copy preflight %q: %w", name, err)
		}
	}
	return nil
}
