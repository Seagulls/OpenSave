package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// Independent regression for a native RemoveRoot/active-writer race.
// The test uses only temporary roots and the existing native HTTP test server.
// Expected on the unmodified dev-public baseline: FAIL (RemoveRoot returns 200).
// Expected after the targeted topology admission fix: PASS (409, no mutation).
func TestRegressionRemoveRootRefusesActiveNativeWriter(t *testing.T) {
	ts := startTestServer(t)
	id := trackProbeGame(t, ts)
	root := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "slot.sav")
	if err := os.WriteFile(file, []byte("ORIGINAL"), 0600); err != nil {
		t.Fatal(err)
	}
	resp, body := ts.do(t, http.MethodPost, "/api/games/"+id+"/roots",
		map[string]string{"name": "config", "path": root})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initial AddRoot: HTTP %d, %v", resp.StatusCode, body)
	}

	// This admission gate models the lifetime of an already admitted native
	// writer. The final actual file transfer is outside this focused witness.
	release := ts.daemon.P2P.Sync.Writing(id)
	resp, body = ts.do(t, http.MethodDelete, "/api/games/"+id+"/roots/config", nil)
	paths, err := ts.daemon.Store.GameRootPaths(id)
	if resp.StatusCode != http.StatusConflict || err != nil || paths["config"] != root {
		release()
		t.Fatalf("RemoveRoot during admitted write: HTTP=%d body=%v paths=%v err=%v; want 409 and unchanged mapping",
			resp.StatusCode, body, paths, err)
	}
	release()

	resp, body = ts.do(t, http.MethodDelete, "/api/games/"+id+"/roots/config", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("RemoveRoot after write completes: HTTP=%d body=%v", resp.StatusCode, body)
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != "ORIGINAL" {
		t.Fatalf("RemoveRoot touched physical save data: bytes=%q err=%v", got, err)
	}
}
