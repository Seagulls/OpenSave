package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/opensave/opensave/internal/daemon"
	"github.com/opensave/opensave/internal/p2p/syncengine"
	"github.com/opensave/opensave/internal/snapshot"
	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/internal/sysintegration"
)

// routes registers the Phase 1 endpoint surface. Peer/cloud/p2p routes
// attach in Phases 2-3; window-control and dialog routes attach with the
// Wails app in Phase 4.
func (s *Server) routes(r chi.Router) {
	r.Get("/api/status", s.handleStatus)
	r.Get("/api/settings", s.handleGetSettings)
	r.Post("/api/settings", s.handleUpdateSettings)

	r.Post("/api/watch/reload", s.handleReloadWatchers)

	r.Get("/api/games", s.handleListGames)
	r.Post("/api/games", s.handleTrackGame)
	r.Get("/api/suggest-name", s.handleSuggestName)
	r.Post("/api/games/untrack-bulk", s.handleBulkUntrack)

	// Games a peer syncs that this device has no folder for. Only ever
	// populated when the "ask before tracking" setting is on.
	r.Get("/api/offered-games", s.handleListOfferedGames)
	r.Post("/api/offered-games/{gameId}/place", s.handlePlaceOfferedGame)
	r.Post("/api/offered-games/{gameId}/decline", s.handleDeclineOfferedGame)
	r.Patch("/api/games/{gameId}", s.handleUpdateGame)
	r.Delete("/api/games/{gameId}", s.handleUntrackGame)

	r.Get("/api/games/{gameId}/aliases", s.handleListAliases)
	r.Post("/api/games/{gameId}/link", s.handleLinkGame)
	r.Get("/api/games/{gameId}/roots", s.handleListGameRoots)
	r.Post("/api/games/{gameId}/roots", s.handleAddGameRoot)
	r.Delete("/api/games/{gameId}/roots/{root}", s.handleRemoveGameRoot)
	r.Delete("/api/games/{gameId}/alias/{aliasId}", s.handleUnlinkGame)

	r.Post("/api/games/{gameId}/snapshot", s.handleCreateSnapshot)
	r.Post("/api/games/{gameId}/rollback", s.handleRollback)
	r.Get("/api/games/{gameId}/save-files", s.handleGameSaveFiles)
	r.Get("/api/games/{gameId}/snapshot/{snapshotId}/files", s.handleSnapshotFiles)
	r.Post("/api/games/{gameId}/snapshot/{snapshotId}/restore-file", s.handleRestoreFile)
	r.Delete("/api/games/{gameId}/snapshot/{snapshotId}", s.handleDeleteSnapshot)
	r.Patch("/api/games/{gameId}/snapshot/{snapshotId}", s.handleEditSnapshot)
	r.Get("/api/games/{gameId}/sessions", s.handleGameSessions)
	r.Get("/api/snapshots/check", s.handleSnapshotChecks)
	r.Get("/api/games/{gameId}/snapshot/{snapshotId}/compare/{otherId}", s.handleCompareSnapshots)
	r.Post("/api/snapshots/check", s.handleVerifySnapshots)
	r.Post("/api/games/{gameId}/session", s.handleMarkSession)
	r.Get("/api/games/{gameId}/snapshot/{snapshotId}/preview", s.handlePreviewRestore)

	r.Post("/api/games/{gameId}/branch", s.handleCreateBranch)
	r.Post("/api/games/{gameId}/branch/switch", s.handleSwitchBranch)
	r.Delete("/api/games/{gameId}/branch/{branch}", s.handleDeleteBranch)
	r.Post("/api/games/{gameId}/launch", s.handleLaunchGame)

	r.Post("/api/backup/export", s.handleBackupExport)
	r.Post("/api/backup/restore", s.handleBackupRestore)

	r.Post("/api/snapshots/prune", s.handlePruneSnapshots)
	r.Post("/api/snapshots/all", s.handleSnapshotAll)
	r.Get("/api/storage", s.handleStorage)
	r.Post("/api/storage/compact", s.handleCompact)
	r.Get("/api/activity", s.handleActivity)
	r.Post("/api/snapshots/repair", s.handleRepairSnapshots)
	r.Post("/api/snapshots/forget-damaged", s.handleForgetDamaged)
	r.Get("/api/emptied", s.handleEmptiedList)
	r.Post("/api/games/{gameId}/emptied", s.handleEmptiedAnswer)

	r.Get("/api/collections", s.handleListCollections)
	r.Post("/api/collections", s.handleCreateCollection)
	r.Patch("/api/collections/{id}", s.handleRenameCollection)
	r.Delete("/api/collections/{id}", s.handleDeleteCollection)
	r.Post("/api/collections/{id}/games", s.handleSetInCollection)

	r.Get("/api/transfers", s.handleTransfers)
	r.Get("/api/sync/pause", s.handleSyncPauseStatus)
	r.Post("/api/sync/pause", s.handleSyncPause)
	r.Post("/api/sync/resume", s.handleSyncResume)

	r.Get("/api/presets/scan", s.handlePresetScan)
	r.Post("/api/presets/new/dismiss", s.handleNewGamesDismiss)
	r.Get("/api/cover", s.handleCover)
	r.Get("/api/steam/app", s.handleSteamApp)

	s.peerRoutes(r)
	s.cloudRoutes(r)

	r.Get("/ws", s.Hub.ServeHTTP)
}

// ... rest of file with the RewatchGame change at the re-watch site
