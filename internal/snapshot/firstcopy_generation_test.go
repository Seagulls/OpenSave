package snapshot

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFirstCopyGenerationCertifiesNamedRootsAndStreamsLargeFiles(t *testing.T) {
	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	config := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(primary, "empty"), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primary, "same.sav"), []byte("PRIMARY"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "same.sav"), []byte("CONFIG"), 0o666); err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(primary, "large.sav")
	largeFile, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := largeFile.Truncate(40 << 20); err != nil {
		largeFile.Close()
		t.Fatal(err)
	}
	if err := largeFile.Close(); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "generation.zip")
	captured, gen, err := createFirstCopyArchive(primary, map[string]string{"config": config}, out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(captured) != 3 {
		t.Fatalf("captured files = %d, want 3", len(captured))
	}
	if _, ok := gen.Manifest.Files["same.sav"]; !ok {
		t.Fatal("primary file missing")
	}
	if got := gen.Manifest.Extra["config"].Files["same.sav"].Hash; got == gen.Manifest.Files["same.sav"].Hash {
		t.Fatal("identically named files from different roots collapsed")
	}
	if len(gen.Manifest.Dirs) != 1 || gen.Manifest.Dirs[0] != "empty" {
		t.Fatalf("empty directory layout = %v", gen.Manifest.Dirs)
	}
	entry := gen.Manifest.Files["large.sav"]
	last := len(entry.Blocks) - 1
	blocks, err := ReadGenerationBlocks(out, "", "large.sav", []int{last}, entry.BlockSize, entry)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Index != last || len(blocks[0].Data) > entry.BlockSize {
		t.Fatalf("large-file block response = %+v", blocks)
	}
	configEntry := gen.Manifest.Extra["config"].Files["same.sav"]
	blocks, err = ReadGenerationBlocks(out, "config", "same.sav", []int{0}, configEntry.BlockSize, configEntry)
	if err != nil || string(blocks[0].Data) != "CONFIG" {
		t.Fatalf("named-root blocks = %q, %v", blocks[0].Data, err)
	}
}

func TestFirstCopyGenerationRejectsMutationAcrossCaptureWindow(t *testing.T) {
	root := t.TempDir()
	primary := filepath.Join(root, "save")
	if err := os.MkdirAll(primary, 0o777); err != nil {
		t.Fatal(err)
	}
	save := filepath.Join(primary, "slot.sav")
	if err := os.WriteFile(save, []byte("BEFORE"), 0o666); err != nil {
		t.Fatal(err)
	}
	_, _, err := createFirstCopyArchive(primary, nil, filepath.Join(root, "generation.zip"), func() {
		if writeErr := os.WriteFile(save, []byte("AFTER"), 0o666); writeErr != nil {
			t.Fatal(writeErr)
		}
	})
	if err == nil || !strings.Contains(err.Error(), "changed during capture") {
		t.Fatalf("mutation certification error = %v", err)
	}
}

func TestFirstCopyGenerationRejectsMissingRootsAndUnsafeEntries(t *testing.T) {
	root := t.TempDir()
	primary := filepath.Join(root, "save")
	if err := os.MkdirAll(primary, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primary, "slot.sav"), []byte("SAVE"), 0o666); err != nil {
		t.Fatal(err)
	}
	if _, _, err := createFirstCopyArchive(primary, map[string]string{"missing": filepath.Join(root, "absent")}, filepath.Join(root, "missing.zip"), nil); err == nil {
		t.Fatal("missing named root was certified")
	}
	link := filepath.Join(primary, "outside")
	if err := os.Symlink(filepath.Join(root, "elsewhere"), link); err == nil {
		if _, _, err := createFirstCopyArchive(primary, nil, filepath.Join(root, "link.zip"), nil); err == nil {
			t.Fatal("symlink source entry was certified")
		}
	}

	for _, tc := range []struct {
		name    string
		entries []string
	}{
		{name: "traversal", entries: []string{"../escape.sav"}},
		{name: "duplicate", entries: []string{"slot.sav", "slot.sav"}},
		{name: "bad-root", entries: []string{RootPrefix + "../slot.sav"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			zipPath := filepath.Join(t.TempDir(), "bad.zip")
			f, err := os.Create(zipPath)
			if err != nil {
				t.Fatal(err)
			}
			w := zip.NewWriter(f)
			for _, name := range tc.entries {
				entry, err := w.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := entry.Write([]byte("x")); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := GenerationManifest(zipPath); err == nil {
				t.Fatalf("unsafe archive %v was certified", tc.entries)
			}
		})
	}
}

func TestFirstCopyGenerationFailsClosedOnCorruptionAndInvalidBlocks(t *testing.T) {
	root := t.TempDir()
	primary := filepath.Join(root, "save")
	if err := os.MkdirAll(primary, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primary, "slot.sav"), []byte("0123456789"), 0o666); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "generation.zip")
	_, gen, err := createFirstCopyArchive(primary, nil, out, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry := gen.Manifest.Files["slot.sav"]
	for _, indices := range [][]int{{-1}, {len(entry.Blocks)}, {0, 0}} {
		if _, err := ReadGenerationBlocks(out, "", "slot.sav", indices, entry.BlockSize, entry); err == nil {
			t.Fatalf("invalid indices %v were accepted", indices)
		}
	}
	if _, err := ReadGenerationBlocks(out, "", "slot.sav", []int{0}, entry.BlockSize+1, entry); err == nil {
		t.Fatal("wrong block size was accepted")
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(out, info.Size()/2); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerationManifest(out); err == nil {
		t.Fatal("truncated generation was certified")
	}
	if _, err := GenerationManifest(filepath.Join(root, "missing.zip")); err == nil {
		t.Fatal("missing generation was certified")
	}
}
