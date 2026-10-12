package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

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
	row, err := s.Daemon.BeginFirstCopy(gameID, body.Role, body.PeerID, time.Duration(body.TTLSeconds)*time.Second)
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
	writeJSON(w, http.StatusOK, map[string]any{"id": gameID, "aborted": true, "held": true})
}

// handleFinishFirstCopy verifies the digest and marks the lease verified.
// It does not release the hold and does not turn AutoSync on. A third peer
// is still refused. Activation is a separate call.
func (s *Server) handleFinishFirstCopy(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		TxID       string `json:"txId"`
		ExpectHash string `json:"expectHash"`
	}
	if err := readJSON(r, &body); err != nil || body.TxID == "" {
		writeError(w, http.StatusBadRequest, "txId is required")
		return
	}
	row, err := s.Daemon.FinishFirstCopy(gameID, body.TxID, body.ExpectHash)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": gameID, "phase": row.Phase, "contentHash": row.ContentHash,
		"released": false, "autoSync": false, "localOnly": true,
	})
}

// handleActivateFirstCopy lifts the hold on this device only, after finish.
// autoSync defaults to false. The other peer is not activated by this call.
func (s *Server) handleActivateFirstCopy(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		TxID     string `json:"txId"`
		AutoSync *bool  `json:"autoSync"`
	}
	if err := readJSON(r, &body); err != nil || body.TxID == "" {
		writeError(w, http.StatusBadRequest, "txId is required")
		return
	}
	enable := false
	if body.AutoSync != nil {
		enable = *body.AutoSync
	}
	released, err := s.Daemon.ActivateFirstCopy(gameID, body.TxID, enable)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": gameID, "released": released, "autoSync": enable,
		"localOnly": true, "directionHeld": true,
	})
}

// handleOpenFirstCopy expands this game's access to other paired peers.
// The caller must already have independently confirmed BOTH devices are
// ready; a local digest check is not remote attestation. AutoSync stays off.
func (s *Server) handleOpenFirstCopy(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		TxID       string `json:"txId"`
		ExpectHash string `json:"expectHash"`
	}
	if err := readJSON(r, &body); err != nil || body.TxID == "" || body.ExpectHash == "" {
		writeError(w, http.StatusBadRequest, "txId and expectHash are required")
		return
	}
	if err := s.Daemon.OpenFirstCopy(gameID, body.TxID, body.ExpectHash); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": gameID, "opened": true})
}

func (s *Server) handleFirstCopyCapability(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"firstCopy": "1"})
}
