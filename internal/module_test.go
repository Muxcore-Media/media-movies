package internal

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

func newTestModule(t *testing.T) *Module {
	t.Helper()
	m := NewModule(Config{
		DBPath:   filepath.Join(t.TempDir(), "movies.db"),
		ImageDir: filepath.Join(t.TempDir(), "images"),
		GRPCAddr: ":0",
		HTTPAddr: ":0",
	})
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { m.Stop(ctx) })
	return m
}

func TestModuleInfo(t *testing.T) {
	m := NewModule(Config{})
	info := m.Info()
	if info.ID == "" {
		t.Error("module ID must not be empty")
	}
	if info.Version == "" {
		t.Error("module version must not be empty")
	}
	if len(info.Capabilities) == 0 || info.Capabilities[0] != "media.library" {
		t.Errorf("expected media.library capability, got %v", info.Capabilities)
	}
	if len(info.Roles) == 0 || info.Roles[0] != "media_manager" {
		t.Errorf("expected role media_manager, got %v", info.Roles)
	}
}

func TestAddAndGetMovie(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId:   550,
		Title:    "Fight Club",
		Year:     1999,
		Overview: "A ticking-clock thriller.",
		Genres:   []string{"Drama", "Thriller"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if add.MovieId == "" {
		t.Fatal("expected non-empty movie ID")
	}

	get, err := m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: add.MovieId})
	if err != nil {
		t.Fatal(err)
	}
	if get.Movie.Title != "Fight Club" {
		t.Errorf("expected 'Fight Club', got %s", get.Movie.Title)
	}
	if get.Movie.Year != 1999 {
		t.Errorf("expected year 1999, got %d", get.Movie.Year)
	}
	if len(get.Movie.Genres) != 2 {
		t.Fatalf("expected 2 genres, got %d", len(get.Movie.Genres))
	}
	if get.Movie.Genres[0] != "Drama" {
		t.Errorf("expected genre 'Drama', got %s", get.Movie.Genres[0])
	}
}

func TestAddDuplicateTMDBID(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 550, Title: "Fight Club", Year: 1999})
	m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 550, Title: "Fight Club", Year: 1999})

	movies, err := m.ListMovies(ctx, &mgmntv1.ListMoviesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if movies.Total != 1 {
		t.Errorf("expected 1 movie (unique TMDB ID), got %d", movies.Total)
	}
}

func TestRemoveMovie(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	add, _ := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 550, Title: "Fight Club", Year: 1999})

	_, err := m.RemoveMovie(ctx, &mgmntv1.RemoveMovieRequest{MovieId: add.MovieId})
	if err != nil {
		t.Fatal(err)
	}

	_, err = m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: add.MovieId})
	if err == nil {
		t.Fatal("expected error after removal")
	}
}

func TestListMoviesPagination(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
			TmdbId: int32(100 + i),
			Title:  fmt.Sprintf("Movie %d", i+1),
			Year:   2000 + int32(i),
		})
	}

	page1, err := m.ListMovies(ctx, &mgmntv1.ListMoviesRequest{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Movies) != 2 {
		t.Errorf("expected 2 movies on page 1, got %d", len(page1.Movies))
	}
	if page1.Total != 5 {
		t.Errorf("expected total 5, got %d", page1.Total)
	}

	page3, err := m.ListMovies(ctx, &mgmntv1.ListMoviesRequest{Page: 3, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page3.Movies) != 1 {
		t.Errorf("expected 1 movie on page 3, got %d", len(page3.Movies))
	}
}

func TestSearchMovies(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 1, Title: "The Matrix", Year: 1999})
	m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 2, Title: "The Matrix Reloaded", Year: 2003})
	m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 3, Title: "Inception", Year: 2010})

	resp, err := m.ListMovies(ctx, &mgmntv1.ListMoviesRequest{Search: "matrix"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total != 2 {
		t.Errorf("expected 2 Matrix movies, got %d", resp.Total)
	}
}

func TestGetMediaTypeInfo(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	info, err := m.GetMediaTypeInfo(ctx, &mediaadminv1.GetMediaTypeInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if info.DisplayName != "Movies" {
		t.Errorf("expected 'Movies', got %s", info.DisplayName)
	}
	if len(info.FilterFields) == 0 {
		t.Error("expected filter fields")
	}
}

func TestListItems(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 1, Title: "Test Movie", Year: 2020})

	resp, err := m.ListItems(ctx, &mediaadminv1.ListItemsRequest{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
	if resp.Items[0].Title != "Test Movie" {
		t.Errorf("expected 'Test Movie', got %s", resp.Items[0].Title)
	}
	if resp.Items[0].Metadata["tmdb_id"] != "1" {
		t.Errorf("expected tmdb_id=1 in metadata, got %s", resp.Items[0].Metadata["tmdb_id"])
	}
}

func TestGetItem(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	add, _ := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 550, Title: "Fight Club", Year: 1999})

	resp, err := m.GetItem(ctx, &mediaadminv1.GetItemRequest{Id: add.MovieId})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Item.Title != "Fight Club" {
		t.Errorf("expected 'Fight Club', got %s", resp.Item.Title)
	}
}

func TestUpdateMetadata(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	add, _ := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 550, Title: "Fight Club", Year: 1999})

	updated, err := m.UpdateMetadata(ctx, &mediaadminv1.UpdateMetadataRequest{
		Id:          add.MovieId,
		Title:       "Fight Club (Updated)",
		Description: "New description",
		Year:        1999,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Item.Title != "Fight Club (Updated)" {
		t.Errorf("expected updated title, got %s", updated.Item.Title)
	}
}

func TestListArtwork(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	mod := m
	mod.mu.Lock()
	mod.db.ExecContext(ctx,
		`INSERT INTO movies (id, tmdb_id, title, year, poster_path, backdrop_path, monitored, created_at, updated_at)
		 VALUES ('test123', 1, 'Test', 2020, '/poster.jpg', '/backdrop.jpg', 1, 'now', 'now')`)
	mod.mu.Unlock()

	resp, err := m.ListArtwork(ctx, &mediaadminv1.ListArtworkRequest{Id: "test123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Artwork) != 2 {
		t.Fatalf("expected 2 artwork entries, got %d", len(resp.Artwork))
	}
	if resp.Artwork[0].Type != "poster" {
		t.Errorf("expected first artwork type 'poster', got %s", resp.Artwork[0].Type)
	}
}

func TestHealth(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	if err := m.Health(ctx); err != nil {
		t.Fatal("expected health to pass after init")
	}
}

func TestLifecycle(t *testing.T) {
	m := NewModule(Config{
		DBPath:   filepath.Join(t.TempDir(), "lifecycle.db"),
		ImageDir: filepath.Join(t.TempDir(), "images"),
		GRPCAddr: ":0",
		HTTPAddr: ":0",
	})
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
