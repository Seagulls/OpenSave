package daemon

import (
	"context"
	"fmt"

	"github.com/opensave/opensave/internal/store"
)

// Phase C.2 decision functions never remove the first-copy fence. A durable
// decision is a recovery record, NOT authority to call the Phase B Open route.
func (d *Daemon) OfferFirstCopyDecision(gameID, txID string) (store.FirstCopyDecisionMessage, error) {
	if err := d.handoverPreflight(gameID, txID); err != nil {
		return store.FirstCopyDecisionMessage{}, err
	}
	return d.Store.OfferFirstCopyDecision(gameID, txID)
}

func (d *Daemon) AuthorizeFirstCopyDecision(gameID, txID, id string) error {
	if err := d.handoverPreflight(gameID, txID); err != nil {
		return err
	}
	return d.Store.AuthorizeFirstCopyDecision(gameID, txID, id)
}

func (d *Daemon) SendFirstCopyDecision(ctx context.Context, gameID, txID, stage string) (store.FirstCopyDecisionMessage, error) {
	if err := d.handoverPreflight(gameID, txID); err != nil {
		return store.FirstCopyDecisionMessage{}, err
	}
	msg, peer, err := d.Store.OutboundFirstCopyDecision(gameID, txID, stage)
	if err != nil {
		return store.FirstCopyDecisionMessage{}, err
	}
	if d.P2P == nil {
		return store.FirstCopyDecisionMessage{}, fmt.Errorf("paired transport unavailable")
	}
	if err := d.P2P.SendFirstCopyDecision(ctx, peer, msg); err != nil {
		return store.FirstCopyDecisionMessage{}, err
	}
	return msg, nil
}

func (d *Daemon) CancelFirstCopyDecision(gameID, txID string) error {
	// Cancellation must remain available even if save content changed or an
	// earlier operator receipt expired. The SQLite fence itself is checked.
	return d.Store.CancelFirstCopyDecision(gameID, txID)
}

func (d *Daemon) ReadFirstCopyDecisionStatus(gameID, txID string) (store.FirstCopyDecisionStatus, error) {
	return d.Store.ReadFirstCopyDecisionStatus(gameID, txID)
}
