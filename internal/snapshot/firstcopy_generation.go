package snapshot

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/store"
)

// CreateFirstCopyGeneration captures and certifies one immutable source
// generation without pruning, uploading or publishing it. The caller binds
// and pins the returned snapshot in the same store transaction as the
// first-copy lease.
//
// Several independent roots cannot be frozen atomically by portable Go file
// APIs. Instead this takes full content manifests before and after capture
// and accepts the archive only when all three views are identical. A source
// that keeps changing is rejected; live saves are never frozen or rewritten.
func (m *Manager) CreateFirstCopyGeneration(gameID string) (store.Snapshot, Generation, error) {
	m.inFlight.Add()
	defer m.inFlight.Done()

	game, err := m.Store.GetGame(gameID)
	if err != nil {
		return store.Snapshot{}, Generation{}, err
	}
	settings, err := m.Store.GetSettings()
	if err != nil {
		return store.Snapshot{}, Generation{}, err
	}
	extra, err := m.Store.GameRootPaths(gameID)
	if err != nil {
		return store.Snapshot{}, Generation{}, fmt.Errorf("read first-copy roots: %w", err)
	}

	backupDir := filepath.Join(settings.BackupsDir, gameID, game.ActiveBranch)
	if err := os.MkdirAll(backupDir, 0o777); err != nil {
		return store.Snapshot{}, Generation{}, fmt.Errorf("create backup dir: %w", err)
	}
	stagingPath := filepath.Join(backupDir,
		fmt.Sprintf(".first-copy-%d-%d.zip", os.Getpid(), m.stagingSeq.Add(1)))
	defer os.Remove(stagingPath)

	captured, gen, err := createFirstCopyArchive(game.SavePath, extra, stagingPath, nil)
	if err != nil {
		return store.Snapshot{}, Generation{}, err
	}
	gen = transferableGeneration(gen)
	snap, zipPath, err := m.claimAndInsert(
		gameID, game.ActiveBranch, backupDir, stagingPath,
		"first-copy generation", true, true,
	)
	if err != nil {
		return store.Snapshot{}, Generation{}, err
	}
	if err := m.Store.RecordSnapshotFiles(snap.ID, captured); err != nil {
		_ = m.Store.DeleteSnapshot(snap.ID)
		_ = os.Remove(zipPath)
		return store.Snapshot{}, Generation{}, fmt.Errorf("record first-copy generation files: %w", err)
	}
	return snap, gen, nil
}

// createFirstCopyArchive exists separately so tests can mutate a synthetic
// save at the exact boundary after capture and prove certification fails.
func createFirstCopyArchive(primary string, extra map[string]string, outPath string, afterCapture func()) ([]store.CapturedFile, Generation, error) {
	before, err := strictMultiManifest(primary, extra)
	if err != nil {
		return nil, Generation{}, fmt.Errorf("scan first-copy source before capture: %w", err)
	}
	captured, err := zipRootsStrict(primary, extra, outPath)
	if err != nil {
		return nil, Generation{}, fmt.Errorf("capture first-copy source: %w", err)
	}
	if afterCapture != nil {
		afterCapture()
	}
	gen, err := GenerationManifest(outPath)
	if err != nil {
		return nil, Generation{}, fmt.Errorf("validate first-copy archive: %w", err)
	}
	after, err := strictMultiManifest(primary, extra)
	if err != nil {
		return nil, Generation{}, fmt.Errorf("scan first-copy source after capture: %w", err)
	}
	beforeDigest := GenerationManifestDigest(before)
	afterDigest := GenerationManifestDigest(after)
	if beforeDigest != gen.Digest || afterDigest != gen.Digest {
		return nil, Generation{}, fmt.Errorf(
			"first-copy source changed during capture (before=%s archive=%s after=%s)",
			shortDigest(beforeDigest), shortDigest(gen.Digest), shortDigest(afterDigest),
		)
	}
	return captured, gen, nil
}

func shortDigest(digest string) string {
	if len(digest) <= 16 {
		return digest
	}
	return digest[:16]
}

func strictMultiManifest(primary string, extra map[string]string) (delta.Manifest, error) {
	m, err := strictRootManifest(primary)
	if err != nil {
		return delta.Manifest{}, err
	}
	names := make([]string, 0, len(extra))
	for name := range extra {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !validGenerationRoot(name) || strings.TrimSpace(extra[name]) == "" {
			return delta.Manifest{}, fmt.Errorf("invalid first-copy root %q", name)
		}
		sub, err := strictRootManifest(extra[name])
		if err != nil {
			return delta.Manifest{}, fmt.Errorf("root %q: %w", name, err)
		}
		if m.Extra == nil {
			m.Extra = map[string]delta.RootManifest{}
		}
		m.Extra[name] = delta.RootManifest{Files: sub.Files, Dirs: sub.Dirs}
	}
	return m, nil
}

// FirstCopySourceDigest reads the live source with the same fail-closed rules
// and root layout used by generation capture.
func FirstCopySourceDigest(primary string, extra map[string]string) (string, error) {
	m, err := strictMultiManifest(primary, extra)
	if err != nil {
		return "", err
	}
	return transferableManifestDigest(m), nil
}

