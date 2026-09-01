package internal

import (
	"context"
	"testing"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

func TestExportImportStateRoundTrip(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId: 42, Title: "Backup Movie", Year: 2024,
		MinimumAvailability: "released",
		ReleaseDate:         "2024-01-01",
	})
	if err != nil {
		t.Fatal(err)
	}

	snap, err := m.ExportState(ctx)
	if err != nil || len(snap) == 0 {
		t.Fatalf("export: %v len=%d", err, len(snap))
	}

	if _, err := m.RemoveMovie(ctx, &mgmntv1.RemoveMovieRequest{MovieId: add.MovieId}); err != nil {
		t.Fatal(err)
	}
	got, err := m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: add.MovieId})
	if err == nil && got.GetMovie() != nil {
		t.Fatal("expected movie removed before import")
	}

	if err := m.ImportState(ctx, snap); err != nil {
		t.Fatal(err)
	}

	restored, err := m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: add.MovieId})
	if err != nil {
		t.Fatal(err)
	}
	if restored.GetMovie().GetTitle() != "Backup Movie" {
		t.Fatalf("restored title=%q", restored.GetMovie().GetTitle())
	}
	if restored.GetMovie().GetMinimumAvailability() != "released" {
		t.Fatalf("minimum_availability=%q", restored.GetMovie().GetMinimumAvailability())
	}

	paths := m.BackupExtraPaths()
	if len(paths) != 1 || paths[0] == "" {
		t.Fatalf("BackupExtraPaths: %+v", paths)
	}
}
