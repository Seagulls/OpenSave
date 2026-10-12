package snapshot

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"path"
	"sort"
	"strings"

	"github.com/opensave/opensave/internal/delta"
)

// Generation is the complete, immutable view certified from one first-copy
// archive. Manifest contains the primary location and every named location;
// Digest binds both the content and the root layout.
type Generation struct {
	Manifest delta.Manifest
	Digest   string
}

// GenerationManifest reads and validates a generation archive in full. ZIP
// CRCs, canonical paths, duplicate entries, root names, file sizes and block
// hashes are all checked while bytes are streamed through bounded buffers.
func GenerationManifest(zipPath string) (Generation, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return Generation{}, err
	}
	defer r.Close()

	manifest := delta.Manifest{Files: map[string]delta.FileEntry{}}
	rootFiles := map[string]map[string]delta.FileEntry{}
	rootDirs := map[string]map[string]bool{"": {}}
	seen := map[string]string{}

	for _, f := range r.File {
		root, rel, isDir, err := parseGenerationEntry(f.Name, f.FileInfo().IsDir())
		if err != nil {
			return Generation{}, err
		}
		key := root + "\x00" + rel
		kind := "file"
		if isDir {
			kind = "directory"
		}
		if old, ok := seen[key]; ok {
			return Generation{}, fmt.Errorf("generation contains duplicate %s %q (also %s)", kind, f.Name, old)
		}
		seen[key] = f.Name

		if rootDirs[root] == nil {
			rootDirs[root] = map[string]bool{}
		}
		if isDir {
			if rel != "" {
				rootDirs[root][rel] = true
			}
			continue
		}
		entry, err := hashGenerationEntry(f)
		if err != nil {
			return Generation{}, fmt.Errorf("generation entry %q: %w", f.Name, err)
		}
		if root == "" {
			manifest.Files[rel] = entry
			continue
		}
		if rootFiles[root] == nil {
			rootFiles[root] = map[string]delta.FileEntry{}
		}
		rootFiles[root][rel] = entry
	}

	manifest.Dirs = sortedDirSet(rootDirs[""])
	for root, dirs := range rootDirs {
		if root == "" {
			continue
		}
		if manifest.Extra == nil {
			manifest.Extra = map[string]delta.RootManifest{}
		}
		files := rootFiles[root]
		if files == nil {
			files = map[string]delta.FileEntry{}
		}
		manifest.Extra[root] = delta.RootManifest{Files: files, Dirs: sortedDirSet(dirs)}
	}
	for root, files := range rootFiles {
		if _, ok := manifest.Extra[root]; ok {
			continue
		}
		if manifest.Extra == nil {
			manifest.Extra = map[string]delta.RootManifest{}
		}
		manifest.Extra[root] = delta.RootManifest{Files: files}
	}

	return Generation{Manifest: manifest, Digest: GenerationManifestDigest(manifest)}, nil
}

// PrimaryManifest is retained for callers outside first copy. First-copy
// serving uses GenerationManifest so named roots cannot disappear.
func PrimaryManifest(zipPath string) (delta.Manifest, error) {
	gen, err := GenerationManifest(zipPath)
	if err != nil {
		return delta.Manifest{}, err
	}
	primary := gen.Manifest
	primary.Extra = nil
	return primary, nil
}

// GenerationDigest returns the canonical primary-plus-named-root digest.
func GenerationDigest(zipPath string) (string, error) {
	gen, err := GenerationManifest(zipPath)
	return gen.Digest, err
}

// GenerationManifestDigest binds content, empty directories, root names and
// root layout. Root names are deliberately included even when two roots hold
// identical files.
func GenerationManifestDigest(m delta.Manifest) string {
	parts := make([]string, 0, len(m.Extra)+1)
	parts = append(parts, "primary:"+m.ManifestHash())
	names := make([]string, 0, len(m.Extra))
	for name := range m.Extra {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		parts = append(parts, name+":"+m.RootHash(name))
	}
	return strings.Join(parts, "\n")
}

