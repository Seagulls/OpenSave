package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleBeginFirstCopyPairFinalize(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		TxID string `json:"txId"`
	}
	if err := readJSON(r, &body); err != nil || body.TxID == "" {
		writeError(w, http.StatusBadRequest, "txId is required")
		return
	}
	status, err := s.Daemon.BeginFirstCopyPairFinalization(gameID, body.TxID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}
func (s *Server) handleSendFirstCopyPairFinalize(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		TxID  string `json:"txId"`
		Stage string `json:"stage"`
	}
	if err := readJSON(r, &body); err != nil || body.TxID == "" || body.Stage == "" {
		writeError(w, http.StatusBadRequest, "txId and stage required")
		return
	}
	msg, err := s.Daemon.SendFirstCopyPairFinalization(r.Context(), gameID, body.TxID, body.Stage)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "stage": msg.Stage, "decisionId": msg.DecisionID,
		"fenceActive": true, "thirdPeerAdmitted": false, "opened": false, "autoSync": false})
}
func (s *Server) handleReadFirstCopyPairFinalize(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	lease, err := s.Daemon.Store.RecordedFirstCopy(gameID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if lease == nil {
		writeError(w, http.StatusNotFound, "first-copy transaction not found")
		return
	}
	verification := r.URL.Query().Get("verifyContent")
	if verification != "" && verification != "0" && verification != "1" {
		writeError(w, http.StatusBadRequest, "verifyContent must be 0 or 1")
		return
	}
	status, err := s.Daemon.ReadFirstCopyPairFinalizationStatus(gameID, lease.TxID, verification != "0")
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}
func (s *Server) handleCancelFirstCopyPairFinalize(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		TxID string `json:"txId"`
	}
	if err := readJSON(r, &body); err != nil || body.TxID == "" {
		writeError(w, http.StatusBadRequest, "txId required")
		return
	}
	if err := s.Daemon.CancelFirstCopyPairFinalization(gameID, body.TxID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true, "fenceActive": true, "autoSync": false, "opened": false})
}
