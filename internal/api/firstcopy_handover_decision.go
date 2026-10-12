package api

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *Server) handleOfferFirstCopyDecision(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var in struct {
		TxID string `json:"txId"`
	}
	if err := readJSON(r, &in); err != nil || in.TxID == "" {
		writeError(w, http.StatusBadRequest, "txId required")
		return
	}
	out, err := s.Daemon.OfferFirstCopyDecision(gameID, in.TxID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"offer": out, "fenceActive": true, "autoSync": false, "opened": false})
}

func (s *Server) handleAuthorizeFirstCopyDecision(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var in struct {
		TxID       string `json:"txId"`
		DecisionID string `json:"decisionId"`
	}
	if err := readJSON(r, &in); err != nil || in.TxID == "" || in.DecisionID == "" {
		writeError(w, http.StatusBadRequest, "txId and decisionId required")
		return
	}
	if err := s.Daemon.AuthorizeFirstCopyDecision(gameID, in.TxID, in.DecisionID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recorded": true, "fenceActive": true, "autoSync": false, "opened": false})
}

func (s *Server) handleSendFirstCopyDecision(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var in struct {
		TxID  string `json:"txId"`
		Stage string `json:"stage"`
	}
	if err := readJSON(r, &in); err != nil || in.TxID == "" || in.Stage == "" {
		writeError(w, http.StatusBadRequest, "txId and stage required")
		return
	}
	out, err := s.Daemon.SendFirstCopyDecision(r.Context(), gameID, in.TxID, in.Stage)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "decision": out, "fenceActive": true, "autoSync": false, "opened": false})
}

func (s *Server) handleReadFirstCopyDecision(w http.ResponseWriter, r *http.Request) {
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
	status, err := s.Daemon.ReadFirstCopyDecisionStatus(gameID, lease.TxID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleCancelFirstCopyDecision(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var in struct {
		TxID string `json:"txId"`
	}
	if err := readJSON(r, &in); err != nil || in.TxID == "" {
		writeError(w, http.StatusBadRequest, "txId required")
		return
	}
	if err := s.Daemon.CancelFirstCopyDecision(gameID, in.TxID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true, "fenceActive": true, "autoSync": false, "opened": false})
}
