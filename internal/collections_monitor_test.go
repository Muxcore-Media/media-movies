package internal

import (
	"context"
	"testing"

	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"
	metadatav1 "github.com/Muxcore-Media/contracts-metadata/muxcore/metadata/v1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

type fakeMetadata struct {
	metadatav1.UnimplementedMetadataServiceServer
}

func (fakeMetadata) GetCollection(context.Context, *metadatav1.GetCollectionRequest) (*metadatav1.GetCollectionResponse, error) {
	return &metadatav1.GetCollectionResponse{
		Id:   100,
		Name: "Test Collection",
		Parts: []*metadatav1.CollectionPart{
			{Id: 501, Title: "Part One", ReleaseDate: "2020-01-01"},
		},
	}, nil
}

func TestCollectionPrefsAndSyncSearchOnAdd(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	m.metadataClientFn = func(ctx context.Context) (metadatav1.MetadataServiceClient, func(), error) {
		cli, closeFn := newMetadataTestClient(t, fakeMetadata{})
		return cli, closeFn, nil
	}
	var searched int
	m.automationSearchFn = func(ctx context.Context, req *automationv1.SearchItemRequest) (*automationv1.SearchItemResponse, error) {
		searched++
		if req.GetTmdbId() != 501 {
			t.Fatalf("search tmdb_id=%d", req.GetTmdbId())
		}
		return &automationv1.SearchItemResponse{}, nil
	}

	if _, err := m.SetCollectionMonitored(ctx, &mgmntv1.SetCollectionMonitoredRequest{
		CollectionId: 100, Monitored: true, SearchOnAdd: true,
		QualityProfileId: "qp1", RootFolderPath: "/movies",
	}); err != nil {
		t.Fatal(err)
	}

	prefs, err := m.GetCollectionPrefs(ctx, &mgmntv1.GetCollectionPrefsRequest{CollectionId: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !prefs.GetMonitored() || !prefs.GetSearchOnAdd() {
		t.Fatalf("prefs: %+v", prefs)
	}

	resp, err := m.SyncCollection(ctx, &mgmntv1.SyncCollectionRequest{
		CollectionId: 100, AddMissing: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetAdded() != 1 {
		t.Fatalf("added=%d", resp.GetAdded())
	}
	if searched != 1 {
		t.Fatalf("expected automation search once, got %d", searched)
	}
}