func sortedDirSet(in map[string]bool) []string {
	out := make([]string, 0, len(in))
	for name := range in {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func parseGenerationEntry(name string, headerDir bool) (root, rel string, isDir bool, err error) {
	if name == "" || strings.ContainsRune(name, '\x00') || strings.Contains(name, "\\") {
		return "", "", false, fmt.Errorf("generation contains invalid archive path %q", name)
	}
	if strings.HasPrefix(name, "./") {
		name = strings.TrimPrefix(name, "./")
	}
	isDir = headerDir || strings.HasSuffix(name, "/")
	cleanInput := strings.TrimSuffix(name, "/")
	if cleanInput == "" || path.IsAbs(cleanInput) || path.Clean(cleanInput) != cleanInput {
		return "", "", false, fmt.Errorf("generation contains unsafe archive path %q", name)
	}

	if strings.HasPrefix(cleanInput, RootPrefix) {
		rest := strings.TrimPrefix(cleanInput, RootPrefix)
		root, rel, _ = strings.Cut(rest, "/")
		if !validGenerationRoot(root) {
			return "", "", false, fmt.Errorf("generation contains invalid root in %q", name)
		}
		if rel == "" && !isDir {
			return "", "", false, fmt.Errorf("generation root marker %q is not a directory", name)
		}
	} else {
		rel = cleanInput
	}
	if rel != "" && (path.IsAbs(rel) || path.Clean(rel) != rel || rel == "." || strings.HasPrefix(rel, "../")) {
		return "", "", false, fmt.Errorf("generation contains unsafe archive path %q", name)
	}
	return root, rel, isDir, nil
}

func validGenerationRoot(root string) bool {
	return root != "" && root != "." && root != ".." &&
		!strings.ContainsAny(root, "/\\\x00")
}

func hashGenerationEntry(f *zip.File) (delta.FileEntry, error) {
	if f.UncompressedSize64 > math.MaxInt64 {
		return delta.FileEntry{}, fmt.Errorf("declared size exceeds supported file size")
	}
	rc, err := f.Open()
	if err != nil {
		return delta.FileEntry{}, err
	}
	defer rc.Close()

	blockSize := delta.BlockSizeFor(int64(f.UncompressedSize64))
	buf := make([]byte, blockSize)
	whole := sha256.New()
	var blocks []delta.Block
	var total uint64
	for index := 0; ; index++ {
		n, readErr := io.ReadFull(rc, buf)
		if n > 0 {
			chunk := buf[:n]
			whole.Write(chunk)
			sum := sha256.Sum256(chunk)
			blocks = append(blocks, delta.Block{Index: index, Hash: hex.EncodeToString(sum[:]), Length: n})
			total += uint64(n)
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return delta.FileEntry{}, readErr
		}
	}
	if total != f.UncompressedSize64 {
		return delta.FileEntry{}, fmt.Errorf("size is %d bytes, archive declares %d", total, f.UncompressedSize64)
	}
	if total == 0 {
		sum := sha256.Sum256(nil)
		blocks = []delta.Block{{Index: 0, Hash: hex.EncodeToString(sum[:]), Length: 0}}
	}
	return delta.FileEntry{
		Size: int64(total), Hash: hex.EncodeToString(whole.Sum(nil)), Blocks: blocks,
		BlockSize: blockSize, MtimeMs: delta.Milli(f.Modified.UnixMilli()),
	}, nil
}

// ReadGenerationBlocks reads only the requested blocks from one certified
// archive entry. Memory is bounded by the requested batch rather than the
// file or archive size, and every returned block is checked against the
// manifest certified by GenerationManifest.
func ReadGenerationBlocks(zipPath, root, rel string, indices []int, blockSize int, expected delta.FileEntry) ([]delta.BlockSource, error) {
	if root != "" && !validGenerationRoot(root) {
		return nil, fmt.Errorf("invalid generation root")
	}
	if rel == "" || path.IsAbs(rel) || path.Clean(rel) != rel || strings.Contains(rel, "\\") {
		return nil, fmt.Errorf("invalid generation path")
	}
	if blockSize <= 0 {
		blockSize = expected.BlockSize
	}
	if blockSize != expected.BlockSize || blockSize != delta.BlockSizeFor(expected.Size) {
		return nil, fmt.Errorf("invalid generation block size")
	}

	want := rel
	if root != "" {
		want = RootPrefix + root + "/" + rel
	}
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var found *zip.File
	seen := map[string]bool{}
	for _, f := range r.File {
		rn, rp, isDir, parseErr := parseGenerationEntry(f.Name, f.FileInfo().IsDir())
		if parseErr != nil {
			return nil, parseErr
		}
		key := rn + "\x00" + rp
		if seen[key] {
			return nil, fmt.Errorf("generation contains duplicate entry %q", f.Name)
		}
		seen[key] = true
		if !isDir && f.Name == want {
			found = f
		}
	}
	if found == nil {
		return nil, fmt.Errorf("generation has no file %s", want)
	}
	if found.UncompressedSize64 > math.MaxInt64 {
		return nil, fmt.Errorf("generation file size exceeds supported limit")
	}
	if int64(found.UncompressedSize64) != expected.Size {
		return nil, fmt.Errorf("generation file size changed")
	}

	wanted := make(map[int]int, len(indices))
	for pos, index := range indices {
		if index < 0 || index >= len(expected.Blocks) {
			return nil, fmt.Errorf("invalid generation block index %d", index)
		}
		if _, duplicate := wanted[index]; duplicate {
			return nil, fmt.Errorf("duplicate generation block index %d", index)
		}
		wanted[index] = pos
	}
	out := make([]delta.BlockSource, len(indices))
	rc, err := found.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	buf := make([]byte, blockSize)
	for index, block := range expected.Blocks {
		n, readErr := io.ReadFull(rc, buf[:block.Length])
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return nil, readErr
		}
		if n != block.Length {
			return nil, fmt.Errorf("generation block %d is truncated", index)
		}
		if pos, ok := wanted[index]; ok {
			chunk := append([]byte(nil), buf[:n]...)
			sum := sha256.Sum256(chunk)
			if hex.EncodeToString(sum[:]) != block.Hash {
				return nil, fmt.Errorf("generation block %d failed content verification", index)
			}
			out[pos] = delta.BlockSource{Index: index, Data: chunk}
		}
	}
	return out, nil
}
