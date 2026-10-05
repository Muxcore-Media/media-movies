package internal

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Muxcore-Media/core/sdk/go/module/meshtls"
	automationv1 "github.com/Muxcore-Media/media-automation/proto/automationv1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	metadatav1 "github.com/Muxcore-Media/metadata-tmdb/proto/metadatav1"
)

type collectionPart struct {
	TmdbID       int32
	Title        string
	Year         int32
	Overview     string
	PosterPath   string
	BackdropPath string
}

func (m *Module) ensureCollectionPrefsTable(ctx context.Context) error {
	_, err := m.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS collection_prefs (
			collection_id INTEGER PRIMARY KEY,
			name TEXT DEFAULT '',
			monitored INTEGER DEFAULT 0,
			search_on_add INTEGER DEFAULT 1,
			quality_profile_id TEXT DEFAULT '',
			root_folder_path TEXT DEFAULT '',
			updated_at TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("create collection_prefs: %w", err)
	}
	return nil
}

func (m *Module) collectionName(ctx context.Context, id int32) string {
	var name string
	_ = m.db.QueryRowContext(ctx, `SELECT collection_name FROM movies WHERE collection_id = ? AND collection_name != '' LIMIT 1`, id).Scan(&name)
	if name != "" {
		return name
	}
	_ = m.db.QueryRowContext(ctx, `SELECT name FROM collection_prefs WHERE collection_id = ?`, id).Scan(&name)
	return name
}

func (m *Module) loadCollectionPrefsLocked(ctx context.Context, id int32) *mgmntv1.CollectionPrefs {
	prefs := &mgmntv1.CollectionPrefs{
		CollectionId: id,
		Name:         m.collectionName(ctx, id),
		SearchOnAdd:  true,
	}
	var monitored, searchOnAdd int
	var name, quality, root string
	err := m.db.QueryRowContext(ctx,
		`SELECT name, monitored, search_on_add, quality_profile_id, root_folder_path
		 FROM collection_prefs WHERE collection_id = ?`, id,
	).Scan(&name, &monitored, &searchOnAdd, &quality, &root)
	if err != nil {
		return prefs
	}
	if name != "" {
		prefs.Name = name
	}
	prefs.Monitored = monitored == 1
	prefs.SearchOnAdd = searchOnAdd == 1
	prefs.QualityProfileId = quality
	prefs.RootFolderPath = root
	return prefs
}

func (m *Module) GetCollectionPrefs(ctx context.Context, req *mgmntv1.GetCollectionPrefsRequest) (*mgmntv1.GetCollectionPrefsResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	if req.GetCollectionId() == 0 {
		return nil, fmt.Errorf("collection_id required")
	}
	return &mgmntv1.GetCollectionPrefsResponse{Prefs: m.loadCollectionPrefsLocked(ctx, req.GetCollectionId())}, nil
}

