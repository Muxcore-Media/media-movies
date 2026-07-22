package internal

import (
	"os"
	"path/filepath"
	"strings"
)

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
