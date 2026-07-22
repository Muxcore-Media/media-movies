package internal

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	metadatav1 "github.com/Muxcore-Media/metadata-tmdb/proto/metadatav1"
)

const (
	titleSourcePrimary  = "primary"
	titleSourceOriginal = "original"
	titleSourceTMDBAlt  = "tmdb_alt"
	titleSourceUser     = "user"
)

var reSpaces = regexp.MustCompile(`\s+`)

func cleanMatchTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '\'' || r == '`' || r == '´':
			// drop apostrophes without inserting space
		default:
			b.WriteByte(' ')
		}
	}
	s = reSpaces.ReplaceAllString(strings.TrimSpace(b.String()), " ")
	for _, art := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(s, art) {
			s = strings.TrimSpace(s[len(art):])
			break
		}
	}
	return s
}

func (m *Module) ensureMovieTitlesTable(ctx context.Context) error {
	if _, err := m.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS movie_titles (
			id TEXT PRIMARY KEY,
			movie_id TEXT NOT NULL,
			title TEXT NOT NULL,
			clean_title TEXT NOT NULL,
			source TEXT NOT NULL,
			UNIQUE(movie_id, clean_title),
			FOREIGN KEY (movie_id) REFERENCES movies(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("create movie_titles: %w", err)
	}
	if _, err := m.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_movie_titles_clean ON movie_titles(clean_title)
	`); err != nil {
		return fmt.Errorf("create movie_titles index: %w", err)
	}
	return nil
}

func (m *Module) backfillMovieTitles(ctx context.Context) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT m.id, m.title, m.original_title FROM movies m
		WHERE NOT EXISTS (
			SELECT 1 FROM movie_titles t WHERE t.movie_id = m.id AND t.source IN ('primary','original')
		)
	`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, title, original string
		if err := rows.Scan(&id, &title, &original); err != nil {
			continue
		}
		m.upsertMovieTitleLocked(ctx, id, title, titleSourcePrimary)
		if original != "" && cleanMatchTitle(original) != cleanMatchTitle(title) {
			m.upsertMovieTitleLocked(ctx, id, original, titleSourceOriginal)
		}
	}
}

func (m *Module) upsertMovieTitleLocked(ctx context.Context, movieID, title, source string) {
	title = strings.TrimSpace(title)
	clean := cleanMatchTitle(title)
	if movieID == "" || clean == "" {
		return
	}
	id := fmt.Sprintf("mt_%s_%s_%d", movieID, source, time.Now().UnixNano())
	_, _ = m.db.ExecContext(ctx, `
		INSERT INTO movie_titles (id, movie_id, title, clean_title, source)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(movie_id, clean_title) DO UPDATE SET
			title = excluded.title,
			source = CASE
				WHEN movie_titles.source = 'user' THEN movie_titles.source
				WHEN excluded.source = 'user' THEN 'user'
				WHEN movie_titles.source = 'primary' THEN movie_titles.source
				WHEN excluded.source = 'primary' THEN 'primary'
				WHEN movie_titles.source = 'original' THEN movie_titles.source
				ELSE excluded.source
			END
	`, id, movieID, title, clean, source)
}

func (m *Module) syncMoviePrimaryTitlesLocked(ctx context.Context, movieID, title, original string) {
	_, _ = m.db.ExecContext(ctx, `DELETE FROM movie_titles WHERE movie_id = ? AND source IN (?, ?)`,
		movieID, titleSourcePrimary, titleSourceOriginal)
	m.upsertMovieTitleLocked(ctx, movieID, title, titleSourcePrimary)
	if original != "" && cleanMatchTitle(original) != cleanMatchTitle(title) {
		m.upsertMovieTitleLocked(ctx, movieID, original, titleSourceOriginal)
	}
}

func (m *Module) replaceTMDBAltTitlesLocked(ctx context.Context, movieID string, titles []string) {
	_, _ = m.db.ExecContext(ctx, `DELETE FROM movie_titles WHERE movie_id = ? AND source = ?`,
		movieID, titleSourceTMDBAlt)
	for _, t := range titles {
		m.upsertMovieTitleLocked(ctx, movieID, t, titleSourceTMDBAlt)
	}
}

