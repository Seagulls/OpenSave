package syncengine

import (
	"context"
	"fmt"
)

type pairLiveEpochKey struct{}

func WithPairLiveEpoch(ctx context.Context, epoch string) context.Context {
	if epoch == "" {
		return ctx
	}
	return context.WithValue(ctx, pairLiveEpochKey{}, epoch)
}
func PairLiveEpoch(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(pairLiveEpochKey{}).(string)
	return s
}

// PairLiveEventData binds a state-changing completion report to the exact
// live-pair decision that produced it. Progress and legacy events remain
// unchanged. The receiver independently checks the signing peer and epoch.
func PairLiveEventData(ctx context.Context, data map[string]any) map[string]any {
	if epoch := PairLiveEpoch(ctx); epoch != "" {
		data["pairLiveEpoch"] = epoch
	}
	return data
}

// Every metadata writer goes through a decision-scoped SQLite transaction.
// Old callbacks and legacy methods never obtain this token.
func (e *Engine) setAgreedHash(ctx context.Context, gameID, peerID, hash string) error {
	if epoch := PairLiveEpoch(ctx); epoch != "" {
		return e.Store.PairLiveWrite(gameID, peerID, epoch, "agreed", "", hash, nil, nil)
	}
	return e.Store.SetAgreedHash(gameID, peerID, hash)
}
func (e *Engine) setPushedHash(ctx context.Context, gameID, peerID, hash string) error {
	if epoch := PairLiveEpoch(ctx); epoch != "" {
		return e.Store.PairLiveWrite(gameID, peerID, epoch, "pushed", "", hash, nil, nil)
	}
	return e.Store.SetPushedHash(gameID, peerID, hash)
}
func (e *Engine) setAgreedHashForRoot(ctx context.Context, gameID, peerID, root, hash string) error {
	if epoch := PairLiveEpoch(ctx); epoch != "" {
		return e.Store.PairLiveWrite(gameID, peerID, epoch, "agreed", root, hash, nil, nil)
	}
	return e.Store.SetAgreedHashForRoot(gameID, peerID, root, hash)
}
func (e *Engine) setPushedHashForRoot(ctx context.Context, gameID, peerID, root, hash string) error {
	if epoch := PairLiveEpoch(ctx); epoch != "" {
		return e.Store.PairLiveWrite(gameID, peerID, epoch, "pushed", root, hash, nil, nil)
	}
	return e.Store.SetPushedHashForRoot(gameID, peerID, root, hash)
}
func (e *Engine) setLineage(ctx context.Context, gameID, peerID, root string, files, dirs []string) error {
	if epoch := PairLiveEpoch(ctx); epoch != "" {
		return e.Store.PairLiveWrite(gameID, peerID, epoch, "lineage", root, "", files, dirs)
	}
	return e.Store.SetSyncStateForRoot(gameID, peerID, root, files, dirs)
}
func (e *Engine) setSynced(ctx context.Context, gameID, peerID, stamp string) error {
	if epoch := PairLiveEpoch(ctx); epoch != "" {
		return e.Store.PairLiveWrite(gameID, peerID, epoch, "synced", "", stamp, nil, nil)
	}
	return e.Store.RecordGamePeerSynced(gameID, peerID, stamp)
}
func (e *Engine) requirePairLiveEpoch(ctx context.Context, gameID, peerID string) error {
	epoch := PairLiveEpoch(ctx)
	if epoch == "" {
		return fmt.Errorf("no signed native pair-live epoch")
	}
	return e.Store.PairLiveValidateIncoming(gameID, peerID, epoch)
}
