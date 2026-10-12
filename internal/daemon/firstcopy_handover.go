package daemon

import (
	"context"
	"fmt"

	"github.com/opensave/opensave/internal/store"
)

// handoverPreflight requires a stable local save and an activated direction
// fence. Neither successful preparation nor remote acknowledgement opens it.
func (d *Daemon) handoverPreflight(gameID, txID string) error {
	row, err := d.Store.BoundFirstCopy(gameID)
	if err != nil {
		return err
	}
	if row == nil || row.TxID != txID || row.Phase != store.FirstCopyActivated {
		return fmt.Errorf("handover requires the matching activated direction fence")
	}
	if d.P2P != nil && d.P2P.Pause != nil && d.P2P.Pause.Paused() {
		return fmt.Errorf("handover refused while this device is paused")
	}
	if d.P2P != nil && d.P2P.Sync != nil && d.P2P.Sync.SyncBusy(gameID) {
		return fmt.Errorf("handover refused during an active sync")
	}
	game, err := d.Store.GetGame(gameID)
	if err != nil {
		return err
	}
	digest, err := d.firstCopyDigest(game)
	if err != nil {
		return err
	}
	if digest != row.ContentHash {
		return fmt.Errorf("handover refused: local save differs from verified generation")
	}
	if game.AutoSync {
		return fmt.Errorf("handover refused while AutoSync is enabled")
	}
	return nil
}

// PrepareFirstCopyHandover records local intent after a fresh authenticated
// Phase B receipt. This has no effect on serving, watching or game settings.
func (d *Daemon) PrepareFirstCopyHandover(gameID, txID string) (store.FirstCopyHandoverPrepare, error) {
	if err := d.handoverPreflight(gameID, txID); err != nil {
		return store.FirstCopyHandoverPrepare{}, err
	}
	return d.Store.PrepareFirstCopyHandover(gameID, txID)
}

// SendFirstCopyHandoverPrepare explicitly transmits the durable intent to the
// named paired peer. No background retry, Open, or AutoSync side effect.
func (d *Daemon) SendFirstCopyHandoverPrepare(ctx context.Context, gameID, txID string) (store.FirstCopyHandoverPrepare, error) {
	if err := d.handoverPreflight(gameID, txID); err != nil {
		return store.FirstCopyHandoverPrepare{}, err
	}
	p, peerID, err := d.Store.OutboundFirstCopyHandoverPrepare(gameID, txID)
	if err != nil {
		return store.FirstCopyHandoverPrepare{}, err
	}
	if d.P2P == nil {
		return store.FirstCopyHandoverPrepare{}, fmt.Errorf("paired-peer transport unavailable")
	}
	if err := d.P2P.SendFirstCopyHandoverPrepare(ctx, peerID, p); err != nil {
		return store.FirstCopyHandoverPrepare{}, err
	}
	return p, nil
}

func (d *Daemon) ReadFirstCopyHandoverStatus(gameID, txID string) (store.FirstCopyHandoverStatus, error) {
	if err := d.handoverPreflight(gameID, txID); err != nil {
		return store.FirstCopyHandoverStatus{}, err
	}
	return d.Store.ReadFirstCopyHandoverStatus(gameID, txID)
}

func (d *Daemon) CancelFirstCopyHandover(gameID, txID string) error {
	// Cancelling is always allowed when the local fence is still activated;
	// even changed save content should not prevent de-authorizing an intent.
	return d.Store.CancelFirstCopyHandover(gameID, txID)
}
