package syncengine

import (
	"testing"

	"github.com/opensave/opensave/internal/delta"
)

// An absent agreed hash must not turn two distinct saves into a silent
// overwrite merely because both filesystem mtimes round below the last
// recorded sync millisecond.
func TestDetectConflict_NoBaseBothBeforeLastSyncedFailsClosed(t *testing.T) {
	save := func(hash string, mtime int64) delta.Manifest {
		return manifest(mtime, map[string]delta.FileEntry{
			"settings.ini": fileEntry(hash, mtime),
		})
	}
	check := func(name string, local, remote delta.Manifest, lastSynced int64, want bool) {
		t.Run(name, func(t *testing.T) {
			if got := DetectConflict(local, remote, lastSynced, ""); got != want {
				t.Fatalf("DetectConflict = %v, want %v; local=%d remote=%d lastSynced=%d",
					got, want, local.LatestMtime, remote.LatestMtime, lastSynced)
			}
		})
	}
	check("both edits one millisecond before sync", save("A", 1349), save("B", 1349), 1350, true)
	check("both edits exactly at sync", save("A", 1350), save("B", 1350), 1350, true)
	check("different older copies are ambiguous", save("A", 800), save("B", 900), 1350, true)
	check("identical bytes are never a conflict", save("A", 1349), save("A", 1349), 1350, false)
	check("remote changed, local clearly older", save("A", 800), save("B", 1400), 1350, false)
	check("local changed, remote clearly older", save("A", 1400), save("B", 800), 1350, false)
	check("both changed after sync", save("A", 1400), save("B", 1401), 1350, true)
	check("previously synced, empty local still uses deletion policy", manifest(0, nil), save("B", 900), 1350, false)
	check("never synced with empty local", manifest(0, nil), save("B", 1349), 0, false)
}
