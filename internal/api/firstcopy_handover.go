package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// The dashboard/CLI initiate each step locally. These endpoints are NOT P2P
// endpoints. The signed peer preparation is a separate authenticated route.
func (s *Server) handlePrepareFirstCopyHandover(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		TxID string `json:"txId"`
	}
	if err := readJSON(r, &body); err != nil || body.TxID == "" {
		writeError(w, http.StatusBadRequest, "txId is required")
		return
	}
	prepare, err := s.Daemon.PrepareFirstCopyHandover(gameID, body.TxID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"prepared": true, "handover": prepare, "fenceActive": true,
		"autoSync": false, "opened": false,
	})
}

func (s *Server) handleSendFirstCopyHandover(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		TxID string `json:"txId"`
	}
	if err := readJSON(r, &body); err != nil || body.TxID == "" {
		writeError(w, http.StatusBadRequest, "txId is required")
		return
	}
	prepare, err := s.Daemon.SendFirstCopyHandoverPrepare(r.Context(), gameID, body.TxID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sent": true, "handover": prepare,
		"fenceActive": true, "autoSync": false, "opened": false,
	})
}

func (s *Server) handleReadFirstCopyHandover(w http.ResponseWriter, r *http.Request) {
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
	status, err := s.Daemon.ReadFirstCopyHandoverStatus(gameID, lease.TxID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleCancelFirstCopyHandover(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		TxID string `json:"txId"`
	}
	if err := readJSON(r, &body); err != nil || body.TxID == "" {
		writeError(w, http.StatusBadRequest, "txId is required")
		return
	}
	if err := s.Daemon.CancelFirstCopyHandover(gameID, body.TxID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cancelled": true, "fenceActive": true, "autoSync": false, "opened": false,
	})
}
