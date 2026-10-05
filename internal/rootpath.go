package internal

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Muxcore-Media/core/sdk/go/module/meshtls"
	"github.com/Muxcore-Media/core/sdk/go/module/pathguard"
	rootsv1 "github.com/Muxcore-Media/media-root-folders/proto/rootsv1"
)

func normalizeRootFolderPath(path string) (string, error) {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "" || cleaned == "." {
		return "", nil
	}
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("root_folder_path must be absolute")
	}
	return cleaned, nil
}

// resolveRootFolderPath normalizes path and, when media.roots is reachable,
// requires it to match a registered root for mediaKind. Empty path is allowed.
// When the roots module is unavailable the path is refused (fail closed,
// RULE-VAL-1).
func (m *Module) resolveRootFolderPath(ctx context.Context, path, mediaKind string) (string, error) {
	cleaned, err := normalizeRootFolderPath(path)
	if err != nil {
		return "", err
	}
	if cleaned == "" {
		return "", nil
	}

	paths, available, err := m.listRegisteredRootPaths(ctx, mediaKind)
	if err != nil {
		return "", err
	}
	if !available {
		return "", errRootsUnavailable
	}
	for _, p := range paths {
		if filepath.Clean(p) == cleaned {
			return cleaned, nil
		}
	}
	return "", fmt.Errorf("root_folder_path %q is not a registered root", cleaned)
}

var errRootsUnavailable = errors.New("registered roots unavailable (media.roots not reachable); refusing path")

// confineMediaFile validates a media file path against the registered roots
// for mediaKind: it must be absolute, free of traversal, and (after symlink
// resolution) inside a registered root. Fails closed when roots are unknown.
//
// Relative paths are opaque storage keys (never resolved against the
// filesystem by this module); they are accepted only when free of NUL bytes
// and ".." segments.
func (m *Module) confineMediaFile(ctx context.Context, path, mediaKind string) (string, error) {
	if path != "" && !filepath.IsAbs(path) {
		if strings.ContainsRune(path, 0) {
			return "", fmt.Errorf("file_path: %w", pathguard.ErrInvalidPath)
		}
		for _, seg := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
			if seg == ".." {
				return "", fmt.Errorf("file_path %q: %w: contains \"..\" segment", path, pathguard.ErrInvalidPath)
			}
		}
		return path, nil
	}
	paths, available, err := m.listRegisteredRootPaths(ctx, mediaKind)
	if err != nil {
		return "", err
	}
	if !available {
		return "", errRootsUnavailable
	}
	roots := make([]string, 0, len(paths))
	for _, p := range paths {
		if p = filepath.Clean(strings.TrimSpace(p)); filepath.IsAbs(p) {
			roots = append(roots, p)
		}
	}
	if _, err := pathguard.Confine(path, roots); err != nil {
		return "", fmt.Errorf("file_path %q: %w", path, err)
	}
	return filepath.Clean(path), nil
}

func (m *Module) listRegisteredRootPaths(ctx context.Context, mediaKind string) ([]string, bool, error) {
	if m.rootsListFn != nil {
		paths, err := m.rootsListFn(ctx, mediaKind)
		if err != nil {
			return nil, true, err
		}
		return paths, true, nil
	}
	if err := m.ensureRoots(ctx); err != nil {
		return nil, false, nil
	}
	m.mu.RLock()
	cli := m.rootsClient
	m.mu.RUnlock()
	if cli == nil {
		return nil, false, nil
	}
	resp, err := cli.ListRoots(ctx, &rootsv1.ListRootsRequest{MediaKind: mediaKind})
	if err != nil {
		return nil, false, nil
	}
	out := make([]string, 0, len(resp.GetRoots()))
	for _, r := range resp.GetRoots() {
		out = append(out, r.GetPath())
	}
	return out, true, nil
}

func (m *Module) ensureRoots(ctx context.Context) error {
	m.mu.RLock()
	if m.rootsClient != nil {
		m.mu.RUnlock()
		return nil
	}
	m.mu.RUnlock()

	addr, err := m.findRootsAddr(ctx)
	if err != nil {
		return err
	}
	conn, err := meshtls.Dial(addr)
	if err != nil {
		return fmt.Errorf("dial roots: %w", err)
	}
	m.mu.Lock()
	if m.rootsClient == nil {
		m.rootsConn = conn
		m.rootsClient = rootsv1.NewRootFolderServiceClient(conn)
	} else {
		_ = conn.Close()
	}
	m.mu.Unlock()
	return nil
}

func (m *Module) findRootsAddr(ctx context.Context) (string, error) {
	mc := m.coreClient()
	if mc == nil {
		return "", fmt.Errorf("not connected to core")
	}
	modules, err := mc.Discovery.FindByCapability(ctx, "media.roots")
	if err != nil {
		return "", fmt.Errorf("discover media.roots: %w", err)
	}
	for _, mod := range modules {
		addr := dialAddrForModule(mod.Id, mod.HttpAddr)
		if addr != "" {
			return addr, nil
		}
	}
	return "", fmt.Errorf("no media.roots module found")
}
