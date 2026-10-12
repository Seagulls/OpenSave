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

// Signed peer identity and nonce are extracted from the authenticated LAN
// headers or WAN envelope. A claimed identity inside JSON is never trusted.
func (e *Engine) acceptFirstCopyFinalize(gameID, peerID, nonce string, msg store.FirstCopyFinalizeMessage) error {
	if peerID == "" {
		return fmt.Errorf("pairing-key-authenticated peer required")
	}
	if msg.Stage != store.FinalizeAbort {
		if e.Pause != nil && e.Pause.Paused() {
			return fmt.Errorf("pair finalization refused while paused")
		}
		if err := e.localReceiptDigestMatches(gameID); err != nil {
			return err
		}
	}
	return e.Store.AcceptFirstCopyFinalizeMessage(gameID, peerID, nonce, msg)
}

func (e *Engine) handleFirstCopyFinalize(w http.ResponseWriter, r *http.Request) {
	peerID := signedLANPeerID(r)
	if peerID == "" {
		jsonError(w, http.StatusUnauthorized, "signed paired-peer finalization required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var msg store.FirstCopyFinalizeMessage
	if err := dec.Decode(&msg); err != nil {
		jsonError(w, http.StatusBadRequest, "malformed pair finalization")
		return
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		jsonError(w, http.StatusBadRequest, "trailing JSON forbidden")
		return
	} else if !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "invalid trailing JSON")
		return
	}
	if err := e.acceptFirstCopyFinalize(chi.URLParam(r, "gameId"), peerID, r.Header.Get(lanAuthNonceHeader), msg); err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	jsonOK(w, map[string]any{"accepted": true, "openAuthorized": false, "autoSync": false, "fenceActive": true})
}

func (e *Engine) SendFirstCopyFinalize(ctx context.Context, peerID string, msg store.FirstCopyFinalizeMessage) error {
	if msg.Stage != store.FinalizeAbort {
		if e.Pause != nil && e.Pause.Paused() {
			return fmt.Errorf("pair finalization cannot send while paused")
		}
		if err := e.localReceiptDigestMatches(msg.GameID); err != nil {
			return err
		}
	}
	row, err := e.Store.PairGuardForServe(msg.GameID)
	if err != nil {
		return err
	}
	if row == nil || row.PeerID != peerID || row.DecisionID != msg.DecisionID || row.Role != msg.Role {
		return fmt.Errorf("local pair guard not bound to recipient")
	}
	peer, err := e.Store.GetPeer(peerID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if peer.Address == "relay" {
		if e.Wan == nil {
			return fmt.Errorf("relay transport unavailable")
		}
		_, err = e.Wan.Request(ctx, peerID, "/first-copy/handover/finalize/"+msg.GameID, "POST", msg)
		return err
	}
	lan := &lanTransport{engine: e}
	counterpart := syncengine.Peer{ID: peer.ID, Name: peer.Name, Address: peer.Address, Port: peer.Port}
	return lan.postJSON(ctx, counterpart, peerURL(counterpart, "/first-copy/handover/finalize/"+msg.GameID), msg, nil)
}
