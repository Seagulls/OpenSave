package api

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

// Explicit local-user exchange. Both devices must have already finished and
// activated with AutoSync off; target sends first, source sends second.
func (s *Server) handleSendFirstCopyReceipt(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		TxID string `json:"txId"`
	}
	if err := readJSON(r, &body); err != nil || body.TxID == "" {
		writeError(w, http.StatusBadRequest, "txId is required")
		return
	}
	receipt, err := s.Daemon.SendFirstCopyReceipt(r.Context(), gameID, body.TxID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sent": true, "role": receipt.Role, "sourceTxId": receipt.SourceTxID,
		"targetTxId": receipt.TargetTxID, "generationId": receipt.GenerationID,
		"digest": receipt.Digest, "opened": false, "autoSync": false,
	})
}

func (s *Server) handleReadFirstCopyReceipt(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	lease, err := s.Daemon.Store.RecordedFirstCopy(gameID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "first-copy state unreadable")
		return
	}
	if lease == nil {
		writeError(w, http.StatusNotFound, "no first-copy transaction")
		return
	}
	remote, err := s.Daemon.Store.RemoteFirstCopyReceipt(gameID, lease.TxID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "receipt state unreadable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"txId": lease.TxID, "role": lease.Role, "phase": lease.Phase,
		"peerId": lease.PeerID, "remoteReceipt": remote,
		"peerReceiptVerified": remote != nil, "autoSync": false,
	})
}
