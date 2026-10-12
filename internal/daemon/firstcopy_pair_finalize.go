package daemon

import (
	"context"
	"fmt"

	"github.com/opensave/opensave/internal/store"
)

// Phase C.3 does not expand ordinary sync/serving permissions. Each operator
// explicitly begins a pair-only finalization; each signed message is explicit.
func (d *Daemon) BeginFirstCopyPairFinalization(gameID, txID string) (store.FirstCopyFinalizationStatus, error) {
	if err := d.handoverPreflight(gameID, txID); err != nil {
		return store.FirstCopyFinalizationStatus{}, err
	}
	return d.Store.BeginFirstCopyPairFinalization(gameID, txID)
}

func (d *Daemon) SendFirstCopyPairFinalization(ctx context.Context, gameID, txID, stage string) (store.FirstCopyFinalizeMessage, error) {
	if stage != store.FinalizeAbort {
		if err := d.handoverPreflight(gameID, txID); err != nil {
			return store.FirstCopyFinalizeMessage{}, err
		}
	}
	msg, peer, err := d.Store.StageFirstCopyFinalizeSend(gameID, txID, stage)
	if err != nil {
		return store.FirstCopyFinalizeMessage{}, err
	}
	if d.P2P == nil {
		return store.FirstCopyFinalizeMessage{}, fmt.Errorf("paired transport is unavailable; durable state retained for recovery")
	}
	if err := d.P2P.SendFirstCopyFinalize(ctx, peer, msg); err != nil {
		return store.FirstCopyFinalizeMessage{}, err
	}
	return msg, nil
}

// Status remains inspectable even after a local save mutation, timeout,
// pause, or transport outage. A red status is never permission to Open.
func (d *Daemon) ReadFirstCopyPairFinalization(gameID, txID string) (store.FirstCopyFinalizationStatus, error) {
	return d.ReadFirstCopyPairFinalizationStatus(gameID, txID, true)
}

// Lightweight journal reads do NOT claim that the current live save matches.
// The existing full preflight remains mandatory for every permission-changing
// operator action and for default status requests.
func (d *Daemon) ReadFirstCopyPairFinalizationStatus(gameID, txID string, verifyContent bool) (store.FirstCopyFinalizationStatus, error) {
	status, err := d.Store.ReadFirstCopyPairFinalization(gameID, txID)
	if err != nil {
		return status, err
	}
	status.ContentChecked = verifyContent
	if !verifyContent {
		return status, nil
	}
	if err := d.handoverPreflight(gameID, txID); err != nil {
		status.ContentValid = false
		status.RecoveryRequired = true
	} else {
		status.ContentValid = true
	}
	return status, nil
}

func (d *Daemon) CancelFirstCopyPairFinalization(gameID, txID string) error {
	// Local veto must remain possible even when save content changed.
	return d.Store.CancelFirstCopyPairFinalization(gameID, txID)
}
