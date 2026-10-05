package internal

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	"github.com/Muxcore-Media/core/pkg/contracts"
	automationv1 "github.com/Muxcore-Media/media-automation/proto/automationv1"
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
	m.rootsListFn = func(context.Context, string) ([]string, error) {
		return []string{"/media/movies", "/media/movies-uhd", "/media", "/movies", "/tmp", "/data/media/Movies"}, nil
	}
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(ctx) })
	return m
}

// setRoots overrides the registered movie roots for a test.
func setRoots(m *Module, roots ...string) {
	m.rootsListFn = func(context.Context, string) ([]string, error) { return roots, nil }
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
	caps := map[string]bool{}
	for _, c := range info.Capabilities {
		caps[c] = true
	}
	if !caps["media.library"] || !caps["media.library.movies"] {
		t.Errorf("expected media.library and media.library.movies, got %v", info.Capabilities)
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

	if _, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 550, Title: "Fight Club", Year: 1999}); err != nil {
		t.Fatal(err)
	}
	// The duplicate add may be rejected or merged; the resulting list is asserted below.
	_, _ = m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 550, Title: "Fight Club", Year: 1999})

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

func TestRemoveMovieDeleteFiles(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	root := t.TempDir()
	setRoots(m, root)
	dir := filepath.Join(root, "Movies", "Fight Club (1999)")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "Fight Club.1999.mkv")
	if err := os.WriteFile(f, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId: 550, Title: "Fight Club", Year: 1999, RootFolderPath: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	fileResp, err := m.AddFile(ctx, &mgmntv1.AddFileRequest{MovieId: add.MovieId, FilePath: f, Quality: "1080p"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = m.RemoveFile(ctx, &mgmntv1.RemoveFileRequest{FileId: fileResp.FileId})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f); err != nil {
		t.Fatal("delete_files=false must leave file")
	}

	_, err = m.AddFile(ctx, &mgmntv1.AddFileRequest{MovieId: add.MovieId, FilePath: f, Quality: "1080p"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.RemoveMovie(ctx, &mgmntv1.RemoveMovieRequest{MovieId: add.MovieId, DeleteFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatal("expected media file deleted")
	}
}

func TestListMoviesPagination(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
			TmdbId: int32(100 + i),
			Title:  fmt.Sprintf("Movie %d", i+1),
			Year:   2000 + int32(i),
		}); err != nil {
			t.Fatal(err)
		}
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

	if _, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 1, Title: "The Matrix", Year: 1999}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 2, Title: "The Matrix Reloaded", Year: 2003}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 3, Title: "Inception", Year: 2010}); err != nil {
		t.Fatal(err)
	}

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
	want := map[mediaadminv1.Feature]bool{
		mediaadminv1.Feature_FEATURE_MISSING:     true,
		mediaadminv1.Feature_FEATURE_TAGS:        true,
		mediaadminv1.Feature_FEATURE_COLLECTIONS: true,
	}
	for _, f := range info.Features {
		delete(want, f)
	}
	if len(want) != 0 {
		t.Errorf("missing features: %v", want)
	}
}

