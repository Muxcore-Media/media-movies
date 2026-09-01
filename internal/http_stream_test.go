package internal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

func TestHandleStreamMovieAuthAndRoot(t *testing.T) {
	root := t.TempDir()
	movieFile := filepath.Join(root, "film.mkv")
	if err := os.WriteFile(movieFile, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.mkv")
	if err := os.WriteFile(outside, []byte("nope"), 0600); err != nil {
		t.Fatal(err)
	}

	m := NewModule(Config{
		DBPath:    filepath.Join(t.TempDir(), "movies.db"),
		ImageDir:  filepath.Join(t.TempDir(), "images"),
		GRPCAddr:  ":0",
		HTTPAddr:  "127.0.0.1:0",
		HTTPToken: "test-token",
	})
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(ctx) })

	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId: 1, Title: "Film", Year: 2020, RootFolderPath: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddFile(ctx, &mgmntv1.AddFileRequest{
		MovieId: add.MovieId, FilePath: movieFile, Quality: "1080p",
	}); err != nil {
		t.Fatal(err)
	}

	m.mu.Lock()
	_, _ = m.db.ExecContext(ctx, `UPDATE movie_files SET file_path=? WHERE movie_id=?`, outside, add.MovieId)
	m.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/stream/movies/"+add.MovieId, nil)
	rec := httptest.NewRecorder()
	m.handleStreamMovie(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status=%d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/stream/movies/"+add.MovieId, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	m.handleStreamMovie(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outside root status=%d", rec.Code)
	}

	m.mu.Lock()
	_, _ = m.db.ExecContext(ctx, `UPDATE movie_files SET file_path=? WHERE movie_id=?`, movieFile, add.MovieId)
	m.mu.Unlock()

	req = httptest.NewRequest(http.MethodGet, "/stream/movies/"+add.MovieId, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	m.handleStreamMovie(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("in-root status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestListMissingExcludesFutureRelease(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	future := "2099-12-31"
	_, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId: 99, Title: "Future Film", Year: 2099,
		MinimumAvailability: "released",
		ReleaseDate:         future,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId: 100, Title: "Ready Film", Year: 2020,
		MinimumAvailability: "released",
		ReleaseDate:         "2020-01-01",
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := m.ListMissing(ctx, &mgmntv1.ListMissingRequest{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetTotal() != 1 || len(resp.GetItems()) != 1 {
		t.Fatalf("missing=%+v", resp)
	}
	if resp.GetItems()[0].GetTitle() != "Ready Film" {
		t.Fatalf("item=%+v", resp.GetItems()[0])
	}
}
