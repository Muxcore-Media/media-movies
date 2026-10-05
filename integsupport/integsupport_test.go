package integsupport

import (
	"context"
	"testing"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

func TestNewTestModuleCRUD(t *testing.T) {
	ctx := context.Background()
	m := NewTestModule(t, Config{})
	if err := m.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 550, Title: "Fight Club", Year: 1999})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	get, err := m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: add.GetMovieId()})
	if err != nil || get.GetMovie().GetTitle() != "Fight Club" {
		t.Fatalf("get: %v %v", get, err)
	}
	list, err := m.ListMovies(ctx, &mgmntv1.ListMoviesRequest{})
	if err != nil || len(list.GetMovies()) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	if _, err := m.RemoveMovie(ctx, &mgmntv1.RemoveMovieRequest{MovieId: add.GetMovieId()}); err != nil {
		t.Fatalf("remove: %v", err)
	}
}

func TestStart(t *testing.T) {
	ctx := context.Background()
	m := NewTestModule(t, Config{})
	if err := Start(ctx, m); err != nil {
		t.Fatalf("start: %v", err)
	}
	if m.GRPCListenAddr() == "127.0.0.1:0" {
		t.Fatal("expected bound port")
	}
}
