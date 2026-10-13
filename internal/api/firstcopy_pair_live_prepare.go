package api

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *Server) handlePreparePairLive(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TxID string `json:"txId"`
	}
	if err := readJSON(r, &body); err != nil || body.TxID == "" {
		writeError(w, http.StatusBadRequest, "txId required")
		return
	}
	status, err := s.Daemon.PreparePairLive(chi.URLParam(r, "gameId"), body.TxID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}
func (s *Server) handleSendPairLiveReady(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TxID string `json:"txId"`
	}
	if err := readJSON(r, &body); err != nil || body.TxID == "" {
		writeError(w, http.StatusBadRequest, "txId required")
		return
	}
	msg, err := s.Daemon.SendPairLiveReady(r.Context(), chi.URLParam(r, "gameId"), body.TxID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "decisionId": msg.DecisionID, "nativePairSyncEnabled": false, "fenceActive": true, "autoSync": false})
}
func (s *Server) handleReadPairLivePrepare(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	row, err := s.Daemon.Store.RecordedFirstCopy(gameID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if row == nil {
		writeError(w, http.StatusNotFound, "first-copy not found")
		return
	}
	status, err := s.Daemon.ReadPairLivePrepare(gameID, row.TxID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleActivatePairLive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TxID string `json:"txId"`
	}
	if err := readJSON(r, &req); err != nil || req.TxID == "" {
		writeError(w, http.StatusBadRequest, "txId required")
		return
	}
	p, err := s.Daemon.ActivatePairLive(chi.URLParam(r, "gameId"), req.TxID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}
func (s *Server) handleSendPairLiveActive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TxID string `json:"txId"`
	}
	if err := readJSON(r, &req); err != nil || req.TxID == "" {
		writeError(w, http.StatusBadRequest, "txId required")
		return
	}
	if err := s.Daemon.SendPairLiveActive(r.Context(), chi.URLParam(r, "gameId"), req.TxID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "thirdPeerAdmitted": false, "autoSync": false})
}
