package snapshot

import (
    "os"
    "path/filepath"
    "testing"
)

// Disposable RED regression tests for checkout preflight.
// Uses setup/writeSave from the project's existing snapshot_test.go.
func TestRegressionBranchSwitchMissingArchivePreservesSave(t *testing.T) {
    env := setup(t)
    writeSave(t, env.saveDir, "slot.sav", "INITIAL")
    target, err := env.mgr.CreateBranch("game1", "other", true)
    if err != nil { t.Fatal(err) }
    snaps, err := env.store.ListSnapshots("game1", target)
    if err != nil || len(snaps) == 0 { t.Fatalf("target snapshots: %v", err) }

    writeSave(t, env.saveDir, "slot.sav", "CURRENT-ONLY")
    if err := os.Remove(snaps[0].ZipPath); err != nil { t.Fatal(err) }
    err = env.mgr.SwitchBranch("game1", target)
    if err == nil { t.Error("checkout incorrectly returned success for missing target ZIP") }
    got, readErr := os.ReadFile(filepath.Join(env.saveDir, "slot.sav"))
    if readErr != nil || string(got) != "CURRENT-ONLY" {
        t.Errorf("active save changed after failed checkout: content=%q readErr=%v", got, readErr)
    }
    game, err := env.store.GetGame("game1")
    if err != nil { t.Fatal(err) }
    if game.ActiveBranch != "main" {
        t.Errorf("active branch changed despite failed checkout: %q", game.ActiveBranch)
    }
}

func TestRegressionBranchSwitchCorruptArchivePreservesSave(t *testing.T) {
    env := setup(t)
    writeSave(t, env.saveDir, "slot.sav", "INITIAL")
    target, err := env.mgr.CreateBranch("game1", "other", true)
    if err != nil { t.Fatal(err) }
    snaps, err := env.store.ListSnapshots("game1", target)
    if err != nil || len(snaps) == 0 { t.Fatalf("target snapshots: %v", err) }

    if err := os.WriteFile(snaps[0].ZipPath, []byte("not a ZIP archive"), 0600); err != nil { t.Fatal(err) }
    writeSave(t, env.saveDir, "slot.sav", "CURRENT-ONLY")
    err = env.mgr.SwitchBranch("game1", target)
    if err == nil { t.Error("checkout incorrectly returned success for corrupt target ZIP") }
    got, readErr := os.ReadFile(filepath.Join(env.saveDir, "slot.sav"))
    if readErr != nil || string(got) != "CURRENT-ONLY" {
        t.Errorf("active save changed after failed checkout: content=%q readErr=%v", got, readErr)
    }
    game, err := env.store.GetGame("game1")
    if err != nil { t.Fatal(err) }
    if game.ActiveBranch != "main" {
        t.Errorf("active branch changed despite failed checkout: %q", game.ActiveBranch)
    }
}
