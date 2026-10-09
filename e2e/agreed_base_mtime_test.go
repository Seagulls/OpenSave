package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

func touchOlder(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func touchNewer(t *testing.T, path string) {
	t.Helper()
	now := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
}

// The side that still holds the agreed base must not win just because its
// file was touched more recently. Covers the primary folder and one named
// extra location, through real daemons rather than Compute alone.
func TestAgreedBaseBeatsNewerMtimeOnPrimaryAndExtraRoot(t *testing.T) {
	a, b, gameID, aConfig, bConfig := twoLocationPair(t, "BaseMtime", map[string]string{
		"settings.ini": "base-config",
	})
	pauseAutoSync(t, a, b, gameID)

	writeIn(t, a.SaveDir, "save.sav", "progressed-primary")
	touchOlder(t, filepath.Join(a.SaveDir, "save.sav"))
	touchNewer(t, filepath.Join(b.SaveDir, "save.sav"))

	writeIn(t, aConfig, "settings.ini", "progressed-config")
	touchOlder(t, filepath.Join(aConfig, "settings.ini"))
	touchNewer(t, filepath.Join(bConfig, "settings.ini"))

	if !testutil.WaitFor(45*time.Second, func() bool {
		a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
		return readIn(b.SaveDir, "save.sav") == "progressed-primary" &&
			readIn(bConfig, "settings.ini") == "progressed-config" &&
			readIn(a.SaveDir, "save.sav") == "progressed-primary" &&
			readIn(aConfig, "settings.ini") == "progressed-config"
	}) {
		t.Fatalf("agreed base overwrote progress: a primary=%q config=%q b primary=%q config=%q",
			readIn(a.SaveDir, "save.sav"), readIn(aConfig, "settings.ini"),
			readIn(b.SaveDir, "save.sav"), readIn(bConfig, "settings.ini"))
	}
}
