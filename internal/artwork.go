package internal

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
)

const (
	maxArtworkBytes = 20 << 20
	tmdbImageBase   = "https://image.tmdb.org/t/p/original"
)

var artworkHTTPClient = &http.Client{Timeout: 30 * time.Second}

func resolveRemoteURL(urlOrPath string) string {
	if urlOrPath == "" {
		return ""
	}
	if strings.HasPrefix(urlOrPath, "http://") || strings.HasPrefix(urlOrPath, "https://") {
		return urlOrPath
	}
	if strings.HasPrefix(urlOrPath, "/") {
		return tmdbImageBase + urlOrPath
	}
	return ""
}

func isTMDBPath(p string) bool {
	return strings.HasPrefix(p, "/")
}

func normalizeArtworkKind(t string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "poster":
		return "poster", nil
	case "background", "backdrop":
		return "backdrop", nil
	default:
		return "", fmt.Errorf("unknown artwork type: %s", t)
	}
}

func extFromFilenameOrMIME(filename, contentType string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		if ext == ".jpeg" {
			return ".jpg"
		}
		return ext
	}
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "png"):
		return ".png"
	case strings.Contains(ct, "webp"):
		return ".webp"
	case strings.Contains(ct, "gif"):
		return ".gif"
	default:
		return ".jpg"
	}
}

func artworkURL(httpAddr, relPath string) string {
	relPath = strings.TrimPrefix(filepath.ToSlash(relPath), "/")
	return fmt.Sprintf("http://%s/images/%s", httpAddr, relPath)
}

func (m *Module) localArtworkExists(relPath string) bool {
	if relPath == "" || isTMDBPath(relPath) {
		return false
	}
	_, err := os.Stat(filepath.Join(m.imageDir, filepath.FromSlash(relPath)))
	return err == nil
}

func (m *Module) removeItemArtwork(itemID string) {
	if itemID == "" {
		return
	}
	_ = os.RemoveAll(filepath.Join(m.imageDir, itemID))
}

func (m *Module) writeArtworkBytes(itemID, kind, filename, contentType string, data []byte) (relPath, mime string, err error) {
	if itemID == "" {
		return "", "", fmt.Errorf("item id required")
	}
	kind, err = normalizeArtworkKind(kind)
	if err != nil {
		return "", "", err
	}
	if len(data) == 0 {
		return "", "", fmt.Errorf("empty artwork data")
	}
	if len(data) > maxArtworkBytes {
		return "", "", fmt.Errorf("artwork exceeds %d bytes", maxArtworkBytes)
	}

	ext := extFromFilenameOrMIME(filename, contentType)
	dir := filepath.Join(m.imageDir, itemID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", fmt.Errorf("create artwork dir: %w", err)
	}
	relPath = filepath.ToSlash(filepath.Join(itemID, kind+ext))
	abs := filepath.Join(m.imageDir, filepath.FromSlash(relPath))
	if err := os.WriteFile(abs, data, 0600); err != nil {
		return "", "", fmt.Errorf("write artwork: %w", err)
	}
	mime = contentType
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	return relPath, mime, nil
}

func (m *Module) cacheRemoteArtwork(ctx context.Context, itemID, kind, remoteURL string) (relPath, mime string, err error) {
	if remoteURL == "" {
		return "", "", fmt.Errorf("empty remote url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := artworkHTTPClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download artwork: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArtworkBytes+1))
	if err != nil {
		return "", "", err
	}
	if len(data) > maxArtworkBytes {
		return "", "", fmt.Errorf("artwork exceeds %d bytes", maxArtworkBytes)
	}
	ct := resp.Header.Get("Content-Type")
	return m.writeArtworkBytes(itemID, kind, filepath.Base(remoteURL), ct, data)
}

func (m *Module) persistCachedArtwork(ctx context.Context, itemID, posterSrc, backdropSrc string) {
	var posterRel, backdropRel string
	if src := resolveRemoteURL(posterSrc); src != "" {
		rel, _, err := m.cacheRemoteArtwork(ctx, itemID, "poster", src)
		if err != nil {
			slog.Warn("cache poster failed", "item_id", itemID, "error", err)
		} else {
			posterRel = rel
		}
	}
	if src := resolveRemoteURL(backdropSrc); src != "" {
		rel, _, err := m.cacheRemoteArtwork(ctx, itemID, "backdrop", src)
		if err != nil {
			slog.Warn("cache backdrop failed", "item_id", itemID, "error", err)
		} else {
			backdropRel = rel
		}
	}
	if posterRel == "" && backdropRel == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return
	}
	if posterRel != "" && backdropRel != "" {
		_, _ = m.db.ExecContext(ctx,
			`UPDATE movies SET poster_path=?, backdrop_path=?, updated_at=? WHERE id=?`,
			posterRel, backdropRel, time.Now().UTC().Format(time.RFC3339), itemID,
		)
		return
	}
	if posterRel != "" {
		_, _ = m.db.ExecContext(ctx,
			`UPDATE movies SET poster_path=?, updated_at=? WHERE id=?`,
			posterRel, time.Now().UTC().Format(time.RFC3339), itemID,
		)
	}
	if backdropRel != "" {
		_, _ = m.db.ExecContext(ctx,
			`UPDATE movies SET backdrop_path=?, updated_at=? WHERE id=?`,
			backdropRel, time.Now().UTC().Format(time.RFC3339), itemID,
		)
	}
}

func (m *Module) resolveServablePath(ctx context.Context, itemID, kind, stored string) string {
	if stored == "" {
		return ""
	}
	if m.localArtworkExists(stored) {
		return stored
	}
	remote := resolveRemoteURL(stored)
	if remote == "" {
		return ""
	}
	rel, _, err := m.cacheRemoteArtwork(ctx, itemID, kind, remote)
	if err != nil {
		slog.Debug("artwork heal failed", "item_id", itemID, "kind", kind, "error", err)
		return ""
	}
	m.mu.Lock()
	if m.db != nil {
		col := "poster_path"
		if kind == "backdrop" {
			col = "backdrop_path"
		}
		_, _ = m.db.ExecContext(ctx,
			fmt.Sprintf(`UPDATE movies SET %s=?, updated_at=? WHERE id=?`, col),
			rel, time.Now().UTC().Format(time.RFC3339), itemID,
		)
	}
	m.mu.Unlock()
	return rel
}

func (m *Module) buildArtworkInfos(itemID, poster, backdrop string) []*mediaadminv1.ArtworkInfo {
	var artwork []*mediaadminv1.ArtworkInfo
	if m.localArtworkExists(poster) {
		artwork = append(artwork, &mediaadminv1.ArtworkInfo{
			Id: itemID + "_poster", ItemId: itemID,
			Type: "poster", Url: artworkURL(m.httpAddr, poster),
		})
	}
	if m.localArtworkExists(backdrop) {
		artwork = append(artwork, &mediaadminv1.ArtworkInfo{
			Id: itemID + "_backdrop", ItemId: itemID,
			Type: "background", Url: artworkURL(m.httpAddr, backdrop),
		})
	}
	return artwork
}
