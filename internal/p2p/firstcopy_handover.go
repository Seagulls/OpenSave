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

func (e *Engine) acceptFirstCopyHandoverPrepare(gameID, peerID, authenticatedNonce string, p store.FirstCopyHandoverPrepare) error {
	if peerID == "" {
		return fmt.Errorf("authenticated pairing key required for handover preparation")
	}
	if e.Pause != nil && e.Pause.Paused() {
		return fmt.Errorf("handover refused while this device is paused")
	}
	if err := e.localReceiptDigestMatches(gameID); err != nil {
		return err
	}
	return e.Store.AcceptFirstCopyHandoverPrepare(gameID, peerID, authenticatedNonce, p)
}

func (e *Engine) handleFirstCopyHandoverPrepare(w http.ResponseWriter, r *http.Request) {
	signedPeer := signedLANPeerID(r)
	if signedPeer == "" {
		jsonError(w, http.StatusUnauthorized, "handover prepare requires a signed paired-peer request")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	var p store.FirstCopyHandoverPrepare
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		jsonError(w, http.StatusBadRequest, "malformed first-copy handover prepare")
		return
	}
	// Reject trailing JSON values as well as oversized request bodies.
	var extra any
	if err := dec.Decode(&extra); err == nil {
		jsonError(w, http.StatusBadRequest, "trailing handover prepare content")
		return
	} else if !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "invalid handover prepare trailing data")
		return
	}
	if err := e.acceptFirstCopyHandoverPrepare(chi.URLParam(r, "gameId"), signedPeer, r.Header.Get(lanAuthNonceHeader), p); err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	jsonOK(w, map[string]any{"accepted": true, "opened": false, "autoSync": false, "fenceActive": true})
}

func (e *Engine) SendFirstCopyHandoverPrepare(ctx context.Context, peerID string, p store.FirstCopyHandoverPrepare) error {
	if err := e.localReceiptDigestMatches(p.GameID); err != nil {
		return err
	}
	row, err := e.Store.BoundFirstCopy(p.GameID)
	if err != nil {
		return err
	}
	if row == nil || row.Phase != store.FirstCopyActivated || row.PeerID != peerID || row.Role != p.Role {
		return fmt.Errorf("handover sender is not bound to the named peer")
	}
	peer, err := e.Store.GetPeer(peerID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if peer.Address == "relay" {
		if e.Wan == nil {
			return fmt.Errorf("handover WAN transport unavailable")
		}
		_, err = e.Wan.Request(ctx, peerID, "/first-copy/handover/prepare/"+p.GameID, "POST", p)
		return err
	}
	lan := &lanTransport{engine: e}
	counterpart := syncengine.Peer{ID: peer.ID, Name: peer.Name, Address: peer.Address, Port: peer.Port}
	return lan.postJSON(ctx, counterpart, peerURL(counterpart, "/first-copy/handover/prepare/"+p.GameID), p, nil)
}
