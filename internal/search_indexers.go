package internal

import (
	"context"
	"fmt"
	"strings"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	"github.com/Muxcore-Media/core/sdk/go/module/meshtls"
	automationv1 "github.com/Muxcore-Media/media-automation/proto/automationv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const capMediaAutomation = "media.automation"

func (m *Module) SearchIndexers(ctx context.Context, req *mediaadminv1.SearchIndexersRequest) (*mediaadminv1.SearchIndexersResponse, error) {
	if req.GetItemId() == "" {
		return nil, status.Error(codes.InvalidArgument, "item_id required")
	}

	title, year, tmdbID, qualityProfileID, err := m.lookupMovieForIndexerSearch(ctx, req.GetItemId())
	if err != nil {
		return nil, err
	}

	limit := req.GetLimit()
	if limit <= 0 {
		limit = 50
	}

	searchResp, err := m.automationSearchItem(ctx, &automationv1.SearchItemRequest{
		ItemType:         "movie",
		Query:            title,
		TmdbId:           tmdbID,
		Year:             year,
		Limit:            limit,
		QualityProfileId: qualityProfileID,
	})
	if err != nil {
		return emptySearchIndexersResponse(), nil
	}

	results := releaseMatchesToIndexerResults(searchResp.GetMatches())
	return &mediaadminv1.SearchIndexersResponse{
		Results: results,
		Total:   int32(len(results)),
	}, nil
}

func (m *Module) lookupMovieForIndexerSearch(ctx context.Context, itemID string) (title string, year, tmdbID int32, qualityProfileID string, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return "", 0, 0, "", fmt.Errorf("not initialized")
	}
	var y, tmdb int64
	err = m.db.QueryRowContext(ctx,
		`SELECT title, year, tmdb_id, quality_profile_id FROM movies WHERE id = ?`, itemID,
	).Scan(&title, &y, &tmdb, &qualityProfileID)
	if err != nil {
		return "", 0, 0, "", status.Errorf(codes.NotFound, "movie not found: %s", itemID)
	}
	return title, int32(y), int32(tmdb), qualityProfileID, nil
}

func (m *Module) automationSearchItem(ctx context.Context, req *automationv1.SearchItemRequest) (*automationv1.SearchItemResponse, error) {
	if m.automationSearchFn != nil {
		return m.automationSearchFn(ctx, req)
	}
	if m.coreClient() == nil {
		return nil, fmt.Errorf("not connected to core")
	}
	addr, err := m.findAutomationAddr(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := meshtls.Dial(addr)
	if err != nil {
		return nil, fmt.Errorf("dial automation: %w", err)
	}
	defer conn.Close()
	return automationv1.NewAutomationServiceClient(conn).SearchItem(ctx, req)
}

func (m *Module) findAutomationAddr(ctx context.Context) (string, error) {
	mc := m.coreClient()
	if mc == nil {
		return "", fmt.Errorf("not connected to core")
	}
	modules, err := mc.Discovery.FindByCapability(ctx, capMediaAutomation)
	if err != nil {
		return "", fmt.Errorf("discover %s: %w", capMediaAutomation, err)
	}
	for _, mod := range modules {
		addr := dialAddrForModule(mod.Id, mod.HttpAddr)
		if addr != "" {
			return addr, nil
		}
	}
	return "", fmt.Errorf("no %s module found", capMediaAutomation)
}

func emptySearchIndexersResponse() *mediaadminv1.SearchIndexersResponse {
	return &mediaadminv1.SearchIndexersResponse{}
}

func releaseMatchesToIndexerResults(matches []*automationv1.ReleaseMatch) []*mediaadminv1.IndexerResult {
	results := make([]*mediaadminv1.IndexerResult, 0, len(matches))
	for _, match := range matches {
		if match == nil {
			continue
		}
		results = append(results, releaseMatchToIndexerResult(match))
	}
	return results
}

func releaseMatchToIndexerResult(match *automationv1.ReleaseMatch) *mediaadminv1.IndexerResult {
	return &mediaadminv1.IndexerResult{
		Title:       match.GetTitle(),
		Guid:        match.GetGuid(),
		Indexer:     match.GetIndexerName(),
		Size:        match.GetSize(),
		Quality:     qualityFromReleaseTitle(match.GetTitle()),
		Seeders:     match.GetSeeders(),
		DownloadUrl: match.GetDownloadUrl(),
		Approved:    true,
	}
}

func qualityFromReleaseTitle(title string) string {
	upper := strings.ToUpper(title)
	for _, q := range []struct {
		needle string
		out    string
	}{
		{"2160P", "2160p"},
		{"1080P", "1080p"},
		{"720P", "720p"},
		{"480P", "480p"},
		{"4K", "2160p"},
	} {
		if strings.Contains(upper, q.needle) {
			return q.out
		}
	}
	return ""
}
