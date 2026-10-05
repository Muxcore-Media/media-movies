package internal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

func TestRootFolderFailsClosedWhenRootsUnavailable(t *testing.T) {
	m := newTestModule(t)
	m.rootsListFn = nil // no mesh in unit tests => roots unavailable
	ctx := context.Background()

	_, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId: 1001, Title: "Closed Root", Year: 2020, RootFolderPath: "/unregistered/movies",
	})
	if err == nil || !strings.Contains(err.Error(), "refusing path") {
		t.Fatalf("expected fail-closed error, got %v", err)
	}
	// Empty root stays allowed.
	if _, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 1002, Title: "No Root", Year: 2020}); err != nil {
		t.Fatal(err)
	}
	// File paths are refused too.
	add, _ := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 1003, Title: "F", Year: 2020})
	if _, err := m.AddFile(ctx, &mgmntv1.AddFileRequest{MovieId: add.MovieId, FilePath: "/media/f.mkv"}); err == nil {
		t.Fatal("AddFile must fail closed when roots are unavailable")
	}
	// Collection prefs root as well.
	if _, err := m.SetCollectionMonitored(ctx, &mgmntv1.SetCollectionMonitoredRequest{CollectionId: 5, RootFolderPath: "/x"}); err == nil {
		t.Fatal("SetCollectionMonitored must fail closed")
	}
}

func TestAddFileConfinedToRegisteredRoots(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	root := t.TempDir()
	outside := t.TempDir()
	// sibling-prefix directory: <root>-evil
	evil := root + "-evil"
	if err := os.MkdirAll(evil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(evil) })
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	setRoots(m, root)
	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 2001, Title: "C", Year: 2020, RootFolderPath: root})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"traversal":       root + "/../etc/passwd",
		"sibling prefix":  filepath.Join(evil, "a.mkv"),
		"symlink escape":  filepath.Join(root, "link", "a.mkv"),
		"relative-dotdot": "movies/../../a.mkv",
		"outside":         filepath.Join(outside, "a.mkv"),
	}
	for name, p := range cases {
		if _, err := m.AddFile(ctx, &mgmntv1.AddFileRequest{MovieId: add.MovieId, FilePath: p}); err == nil {
			t.Errorf("%s: AddFile(%q) must be rejected", name, p)
		}
	}
	if _, err := m.AddFile(ctx, &mgmntv1.AddFileRequest{MovieId: add.MovieId, FilePath: filepath.Join(root, "ok", "a.mkv")}); err != nil {
		t.Errorf("in-root file rejected: %v", err)
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
