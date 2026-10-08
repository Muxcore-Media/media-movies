package internal

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestRatingLadderPinned pins the local copy of the ADR-0031 ladder against
// parental.RatingLevel in userdata-local. A change here must be made there
// first (and in the BFF), never silently in one place.
func TestRatingLadderPinned(t *testing.T) {
	want := map[string]int{
		"G": 0, "TV-Y": 0, "TV-Y7": 0, "TV-Y7-FV": 0, "ALL": 0, "E": 0,
		"PG": 1, "TV-G": 1, "TV-PG": 1, "E10+": 1,
		"PG-13": 2, "TV-14": 2, "T": 2,
		"R": 3, "TV-MA": 3, "M": 3, "MA": 3,
		"NC-17": 4, "AO": 4, "X": 4,
	}
	if len(ratingLevels) != len(want) {
		t.Errorf("ladder has %d tokens, want %d", len(ratingLevels), len(want))
	}
	for tok, lvl := range want {
		if got, ok := ratingLevel(tok); !ok || got != lvl {
			t.Errorf("ratingLevel(%q) = %d,%v want %d", tok, got, ok, lvl)
		}
	}
	for _, tok := range []string{"", "NR", "UR", "15", "12A", "PG13", "TV-13"} {
		if _, ok := ratingLevel(tok); ok {
			t.Errorf("ratingLevel(%q) must not be on the ladder", tok)
		}
	}
	if lvl, ok := ratingLevel("  pg-13 "); !ok || lvl != 2 {
		t.Errorf("ratingLevel must ignore case and space, got %d,%v", lvl, ok)
	}
}

func addTestMovie(t *testing.T, m *Module, tmdb int32, title string) string {
	t.Helper()
	r, err := m.AddMovie(context.Background(), &mgmntv1.AddMovieRequest{TmdbId: tmdb, Title: title, Year: 2000})
	if err != nil {
		t.Fatal(err)
	}
	return r.MovieId
}

func setRating(t *testing.T, m *Module, id, rating string, unrated bool) {
	t.Helper()
	if _, err := m.SetContentRating(context.Background(), &mgmntv1.SetContentRatingRequest{
		MovieId: id, ContentRating: rating, ExplicitUnrated: unrated,
	}); err != nil {
		t.Fatalf("SetContentRating(%s,%q,%v): %v", id, rating, unrated, err)
	}
}

func getMovie(t *testing.T, m *Module, id string) *mgmntv1.MovieItem {
	t.Helper()
	r, err := m.GetMovie(context.Background(), &mgmntv1.GetMovieRequest{MovieId: id})
	if err != nil {
		t.Fatal(err)
	}
	return r.GetMovie()
}

func TestSetContentRatingRoundTrip(t *testing.T) {
	m := newTestModule(t)
	id := addTestMovie(t, m, 1, "Alpha")

	// Nothing recorded: unavailable, not unrated.
	if mv := getMovie(t, m, id); mv.GetContentRating() != "" || mv.GetContentRatingSource() != "" {
		t.Fatalf("fresh movie = %q/%q, want unavailable", mv.GetContentRating(), mv.GetContentRatingSource())
	}

	setRating(t, m, id, " pg-13 ", false)
	if mv := getMovie(t, m, id); mv.GetContentRating() != "PG-13" || mv.GetContentRatingSource() != "operator" {
		t.Fatalf("after set = %q/%q", mv.GetContentRating(), mv.GetContentRatingSource())
	}

	// Replace.
	setRating(t, m, id, "R", false)
	if mv := getMovie(t, m, id); mv.GetContentRating() != "R" {
		t.Fatalf("after replace = %q", mv.GetContentRating())
	}

	// Explicit NR.
	setRating(t, m, id, "", true)
	if mv := getMovie(t, m, id); mv.GetContentRating() != "NR" || mv.GetContentRatingSource() != "operator" {
		t.Fatalf("after NR = %q/%q", mv.GetContentRating(), mv.GetContentRatingSource())
	}

	// Clear: back to unavailable.
	setRating(t, m, id, "", false)
	if mv := getMovie(t, m, id); mv.GetContentRating() != "" || mv.GetContentRatingSource() != "" {
		t.Fatalf("after clear = %q/%q, want unavailable", mv.GetContentRating(), mv.GetContentRatingSource())
	}
	assertCount(t, m.db, `SELECT count(*) FROM movie_content_rating`, 0)

	// The same values come back through the list and update paths.
	setRating(t, m, id, "TV-MA", false)
	list, err := m.ListMovies(context.Background(), &mgmntv1.ListMoviesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Movies) != 1 || list.Movies[0].GetContentRating() != "TV-MA" || list.Movies[0].GetContentRatingSource() != "operator" {
		t.Fatalf("list = %+v", list.Movies)
	}
	mon := true
	upd, err := m.UpdateMovie(context.Background(), &mgmntv1.UpdateMovieRequest{MovieId: id, Monitored: &mon})
	if err != nil {
		t.Fatal(err)
	}
	if upd.GetMovie().GetContentRating() != "TV-MA" {
		t.Fatalf("UpdateMovie result rating = %q", upd.GetMovie().GetContentRating())
	}

	// Removing the movie removes its classification.
	if _, err := m.RemoveMovie(context.Background(), &mgmntv1.RemoveMovieRequest{MovieId: id}); err != nil {
		t.Fatal(err)
	}
	assertCount(t, m.db, `SELECT count(*) FROM movie_content_rating`, 0)
}

