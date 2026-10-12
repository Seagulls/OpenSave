package p2p

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/opensave/opensave/internal/p2p/syncengine"
	"github.com/opensave/opensave/internal/store"
)

// Signed requests are the sole source of peer authority and request nonce.
// A local HTTP request cannot forge the counterpart's decision.
func (e *Engine) acceptFirstCopyDecision(gameID, peerID, nonce string, msg store.FirstCopyDecisionMessage) error {
	if peerID == "" {
		return fmt.Errorf("pairing-key authenticated decision required")
	}
	if e.Pause != nil && e.Pause.Paused() {
		return fmt.Errorf("device is paused")
	}
	if err := e.localReceiptDigestMatches(gameID); err != nil {
		return err
	}
	return e.Store.AcceptFirstCopyDecision(gameID, peerID, nonce, msg)
}

func (e *Engine) handleFirstCopyDecision(w http.ResponseWriter, r *http.Request) {
	peer := signedLANPeerID(r)
	if peer == "" {
		jsonError(w, http.StatusUnauthorized, "signed paired peer required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var msg store.FirstCopyDecisionMessage
	if err := dec.Decode(&msg); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid handover decision")
		return
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		jsonError(w, http.StatusBadRequest, "unexpected trailing data")
		return
	} else if !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "invalid trailing data")
		return
	}
	if err := e.acceptFirstCopyDecision(chi.URLParam(r, "gameId"), peer, r.Header.Get(lanAuthNonceHeader), msg); err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	jsonOK(w, map[string]any{"accepted": true, "fenceActive": true, "autoSync": false, "opened": false})
}

func (e *Engine) SendFirstCopyDecision(ctx context.Context, peerID string, msg store.FirstCopyDecisionMessage) error {
	if e.Pause != nil && e.Pause.Paused() {
		return fmt.Errorf("device is paused")
	}
	if err := e.localReceiptDigestMatches(msg.GameID); err != nil {
		return err
	}
	row, err := e.Store.BoundFirstCopy(msg.GameID)
	if err != nil {
		return err
	}
	if row == nil || row.Phase != store.FirstCopyActivated || row.PeerID != peerID || row.Role != msg.Role {
		return fmt.Errorf("decision sender is not bound to the named peer")
	}
	peer, err := e.Store.GetPeer(peerID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if peer.Address == "relay" {
		if e.Wan == nil {
			return fmt.Errorf("WAN transport unavailable")
		}
		_, err = e.Wan.Request(ctx, peerID, "/first-copy/handover/decision/"+msg.GameID, "POST", msg)
		return err
	}
	lan := &lanTransport{engine: e}
	counterpart := syncengine.Peer{ID: peer.ID, Name: peer.Name, Address: peer.Address, Port: peer.Port}
	return lan.postJSON(ctx, counterpart, peerURL(counterpart, "/first-copy/handover/decision/"+msg.GameID), msg, nil)
}