func (m *Module) SetCollectionMonitored(ctx context.Context, req *mgmntv1.SetCollectionMonitoredRequest) (*mgmntv1.SetCollectionMonitoredResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	id := req.GetCollectionId()
	if id == 0 {
		return nil, fmt.Errorf("collection_id required")
	}
	cur := m.loadCollectionPrefsLocked(ctx, id)
	searchOnAdd := cur.GetSearchOnAdd()
	if req.SearchOnAdd != nil {
		searchOnAdd = req.GetSearchOnAdd()
	}
	quality := strings.TrimSpace(req.GetQualityProfileId())
	if quality == "" {
		quality = cur.GetQualityProfileId()
	}
	root := strings.TrimSpace(req.GetRootFolderPath())
	if root == "" {
		root = cur.GetRootFolderPath()
	}
	name := cur.GetName()
	now := time.Now().UTC().Format(time.RFC3339)
	monitored := 0
	if req.GetMonitored() {
		monitored = 1
	}
	search := 0
	if searchOnAdd {
		search = 1
	}
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO collection_prefs (collection_id, name, monitored, search_on_add, quality_profile_id, root_folder_path, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(collection_id) DO UPDATE SET
			name = excluded.name,
			monitored = excluded.monitored,
			search_on_add = excluded.search_on_add,
			quality_profile_id = excluded.quality_profile_id,
			root_folder_path = excluded.root_folder_path,
			updated_at = excluded.updated_at
	`, id, name, monitored, search, quality, root, now)
	if err != nil {
		return nil, fmt.Errorf("save collection prefs: %w", err)
	}
	if req.GetMonitored() {
		_, _ = m.db.ExecContext(ctx, `UPDATE movies SET monitored = 1, updated_at = ? WHERE collection_id = ?`, now, id)
	}
	return &mgmntv1.SetCollectionMonitoredResponse{Prefs: m.loadCollectionPrefsLocked(ctx, id)}, nil
}

func (m *Module) SyncCollection(ctx context.Context, req *mgmntv1.SyncCollectionRequest) (*mgmntv1.SyncCollectionResponse, error) {
	id := req.GetCollectionId()
	if id == 0 {
		return nil, fmt.Errorf("collection_id required")
	}
	m.mu.RLock()
	prefs := m.loadCollectionPrefsLocked(ctx, id)
	m.mu.RUnlock()

	parts, err := m.collectionParts(ctx, id)
	if err != nil {
		return nil, err
	}
	var added, present, missingSrc int32
	for _, part := range parts {
		if part.TmdbID == 0 {
			missingSrc++
			continue
		}
		var existing string
		m.mu.RLock()
		_ = m.db.QueryRowContext(ctx, `SELECT id FROM movies WHERE tmdb_id = ? LIMIT 1`, part.TmdbID).Scan(&existing)
		m.mu.RUnlock()
		if existing != "" {
			present++
			m.attachMovieToCollection(ctx, existing, id, prefs.GetName(), part.Title)
			continue
		}
		if !req.GetAddMissing() {
			continue
		}
		add, addErr := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
			TmdbId:           part.TmdbID,
			Title:            part.Title,
			Year:             part.Year,
			Overview:         part.Overview,
			PosterPath:       part.PosterPath,
			BackdropPath:     part.BackdropPath,
			QualityProfileId: prefs.GetQualityProfileId(),
			RootFolderPath:   prefs.GetRootFolderPath(),
		})
		if addErr != nil {
			slog.Warn("collection sync add failed", "tmdb_id", part.TmdbID, "error", addErr)
			continue
		}
		added++
		m.attachMovieToCollection(ctx, add.GetMovieId(), id, firstNonEmpty(prefs.GetName(), part.Title+" Collection"), part.Title)
		if prefs.GetMonitored() {
			mon := true
			_, _ = m.UpdateMovie(ctx, &mgmntv1.UpdateMovieRequest{MovieId: add.GetMovieId(), Monitored: &mon})
		}
		if prefs.GetSearchOnAdd() {
			if _, searchErr := m.automationSearchItem(ctx, &automationv1.SearchItemRequest{
				ItemType:         "movie",
				Query:            part.Title,
				TmdbId:           part.TmdbID,
				Year:             part.Year,
				Limit:            20,
				QualityProfileId: prefs.GetQualityProfileId(),
			}); searchErr != nil {
				slog.Warn("collection sync search failed", "movie_id", add.GetMovieId(), "error", searchErr)
			}
		}
	}
	if name := firstNonEmpty(prefs.GetName()); name != "" {
		m.mu.Lock()
		_, _ = m.db.ExecContext(ctx, `UPDATE collection_prefs SET name = ? WHERE collection_id = ? AND (name = '' OR name IS NULL)`, name, id)
		m.mu.Unlock()
	}
	return &mgmntv1.SyncCollectionResponse{Added: added, AlreadyPresent: present, MissingOnSource: missingSrc}, nil
}

func (m *Module) attachMovieToCollection(ctx context.Context, movieID string, collectionID int32, name, fallbackTitle string) {
	if movieID == "" || collectionID == 0 {
		return
	}
	if name == "" {
		name = strings.TrimSpace(fallbackTitle)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	m.mu.Lock()
	defer m.mu.Unlock()
	_, _ = m.db.ExecContext(ctx,
		`UPDATE movies SET collection_id = ?, collection_name = CASE WHEN ? != '' THEN ? ELSE collection_name END, updated_at = ? WHERE id = ?`,
		collectionID, name, name, now, movieID,
	)
}

func (m *Module) collectionParts(ctx context.Context, collectionID int32) ([]collectionPart, error) {
	if m.collectionPartsFn != nil {
		return m.collectionPartsFn(ctx, collectionID)
	}
	metaAddr, err := m.findMetadataAddr(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := meshtls.Dial(metaAddr)
	if err != nil {
		return nil, fmt.Errorf("dial metadata: %w", err)
	}
	defer conn.Close()
	resp, err := metadatav1.NewMetadataServiceClient(conn).GetCollection(ctx, &metadatav1.GetCollectionRequest{TmdbId: collectionID})
	if err != nil {
		return nil, fmt.Errorf("metadata collection: %w", err)
	}
	if resp.GetName() != "" {
		m.mu.Lock()
		_, _ = m.db.ExecContext(ctx, `
			INSERT INTO collection_prefs (collection_id, name, monitored, search_on_add, quality_profile_id, root_folder_path, updated_at)
			VALUES (?, ?, 0, 1, '', '', ?)
			ON CONFLICT(collection_id) DO UPDATE SET name = CASE WHEN collection_prefs.name = '' THEN excluded.name ELSE collection_prefs.name END
		`, collectionID, resp.GetName(), time.Now().UTC().Format(time.RFC3339))
		m.mu.Unlock()
	}
	out := make([]collectionPart, 0, len(resp.GetParts()))
	for _, p := range resp.GetParts() {
		if p == nil {
			continue
		}
		out = append(out, collectionPart{
			TmdbID:       p.GetId(),
			Title:        p.GetTitle(),
			Year:         extractYear(p.GetReleaseDate()),
			Overview:     p.GetOverview(),
			PosterPath:   p.GetPosterPath(),
			BackdropPath: p.GetBackdropPath(),
		})
	}
	return out, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
