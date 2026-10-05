package internal

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Muxcore-Media/core/sdk/go/module/meshtls"
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
// When the roots module is unavailable, the cleaned path is accepted (soft).
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
		return cleaned, nil
	}
	for _, p := range paths {
		if p == cleaned {
			return cleaned, nil
		}
	}
	return "", fmt.Errorf("root_folder_path %q is not a registered root", cleaned)
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
