package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Enabling AutoSync through the game update route must watch named additional
// save locations, not only the primary path. A change confined to an extra
// location has to produce the normal watcher → snapshot flow.
//
// Regression for #88.
func TestEnablingAutoSyncWatchesAdditionalLocations(t *testing.T) {
	ts := startTestServer(t)
	gameID := ts.mustTrack(t)

	// Primary already watched (AutoSync defaults on). Turn it off so the next
	// enable is the activation path under test.
	resp, _ := ts.do(t, http.MethodPatch, "/api/games/"+gameID,
		map[string]any{"autoSync": false})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disable AutoSync: %d", resp.StatusCode)
	}

	extra := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(extra, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extra, "graphics.ini"), []byte("res=1080p\n"), 0o666); err != nil {
		t.Fatal(err)
	}

	resp, _ = ts.do(t, http.MethodPost, "/api/games/"+gameID+"/roots",
		map[string]any{"name": "config", "path": extra})
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("add root: %d", resp.StatusCode)
	}

	// Baseline: no snapshot yet from the extra location (or take one if the
	// add itself triggered something; we care about the post-enable change).
	before, _ := ts.daemon.Store.ListSnapshots(gameID, "main")
	beforeN := len(before)

	// Enable AutoSync — this is the call site that used to call Watch with
	// only the primary path.
	resp, _ = ts.do(t, http.MethodPatch, "/api/games/"+gameID,
		map[string]any{"autoSync": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enable AutoSync: %d", resp.StatusCode)
	}

	// Give the re-watch a moment to register the folders.
	time.Sleep(200 * time.Millisecond)

	// Modify only the additional location.
	if err := os.WriteFile(filepath.Join(extra, "graphics.ini"), []byte("res=1440p\n"), 0o666); err != nil {
		t.Fatal(err)
	}

	// The watcher should notice and take an automatic snapshot.
	deadline := time.Now().Add(5 * time.Second)
	var after int
	for time.Now().Before(deadline) {
		snaps, _ := ts.daemon.Store.ListSnapshots(gameID, "main")
		after = len(snaps)
		if after > beforeN {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if after <= beforeN {
		t.Fatalf("change under additional location produced no snapshot (had %d, still %d); "+
			"enabling AutoSync must watch named roots", beforeN, after)
	}
}
