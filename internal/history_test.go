package internal

import (
	"context"
	"testing"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	"github.com/Muxcore-Media/core/pkg/contracts"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

func TestHistoryImportDeleteAndList(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 550, Title: "Fight Club", Year: 1999})
	if err != nil {
		t.Fatal(err)
	}
	fileResp, err := m.AddFile(ctx, &mgmntv1.AddFileRequest{
		MovieId: add.MovieId, FilePath: "/media/Fight.Club.mkv", Quality: "Bluray-1080p",
	})
	if err != nil {
		t.Fatal(err)
	}

	hist, err := m.ListHistory(ctx, &mediaadminv1.ListHistoryRequest{Page: 1, PageSize: 20, ItemId: add.MovieId})
	if err != nil {
		t.Fatal(err)
	}
	if hist.Total != 1 || len(hist.Records) != 1 {
		t.Fatalf("expected 1 import history row, got total=%d records=%d", hist.Total, len(hist.Records))
	}
	if hist.Records[0].EventType != historyImport {
		t.Fatalf("expected import, got %s", hist.Records[0].EventType)
	}

	if _, err := m.RemoveFile(ctx, &mgmntv1.RemoveFileRequest{FileId: fileResp.FileId}); err != nil {
		t.Fatal(err)
	}
	hist, err = m.ListHistory(ctx, &mediaadminv1.ListHistoryRequest{Page: 1, PageSize: 20, ItemId: add.MovieId, EventType: historyDeleteFile})
	if err != nil {
		t.Fatal(err)
	}
	if hist.Total != 1 {
		t.Fatalf("expected 1 delete_file row, got %d", hist.Total)
	}

	if _, err := m.RemoveMovie(ctx, &mgmntv1.RemoveMovieRequest{MovieId: add.MovieId}); err != nil {
		t.Fatal(err)
	}
	hist, err = m.ListHistory(ctx, &mediaadminv1.ListHistoryRequest{Page: 1, PageSize: 20, EventType: historyDeleteItem})
	if err != nil {
		t.Fatal(err)
	}
	if hist.Total != 1 {
		t.Fatalf("expected 1 delete_item row, got %d", hist.Total)
	}
	if hist.Records[0].ItemId != add.MovieId {
		t.Fatalf("expected item_id %s, got %s", add.MovieId, hist.Records[0].ItemId)
	}
}

func TestHistoryGrabFromDownloadDispatched(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 680, Title: "Pulp Fiction", Year: 1994})
	if err != nil {
		t.Fatal(err)
	}

	m.handleDownloadDispatched(ctx, contracts.DownloadDispatchedPayload{
		Title: "Pulp.Fiction.1994.1080p", ItemType: "movie", ItemID: add.MovieId,
		Indexer: "test-indexer", DownloadID: "dl-1", GUID: "guid-1",
	})
	m.handleDownloadDispatched(ctx, contracts.DownloadDispatchedPayload{
		Title: "Other", ItemType: "tv", ItemID: "ep_1",
	})

	hist, err := m.ListHistory(ctx, &mediaadminv1.ListHistoryRequest{Page: 1, PageSize: 20, EventType: historyGrab})
	if err != nil {
		t.Fatal(err)
	}
	if hist.Total != 1 {
		t.Fatalf("expected 1 grab, got %d", hist.Total)
	}
	if hist.Records[0].Indexer != "test-indexer" {
		t.Fatalf("expected indexer, got %q", hist.Records[0].Indexer)
	}
}
