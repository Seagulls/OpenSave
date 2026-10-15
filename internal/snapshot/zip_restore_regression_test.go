package snapshot

import (
    "archive/zip"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

// Disposable RED regression tests for ZIP restore atomicity.
// Uses setup/writeSave from the project's existing snapshot_test.go.
func regressionWriteZIP(t *testing.T, path string, names []string) {
    t.Helper()
    out, err := os.Create(path)
    if err != nil { t.Fatal(err) }
    w := zip.NewWriter(out)
    for _, name := range names {
        file, err := w.Create(name)
        if err != nil { t.Fatal(err) }
        if !strings.HasSuffix(name, "/") {
            if _, err := file.Write([]byte("synthetic test bytes")); err != nil { t.Fatal(err) }
        }
    }
    if err := w.Close(); err != nil { t.Fatal(err) }
    if err := out.Close(); err != nil { t.Fatal(err) }
}

func TestRegressionRestoreValidZIPTopologyErrorPreservesSave(t *testing.T) {
    env := setup(t)
    writeSave(t, env.saveDir, "important.sav", "CURRENT-ONLY")
    snap, err := env.mgr.Create("game1", "baseline", false)
    if err != nil { t.Fatal(err) }
    // CRC-valid ZIP but impossible topology: block is both a file and a parent directory.
    regressionWriteZIP(t, snap.ZipPath, []string{"block", "block/child.sav"})
    if err := VerifyArchive(snap.ZipPath); err != nil { t.Fatalf("CRC check unexpectedly failed: %v", err) }
    _, restoreErr := env.mgr.Restore("game1", snap.ID)
    if restoreErr == nil { t.Error("expected extraction error for file/directory collision") }
    got, readErr := os.ReadFile(filepath.Join(env.saveDir, "important.sav"))
    if readErr != nil || string(got) != "CURRENT-ONLY" {
        t.Errorf("restore error left live save changed: content=%q readErr=%v", got, readErr)
    }
}

func TestRegressionUnzipExtraRootErrorPreservesBothRoots(t *testing.T) {
    primary := t.TempDir()
    extra := t.TempDir()
    writeSave(t, primary, "important.sav", "LOCAL-PRIMARY")
    writeSave(t, extra, "config.ini", "LOCAL-EXTRA")
    archive := filepath.Join(t.TempDir(), "crafted.zip")
    regressionWriteZIP(t, archive, []string{
        "archive.sav", RootPrefix + "config/", RootPrefix + "config/block", RootPrefix + "config/block/child.ini",
    })
    if err := VerifyArchive(archive); err != nil { t.Fatalf("CRC check unexpectedly failed: %v", err) }
    if _, err := UnzipRoots(archive, primary, map[string]string{"config": extra}); err == nil {
        t.Error("expected extraction error for additional-root file/directory collision")
    }
    if got, err := os.ReadFile(filepath.Join(primary, "important.sav")); err != nil || string(got) != "LOCAL-PRIMARY" {
        t.Errorf("primary save was altered: content=%q err=%v", got, err)
    }
    if got, err := os.ReadFile(filepath.Join(extra, "config.ini")); err != nil || string(got) != "LOCAL-EXTRA" {
        t.Errorf("additional root was altered: content=%q err=%v", got, err)
    }
}