func TestSetContentRatingValidation(t *testing.T) {
	m := newTestModule(t)
	id := addTestMovie(t, m, 1, "Alpha")
	ctx := context.Background()

	for _, bad := range []string{"15", "12A", "PG13", "NR", "UR", "bogus", "R; DROP TABLE movies"} {
		_, err := m.SetContentRating(ctx, &mgmntv1.SetContentRatingRequest{MovieId: id, ContentRating: bad})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("rating %q: code = %v, want InvalidArgument (err=%v)", bad, status.Code(err), err)
		}
	}
	// A token together with explicit_unrated is contradictory.
	_, err := m.SetContentRating(ctx, &mgmntv1.SetContentRatingRequest{MovieId: id, ContentRating: "PG", ExplicitUnrated: true})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("rating+unrated: code = %v, want InvalidArgument", status.Code(err))
	}
	_, err = m.SetContentRating(ctx, &mgmntv1.SetContentRatingRequest{ContentRating: "PG"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("missing movie_id: code = %v, want InvalidArgument", status.Code(err))
	}
	_, err = m.SetContentRating(ctx, &mgmntv1.SetContentRatingRequest{MovieId: "nope", ContentRating: "PG"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("unknown movie: code = %v, want NotFound", status.Code(err))
	}
	// Rejected writes must not have stored anything.
	if mv := getMovie(t, m, id); mv.GetContentRating() != "" {
		t.Errorf("rejected write stored %q", mv.GetContentRating())
	}
}

// A corrupt or untrusted stored row reads as unavailable, never as a rating.
func TestUntrustedStoredRowsReadAsUnavailable(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	rows := []struct{ rating, source string }{
		{"PG", ""}, {"PG", "tmdb"}, {"PG", "anything"}, {"15", "operator"}, {"", "operator"},
	}
	for i, r := range rows {
		id := addTestMovie(t, m, int32(100+i), fmt.Sprintf("M%d", i))
		if _, err := m.db.ExecContext(ctx,
			`INSERT INTO movie_content_rating (movie_id, content_rating, source, updated_at) VALUES (?,?,?,'x')`,
			id, r.rating, r.source); err != nil {
			t.Fatal(err)
		}
		if mv := getMovie(t, m, id); mv.GetContentRating() != "" || mv.GetContentRatingSource() != "" {
			t.Errorf("row %+v read as %q/%q, want unavailable", r, mv.GetContentRating(), mv.GetContentRatingSource())
		}
	}
	resp, err := m.ListMovies(ctx, &mgmntv1.ListMoviesRequest{
		ClassificationFilter: &mgmntv1.ClassificationFilter{Enabled: true, AllowUnrated: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total != 0 || len(resp.Movies) != 0 {
		t.Errorf("untrusted rows must be invisible, got total=%d", resp.Total)
	}
}

func TestTagLabelsPopulated(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	id := addTestMovie(t, m, 1, "Alpha")
	other := addTestMovie(t, m, 2, "Beta")
	var tagIDs []string
	for _, l := range []string{"zeta", "Gore", "alpha"} {
		r, err := m.CreateTag(ctx, &mgmntv1.CreateTagRequest{Label: l})
		if err != nil {
			t.Fatal(err)
		}
		tagIDs = append(tagIDs, r.TagId)
	}
	if _, err := m.SetItemTags(ctx, &mgmntv1.SetItemTagsRequest{ItemId: id, TagIds: tagIDs}); err != nil {
		t.Fatal(err)
	}
	want := []string{"Gore", "alpha", "zeta"} // ORDER BY label, as GetItemTags
	if got := getMovie(t, m, id).GetTagLabels(); !slices.Equal(got, want) {
		t.Errorf("GetMovie tag_labels = %v, want %v", got, want)
	}
	if got := getMovie(t, m, other).GetTagLabels(); len(got) != 0 {
		t.Errorf("untagged movie tag_labels = %v", got)
	}
	list, err := m.ListMovies(ctx, &mgmntv1.ListMoviesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, mv := range list.Movies {
		if mv.GetId() == id && !slices.Equal(mv.GetTagLabels(), want) {
			t.Errorf("list tag_labels = %v, want %v", mv.GetTagLabels(), want)
		}
	}
	// Filtered path carries them too.
	setRating(t, m, id, "G", false)
	fl, err := m.ListMovies(ctx, &mgmntv1.ListMoviesRequest{ClassificationFilter: &mgmntv1.ClassificationFilter{Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(fl.Movies) != 1 || !slices.Equal(fl.Movies[0].GetTagLabels(), want) || fl.Movies[0].GetContentRating() != "G" {
		t.Errorf("filtered list = %+v", fl.Movies)
	}
	// Collection listing carries them as well.
	if _, err := m.db.ExecContext(ctx, `UPDATE movies SET collection_id=7, collection_name='C' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	cm, err := m.GetCollectionMovies(ctx, &mgmntv1.GetCollectionMoviesRequest{CollectionId: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(cm.Movies) != 1 || cm.Movies[0].GetContentRating() != "G" || !slices.Equal(cm.Movies[0].GetTagLabels(), want) {
		t.Errorf("collection movies = %+v", cm.Movies)
	}
}

// filterFixture builds a library covering every classification state.
func filterFixture(t *testing.T) (*Module, map[string]string) {
	t.Helper()
	m := newTestModule(t)
	ctx := context.Background()
	ids := map[string]string{}
	add := func(name string, tmdb int32) { ids[name] = addTestMovie(t, m, tmdb, name) }
	add("a-g", 1)
	add("b-pg", 2)
	add("c-pg13", 3)
	add("d-r", 4)
	add("e-nc17", 5)
	add("f-nr", 6)
	add("g-none", 7)
	add("h-pg-gore", 8)
	add("i-g-kids", 9)
	setRating(t, m, ids["a-g"], "G", false)
	setRating(t, m, ids["b-pg"], "PG", false)
	setRating(t, m, ids["c-pg13"], "PG-13", false)
	setRating(t, m, ids["d-r"], "R", false)
	setRating(t, m, ids["e-nc17"], "NC-17", false)
	setRating(t, m, ids["f-nr"], "", true)
	// g-none stays unavailable.
	setRating(t, m, ids["h-pg-gore"], "PG", false)
	setRating(t, m, ids["i-g-kids"], "G", false)

	tag := func(label string) string {
		r, err := m.CreateTag(ctx, &mgmntv1.CreateTagRequest{Label: label})
		if err != nil {
			t.Fatal(err)
		}
		return r.TagId
	}
	gore, kids := tag("Gore"), tag("kids")
	if _, err := m.SetItemTags(ctx, &mgmntv1.SetItemTagsRequest{ItemId: ids["h-pg-gore"], TagIds: []string{gore, kids}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetItemTags(ctx, &mgmntv1.SetItemTagsRequest{ItemId: ids["i-g-kids"], TagIds: []string{kids}}); err != nil {
		t.Fatal(err)
	}
	// An allowed tag on an unavailable and on an over-ceiling item must not unlock them.
	if _, err := m.SetItemTags(ctx, &mgmntv1.SetItemTagsRequest{ItemId: ids["g-none"], TagIds: []string{kids}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetItemTags(ctx, &mgmntv1.SetItemTagsRequest{ItemId: ids["d-r"], TagIds: []string{kids}}); err != nil {
		t.Fatal(err)
	}
	return m, ids
}

func visibleNames(t *testing.T, m *Module, req *mgmntv1.ListMoviesRequest) ([]string, *mgmntv1.ListMoviesResponse) {
	t.Helper()
	resp, err := m.ListMovies(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, mv := range resp.Movies {
		names = append(names, mv.GetTitle())
	}
	return names, resp
}

func TestClassificationFilter(t *testing.T) {
	m, _ := filterFixture(t)

	cases := []struct {
		name string
		f    *mgmntv1.ClassificationFilter
		want []string
	}{
		{"no ceiling hides only unrated and unavailable",
			&mgmntv1.ClassificationFilter{Enabled: true},
			[]string{"a-g", "b-pg", "c-pg13", "d-r", "e-nc17", "h-pg-gore", "i-g-kids"}},
		{"allow_unrated adds NR but never unavailable",
			&mgmntv1.ClassificationFilter{Enabled: true, AllowUnrated: true},
			[]string{"a-g", "b-pg", "c-pg13", "d-r", "e-nc17", "f-nr", "h-pg-gore", "i-g-kids"}},
		{"PG ceiling",
			&mgmntv1.ClassificationFilter{Enabled: true, MaxRating: "PG"},
			[]string{"a-g", "b-pg", "h-pg-gore", "i-g-kids"}},
		{"ceiling is case and space insensitive",
			&mgmntv1.ClassificationFilter{Enabled: true, MaxRating: " pg-13 "},
			[]string{"a-g", "b-pg", "c-pg13", "h-pg-gore", "i-g-kids"}},
		{"G ceiling",
			&mgmntv1.ClassificationFilter{Enabled: true, MaxRating: "TV-Y"},
			[]string{"a-g", "i-g-kids"}},
		{"blocked tag wins (exact, case-folded)",
			&mgmntv1.ClassificationFilter{Enabled: true, MaxRating: "PG", BlockedTags: []string{" GORE "}},
			[]string{"a-g", "b-pg", "i-g-kids"}},
		{"blocked tag is exact, not a substring",
			&mgmntv1.ClassificationFilter{Enabled: true, MaxRating: "PG", BlockedTags: []string{"gor"}},
			[]string{"a-g", "b-pg", "h-pg-gore", "i-g-kids"}},
		{"allowed tags require a match",
			&mgmntv1.ClassificationFilter{Enabled: true, AllowedTags: []string{"KIDS"}, MaxRating: "PG"},
			[]string{"h-pg-gore", "i-g-kids"}},
		{"blocked beats allowed",
			&mgmntv1.ClassificationFilter{Enabled: true, AllowedTags: []string{"kids"}, BlockedTags: []string{"gore"}, MaxRating: "PG"},
			[]string{"i-g-kids"}},
		{"allowed tag never unlocks a rating or unavailable denial",
			&mgmntv1.ClassificationFilter{Enabled: true, AllowedTags: []string{"kids"}, MaxRating: "PG-13", AllowUnrated: true},
			[]string{"h-pg-gore", "i-g-kids"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, resp := visibleNames(t, m, &mgmntv1.ListMoviesRequest{PageSize: 100, ClassificationFilter: c.f})
			if !slices.Equal(got, c.want) {
				t.Errorf("visible = %v, want %v", got, c.want)
			}
			if int(resp.Total) != len(c.want) {
				t.Errorf("total = %d, want %d", resp.Total, len(c.want))
			}
		})
	}
}

func TestClassificationFilterNeverWidens(t *testing.T) {
	m, _ := filterFixture(t)

	base, _ := visibleNames(t, m, &mgmntv1.ListMoviesRequest{PageSize: 100})
	if len(base) != 9 {
		t.Fatalf("unfiltered library = %d movies, want 9", len(base))
	}

	// Disabled or absent filter changes nothing.
	for _, f := range []*mgmntv1.ClassificationFilter{
		nil,
		{},
		{Enabled: false, MaxRating: "G", BlockedTags: []string{"kids"}},
	} {
		got, resp := visibleNames(t, m, &mgmntv1.ListMoviesRequest{PageSize: 100, ClassificationFilter: f})
		if !slices.Equal(got, base) || resp.Total != 9 {
			t.Errorf("disabled filter %+v changed results: %v total=%d", f, got, resp.Total)
		}
	}

	// Every enabled filter is a subset of the other request criteria, and
	// composes with search / genre / tag_id (it narrows those, never widens).
	narrow, _ := visibleNames(t, m, &mgmntv1.ListMoviesRequest{Search: "pg", PageSize: 100})
	got, resp := visibleNames(t, m, &mgmntv1.ListMoviesRequest{
		Search: "pg", PageSize: 100,
		ClassificationFilter: &mgmntv1.ClassificationFilter{Enabled: true, MaxRating: "PG"},
	})
	for _, n := range got {
		if !slices.Contains(narrow, n) {
			t.Errorf("%q appeared only because of the filter", n)
		}
	}
	// Search "pg" matches b-pg, c-pg13 and h-pg-gore; the PG ceiling keeps two.
	if !slices.Equal(got, []string{"b-pg", "h-pg-gore"}) {
		t.Errorf("search+filter = %v", got)
	}
	if resp.Total != 2 {
		t.Errorf("search+filter total = %d, want 2", resp.Total)
	}

	// Invalid filters are errors, never a weaker filter.
	for _, f := range []*mgmntv1.ClassificationFilter{
		{Enabled: true, MaxRating: "15"},
		{Enabled: true, MaxRating: "NR"},
		{Enabled: true, BlockedTags: []string{"ok", "  "}},
		{Enabled: true, AllowedTags: []string{""}},
	} {
		_, err := m.ListMovies(context.Background(), &mgmntv1.ListMoviesRequest{ClassificationFilter: f})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("filter %+v: code = %v, want InvalidArgument", f, status.Code(err))
		}
	}
}

func TestClassificationFilterPaginationAndTotal(t *testing.T) {
	m, _ := filterFixture(t)
	f := &mgmntv1.ClassificationFilter{Enabled: true, MaxRating: "PG-13"} // a-g b-pg c-pg13 h-pg-gore i-g-kids
	want := []string{"a-g", "b-pg", "c-pg13", "h-pg-gore", "i-g-kids"}

	var all []string
	for page := int32(1); page <= 4; page++ {
		got, resp := visibleNames(t, m, &mgmntv1.ListMoviesRequest{Page: page, PageSize: 2, ClassificationFilter: f})
		if resp.Total != 5 {
			t.Errorf("page %d total = %d, want 5 (visible items only, not 9)", page, resp.Total)
		}
		if resp.Page != page || resp.PageSize != 2 {
			t.Errorf("page %d echoed %d/%d", page, resp.Page, resp.PageSize)
		}
		all = append(all, got...)
	}
	if !slices.Equal(all, want) {
		t.Errorf("paged = %v, want %v (no hidden item may occupy a slot)", all, want)
	}
	if got, _ := visibleNames(t, m, &mgmntv1.ListMoviesRequest{Page: 3, PageSize: 2, ClassificationFilter: f}); len(got) != 1 {
		t.Errorf("last page = %v, want 1 item", got)
	}

	// Sorting still applies to the visible set.
	desc, _ := visibleNames(t, m, &mgmntv1.ListMoviesRequest{PageSize: 100, SortOrder: "desc", ClassificationFilter: f})
	slices.Reverse(want)
	if !slices.Equal(desc, want) {
		t.Errorf("desc = %v, want %v", desc, want)
	}

	// A filter that hides everything reports total 0, not the library size.
	none, resp := visibleNames(t, m, &mgmntv1.ListMoviesRequest{
		ClassificationFilter: &mgmntv1.ClassificationFilter{Enabled: true, MaxRating: "G", BlockedTags: []string{"kids"}, AllowedTags: []string{"nothing"}},
	})
	if len(none) != 0 || resp.Total != 0 {
		t.Errorf("hide-all = %v total=%d", none, resp.Total)
	}
}

// Items with no recorded rating are never visible to an enabled filter, however
// permissive: there is no allow_unavailable option.
func TestUnavailableNeverVisibleToEnabledFilter(t *testing.T) {
	m := newTestModule(t)
	addTestMovie(t, m, 1, "legacy")
	for _, f := range []*mgmntv1.ClassificationFilter{
		{Enabled: true},
		{Enabled: true, AllowUnrated: true},
		{Enabled: true, MaxRating: "X", AllowUnrated: true},
	} {
		if got, resp := visibleNames(t, m, &mgmntv1.ListMoviesRequest{ClassificationFilter: f}); len(got) != 0 || resp.Total != 0 {
			t.Errorf("filter %+v exposed an unavailable item: %v total=%d", f, got, resp.Total)
		}
	}
}

func TestSetContentRatingConcurrentWithList(t *testing.T) {
	m, ids := filterFixture(t)
	ctx := context.Background()
	f := &mgmntv1.ClassificationFilter{Enabled: true, MaxRating: "PG-13", AllowUnrated: true}

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	tokens := []string{"G", "PG", "R", "NC-17", "PG-13"}
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				req := &mgmntv1.SetContentRatingRequest{MovieId: ids["g-none"], ContentRating: tokens[(w+i)%len(tokens)]}
				if i%7 == 0 {
					req = &mgmntv1.SetContentRatingRequest{MovieId: ids["g-none"]} // clear
				}
				if _, err := m.SetContentRating(ctx, req); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				resp, err := m.ListMovies(ctx, &mgmntv1.ListMoviesRequest{PageSize: 100, ClassificationFilter: f})
				if err != nil {
					errs <- err
					return
				}
				if int(resp.Total) != len(resp.Movies) {
					errs <- fmt.Errorf("total %d != page size %d under a single-page filter", resp.Total, len(resp.Movies))
					return
				}
				for _, mv := range resp.Movies {
					if mv.GetContentRating() == "" {
						errs <- fmt.Errorf("unavailable item %s leaked through the filter", mv.GetTitle())
						return
					}
				}
				if _, err := m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: ids["g-none"]}); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
