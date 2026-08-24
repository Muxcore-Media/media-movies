package internal

import (
	"context"
	"fmt"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	metadatav1 "github.com/Muxcore-Media/contracts-metadata/muxcore/metadata/v1"
)

func (m *Module) ensureCollectionPrefs(ctx context.Context) error {
	_, err := m.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS collection_prefs (
			collection_id INTEGER PRIMARY KEY,
			monitored INTEGER NOT NULL DEFAULT 0,
			search_on_add INTEGER NOT NULL DEFAULT 1,
			quality_profile_id TEXT NOT NULL DEFAULT '',
			root_folder_path TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL
		)
	`)
	return err
}

func (m *Module) SetCollectionMonitored(ctx context.Context, req *mgmntv1.SetCollectionMonitoredRequest) (*mgmntv1.SetCollectionMonitoredResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	if req.GetCollectionId() == 0 {
		return nil, fmt.Errorf("collection_id required")
	}
	if err := m.ensureCollectionPrefs(ctx); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	monitored := 0
	if req.GetMonitored() {
		monitored = 1
	}
	searchOnAdd := 0
	if req.GetSearchOnAdd() {
		searchOnAdd = 1
	}
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO collection_prefs (collection_id, monitored, search_on_add, quality_profile_id, root_folder_path, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(collection_id) DO UPDATE SET
			monitored=excluded.monitored,
			search_on_add=excluded.search_on_add,
			quality_profile_id=excluded.quality_profile_id,
			root_folder_path=excluded.root_folder_path,
			updated_at=excluded.updated_at
	`, req.GetCollectionId(), monitored, searchOnAdd, req.GetQualityProfileId(), req.GetRootFolderPath(), now)
	if err != nil {
		return nil, fmt.Errorf("upsert collection prefs: %w", err)
	}
	if monitored == 1 {
		_, _ = m.db.ExecContext(ctx,
			`UPDATE movies SET monitored=1, updated_at=? WHERE collection_id=?`,
			now, req.GetCollectionId(),
		)
	}
	return &mgmntv1.SetCollectionMonitoredResponse{}, nil
}

func (m *Module) GetCollectionPrefs(ctx context.Context, req *mgmntv1.GetCollectionPrefsRequest) (*mgmntv1.GetCollectionPrefsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	if req.GetCollectionId() == 0 {
		return nil, fmt.Errorf("collection_id required")
	}
	if err := m.ensureCollectionPrefs(ctx); err != nil {
		return nil, err
	}
	var monitored, searchOnAdd int
	var profile, root string
	err := m.db.QueryRowContext(ctx,
		`SELECT monitored, search_on_add, quality_profile_id, root_folder_path FROM collection_prefs WHERE collection_id=?`,
		req.GetCollectionId(),
	).Scan(&monitored, &searchOnAdd, &profile, &root)
	if err != nil {
		return &mgmntv1.GetCollectionPrefsResponse{CollectionId: req.GetCollectionId(), SearchOnAdd: true}, nil
	}
	return &mgmntv1.GetCollectionPrefsResponse{
		CollectionId:     req.GetCollectionId(),
		Monitored:         monitored != 0,
		SearchOnAdd:       searchOnAdd != 0,
		QualityProfileId: profile,
		RootFolderPath:   root,
	}, nil
}

func (m *Module) SyncCollection(ctx context.Context, req *mgmntv1.SyncCollectionRequest) (*mgmntv1.SyncCollectionResponse, error) {
	if req.GetCollectionId() == 0 {
		return nil, fmt.Errorf("collection_id required")
	}
	metaClient, closer, err := m.metadataClient(ctx)
	if err != nil {
		return nil, err
	}
	defer closer()

	coll, err := metaClient.GetCollection(ctx, &metadatav1.GetCollectionRequest{TmdbId: req.GetCollectionId()})
	if err != nil {
		return nil, fmt.Errorf("tmdb collection: %w", err)
	}

	m.mu.Lock()
	if m.db == nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("not initialized")
	}
	_ = m.ensureCollectionPrefs(ctx)
	var profile, root string
	_ = m.db.QueryRowContext(ctx,
		`SELECT quality_profile_id, root_folder_path FROM collection_prefs WHERE collection_id=?`,
		req.GetCollectionId(),
	).Scan(&profile, &root)

	type partInfo struct {
		tmdbID       int32
		title        string
		overview     string
		poster       string
		backdrop     string
		releaseDate  string
		alreadyInLib bool
		movieID      string
	}
	parts := make([]partInfo, 0, len(coll.GetParts()))
	for _, part := range coll.GetParts() {
		if part.GetId() == 0 {
			continue
		}
		var existing string
		_ = m.db.QueryRowContext(ctx, `SELECT id FROM movies WHERE tmdb_id=?`, part.GetId()).Scan(&existing)
		parts = append(parts, partInfo{
			tmdbID: part.GetId(), title: part.GetTitle(), overview: part.GetOverview(),
			poster: part.GetPosterPath(), backdrop: part.GetBackdropPath(),
			releaseDate: part.GetReleaseDate(), alreadyInLib: existing != "", movieID: existing,
		})
		if existing != "" {
			_, _ = m.db.ExecContext(ctx,
				`UPDATE movies SET collection_id=?, collection_name=? WHERE id=?`,
				req.GetCollectionId(), coll.GetName(), existing,
			)
		}
	}
	m.mu.Unlock()

	var added, present int
	for _, p := range parts {
		if p.alreadyInLib {
			present++
			continue
		}
		if !req.GetAddMissing() {
			continue
		}
		resp, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
			TmdbId:            p.tmdbID,
			Title:             p.title,
			Year:              extractYear(p.releaseDate),
			Overview:          p.overview,
			PosterPath:        p.poster,
			BackdropPath:      p.backdrop,
			QualityProfileId: profile,
			RootFolderPath:   root,
		})
		if err != nil {
			continue
		}
		added++
		m.mu.Lock()
		_, _ = m.db.ExecContext(ctx,
			`UPDATE movies SET collection_id=?, collection_name=?, monitored=1 WHERE id=?`,
			req.GetCollectionId(), coll.GetName(), resp.GetMovieId(),
		)
		m.mu.Unlock()
	}
	return &mgmntv1.SyncCollectionResponse{
		Added:          int32(added),
		AlreadyPresent: int32(present),
	}, nil
}
