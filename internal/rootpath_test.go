package internal

import (
	"context"
	"strings"
	"testing"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

func TestRootFolderSoftUnavailable(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId:         1001,
		Title:          "Soft Root",
		Year:           2020,
		RootFolderPath: "/unregistered/movies",
	})
	if err != nil {
		t.Fatal(err)
	}
	get, err := m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: add.MovieId})
	if err != nil {
		t.Fatal(err)
	}
	if get.Movie.RootFolderPath != "/unregistered/movies" {
		t.Errorf("path: %q", get.Movie.RootFolderPath)
	}
}

func TestRootFolderRejectsUnknownWhenAvailable(t *testing.T) {
	m := newTestModule(t)
	m.rootsListFn = func(ctx context.Context, mediaKind string) ([]string, error) {
		return []string{"/media/movies"}, nil
	}
	ctx := context.Background()

	_, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId:         1002,
		Title:          "Bad Root",
		Year:           2021,
		RootFolderPath: "/elsewhere",
	})
	if err == nil || !strings.Contains(err.Error(), "not a registered root") {
		t.Fatalf("expected registered root error, got %v", err)
	}
}

func TestRootFolderAcceptsRegistered(t *testing.T) {
	m := newTestModule(t)
	m.rootsListFn = func(ctx context.Context, mediaKind string) ([]string, error) {
		if mediaKind != "movies" {
			t.Errorf("mediaKind: %q", mediaKind)
		}
		return []string{"/media/movies"}, nil
	}
	ctx := context.Background()

	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId:         1003,
		Title:          "Good Root",
		Year:           2022,
		RootFolderPath: "/media/movies/",
	})
	if err != nil {
		t.Fatal(err)
	}
	get, err := m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: add.MovieId})
	if err != nil {
		t.Fatal(err)
	}
	if get.Movie.RootFolderPath != "/media/movies" {
		t.Errorf("normalized path: %q", get.Movie.RootFolderPath)
	}

	bad := "/nope"
	_, err = m.UpdateMovie(ctx, &mgmntv1.UpdateMovieRequest{
		MovieId:        add.MovieId,
		RootFolderPath: &bad,
	})
	if err == nil || !strings.Contains(err.Error(), "not a registered root") {
		t.Fatalf("expected update reject, got %v", err)
	}

	empty := ""
	upd, err := m.UpdateMovie(ctx, &mgmntv1.UpdateMovieRequest{
		MovieId:        add.MovieId,
		RootFolderPath: &empty,
	})
	if err != nil {
		t.Fatal(err)
	}
	if upd.Movie.RootFolderPath != "" {
		t.Errorf("cleared path: %q", upd.Movie.RootFolderPath)
	}
}

func TestRootFolderRejectsRelative(t *testing.T) {
	m := newTestModule(t)
	_, err := m.AddMovie(context.Background(), &mgmntv1.AddMovieRequest{
		TmdbId:         1004,
		Title:          "Rel",
		Year:           2023,
		RootFolderPath: "relative/path",
	})
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected absolute error, got %v", err)
	}
}