// TransferableGeneration returns the protocol-visible subset of a fully
// validated archive. Dot entries and interrupted-patch temporaries remain
// safely present in the backup but are not advertised or transferred,
// matching ordinary manifests.
func TransferableGeneration(gen Generation) Generation {
	return transferableGeneration(gen)
}

func transferableGeneration(gen Generation) Generation {
	gen.Manifest = transferableManifest(gen.Manifest)
	gen.Digest = GenerationManifestDigest(gen.Manifest)
	return gen
}

func transferableManifestDigest(m delta.Manifest) string {
	return GenerationManifestDigest(transferableManifest(m))
}

func transferableManifest(m delta.Manifest) delta.Manifest {
	out := delta.Manifest{Files: map[string]delta.FileEntry{}}
	for name, entry := range m.Files {
		if firstCopyProtocolPath(name) {
			out.Files[name] = entry
		}
	}
	for _, name := range m.Dirs {
		if firstCopyProtocolPath(name) {
			out.Dirs = append(out.Dirs, name)
		}
	}
	for root, manifest := range m.Extra {
		if out.Extra == nil {
			out.Extra = map[string]delta.RootManifest{}
		}
		filtered := delta.RootManifest{Files: map[string]delta.FileEntry{}}
		for name, entry := range manifest.Files {
			if firstCopyProtocolPath(name) {
				filtered.Files[name] = entry
			}
		}
		for _, name := range manifest.Dirs {
			if firstCopyProtocolPath(name) {
				filtered.Dirs = append(filtered.Dirs, name)
			}
		}
		out.Extra[root] = filtered
	}
	return out
}

func firstCopyProtocolPath(name string) bool {
	if strings.HasSuffix(name, delta.TmpSuffix) {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

func strictRootManifest(root string) (delta.Manifest, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return delta.Manifest{}, err
	}
	if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return delta.Manifest{}, fmt.Errorf("unsafe source type at %s", root)
	}
	m := delta.Manifest{Files: map[string]delta.FileEntry{}}
	if !info.IsDir() {
		entry, err := delta.HashFile(root)
		if err != nil {
			return delta.Manifest{}, err
		}
		m.Files[filepath.Base(root)] = entry
		return m, nil
	}
	err = filepath.Walk(root, func(filePath string, walkInfo os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == root {
			return nil
		}
		if walkInfo.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("unsafe source type at %s", filePath)
		}
		rel, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if walkInfo.IsDir() {
			m.Dirs = append(m.Dirs, rel)
			return nil
		}
		entry, err := delta.HashFile(filePath)
		if err != nil {
			return err
		}
		m.Files[rel] = entry
		return nil
	})
	if err != nil {
		return delta.Manifest{}, err
	}
	sort.Strings(m.Dirs)
	return m, nil
}

func zipRootsStrict(primary string, extra map[string]string, outPath string) ([]store.CapturedFile, error) {
	out, err := os.Create(outPath)
	if err != nil {
		return nil, err
	}
	w := newSnapshotWriter(out)
	closeWith := func(base error) error {
		if err := w.Close(); base == nil && err != nil {
			base = err
		}
		if err := out.Sync(); base == nil && err != nil {
			base = err
		}
		if err := out.Close(); base == nil && err != nil {
			base = err
		}
		return base
	}

	var captured []store.CapturedFile
	files, err := archiveIntoStrict(w, primary, "", "")
	if err != nil {
		return nil, closeWith(err)
	}
	captured = append(captured, files...)
	names := make([]string, 0, len(extra))
	for name := range extra {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !validGenerationRoot(name) || strings.TrimSpace(extra[name]) == "" {
			return nil, closeWith(fmt.Errorf("invalid first-copy root %q", name))
		}
		files, err := archiveIntoStrict(w, extra[name], RootPrefix+name+"/", name)
		if err != nil {
			return nil, closeWith(fmt.Errorf("root %q: %w", name, err))
		}
		captured = append(captured, files...)
	}
	if err := closeWith(nil); err != nil {
		return nil, err
	}
	return captured, nil
}

func archiveIntoStrict(w *zip.Writer, sourcePath, prefix, root string) ([]store.CapturedFile, error) {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return nil, err
	}
	if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return nil, fmt.Errorf("unsafe source type at %s", sourcePath)
	}
	if !info.IsDir() {
		name := filepath.Base(sourcePath)
		hash, err := addFileEntry(w, sourcePath, prefix+name)
		if err != nil {
			return nil, err
		}
		return []store.CapturedFile{{Root: root, Path: name, Hash: hash}}, nil
	}
	if prefix != "" {
		if _, err := w.CreateHeader(&zip.FileHeader{Name: prefix, Method: zip.Store}); err != nil {
			return nil, err
		}
	}
	var captured []store.CapturedFile
	err = filepath.Walk(sourcePath, func(filePath string, walkInfo os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == sourcePath {
			return nil
		}
		if walkInfo.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("unsafe source type at %s", filePath)
		}
		rel, err := filepath.Rel(sourcePath, filePath)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if walkInfo.IsDir() {
			_, err := w.CreateHeader(&zip.FileHeader{Name: prefix + rel + "/", Method: zip.Store})
			return err
		}
		hash, err := addFileEntry(w, filePath, prefix+rel)
		if err != nil {
			return err
		}
		captured = append(captured, store.CapturedFile{Root: root, Path: rel, Hash: hash})
		return nil
	})
	return captured, err
}
