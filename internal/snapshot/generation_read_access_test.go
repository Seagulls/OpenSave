package snapshot

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestReadGenerationBlocksSelectedRanges(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "generation.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	entry, err := w.Create("large.sav")
	if err != nil {
		t.Fatal(err)
	}
	// Large enough to have many delta blocks, but small enough for a
	// quick, hermetic unit test. No live save directories are touched.
	contents := bytes.Repeat([]byte("0123456789abcdef"), 256*1024)
	if _, err := entry.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	certified, err := GenerationManifest(archive)
	if err != nil {
		t.Fatal(err)
	}
	file := certified.Manifest.Files["large.sav"]
	if len(file.Blocks) < 2 {
		t.Fatalf("expected multiple blocks, got %d", len(file.Blocks))
	}
	for _, index := range []int{0, len(file.Blocks) / 2, len(file.Blocks) - 1} {
		got, err := ReadGenerationBlocks(archive, "", "large.sav", []int{index}, file.BlockSize, file)
		if err != nil {
			t.Fatalf("block %d: %v", index, err)
		}
		if len(got) != 1 || got[0].Index != index || !bytes.Equal(got[0].Data, contents[index*file.BlockSize:index*file.BlockSize+file.Blocks[index].Length]) {
			t.Fatalf("block %d changed", index)
		}
	}
	if _, err := ReadGenerationBlocks(archive, "", "large.sav", []int{len(file.Blocks)}, file.BlockSize, file); err == nil {
		t.Fatal("out-of-range block accepted")
	}
}
