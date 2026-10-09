package syncengine

import (
	"testing"

	"github.com/opensave/opensave/internal/delta"
)

// A save that progressed after the agreed base must not be overwritten by a
// byte-for-byte copy of that base whose file was touched or restored later.
func TestBaseFirstPreventsMtimeRollback(t *testing.T) {
	old := delta.Manifest{Files: map[string]delta.FileEntry{
		"profile.sav": {Hash: "same", MtimeMs: 3000},
		"slot.sav":    {Hash: "older-progress", MtimeMs: 3000},
	}}
	newer := delta.Manifest{Files: map[string]delta.FileEntry{
		"profile.sav": {Hash: "same", MtimeMs: 1000},
		"slot.sav":    {Hash: "newer-progress", MtimeMs: 1000},
	}}
	base := old.ManifestHash()
	lineage := map[string]struct{}{"profile.sav": {}, "slot.sav": {}}
	tests := []struct {
		name               string
		local, remote      delta.Manifest
		wantPush, wantPull bool
	}{
		{"local changed, remote base has later mtime", newer, old, true, false},
		{"remote changed, local base has later mtime", old, newer, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if DetectConflict(tc.local, tc.remote, 0, base) {
				t.Fatal("one-sided post-base save update must not be considered a conflict")
			}
			d := ComputeWithBase(tc.local, tc.remote, lineage, nil, base)
			gotPush := len(d.FilesToPush) == 1 && d.FilesToPush[0] == "slot.sav"
			gotPull := len(d.FilesToPull) == 1 && d.FilesToPull[0] == "slot.sav"
			if gotPush != tc.wantPush || gotPull != tc.wantPull || len(d.FilesToDeleteLocally) != 0 || len(d.FilesToDeleteOnPeer) != 0 {
				t.Fatalf("decision = %+v, want push=%v pull=%v and no deletions", d, tc.wantPush, tc.wantPull)
			}
		})
	}
}

func TestBaseFirstRetainsLegacyMtimeWithoutBase(t *testing.T) {
	old := delta.Manifest{Files: map[string]delta.FileEntry{
		"slot.sav": {Hash: "old", MtimeMs: 3000},
	}}
	newer := delta.Manifest{Files: map[string]delta.FileEntry{
		"slot.sav": {Hash: "new", MtimeMs: 1000},
	}}
	d := ComputeWithBase(newer, old, map[string]struct{}{"slot.sav": {}}, nil, "")
	if len(d.FilesToPull) != 1 || d.FilesToPull[0] != "slot.sav" {
		t.Fatalf("without an agreed base the newer mtime still wins: %+v", d)
	}
}

func TestBaseFirstNoChangeForIdenticalContents(t *testing.T) {
	left := delta.Manifest{Files: map[string]delta.FileEntry{
		"slot.sav": {Hash: "same", MtimeMs: 1},
	}}
	right := delta.Manifest{Files: map[string]delta.FileEntry{
		"slot.sav": {Hash: "same", MtimeMs: 9999},
	}}
	d := ComputeWithBase(left, right, map[string]struct{}{"slot.sav": {}}, nil, left.ManifestHash())
	if d.HasChanges() {
		t.Fatalf("identical contents must not transfer: %+v", d)
	}
}

func TestBaseFirstBothChangedStillConflict(t *testing.T) {
	base := delta.Manifest{Files: map[string]delta.FileEntry{
		"slot.sav": {Hash: "old", MtimeMs: 100},
	}}
	left := delta.Manifest{Files: map[string]delta.FileEntry{
		"slot.sav": {Hash: "local-new", MtimeMs: 1000},
	}}
	right := delta.Manifest{Files: map[string]delta.FileEntry{
		"slot.sav": {Hash: "remote-new", MtimeMs: 2000},
	}}
	if !DetectConflict(left, right, 0, base.ManifestHash()) {
		t.Fatal("both sides changed relative to base: must require conflict resolution")
	}
}

// A base that matches neither side is not evidence. Mtime remains the only
// ordering this function will use, and that can still be wrong. This pins the
// limit rather than inventing a winner.
func TestStaleAgreedBaseDoesNotOverrideMtime(t *testing.T) {
	local := tieManifest("LOCAL", 2000)
	remote := tieManifest("REMOTE", 1000)
	d := ComputeWithBase(local, remote, map[string]struct{}{"save.dat": {}}, nil, "stale-base-neither-side-holds")
	if len(d.FilesToPush) != 1 || len(d.FilesToPull) != 0 {
		t.Fatalf("a stale base changed the mtime decision: %+v", d)
	}
}