func TestListItems(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	if _, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 1, Title: "Test Movie", Year: 2020}); err != nil {
		t.Fatal(err)
	}

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

	relPoster := "test123/poster.jpg"
	relBackdrop := "test123/backdrop.jpg"
	if err := os.MkdirAll(filepath.Join(m.imageDir, "test123"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.imageDir, relPoster), []byte("poster"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.imageDir, relBackdrop), []byte("backdrop"), 0600); err != nil {
		t.Fatal(err)
	}

	m.mu.Lock()
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO movies (id, tmdb_id, title, year, poster_path, backdrop_path, monitored, created_at, updated_at)
		 VALUES ('test123', 1, 'Test', 2020, ?, ?, 1, 'now', 'now')`, relPoster, relBackdrop)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	resp, err := m.ListArtwork(ctx, &mediaadminv1.ListArtworkRequest{Id: "test123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Artwork) != 2 {
		t.Fatalf("expected 2 artwork entries, got %d", len(resp.Artwork))
	}
	if resp.Artwork[0].Type != mediaadminv1.ArtworkType_ARTWORK_TYPE_POSTER {
		t.Errorf("expected first artwork type poster, got %v", resp.Artwork[0].Type)
	}
	if !strings.Contains(resp.Artwork[0].Url, "/images/"+relPoster) {
		t.Errorf("unexpected poster url: %s", resp.Artwork[0].Url)
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

func TestFindMovieIDByTMDBAndTitle(t *testing.T) {
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

	if id := m.findMovieID(550, "", 0); id != add.MovieId {
		t.Errorf("tmdb match: got %q want %q", id, add.MovieId)
	}
	if id := m.findMovieID(0, "fight club", 1999); id != add.MovieId {
		t.Errorf("title+year match: got %q want %q", id, add.MovieId)
	}
	if id := m.findMovieID(0, "Fight Club", 0); id != add.MovieId {
		t.Errorf("title year-optional: got %q want %q", id, add.MovieId)
	}
	if id := m.findMovieID(999, "Nope", 2022); id != "" {
		t.Errorf("expected no match, got %q", id)
	}
}

func TestHandleFileImportedMatchesExisting(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId: 680,
		Title:  "Pulp Fiction",
		Year:   1994,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = m.handleFileImported(ctx, contracts.FileImportedPayload{
		MediaType:       "movie",
		Title:           "Pulp Fiction",
		Year:            1994,
		TMDBID:          680,
		StorageKey:      "media/Movies/Pulp Fiction (1994)/Pulp.Fiction.1994.mkv",
		DestinationPath: "/data/media/Movies/Pulp Fiction (1994)/Pulp.Fiction.1994.mkv",
		Quality:         "1080p",
	})
	if err != nil {
		t.Fatal(err)
	}

	files, err := m.ListFiles(ctx, &mgmntv1.ListFilesRequest{MovieId: add.MovieId})
	if err != nil {
		t.Fatal(err)
	}
	if len(files.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files.Files))
	}
	want := "/data/media/Movies/Pulp Fiction (1994)/Pulp.Fiction.1994.mkv"
	if files.Files[0].FilePath != want {
		t.Errorf("unexpected path %s (want absolute destination)", files.Files[0].FilePath)
	}
}

func TestAddMovieReturnsExistingID(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	first, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 11, Title: "Star Wars", Year: 1977})
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 11, Title: "Star Wars", Year: 1977})
	if err != nil {
		t.Fatal(err)
	}
	if first.MovieId != second.MovieId {
		t.Errorf("expected same movie id, got %s vs %s", first.MovieId, second.MovieId)
	}
}

func TestMovieQualityProfileBinding(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	profileID := "qp_test_1"
	root := "/media/movies"
	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId:           680,
		Title:            "Pulp Fiction",
		Year:             1994,
		QualityProfileId: profileID,
		RootFolderPath:   root,
	})
	if err != nil {
		t.Fatal(err)
	}

	get, err := m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: add.MovieId})
	if err != nil {
		t.Fatal(err)
	}
	if get.Movie.QualityProfileId != profileID {
		t.Errorf("quality_profile_id: got %q want %q", get.Movie.QualityProfileId, profileID)
	}
	if get.Movie.RootFolderPath != root {
		t.Errorf("root_folder_path: got %q want %q", get.Movie.RootFolderPath, root)
	}

	newProfile := "qp_test_2"
	newRoot := "/media/movies-uhd"
	upd, err := m.UpdateMovie(ctx, &mgmntv1.UpdateMovieRequest{
		MovieId:          add.MovieId,
		QualityProfileId: &newProfile,
		RootFolderPath:   &newRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if upd.Movie.QualityProfileId != newProfile || upd.Movie.RootFolderPath != newRoot {
		t.Errorf("update binding: got profile=%q root=%q", upd.Movie.QualityProfileId, upd.Movie.RootFolderPath)
	}

	item, err := m.GetItem(ctx, &mediaadminv1.GetItemRequest{Id: add.MovieId})
	if err != nil {
		t.Fatal(err)
	}
	if item.Item.Metadata["quality_profile_id"] != newProfile {
		t.Errorf("admin metadata quality_profile_id: %q", item.Item.Metadata["quality_profile_id"])
	}
	if item.Item.Metadata["root_folder_path"] != newRoot {
		t.Errorf("admin metadata root_folder_path: %q", item.Item.Metadata["root_folder_path"])
	}
}

func TestDeleteItem(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 550, Title: "Fight Club", Year: 1999})
	if err != nil {
		t.Fatal(err)
	}
	artDir := filepath.Join(m.imageDir, add.MovieId)
	if err := os.MkdirAll(artDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artDir, "poster.jpg"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := m.DeleteItem(ctx, &mediaadminv1.DeleteItemRequest{Id: add.MovieId}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: add.MovieId}); err == nil {
		t.Fatal("expected error after delete")
	}
	if _, err := os.Stat(artDir); !os.IsNotExist(err) {
		t.Fatalf("expected artwork dir removed, err=%v", err)
	}
}

func TestRefreshItemNotFound(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	_, err := m.RefreshItem(ctx, &mediaadminv1.RefreshItemRequest{Id: "missing"})
	if err == nil {
		t.Fatal("expected not found")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestCacheRemoteArtwork(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
	}))
	t.Cleanup(srv.Close)
	allowLoopbackArtwork(t, srv)

	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 1, Title: "Art", Year: 2020})
	if err != nil {
		t.Fatal(err)
	}

	rel, mime, err := m.cacheRemoteArtwork(ctx, add.MovieId, "poster", srv.URL+"/poster.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if rel != add.MovieId+"/poster.jpg" {
		t.Errorf("rel path: got %q", rel)
	}
	if !strings.Contains(mime, "jpeg") {
		t.Errorf("mime: %q", mime)
	}
	if !m.localArtworkExists(rel) {
		t.Fatal("expected local file")
	}

	m.persistCachedArtwork(ctx, add.MovieId, srv.URL+"/poster.jpg", srv.URL+"/backdrop.jpg")
	var poster string
	m.mu.RLock()
	_ = m.db.QueryRowContext(ctx, `SELECT poster_path FROM movies WHERE id = ?`, add.MovieId).Scan(&poster)
	m.mu.RUnlock()
	if poster != add.MovieId+"/poster.jpg" {
		t.Errorf("db poster_path: got %q", poster)
	}
}

func TestReplaceArtwork(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 2, Title: "Replace", Year: 2021})
	if err != nil {
		t.Fatal(err)
	}

	stream := &fakeReplaceStream{
		ctx: ctx,
		msgs: []*mediaadminv1.ReplaceArtworkRequest{
			{Data: &mediaadminv1.ReplaceArtworkRequest_ItemId{ItemId: add.MovieId}},
			{Data: &mediaadminv1.ReplaceArtworkRequest_ArtworkType{ArtworkType: mediaadminv1.ArtworkType_ARTWORK_TYPE_POSTER}},
			{Data: &mediaadminv1.ReplaceArtworkRequest_Filename{Filename: "custom.png"}},
			{Data: &mediaadminv1.ReplaceArtworkRequest_Chunk{Chunk: []byte{0x89, 0x50, 0x4e, 0x47}}},
		},
	}
	if err := m.ReplaceArtwork(stream); err != nil {
		t.Fatal(err)
	}
	if stream.resp == nil || stream.resp.Artwork == nil {
		t.Fatal("expected artwork response")
	}
	rel := add.MovieId + "/poster.png"
	if !m.localArtworkExists(rel) {
		t.Fatal("expected written poster.png")
	}
	list, err := m.ListArtwork(ctx, &mediaadminv1.ListArtworkRequest{Id: add.MovieId})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Artwork) < 1 {
		t.Fatal("expected list artwork")
	}
	if !strings.Contains(list.Artwork[0].Url, "/images/"+rel) {
		t.Errorf("url: %s", list.Artwork[0].Url)
	}
}

type fakeReplaceStream struct {
	grpc.ServerStream
	ctx  context.Context
	msgs []*mediaadminv1.ReplaceArtworkRequest
	idx  int
	resp *mediaadminv1.ReplaceArtworkResponse
}

func (s *fakeReplaceStream) Context() context.Context { return s.ctx }

func (s *fakeReplaceStream) Recv() (*mediaadminv1.ReplaceArtworkRequest, error) {
	if s.idx >= len(s.msgs) {
		return nil, io.EOF
	}
	msg := s.msgs[s.idx]
	s.idx++
	return msg, nil
}

func (s *fakeReplaceStream) SendAndClose(resp *mediaadminv1.ReplaceArtworkResponse) error {
	s.resp = resp
	return nil
}

func (s *fakeReplaceStream) SetHeader(metadata.MD) error  { return nil }
func (s *fakeReplaceStream) SendHeader(metadata.MD) error { return nil }
func (s *fakeReplaceStream) SetTrailer(metadata.MD)       {}
func (s *fakeReplaceStream) SendMsg(any) error            { return nil }
func (s *fakeReplaceStream) RecvMsg(any) error            { return nil }

func TestListMissing(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	missing, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId: 550, Title: "Fight Club", Year: 1999, QualityProfileId: "qp1", RootFolderPath: "/movies",
	})
	if err != nil {
		t.Fatal(err)
	}
	hasFile, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId: 680, Title: "Pulp Fiction", Year: 1994,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddFile(ctx, &mgmntv1.AddFileRequest{
		MovieId: hasFile.MovieId, FilePath: "/movies/pulp.mkv", Quality: "Bluray-1080p",
	}); err != nil {
		t.Fatal(err)
	}
	unmon := false
	unmonitored, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId: 13, Title: "Forrest Gump", Year: 1994,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateMovie(ctx, &mgmntv1.UpdateMovieRequest{
		MovieId: unmonitored.MovieId, Monitored: &unmon,
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := m.ListMissing(ctx, &mgmntv1.ListMissingRequest{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected 1 missing, got %d", resp.Total)
	}
	if resp.Items[0].MovieId != missing.MovieId {
		t.Errorf("expected %s, got %s", missing.MovieId, resp.Items[0].MovieId)
	}
	if resp.Items[0].QualityProfileId != "qp1" || resp.Items[0].RootFolderPath != "/movies" {
		t.Errorf("profile/root: %+v", resp.Items[0])
	}
}

func TestTagsAndCollections(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 550, Title: "Fight Club", Year: 1999})
	if err != nil {
		t.Fatal(err)
	}
	tag, err := m.CreateTag(ctx, &mgmntv1.CreateTagRequest{Label: "favorites"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.SetItemTags(ctx, &mgmntv1.SetItemTagsRequest{ItemId: add.MovieId, TagIds: []string{tag.TagId}})
	if err != nil {
		t.Fatal(err)
	}
	list, err := m.ListMovies(ctx, &mgmntv1.ListMoviesRequest{Page: 1, PageSize: 20, TagId: tag.TagId})
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 {
		t.Fatalf("tag filter=%d", list.Total)
	}
	got, err := m.GetItemTags(ctx, &mgmntv1.GetItemTagsRequest{ItemId: add.MovieId})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 1 || got.Tags[0].Id != tag.TagId || got.Tags[0].Label != "favorites" {
		t.Fatalf("item tags: %+v", got.Tags)
	}

	m.mu.Lock()
	_, err = m.db.ExecContext(ctx, `UPDATE movies SET collection_id=10, collection_name='Fight Club Collection' WHERE id=?`, add.MovieId)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	cols, err := m.ListCollections(ctx, &mgmntv1.ListCollectionsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cols.Collections) != 1 || cols.Collections[0].CollectionId != 10 {
		t.Fatalf("collections: %+v", cols.Collections)
	}
	cm, err := m.GetCollectionMovies(ctx, &mgmntv1.GetCollectionMoviesRequest{CollectionId: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(cm.Movies) != 1 || cm.Name != "Fight Club Collection" {
		t.Fatalf("collection movies: %+v", cm)
	}
}

func TestCollectionMonitorAndSync(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 550, Title: "Fight Club", Year: 1999})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	_, _ = m.db.ExecContext(ctx, `UPDATE movies SET collection_id=10, collection_name='Fight Club Collection' WHERE id=?`, add.MovieId)
	m.mu.Unlock()

	on := true
	set, err := m.SetCollectionMonitored(ctx, &mgmntv1.SetCollectionMonitoredRequest{
		CollectionId: 10, Monitored: true, SearchOnAdd: &on, QualityProfileId: "qp1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !set.GetPrefs().GetMonitored() || !set.GetPrefs().GetSearchOnAdd() {
		t.Fatalf("prefs: %+v", set.GetPrefs())
	}
	got, err := m.GetCollectionPrefs(ctx, &mgmntv1.GetCollectionPrefsRequest{CollectionId: 10})
	if err != nil || !got.GetPrefs().GetMonitored() {
		t.Fatalf("get prefs: %+v %v", got, err)
	}
	cols, err := m.ListCollections(ctx, &mgmntv1.ListCollectionsRequest{})
	if err != nil || len(cols.Collections) != 1 || !cols.Collections[0].GetMonitored() {
		t.Fatalf("list monitored: %+v %v", cols, err)
	}

	var searched int
	m.collectionPartsFn = func(ctx context.Context, collectionID int32) ([]collectionPart, error) {
		if collectionID != 10 {
			t.Fatalf("collection %d", collectionID)
		}
		return []collectionPart{
			{TmdbID: 550, Title: "Fight Club", Year: 1999},
			{TmdbID: 551, Title: "Fight Club 2", Year: 2010, Overview: "fanfic"},
		}, nil
	}
	m.automationSearchFn = func(ctx context.Context, req *automationv1.SearchItemRequest) (*automationv1.SearchItemResponse, error) {
		searched++
		return &automationv1.SearchItemResponse{}, nil
	}
	sync, err := m.SyncCollection(ctx, &mgmntv1.SyncCollectionRequest{CollectionId: 10, AddMissing: true})
	if err != nil {
		t.Fatal(err)
	}
	if sync.GetAdded() != 1 || sync.GetAlreadyPresent() != 1 {
		t.Fatalf("sync: %+v", sync)
	}
	if searched != 1 {
		t.Fatalf("search calls %d", searched)
	}
	cm, err := m.GetCollectionMovies(ctx, &mgmntv1.GetCollectionMoviesRequest{CollectionId: 10})
	if err != nil || len(cm.Movies) != 2 {
		t.Fatalf("after sync: %+v %v", cm, err)
	}

	s := mediaAdminServer{m: m}
	admin, err := s.SetCollectionMonitored(ctx, &mediaadminv1.SetCollectionMonitoredRequest{
		CollectionId: "10", Monitored: false, SearchOnAdd: true,
	})
	if err != nil || admin == nil {
		t.Fatalf("admin monitor: %v", err)
	}
	adminSync, err := s.SyncCollection(ctx, &mediaadminv1.SyncCollectionRequest{CollectionId: "10", AddMissing: false})
	if err != nil || adminSync.GetAlreadyPresent() != 2 {
		t.Fatalf("admin sync: %+v %v", adminSync, err)
	}
}

func TestMediaAdminLibraryAdapters(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	s := mediaAdminServer{m: m}

	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId: 550, Title: "Fight Club", Year: 1999, QualityProfileId: "qp1",
	})
	if err != nil {
		t.Fatal(err)
	}

	missing, err := s.ListMissing(ctx, &mediaadminv1.ListMissingRequest{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	if missing.Total != 1 || missing.Items[0].Id != add.MovieId {
		t.Fatalf("admin missing: %+v", missing)
	}

	tag, err := s.CreateTag(ctx, &mediaadminv1.CreateTagRequest{Label: "admin-fav"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetItemTags(ctx, &mediaadminv1.SetItemTagsRequest{
		ItemId: add.MovieId, TagIds: []string{tag.TagId},
	}); err != nil {
		t.Fatal(err)
	}
	tags, err := s.ListTags(ctx, &mediaadminv1.ListTagsRequest{})
	if err != nil || len(tags.Tags) != 1 {
		t.Fatalf("admin tags: %+v %v", tags, err)
	}
	filtered, err := m.ListItems(ctx, &mediaadminv1.ListItemsRequest{
		Page: 1, PageSize: 20, TagId: tag.TagId,
	})
	if err != nil || filtered.Total != 1 {
		t.Fatalf("tag filter: %+v %v", filtered, err)
	}

	m.mu.Lock()
	_, err = m.db.ExecContext(ctx, `UPDATE movies SET collection_id=10, collection_name='Fight Club Collection' WHERE id=?`, add.MovieId)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	cols, err := s.ListCollections(ctx, &mediaadminv1.ListCollectionsRequest{})
	if err != nil || len(cols.Collections) != 1 || cols.Collections[0].Id != "10" {
		t.Fatalf("admin collections: %+v %v", cols, err)
	}
	items, err := s.GetCollectionItems(ctx, &mediaadminv1.GetCollectionItemsRequest{CollectionId: "10"})
	if err != nil || len(items.Items) != 1 {
		t.Fatalf("admin collection items: %+v %v", items, err)
	}

	if _, err := m.db.ExecContext(ctx, `UPDATE movies SET release_date='2020-01-15' WHERE id=?`, add.MovieId); err != nil {
		t.Fatal(err)
	}
	cal, err := s.GetCalendar(ctx, &mediaadminv1.GetCalendarRequest{
		StartDate: "2020-01-01", EndDate: "2020-01-31",
	})
	if err != nil || len(cal.GetItems()) != 1 || cal.GetItems()[0].GetDate() != "2020-01-15" {
		t.Fatalf("movie calendar: %+v %v", cal, err)
	}
}

func TestMovieCalendarHTTP(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	now := "2026-09-08T00:00:00Z"
	if _, err := m.db.ExecContext(ctx, `
		INSERT INTO movies (id, tmdb_id, title, year, release_date, monitored, has_file, created_at, updated_at)
		VALUES ('mv_cal', 1, 'Upcoming Film', 2026, '2026-09-12', 1, 0, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/calendar?start=2026-09-01&end=2026-09-30", nil)
	w := httptest.NewRecorder()
	m.handleHTTPCalendar(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Upcoming Film") || !strings.Contains(w.Body.String(), "2026-09-12") {
		t.Fatalf("body %s", w.Body.String())
	}
}

