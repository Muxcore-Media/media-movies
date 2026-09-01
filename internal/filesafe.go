package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func pathUnderRoot(path, root string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	abs = filepath.Clean(abs)
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil {
		return "", fmt.Errorf("path %q is outside root", abs)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q is outside root", abs)
	}
	return abs, nil
}

func safeDeleteMediaFile(filePath, rootFolder string) error {
	if filePath == "" {
		return nil
	}
	cleaned := filepath.Clean(filePath)
	if !filepath.IsAbs(cleaned) {
		return nil
	}
	root := filepath.Clean(rootFolder)
	if root == "" || root == "." {
		return nil
	}
	sep := string(os.PathSeparator)
	if cleaned != root && !strings.HasPrefix(cleaned, root+sep) {
		return nil
	}
	if err := os.Remove(cleaned); err != nil && !os.IsNotExist(err) {
		return err
	}
	removeEmptyParents(cleaned, root)
	return nil
}

func removeEmptyParents(filePath, rootFolder string) {
	root := filepath.Clean(rootFolder)
	dir := filepath.Dir(filepath.Clean(filePath))
	sep := string(os.PathSeparator)
	for dir != root && strings.HasPrefix(dir, root+sep) {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
