package internal

import (
	"context"
	"testing"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

func TestListItemsIgnoresHostileSortBy(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	_, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 1, Title: "Alpha", Year: 2020})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 2, Title: "Beta", Year: 2021})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := m.ListItems(ctx, &mediaadminv1.ListItemsRequest{
		Page: 1, PageSize: 20,
		SortBy:    "title; DROP TABLE movies--",
		SortOrder: "asc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp.Items))
	}
	if resp.Items[0].Title != "Alpha" {
		t.Fatalf("hostile sort_by should default to title asc, got %q first", resp.Items[0].Title)
	}
}

func TestListMoviesSortKeyMapping(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	_, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 10, Title: "Zulu", Year: 2020})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 11, Title: "Alpha", Year: 2021})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := m.ListMovies(ctx, &mgmntv1.ListMoviesRequest{
		Page: 1, PageSize: 20, SortBy: "sort_title", SortOrder: "asc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Movies) != 2 || resp.Movies[0].Title != "Alpha" {
		t.Fatalf("sort_title mapping failed: %+v", resp.Movies)
	}
}