func TestPendingReleaseDate(t *testing.T) {
	date, sub := pendingReleaseDate(2026, "2026-09-01", "2026-09-30")
	if date != "2026-09-01" || sub != "Release year (date pending)" {
		t.Fatalf("clamped=%q %q", date, sub)
	}
	date, sub = pendingReleaseDate(2027, "2026-09-01", "2027-12-31")
	if date != "2027-01-01" {
		t.Fatalf("future year=%q %q", date, sub)
	}
	if date, _ = pendingReleaseDate(2025, "2026-09-01", "2026-09-30"); date != "" {
		t.Fatalf("past year should be outside window, got %q", date)
	}
	if date, _ = pendingReleaseDate(0, "2026-01-01", "2026-12-31"); date != "" {
		t.Fatal("expected empty for year 0")
	}
}

func TestPersistReleaseDateAndYearPendingCalendar(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	now := "2026-09-08T00:00:00Z"
	if _, err := m.db.ExecContext(ctx, `
		INSERT INTO movies (id, tmdb_id, title, year, release_date, monitored, has_file, created_at, updated_at)
		VALUES
		('mv_pending', 2, 'Wanted Film', 2026, '', 1, 0, ?, ?),
		('mv_on_disk', 3, 'Already Have', 2026, '', 1, 1, ?, ?)`, now, now, now, now); err != nil {
		t.Fatal(err)
	}
	items, err := m.listMovieCalendar(ctx, "2026-09-01", "2026-09-30", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "mv_pending" || items[0].Date != "2026-09-01" {
		t.Fatalf("pending calendar=%+v", items)
	}
	if items[0].Subtitle != "Release year (date pending)" {
		t.Fatalf("subtitle=%q", items[0].Subtitle)
	}

	m.persistReleaseDate(ctx, "mv_pending", "2026-09-18")
	items, err = m.listMovieCalendar(ctx, "2026-09-01", "2026-09-30", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Date != "2026-09-18" || items[0].Subtitle != "Theatrical / digital" {
		t.Fatalf("after persist=%+v", items)
	}
}

func TestSortColumnForField(t *testing.T) {
	cases := map[mediaadminv1.SortField]string{
		mediaadminv1.SortField_SORT_FIELD_UNSPECIFIED: "title",
		mediaadminv1.SortField_SORT_FIELD_TITLE:       "title",
		mediaadminv1.SortField_SORT_FIELD_YEAR:        "year",
		mediaadminv1.SortField_SORT_FIELD_CREATED_AT:  "created_at",
		mediaadminv1.SortField_SORT_FIELD_UPDATED_AT:  "updated_at",
		mediaadminv1.SortField_SORT_FIELD_RUNTIME:     "runtime",
		mediaadminv1.SortField_SORT_FIELD_RATING:      "vote_average",
		mediaadminv1.SortField(99):                    "title",
	}
	for f, want := range cases {
		if got := sortColumnForField(f); got != want {
			t.Errorf("%v: got %q want %q", f, got, want)
		}
	}
}

func TestListItemsSortFields(t *testing.T) {
	m := newTestModule(t)
	for f := range mediaadminv1.SortField_name {
		for _, ord := range []string{"asc", "desc"} {
			if _, err := m.ListItems(context.Background(), &mediaadminv1.ListItemsRequest{
				Page: 1, PageSize: 20, SortBy: mediaadminv1.SortField(f), SortOrder: ord,
			}); err != nil {
				t.Fatalf("sort %d %s: %v", f, ord, err)
			}
		}
	}
}
