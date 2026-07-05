package analyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// excludedDirs are noise directories never worth walking into.
var excludedDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	".git":         true,
	"__pycache__":  true,
	"dist":         true,
	"build":        true,
	".next":        true,
	".venv":        true,
	"venv":         true,
	"target":       true, // Rust/Java build output
	".idea":        true,
	".vscode":      true,
}

// ShouldExcludeDir reports whether a directory name is skipped during
// analysis. Exported so the CLI can apply the same filter when packaging a
// project into a zip before upload — no point sending node_modules etc.
// over the wire just to have the server discard it.
func ShouldExcludeDir(name string) bool {
	return excludedDirs[name]
}

// maxWalkFileBytes caps how large a single file can be before it's excluded
// from content-reading steps (secrets/env scanning); it is still counted in
// the manifest.
const maxWalkFileBytes = 2 * 1024 * 1024 // 2MB

// WalkFiles builds a manifest of every file under dir, excluding common
// noise directories (node_modules, vendor, .git, build output, etc.).
func WalkFiles(dir string) (FileManifest, error) {
	manifest := FileManifest{Root: dir}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && excludedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil // skip unreadable file rather than aborting the whole walk
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		manifest.Files = append(manifest.Files, FileEntry{
			Path: rel,
			Size: info.Size(),
			Ext:  strings.ToLower(filepath.Ext(rel)),
		})
		manifest.TotalFiles++
		manifest.TotalBytes += info.Size()
		return nil
	})
	if err != nil {
		return manifest, err
	}

	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	return manifest, nil
}

// ContentHash computes a stable SHA-256 hash over the manifest's file paths,
// sizes, and content (for files under maxWalkFileBytes), used for report
// deduplication. Two uploads of the same project produce the same hash
// regardless of file walk order. onProgress, if non-nil, is invoked
// periodically (not necessarily every file — see progressInterval) with
// (done, total) as files are hashed, always including a final call with
// done == total; pass nil where progress doesn't matter (e.g. a dedup
// lookup that just needs the finished hash).
func (m FileManifest) ContentHash(onProgress func(done, total int)) string {
	h := sha256.New()
	total := len(m.Files)
	interval := progressInterval(total)

	for i, f := range m.Files {
		h.Write([]byte(f.Path))
		h.Write([]byte{0})

		if f.Size > 0 && f.Size <= maxWalkFileBytes {
			if data, err := os.ReadFile(filepath.Join(m.Root, filepath.FromSlash(f.Path))); err == nil {
				h.Write(data)
			}
		}
		h.Write([]byte{0})

		done := i + 1
		if onProgress != nil && (done == total || done%interval == 0) {
			onProgress(done, total)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// progressInterval picks a reporting cadence that yields roughly 20 updates
// across total items, without reporting less often than every file for
// small counts — used by ContentHash and ScanSecrets to throttle how often
// their onProgress callback fires.
func progressInterval(total int) int {
	interval := total / 20
	if interval < 1 {
		interval = 1
	}
	return interval
}

// ReadFileCapped reads a file relative to the manifest root, truncated to
// maxBytes, for safe inclusion in prompts or scans.
func ReadFileCapped(root, relPath string, maxBytes int) (string, error) {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		return "", err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