func (m *Module) syncMovieTitlesFromTMDB(ctx context.Context, movieID string, tmdbID int32, title, original string) {
	if movieID == "" {
		return
	}
	m.mu.Lock()
	if m.db != nil {
		m.syncMoviePrimaryTitlesLocked(ctx, movieID, title, original)
	}
	m.mu.Unlock()

	if tmdbID == 0 {
		return
	}
	alts, err := m.fetchMovieAlternativeTitles(ctx, tmdbID)
	if err != nil {
		slog.Debug("fetch movie alternative titles", "movie_id", movieID, "error", err)
		return
	}
	m.mu.Lock()
	if m.db != nil {
		m.replaceTMDBAltTitlesLocked(ctx, movieID, alts)
	}
	m.mu.Unlock()
}

func (m *Module) fetchMovieAlternativeTitles(ctx context.Context, tmdbID int32) ([]string, error) {
	metaAddr, err := m.findMetadataAddr(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(metaAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	resp, err := metadatav1.NewMetadataServiceClient(conn).GetAlternativeTitles(ctx, &metadatav1.GetAlternativeTitlesRequest{
		TmdbId: tmdbID,
		Type:   metadatav1.MediaType_MEDIA_TYPE_MOVIE,
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(resp.GetTitles()))
	for _, t := range resp.GetTitles() {
		if s := strings.TrimSpace(t.GetTitle()); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *Module) ListAlternateTitles(ctx context.Context, req *mgmntv1.ListAlternateTitlesRequest) (*mgmntv1.ListAlternateTitlesResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	if req.GetMovieId() == "" {
		return nil, fmt.Errorf("movie_id required")
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT id, title, clean_title, source FROM movie_titles WHERE movie_id = ? ORDER BY source, title`,
		req.GetMovieId(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var titles []*mgmntv1.AlternateTitle
	for rows.Next() {
		var id, title, clean, source string
		if err := rows.Scan(&id, &title, &clean, &source); err != nil {
			continue
		}
		titles = append(titles, &mgmntv1.AlternateTitle{
			Id: id, Title: title, CleanTitle: clean, Source: source,
		})
	}
	return &mgmntv1.ListAlternateTitlesResponse{Titles: titles}, nil
}

func (m *Module) AddAlternateTitle(ctx context.Context, req *mgmntv1.AddAlternateTitleRequest) (*mgmntv1.AddAlternateTitleResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	if req.GetMovieId() == "" || strings.TrimSpace(req.GetTitle()) == "" {
		return nil, fmt.Errorf("movie_id and title required")
	}
	var exists string
	_ = m.db.QueryRowContext(ctx, `SELECT id FROM movies WHERE id = ?`, req.GetMovieId()).Scan(&exists)
	if exists == "" {
		return nil, fmt.Errorf("movie not found: %s", req.GetMovieId())
	}
	title := strings.TrimSpace(req.GetTitle())
	clean := cleanMatchTitle(title)
	id := fmt.Sprintf("mt_%s_user_%d", req.GetMovieId(), time.Now().UnixNano())
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO movie_titles (id, movie_id, title, clean_title, source)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(movie_id, clean_title) DO UPDATE SET
			title = excluded.title,
			source = 'user'
	`, id, req.GetMovieId(), title, clean, titleSourceUser)
	if err != nil {
		return nil, fmt.Errorf("insert alternate title: %w", err)
	}
	var outID, outTitle, outClean, outSource string
	_ = m.db.QueryRowContext(ctx,
		`SELECT id, title, clean_title, source FROM movie_titles WHERE movie_id = ? AND clean_title = ?`,
		req.GetMovieId(), clean,
	).Scan(&outID, &outTitle, &outClean, &outSource)
	return &mgmntv1.AddAlternateTitleResponse{
		Title: &mgmntv1.AlternateTitle{Id: outID, Title: outTitle, CleanTitle: outClean, Source: outSource},
	}, nil
}

func (m *Module) RemoveAlternateTitle(ctx context.Context, req *mgmntv1.RemoveAlternateTitleRequest) (*mgmntv1.RemoveAlternateTitleResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	if req.GetMovieId() == "" || req.GetTitleId() == "" {
		return nil, fmt.Errorf("movie_id and title_id required")
	}
	res, err := m.db.ExecContext(ctx,
		`DELETE FROM movie_titles WHERE movie_id = ? AND id = ? AND source = ?`,
		req.GetMovieId(), req.GetTitleId(), titleSourceUser,
	)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, fmt.Errorf("user alternate title not found")
	}
	return &mgmntv1.RemoveAlternateTitleResponse{}, nil
}
