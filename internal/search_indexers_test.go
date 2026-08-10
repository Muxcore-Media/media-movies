package internal

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	automationv1 "github.com/Muxcore-Media/media-automation/proto/automationv1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

func TestSearchIndexersNotFound(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	_, err := m.SearchIndexers(ctx, &mediaadminv1.SearchIndexersRequest{ItemId: "missing"})
	if err == nil {
		t.Fatal("expected not found")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestSearchIndexersNilMeshReturnsEmpty(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId: 550,
		Title:  "Fight Club",
		Year:   1999,
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := m.SearchIndexers(ctx, &mediaadminv1.SearchIndexersRequest{ItemId: add.MovieId})
	if err != nil {
		t.Fatalf("expected soft empty result, got error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if len(resp.GetResults()) != 0 || resp.GetTotal() != 0 {
		t.Fatalf("expected empty results, got %+v", resp)
	}
}

func TestReleaseMatchToIndexerResult(t *testing.T) {
	match := &automationv1.ReleaseMatch{
		Guid:        "guid-1",
		Title:       "Fight Club 1999 1080p BluRay x265",
		Size:        4_500_000_000,
		Seeders:     42,
		IndexerName: "TestIndexer",
		DownloadUrl: "magnet:?xt=urn:btih:abc",
		Score:       100,
	}
	got := releaseMatchToIndexerResult(match)
	if got.GetTitle() != match.GetTitle() {
		t.Errorf("title: got %q want %q", got.GetTitle(), match.GetTitle())
	}
	if got.GetGuid() != match.GetGuid() {
		t.Errorf("guid: got %q want %q", got.GetGuid(), match.GetGuid())
	}
	if got.GetIndexer() != match.GetIndexerName() {
		t.Errorf("indexer: got %q want %q", got.GetIndexer(), match.GetIndexerName())
	}
	if got.GetQuality() != "1080p" {
		t.Errorf("quality: got %q want 1080p", got.GetQuality())
	}
	if !got.GetApproved() {
		t.Error("expected approved=true")
	}
}

func TestSearchIndexersUsesAutomationStub(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId: 550,
		Title:  "Fight Club",
		Year:   1999,
	})
	if err != nil {
		t.Fatal(err)
	}

	var captured *automationv1.SearchItemRequest
	m.automationSearchFn = func(ctx context.Context, req *automationv1.SearchItemRequest) (*automationv1.SearchItemResponse, error) {
		captured = req
		return &automationv1.SearchItemResponse{
			Matches: []*automationv1.ReleaseMatch{{
				Guid:        "g1",
				Title:       "Fight Club 1999 720p WEB-DL",
				IndexerName: "StubIndexer",
				DownloadUrl: "magnet:test",
				Score:       50,
			}},
		}, nil
	}

	resp, err := m.SearchIndexers(ctx, &mediaadminv1.SearchIndexersRequest{ItemId: add.MovieId, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if captured == nil {
		t.Fatal("expected automation SearchItem to be called")
	}
	if captured.GetItemType() != "movie" {
		t.Errorf("item_type: got %q want movie", captured.GetItemType())
	}
	if captured.GetQuery() != "Fight Club" {
		t.Errorf("query: got %q want Fight Club", captured.GetQuery())
	}
	if captured.GetYear() != 1999 {
		t.Errorf("year: got %d want 1999", captured.GetYear())
	}
	if captured.GetLimit() != 10 {
		t.Errorf("limit: got %d want 10", captured.GetLimit())
	}
	if len(resp.GetResults()) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.GetResults()))
	}
	if resp.GetResults()[0].GetQuality() != "720p" {
		t.Errorf("mapped quality: got %q want 720p", resp.GetResults()[0].GetQuality())
	}
}
