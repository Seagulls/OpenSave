package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/opensave/opensave/internal/p2p/syncengine"
	"github.com/opensave/opensave/internal/snapshot"
	"github.com/opensave/opensave/internal/store"
)

// lanSignedPeerKey is deliberately separate from lanPeerKey: that other key
// also accepts a legacy peer identified solely by IP address, which MUST NOT
// authorize a completion receipt, including over loopback in two-daemon tests.
type lanSignedPeerKey struct{}

func signedLANPeerID(r *http.Request) string {
	id, _ := r.Context().Value(lanSignedPeerKey{}).(string)
	return id
}

// localReceiptDigestMatches protects both receipt acceptance and sending
// against a changed save after Finish/Activate. It does not replace the
// independent verification required immediately before explicit Open.
func (e *Engine) localReceiptDigestMatches(gameID string) error {
	lease, err := e.Store.BoundFirstCopy(gameID)
	if err != nil {
		return err
	}
	if lease == nil || lease.Phase != store.FirstCopyActivated {
		return fmt.Errorf("first-copy receipt requires activated direction fence")
	}
	if e.Sync != nil && e.Sync.SyncBusy(gameID) {
		return fmt.Errorf("first-copy save is currently being written")
	}
	game, err := e.Store.GetGame(gameID)
	if err != nil {
		return err
	}
	roots, err := e.Store.GameRootPaths(gameID)
	if err != nil {
		return err
	}
	digest, err := snapshot.FirstCopySourceDigest(game.SavePath, roots)
	if err != nil {
		return err
	}
	if digest != lease.ContentHash {
		return fmt.Errorf("first-copy content changed after activation")
	}
	return nil
}

func (e *Engine) acceptFirstCopyReceipt(gameID, signedPeer, authenticatedNonce string, receipt store.FirstCopyReceipt) error {
	if signedPeer == "" {
		return fmt.Errorf("authenticated pairing key required for first-copy receipts")
	}
	if err := e.localReceiptDigestMatches(gameID); err != nil {
		return err
	}
	return e.Store.AcceptFirstCopyRemoteReceipt(gameID, signedPeer, authenticatedNonce, receipt)
}

// handleFirstCopyRemoteReceipt requires a signed request. Middleware checks
// the MAC + nonce, and stores the proven identity in lanSignedPeerKey. No
// unverified LAN address, loopback request, or legacy pairing is accepted.
func (e *Engine) handleFirstCopyRemoteReceipt(w http.ResponseWriter, r *http.Request) {
	peerID := signedLANPeerID(r)
	if peerID == "" {
		jsonError(w, http.StatusUnauthorized, "first-copy receipt requires a signed paired-peer request")
		return
	}
	var receipt store.FirstCopyReceipt
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&receipt); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid first-copy receipt")
		return
	}
	if err := e.acceptFirstCopyReceipt(chi.URLParam(r, "gameId"), peerID, r.Header.Get(lanAuthNonceHeader), receipt); err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	jsonOK(w, map[string]any{"accepted": true, "opened": false, "autoSync": false})
}

// SendFirstCopyReceipt sends only an already locally verified, durable
// receipt. Both the LAN request and the relay request are signed by the
// existing pairing-key protocol. Never interpret an HTTP acknowledgement as
// evidence of a different peer's saved content; incoming signed receipts
// alone populate the persistent receipt table.
func (e *Engine) SendFirstCopyReceipt(ctx context.Context, peerID string, receipt store.FirstCopyReceipt) error {
	if err := e.localReceiptDigestMatches(receipt.GameID); err != nil {
		return err
	}
	row, err := e.Store.BoundFirstCopy(receipt.GameID)
	if err != nil {
		return err
	}
	if row == nil || row.PeerID != peerID || row.Role != receipt.Role || row.Phase != store.FirstCopyActivated {
		return fmt.Errorf("first-copy receipt sender is not bound to the requested peer")
	}
	peer, err := e.Store.GetPeer(peerID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if peer.Address == "relay" {
		if e.Wan == nil {
			return fmt.Errorf("first-copy WAN receipt transport unavailable")
		}
		_, err = e.Wan.Request(ctx, peerID, "/first-copy/receipt/"+receipt.GameID, "POST", receipt)
		return err
	}
	lan := &lanTransport{engine: e}
	counterpart := syncengine.Peer{ID: peer.ID, Name: peer.Name, Address: peer.Address, Port: peer.Port}
	return lan.postJSON(ctx, counterpart, peerURL(counterpart, "/first-copy/receipt/"+receipt.GameID), receipt, nil)
}
