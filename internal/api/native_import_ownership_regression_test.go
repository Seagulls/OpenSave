package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// Independent portable-backup regression using native HTTP export/import.
// Stock dev-public allows an untracked overwrite import to modify another
// tracked game's primary, named, or enclosing named save root.
// Every path and file is generated under t.TempDir().
func TestRegressionUntrackedImportCannotOverwriteOwnedRoot(t *testing.T) {
	for _, tc := range []struct{ name, kind string }{
		{"exact-primary", "primary"},
		{"exact-named", "named"},
		{"inside-named", "nested"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := startTestServer(t)
			root := filepath.Join(t.TempDir(), "owned-root")
			target := root
			if tc.kind == "nested" {
				target = filepath.Join(root, "nested")
			}
			if err := os.MkdirAll(target, 0700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(target, "slot.sav")
			if err := os.WriteFile(file, []byte("ARCHIVED-OLD"), 0600); err != nil {
				t.Fatal(err)
			}
			archive := exportV2(t, ts, []map[string]string{{
				"id": "incoming-untracked", "name": "Incoming Untracked", "savePath": target,
			}})

			ownerPrimary := ts.saveDir
			if tc.kind == "primary" {
				ownerPrimary = root
			} else if err := os.WriteFile(filepath.Join(ownerPrimary, "primary.sav"), []byte("PRIMARY"), 0600); err != nil {
				t.Fatal(err)
			}
			resp, body := ts.do(t, http.MethodPost, "/api/games", map[string]string{
				"name": "Existing Owner", "savePath": ownerPrimary,
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("track owner: HTTP=%d body=%v", resp.StatusCode, body)
			}
			waitInitialSnapshot(t, ts, "existing-owner")
			if tc.kind != "primary" {
				resp, body = ts.do(t, http.MethodPost, "/api/games/existing-owner/roots",
					map[string]string{"name": "config", "path": root})
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("map owner's config: HTTP=%d body=%v", resp.StatusCode, body)
				}
			}

			if err := os.WriteFile(file, []byte("OWNER-LIVE"), 0600); err != nil {
				t.Fatal(err)
			}
			resp, body = ts.do(t, http.MethodPost, "/api/backup/restore",
				map[string]string{"sourcePath": archive, "mode": "overwrite"})
			after, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("live save absent after import (HTTP=%d body=%v): %v", resp.StatusCode, body, err)
			}
			if string(after) != "OWNER-LIVE" {
				t.Fatalf("import wrote into another game's owned root: HTTP=%d body=%v live=%q", resp.StatusCode, body, after)
			}
			if _, err := ts.daemon.Store.GetGame("incoming-untracked"); err == nil {
				t.Fatal("conflicting untracked backup became a second tracked owner")
			}
		})
	}
}
