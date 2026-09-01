package internal

import (
	"context"
	"testing"

	metadatav1 "github.com/Muxcore-Media/contracts-metadata/muxcore/metadata/v1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

type fakeTrailersMetadata struct {
	metadatav1.UnimplementedMetadataServiceServer
}

func (fakeTrailersMetadata) GetMovieDetails(context.Context, *metadatav1.GetMovieDetailsRequest) (*metadatav1.GetMovieDetailsResponse, error) {
	return &metadatav1.GetMovieDetailsResponse{
		Videos: []*metadatav1.Video{
			{Name: "Trailer", Type: "Trailer", Site: "YouTube", Key: "abc123"},
		},
	}, nil
}

func TestListTrailers(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	m.metadataClientFn = func(ctx context.Context) (metadatav1.MetadataServiceClient, func(), error) {
		cli, closeFn := newMetadataTestClient(t, fakeTrailersMetadata{})
		return cli, closeFn, nil
	}

	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 550, Title: "Fight Club", Year: 1999})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := m.ListTrailers(ctx, &mgmntv1.ListTrailersRequest{MovieId: add.MovieId})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetTrailers()) != 1 {
		t.Fatalf("trailers: %+v", resp.GetTrailers())
	}
	if resp.GetTrailers()[0].GetUrl() != "https://www.youtube.com/watch?v=abc123" {
		t.Fatalf("url=%q", resp.GetTrailers()[0].GetUrl())
	}
}
