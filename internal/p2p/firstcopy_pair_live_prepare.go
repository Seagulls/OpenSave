package p2p

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/opensave/opensave/internal/p2p/syncengine"
	"github.com/opensave/opensave/internal/store"
	"io"
	"net/http"
	"time"
)

func (e *Engine) acceptPairLiveReady(gameID, peerID, nonce string, msg store.PairLiveReadyMessage) error {
	if peerID == "" || nonce == "" {
		return fmt.Errorf("pair-live requires authenticated peer and nonce")
	}
	if e.Pause != nil && e.Pause.Paused() {
		return fmt.Errorf("pair-live preparation refused while paused")
	}
	if msg.Kind != "active" {
		if err := e.localReceiptDigestMatches(gameID); err != nil {
			return err
		}
	}
	if err := e.Store.AcceptPairLiveReady(gameID, peerID, nonce, msg); err != nil {
		return err
	}
	if msg.Kind == "active" {
		if e.OnPairLiveActivated != nil {
			e.OnPairLiveActivated(gameID)
		}
		// The permission check in SyncGame remains game-scoped and fail closed.
		e.GoSync(func(ctx context.Context) {
			if _, err := e.SyncGame(ctx, gameID); err != nil {
				e.Log("info", fmt.Sprintf("pair-live reconciling %s: %v", gameID, err))
			}
		})
	}
	return nil
}
func (e *Engine) handlePairLiveReady(w http.ResponseWriter, r *http.Request) {
	peer := signedLANPeerID(r)
	if peer == "" {
		jsonError(w, http.StatusUnauthorized, "authenticated peer required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var m store.PairLiveReadyMessage
	if err := dec.Decode(&m); err != nil {
		jsonError(w, http.StatusBadRequest, "malformed readiness")
		return
	}
	var extra any
	if err := dec.Decode(&extra); err == nil || !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "extra readiness data")
		return
	}
	if err := e.acceptPairLiveReady(chi.URLParam(r, "gameId"), peer, r.Header.Get(lanAuthNonceHeader), m); err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	jsonOK(w, map[string]any{"accepted": true, "nativePairSyncEnabled": false, "autoSync": false, "fenceActive": true})
}
func (e *Engine) SendPairLiveReady(ctx context.Context, peerID string, msg store.PairLiveReadyMessage) error {
	if e.Pause != nil && e.Pause.Paused() {
		return fmt.Errorf("pair-live readiness refused while paused")
	}
	if msg.Kind != "active" {
		if err := e.localReceiptDigestMatches(msg.GameID); err != nil {
			return err
		}
	}
	guard, err := e.Store.PairGuardForServe(msg.GameID)
	if err != nil {
		return err
	}
	if guard == nil || guard.PeerID != peerID || guard.DecisionID != msg.DecisionID || guard.Role != msg.Role || guard.Phase != store.FinalizePairFinalized {
		return fmt.Errorf("pair-live readiness guard mismatch")
	}
	peer, err := e.Store.GetPeer(peerID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if peer.Address == "relay" {
		if e.Wan == nil {
			return fmt.Errorf("relay unavailable")
		}
		_, err = e.Wan.Request(ctx, peerID, "/first-copy/pair-live/ready/"+msg.GameID, "POST", msg)
		return err
	}
	lan := &lanTransport{engine: e}
	counterpart := syncengine.Peer{ID: peer.ID, Name: peer.Name, Address: peer.Address, Port: peer.Port}
	return lan.postJSON(ctx, counterpart, peerURL(counterpart, "/first-copy/pair-live/ready/"+msg.GameID), msg, nil)
}
