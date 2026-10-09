package internal

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	"github.com/Muxcore-Media/core/sdk/go/module/moduletest"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	metadatav1 "github.com/Muxcore-Media/metadata-tmdb/proto/metadatav1"
)

// openUpgradeModule opens the database at dbPath with the current store entry
// point (Module.Init). The module is stopped (DB closed) at test cleanup.
func openUpgradeModule(t *testing.T, dbPath string) *Module {
	t.Helper()
	m := NewModule(Config{
		DBPath:   dbPath,
		ImageDir: filepath.Join(t.TempDir(), "images"),
		GRPCAddr: "127.0.0.1:0",
		HTTPAddr: "127.0.0.1:0",
	})
	if err := m.Init(context.Background()); err != nil {
		t.Fatalf("Init(%s): %v", dbPath, err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return m
}

// TestUpgradeFromSnapshots opens databases produced by older tags (ADR-0015)
// with the current code, twice, and checks schema, data, defaults, integrity.
func TestUpgradeFromSnapshots(t *testing.T) {
	// hasRelease is true when the snapshot already has movies.release_date;
	// hasPrefs is true when it already has collection_prefs.
	// operatorRows is true when the snapshot already has movie_content_rating
	// with two operator rows (mv_603_seed R, mv_550_seed NR).
	snapshots := []struct {
		tag                  string
		hasRelease, hasPrefs bool
		operatorRows         bool
	}{
		{"v0.1.9", false, false, false},
		{"v0.1.15", true, true, false},
		{"v0.1.21", true, true, false}, // latest release before content ratings (ADR-0031 S2)
		{"v0.1.23", true, true, true},  // operator ratings, before the tmdb source (S4c)
	}
	for _, snap := range snapshots {
		t.Run(snap.tag, func(t *testing.T) {
			path := moduletest.CopyFixture(t, filepath.Join("testdata", "upgrade", snap.tag+".db"))
			fresh := openUpgradeModule(t, filepath.Join(t.TempDir(), "fresh.db"))
			freshSchema := moduletest.Schema(t, fresh.db)

			// Open twice: the second startup must be a no-op.
			for pass := 1; pass <= 2; pass++ {
				m := openUpgradeModule(t, path)
				moduletest.RequireSchemaSuperset(t, moduletest.Schema(t, m.db), freshSchema)
				assertSeededRows(t, m, snap.hasRelease, snap.hasPrefs)
				if snap.operatorRows {
					assertOperatorRowsSurvive(t, m)
				} else {
					assertContentRatingsUnavailable(t, m)
				}
				assertCount(t, m.db, `SELECT count(*) FROM movie_tmdb_rating`, 0)
				moduletest.RequireIntegrity(t, m.db)
				if err := m.Stop(context.Background()); err != nil {
					t.Fatalf("pass %d Stop: %v", pass, err)
				}
			}
		})
	}
}

// TestUpgradeThenClassify checks that an upgraded database accepts operator
// classifications and keeps them across a restart (ADR-0031 Decision 2).
func TestUpgradeThenClassify(t *testing.T) {
	ctx := context.Background()
	path := moduletest.CopyFixture(t, filepath.Join("testdata", "upgrade", "v0.1.21.db"))

	m := openUpgradeModule(t, path)
	if _, err := m.SetContentRating(ctx, &mgmntv1.SetContentRatingRequest{MovieId: "mv_603_seed", ContentRating: "R"}); err != nil {
		t.Fatalf("SetContentRating: %v", err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	m = openUpgradeModule(t, path)
	mv, err := m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: "mv_603_seed"})
	if err != nil {
		t.Fatal(err)
	}
	if mv.GetMovie().GetContentRating() != "R" || mv.GetMovie().GetContentRatingSource() != "operator" {
		t.Errorf("classification lost across restart: %q/%q", mv.GetMovie().GetContentRating(), mv.GetMovie().GetContentRatingSource())
	}
	if got := mv.GetMovie().GetTagLabels(); len(got) != 2 || got[0] != "family-night" || got[1] != "favorites" {
		t.Errorf("tag_labels = %v", got)
	}
	list, err := m.ListMovies(ctx, &mgmntv1.ListMoviesRequest{
		PageSize:             50,
		ClassificationFilter: &mgmntv1.ClassificationFilter{Enabled: true, AllowUnrated: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if list.GetTotal() != 1 || len(list.GetMovies()) != 1 || list.GetMovies()[0].GetId() != "mv_603_seed" {
		t.Errorf("filtered list after classifying one movie = total %d, %v (the other seeded movies stay unavailable)", list.GetTotal(), list.GetMovies())
	}
}

// assertContentRatingsUnavailable proves a pre-classification database reads
// every movie as "unavailable" (empty rating and source): no rating is ever
// inferred from existing data, and a restricted filter shows none of them.
func assertContentRatingsUnavailable(t *testing.T, m *Module) {
	t.Helper()
	ctx := context.Background()
	assertCount(t, m.db, `SELECT count(*) FROM movie_content_rating`, 0)
	list, err := m.ListMovies(ctx, &mgmntv1.ListMoviesRequest{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatalf("ListMovies: %v", err)
	}
	if len(list.GetMovies()) != 3 {
		t.Fatalf("ListMovies returned %d movies, want 3", len(list.GetMovies()))
	}
	for _, mv := range list.GetMovies() {
		if mv.GetContentRating() != "" || mv.GetContentRatingSource() != "" {
			t.Errorf("movie %s reads as %q/%q after upgrade, want unavailable", mv.GetId(), mv.GetContentRating(), mv.GetContentRatingSource())
		}
	}
	filtered, err := m.ListMovies(ctx, &mgmntv1.ListMoviesRequest{
		Page: 1, PageSize: 50,
		ClassificationFilter: &mgmntv1.ClassificationFilter{Enabled: true, AllowUnrated: true},
	})
	if err != nil {
		t.Fatalf("filtered ListMovies: %v", err)
	}
	if filtered.GetTotal() != 0 || len(filtered.GetMovies()) != 0 {
		t.Errorf("enabled filter exposed %d unclassified movies", filtered.GetTotal())
	}
	one, err := m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: "mv_603_seed"})
	if err != nil {
		t.Fatal(err)
	}
	if got := one.GetMovie().GetTagLabels(); len(got) != 2 || got[0] != "family-night" || got[1] != "favorites" {
		t.Errorf("tag_labels for seeded tags = %v", got)
	}
}

func assertSeededRows(t *testing.T, m *Module, hasRelease, hasPrefs bool) {
	t.Helper()
	ctx := context.Background()

	got, err := m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: "mv_550_seed"})
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	mv := got.GetMovie()
	if mv.GetTmdbId() != 550 || mv.GetTitle() != "Fight Club" || mv.GetYear() != 1999 ||
		mv.GetRuntime() != 139 || mv.GetVoteAverage() != 8.4 || mv.GetImdbId() != "tt0137523" ||
		mv.GetQualityProfileId() != "qp-hd" || mv.GetRootFolderPath() != "/media/alice/movies" ||
		!mv.GetMonitored() || !mv.GetHasFile() || mv.GetCreatedAt() != "2025-01-02T03:04:05Z" {
		t.Errorf("unexpected movie after upgrade: %+v", mv)
	}
	if g := mv.GetGenres(); len(g) != 2 || g[0] != "Drama" || g[1] != "Thriller" {
		t.Errorf("genres = %v", g)
	}

	list, err := m.ListMovies(ctx, &mgmntv1.ListMoviesRequest{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatalf("ListMovies: %v", err)
	}
	if len(list.GetMovies()) != 3 {
		t.Errorf("ListMovies returned %d movies, want 3", len(list.GetMovies()))
	}

	// Pulp Fiction was seeded with only NOT NULL columns: defaults must apply.
	pf, err := m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: "mv_680_seed"})
	if err != nil {
		t.Fatalf("GetMovie pulp fiction: %v", err)
	}
	if pf.GetMovie().GetMonitored() || pf.GetMovie().GetHasFile() || pf.GetMovie().GetQualityProfileId() != "" {
		t.Errorf("unexpected pulp fiction row: %+v", pf.GetMovie())
	}

	// Columns added after the snapshot must read back as their defaults on
	// existing rows; seeded values must survive.
	var relDate, collName string
	var collID int
	if err := m.db.QueryRowContext(ctx, `SELECT release_date, collection_id, collection_name FROM movies WHERE id='mv_680_seed'`).
		Scan(&relDate, &collID, &collName); err != nil {
		t.Fatalf("read new columns: %v", err)
	}
	if relDate != "" || collID != 0 || collName != "" {
		t.Errorf("new columns defaults = (%q, %d, %q), want empty/0/empty", relDate, collID, collName)
	}
	wantRelease := ""
	if hasRelease {
		wantRelease = "1999-10-15"
	}
	if err := m.db.QueryRowContext(ctx, `SELECT release_date FROM movies WHERE id='mv_550_seed'`).Scan(&relDate); err != nil {
		t.Fatal(err)
	}
	if relDate != wantRelease {
		t.Errorf("release_date for Fight Club = %q, want %q", relDate, wantRelease)
	}

	files, err := m.ListFiles(ctx, &mgmntv1.ListFilesRequest{MovieId: "mv_550_seed"})
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files.GetFiles()) != 1 {
		t.Fatalf("files = %d, want 1", len(files.GetFiles()))
	}
	if f := files.GetFiles()[0]; f.GetId() != "mf_seed_1" || f.GetQuality() != "Bluray-1080p" ||
		f.GetSizeBytes() != 8589934592 || f.GetContainer() != "mkv" ||
		f.GetFilePath() != "/media/alice/movies/Fight Club (1999)/Fight.Club.1999.1080p.mkv" {
		t.Errorf("unexpected file: %+v", f)
	}

	tags, err := m.GetItemTags(ctx, &mgmntv1.GetItemTagsRequest{ItemId: "mv_603_seed"})
	if err != nil {
		t.Fatalf("GetItemTags: %v", err)
	}
	if ts := tags.GetTags(); len(ts) != 2 || ts[0].GetLabel() != "family-night" || ts[1].GetLabel() != "favorites" {
		t.Errorf("tags for The Matrix = %v", ts)
	}
	all, err := m.ListTags(ctx, &mgmntv1.ListTagsRequest{})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(all.GetTags()) != 2 {
		t.Errorf("ListTags = %d tags, want 2", len(all.GetTags()))
	}

	hist, err := m.ListHistory(ctx, &mediaadminv1.ListHistoryRequest{ItemId: "mv_550_seed"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if hist.GetTotal() != 2 || len(hist.GetRecords()) != 2 {
		t.Fatalf("history total = %d records = %d, want 2/2", hist.GetTotal(), len(hist.GetRecords()))
	}
	for _, r := range hist.GetRecords() {
		if r.GetDownloadId() != "dl-abc123" || r.GetTitle() != "Fight Club" || r.GetQuality() != "Bluray-1080p" {
			t.Errorf("unexpected history record: %+v", r)
		}
	}

	// movie_titles: seeded rows survive; the startup backfill adds the
	// primary title for the movie that had none, without duplicating others.
	assertCount(t, m.db, `SELECT count(*) FROM movie_titles WHERE movie_id='mv_603_seed'`, 2)
	assertCount(t, m.db, `SELECT count(*) FROM movie_titles WHERE movie_id='mv_550_seed'`, 1)
	assertCount(t, m.db, `SELECT count(*) FROM movie_titles WHERE movie_id='mv_680_seed' AND source='primary'`, 1)

	// Collections: the collection row is present in movies for every snapshot;
	// preferences only exist when the snapshot already had collection_prefs.
	cols, err := m.ListCollections(ctx, &mgmntv1.ListCollectionsRequest{})
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	if cs := cols.GetCollections(); len(cs) != 1 || cs[0].GetCollectionId() != 2344 ||
		cs[0].GetName() != "The Matrix Collection" || cs[0].GetMovieCount() != 1 || cs[0].GetMonitored() != hasPrefs {
		t.Errorf("collections = %v (hasPrefs=%v)", cols.GetCollections(), hasPrefs)
	}
	prefs, err := m.GetCollectionPrefs(ctx, &mgmntv1.GetCollectionPrefsRequest{CollectionId: 2344})
	if err != nil {
		t.Fatalf("GetCollectionPrefs: %v", err)
	}
	p := prefs.GetPrefs()
	if hasPrefs {
		if !p.GetMonitored() || p.GetSearchOnAdd() || p.GetQualityProfileId() != "qp-uhd" || p.GetRootFolderPath() != "/media/alice/movies" {
			t.Errorf("collection prefs = %+v", p)
		}
	} else if p.GetMonitored() || !p.GetSearchOnAdd() || p.GetName() != "The Matrix Collection" {
		t.Errorf("default collection prefs = %+v", p)
	}
}

func assertCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if n != want {
		t.Errorf("%s = %d, want %d", query, n, want)
	}
}

// assertOperatorRowsSurvive checks a v0.1.23 database: the two operator rows
// read back unchanged with source "operator", the third movie is unavailable,
// and the new tmdb table starts empty (nothing is inferred or backfilled).
func assertOperatorRowsSurvive(t *testing.T, m *Module) {
	t.Helper()
	ctx := context.Background()
	assertCount(t, m.db, `SELECT count(*) FROM movie_content_rating`, 2)
	for id, want := range map[string]string{"mv_603_seed": "R", "mv_550_seed": "NR", "mv_680_seed": ""} {
		mv, err := m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: id})
		if err != nil {
			t.Fatal(err)
		}
		wantSrc := ""
		if want != "" {
			wantSrc = "operator"
		}
		if mv.GetMovie().GetContentRating() != want || mv.GetMovie().GetContentRatingSource() != wantSrc {
			t.Errorf("%s reads %q/%q after upgrade, want %q/%q", id, mv.GetMovie().GetContentRating(), mv.GetMovie().GetContentRatingSource(), want, wantSrc)
		}
	}
	one, err := m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: "mv_603_seed"})
	if err != nil {
		t.Fatal(err)
	}
	if got := one.GetMovie().GetTagLabels(); len(got) != 2 || got[0] != "family-night" || got[1] != "favorites" {
		t.Errorf("tag_labels for seeded tags = %v", got)
	}
}

