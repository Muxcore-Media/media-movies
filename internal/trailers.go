package internal

import (
	"context"
	"fmt"
	"strings"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	metadatav1 "github.com/Muxcore-Media/metadata-tmdb/proto/metadatav1"
)

func (m *Module) ListTrailers(ctx context.Context, req *mgmntv1.ListTrailersRequest) (*mgmntv1.ListTrailersResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	if req.GetMovieId() == "" {
		return nil, fmt.Errorf("movie_id required")
	}
	var tmdbID int64
	if err := m.db.QueryRowContext(ctx, `SELECT tmdb_id FROM movies WHERE id = ?`, req.GetMovieId()).Scan(&tmdbID); err != nil || tmdbID == 0 {
		return nil, fmt.Errorf("movie not found: %s", req.GetMovieId())
	}

	metaClient, closer, err := m.metadataClient(ctx)
	if err != nil {
		return nil, err
	}
	defer closer()

	details, err := metaClient.GetMovieDetails(ctx, &metadatav1.GetMovieDetailsRequest{
		TmdbId:           int32(tmdbID),
		AppendToResponse: []string{"videos"},
	})
	if err != nil {
		return nil, fmt.Errorf("tmdb details: %w", err)
	}

	out := make([]*mgmntv1.Trailer, 0, len(details.GetVideos()))
	for _, v := range details.GetVideos() {
		url := trailerURL(v.GetSite(), v.GetKey())
		if url == "" {
			continue
		}
		out = append(out, &mgmntv1.Trailer{
			Name: v.GetName(),
			Type: v.GetType(),
			Url:  url,
			Site: v.GetSite(),
		})
	}
	return &mgmntv1.ListTrailersResponse{Trailers: out}, nil
}

func trailerURL(site, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(site)) {
	case "youtube", "":
		return "https://www.youtube.com/watch?v=" + key
	case "vimeo":
		return "https://vimeo.com/" + key
	default:
		return ""
	}
}
