package internal

import (
	"context"
	"testing"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

func TestCleanMatchTitle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Fight Club", "fight club"},
		{"Fight.Club", "fight club"},
		{"The Matrix", "matrix"},
		{"A Clockwork Orange", "clockwork orange"},
		{"An American Tail", "american tail"},
		{"  Pan's Labyrinth ", "pans labyrinth"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := cleanMatchTitle(tc.in); got != tc.want {
			t.Errorf("cleanMatchTitle(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestFindMovieIDByAlternateTitles(t *testing.T) {
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

	m.mu.Lock()
	m.upsertMovieTitleLocked(ctx, add.MovieId, "Бойцовский клуб", titleSourceOriginal)
	m.upsertMovieTitleLocked(ctx, add.MovieId, "Fight Club: Special Edition", titleSourceTMDBAlt)
	m.mu.Unlock()

	if id := m.findMovieID(0, "Fight.Club", 1999); id != add.MovieId {
		t.Errorf("scene title: got %q want %q", id, add.MovieId)
	}
	if id := m.findMovieID(0, "Fight Club: Special Edition", 1999); id != add.MovieId {
		t.Errorf("tmdb alt: got %q want %q", id, add.MovieId)
	}
	if id := m.findMovieID(0, "Fight Club", 2000); id != "" {
		t.Errorf("year mismatch should miss, got %q", id)
	}

	_, err = m.AddAlternateTitle(ctx, &mgmntv1.AddAlternateTitleRequest{
		MovieId: add.MovieId,
		Title:   "Fightclub",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id := m.findMovieID(0, "Fightclub", 1999); id != add.MovieId {
		t.Errorf("user alt: got %q want %q", id, add.MovieId)
	}

	listed, err := m.ListAlternateTitles(ctx, &mgmntv1.ListAlternateTitlesRequest{MovieId: add.MovieId})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Titles) < 2 {
		t.Fatalf("expected multiple titles, got %d", len(listed.Titles))
	}
}
