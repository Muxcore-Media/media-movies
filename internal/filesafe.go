package internal

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Muxcore-Media/core/sdk/go/module/pathguard"
)

// safeDeleteMediaFile removes filePath only when it lies inside rootFolder
// after symlink resolution (RULE-VAL-1). The final path element is removed
// itself (a symlink is unlinked, never followed), so only its parent directory
// is resolved and confined. Out-of-root paths are skipped, never deleted.
func safeDeleteMediaFile(filePath, rootFolder string) error {
	if filePath == "" || rootFolder == "" {
		return nil
	}
	cleaned := filepath.Clean(filePath)
	if !filepath.IsAbs(cleaned) {
		return nil
	}
	root := filepath.Clean(rootFolder)
	if !filepath.IsAbs(root) {
		return nil
	}
	realRoot, err := pathguard.Confine(root, []string{root})
	if err != nil {
		slog.Warn("media delete skipped: bad root", "root", root, "error", err)
		return nil
	}
	if cleaned == root {
		return nil
	}
	parent, err := pathguard.Confine(filepath.Dir(cleaned), []string{realRoot})
	if err != nil {
		slog.Warn("media delete skipped: path outside root", "path", cleaned, "root", root, "error", err)
		return nil
	}
	target := filepath.Join(parent, filepath.Base(cleaned))
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	removeEmptyParents(target, realRoot)
	return nil
}

func removeEmptyParents(filePath, rootFolder string) {
	root := filepath.Clean(rootFolder)
	dir := filepath.Dir(filepath.Clean(filePath))
	for dir != root {
		if _, err := pathguard.Confine(dir, []string{root}); err != nil {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
