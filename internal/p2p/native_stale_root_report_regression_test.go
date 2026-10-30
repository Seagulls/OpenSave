package p2p

import (
	"path/filepath"
	"slices"
	"testing"
)

// Independent regression for a delayed native sync-complete report.
// The existing p2p raceFixture delivers an authentic peer-context event to
// the native HTTP handler, with synthetic root paths and SQLite state.
// Expected on the unmodified dev-public baseline: FAIL (stale lineage stored).
// Expected after the targeted receiver-owned mapping generation fix: PASS.
func TestRegressionDelayedReportCannotAttachToReaddedRoot(t *testing.T) {
	refreshInLine(t, false)
	f := newRaceFixture(t, true)

	if err := f.s.RemoveGameRoot(f.gameID, "config"); err != nil {
		t.Fatal(err)
	}
	readded := filepath.Join(t.TempDir(), "new-config")
	if err := f.s.AddGameRoot(f.gameID, "config", readded); err != nil {
		t.Fatal(err)
	}

	// A delayed event from the previous mapping knows only the root name.
	// A signed/paired peer identity alone does not bind this to the new root.
	f.reportOverLAN(t, map[string]any{
		"root": "config",
		"pulledFiles": []string{"old-save.bin"},
	})
	if files := f.lineage(t, "config"); slices.Contains(files, "old-save.bin") {
		t.Fatalf("old root report contaminated new root lineage: %v", files)
	}
}
