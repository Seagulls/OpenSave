package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// handleBeginFirstCopy records an opt-in, one-peer direction for a held game.
// It does not release the hold and does not sync. Omitted, nothing here runs.
func (s *Server) handleBeginFirstCopy(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		Role       string `json:"role"`
		PeerID     string `json:"peerId"`
		TTLSeconds int    `json:"ttlSeconds"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	row, err := s.Daemon.Store.BeginFirstCopy(gameID, body.Role, body.PeerID, time.Duration(body.TTLSeconds)*time.Second)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) handleAbortFirstCopy(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		TxID string `json:"txId"`
	}
	if err := readJSON(r, &body); err != nil || body.TxID == "" {
		writeError(w, http.StatusBadRequest, "txId is required")
		return
	}
	if err := s.Daemon.Store.AbortFirstCopy(gameID, body.TxID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": gameID, "aborted": true})
}

// handleFinishFirstCopy drops the lease and then releases the hold. autoSync
// defaults to false: finishing the copy does not start ordinary bidirectional
// sync unless the caller asks.
func (s *Server) handleFinishFirstCopy(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		TxID     string `json:"txId"`
		AutoSync *bool  `json:"autoSync"`
	}
	if err := readJSON(r, &body); err != nil || body.TxID == "" {
		writeError(w, http.StatusBadRequest, "txId is required")
		return
	}
	lease, err := s.Daemon.Store.ActiveFirstCopy(gameID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if lease == nil || lease.TxID != body.TxID {
		writeError(w, http.StatusConflict, "first-copy transaction does not match")
		return
	}
	if err := s.Daemon.Store.AbortFirstCopy(gameID, body.TxID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	enable := false
	if body.AutoSync != nil {
		enable = *body.AutoSync
	}
	released, err := s.Daemon.ReleaseProvisioningMode(gameID, enable)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": gameID, "released": released, "autoSync": enable,
	})
}
