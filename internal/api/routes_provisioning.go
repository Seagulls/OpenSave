package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/opensave/opensave/internal/store"
)

// handleReleaseProvisioning ends the hold on one game. It does not sync.
// SyncGame would contact every online peer. The caller syncs after releasing
// only the peers that should converge. A repeat after the hold is gone is a
// success and does not sync.
func (s *Server) handleReleaseProvisioning(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	released, err := s.Daemon.ReleaseProvisioning(gameID)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, map[string]any{
		"id":              gameID,
		"released":        released,
		"alreadyReleased": !released,
	})
}
