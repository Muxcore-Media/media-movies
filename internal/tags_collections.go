package internal

import (
	"context"
	"fmt"
	"strings"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

func (m *Module) CreateTag(ctx context.Context, req *mgmntv1.CreateTagRequest) (*mgmntv1.CreateTagResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	label := strings.TrimSpace(req.GetLabel())
	if label == "" {
		return nil, fmt.Errorf("label required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id := fmt.Sprintf("tag_%d", time.Now().UnixNano())
	_, err := m.db.ExecContext(ctx, `INSERT INTO tags (id, label, created_at) VALUES (?, ?, ?)`, id, label, now)
	if err != nil {
		return nil, fmt.Errorf("create tag: %w", err)
	}
	return &mgmntv1.CreateTagResponse{TagId: id}, nil
}

func (m *Module) DeleteTag(ctx context.Context, req *mgmntv1.DeleteTagRequest) (*mgmntv1.DeleteTagResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	if req.GetTagId() == "" {
		return nil, fmt.Errorf("tag_id required")
	}
	_, _ = m.db.ExecContext(ctx, `DELETE FROM item_tags WHERE tag_id = ?`, req.GetTagId())
	_, err := m.db.ExecContext(ctx, `DELETE FROM tags WHERE id = ?`, req.GetTagId())
	if err != nil {
		return nil, fmt.Errorf("delete tag: %w", err)
	}
	return &mgmntv1.DeleteTagResponse{}, nil
}

func (m *Module) ListTags(ctx context.Context, req *mgmntv1.ListTagsRequest) (*mgmntv1.ListTagsResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	rows, err := m.db.QueryContext(ctx, `SELECT id, label, created_at FROM tags ORDER BY label`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var tags []*mgmntv1.Tag
	for rows.Next() {
		var id, label, created string
		if err := rows.Scan(&id, &label, &created); err != nil {
			return nil, err
		}
		tags = append(tags, &mgmntv1.Tag{Id: id, Label: label, CreatedAt: created})
	}
	return &mgmntv1.ListTagsResponse{Tags: tags}, nil
}

func (m *Module) SetItemTags(ctx context.Context, req *mgmntv1.SetItemTagsRequest) (*mgmntv1.SetItemTagsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	if req.GetItemId() == "" {
		return nil, fmt.Errorf("item_id required")
	}
	var exists string
	if err := m.db.QueryRowContext(ctx, `SELECT id FROM movies WHERE id = ?`, req.GetItemId()).Scan(&exists); err != nil || exists == "" {
		return nil, fmt.Errorf("movie not found: %s", req.GetItemId())
	}
	_, _ = m.db.ExecContext(ctx, `DELETE FROM item_tags WHERE item_id = ?`, req.GetItemId())
	for _, tagID := range req.GetTagIds() {
		if tagID == "" {
			continue
		}
		_, err := m.db.ExecContext(ctx, `INSERT OR IGNORE INTO item_tags (item_id, tag_id) VALUES (?, ?)`, req.GetItemId(), tagID)
		if err != nil {
			return nil, fmt.Errorf("set tag: %w", err)
		}
	}
	return &mgmntv1.SetItemTagsResponse{}, nil
}

func (m *Module) ListCollections(ctx context.Context, req *mgmntv1.ListCollectionsRequest) (*mgmntv1.ListCollectionsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	_ = m.ensureCollectionPrefs(ctx)
	rows, err := m.db.QueryContext(ctx,
		`SELECT m.collection_id, MAX(m.collection_name), COUNT(*),
		 COALESCE((SELECT p.monitored FROM collection_prefs p WHERE p.collection_id = m.collection_id), 0)
		 FROM movies m
		 WHERE m.collection_id > 0 GROUP BY m.collection_id ORDER BY MAX(m.collection_name)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*mgmntv1.CollectionSummary
	for rows.Next() {
		var id, count, monitored int32
		var name string
		if err := rows.Scan(&id, &name, &count, &monitored); err != nil {
			return nil, err
		}
		out = append(out, &mgmntv1.CollectionSummary{
			CollectionId: id, Name: name, MovieCount: count, Monitored: monitored != 0,
		})
	}
	return &mgmntv1.ListCollectionsResponse{Collections: out}, nil
}

func (m *Module) GetCollectionMovies(ctx context.Context, req *mgmntv1.GetCollectionMoviesRequest) (*mgmntv1.GetCollectionMoviesResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	if req.GetCollectionId() == 0 {
		return nil, fmt.Errorf("collection_id required")
	}
	var name string
	_ = m.db.QueryRowContext(ctx,
		`SELECT collection_name FROM movies WHERE collection_id = ? LIMIT 1`, req.GetCollectionId(),
	).Scan(&name)

	rows, err := m.db.QueryContext(ctx,
		`SELECT `+movieSelectCols+`, COALESCE(collection_id, 0), COALESCE(collection_name, '')
		 FROM movies WHERE collection_id = ? ORDER BY year, title`,
		req.GetCollectionId(),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var movies []*mgmntv1.MovieItem
	for rows.Next() {
		movie := m.scanMovieWithCollection(rows)
		if movie != nil {
			movies = append(movies, movie)
		}
	}
	return &mgmntv1.GetCollectionMoviesResponse{
		CollectionId: req.GetCollectionId(),
		Name:         name,
		Movies:       movies,
	}, nil
}
