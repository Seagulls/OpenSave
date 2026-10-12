package daemon

import (
	"context"
	"fmt"

	"github.com/opensave/opensave/internal/store"
)

// SendFirstCopyReceipt explicitly transmits this device's verified claim
// across the pairing-authenticated P2P transport. Sending never releases a
// hold, removes a fence, or starts syncing. The caller cannot select the
// peer, digest, source generation or other device's transaction ID.
func (d *Daemon) SendFirstCopyReceipt(ctx context.Context, gameID, txID string) (store.FirstCopyReceipt, error) {
	row, peerID, err := d.Store.OutboundFirstCopyReceipt(gameID, txID)
	if err != nil {
		return store.FirstCopyReceipt{}, err
	}
	game, err := d.Store.GetGame(gameID)
	if err != nil {
		return store.FirstCopyReceipt{}, err
	}
	if d.P2P != nil && d.P2P.Sync != nil && d.P2P.Sync.SyncBusy(gameID) {
		return store.FirstCopyReceipt{}, fmt.Errorf("first-copy sync is still in progress")
	}
	digest, err := d.firstCopyDigest(game)
	if err != nil {
		return store.FirstCopyReceipt{}, err
	}
	if digest != row.Digest {
		return store.FirstCopyReceipt{}, fmt.Errorf("first-copy save changed after activation")
	}
	if d.P2P == nil {
		return store.FirstCopyReceipt{}, fmt.Errorf("first-copy P2P transport unavailable")
	}
	if err := d.P2P.SendFirstCopyReceipt(ctx, peerID, row); err != nil {
		return store.FirstCopyReceipt{}, err
	}
	return row, nil
}