// TestUpgradeFromV0123ThenTMDB opens the v0.1.23 database, refreshes metadata
// with a fake metadata client and checks that the tmdb source fills only the
// unclassified movie while both operator rows keep winning, across a restart.
func TestUpgradeFromV0123ThenTMDB(t *testing.T) {
	ctx := context.Background()
	path := moduletest.CopyFixture(t, filepath.Join("testdata", "upgrade", "v0.1.23.db"))
	certs := map[int32]string{603: "PG-13", 550: "R", 680: "R"}

	m := openUpgradeModule(t, path)
	m.movieDetailsFn = func(_ context.Context, tmdbID int32) (*metadatav1.GetMovieDetailsResponse, error) {
		return &metadatav1.GetMovieDetailsResponse{Title: "T", Certification: certs[tmdbID], CertificationCountry: "US"}, nil
	}
	for _, id := range []string{"mv_603_seed", "mv_550_seed", "mv_680_seed"} {
		if _, err := m.RefreshMetadata(ctx, &mgmntv1.RefreshMetadataRequest{MovieId: id}); err != nil {
			t.Fatalf("RefreshMetadata(%s): %v", id, err)
		}
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	m = openUpgradeModule(t, path)
	want := map[string][2]string{
		"mv_603_seed": {"R", "operator"},  // operator R beats tmdb PG-13
		"mv_550_seed": {"NR", "operator"}, // operator NR beats tmdb R
		"mv_680_seed": {"R", "tmdb"},      // no operator value: tmdb fills it
	}
	for id, w := range want {
		mv := getMovie(t, m, id)
		if mv.GetContentRating() != w[0] || mv.GetContentRatingSource() != w[1] {
			t.Errorf("%s = %q/%q after restart, want %q/%q", id, mv.GetContentRating(), mv.GetContentRatingSource(), w[0], w[1])
		}
	}
	assertCount(t, m.db, `SELECT count(*) FROM movie_content_rating`, 2)
	assertCount(t, m.db, `SELECT count(*) FROM movie_tmdb_rating`, 3)
}
