package internal

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	metadatav1 "github.com/Muxcore-Media/metadata-tmdb/proto/metadatav1"
)

// Slice S4c: the lower-precedence "tmdb" classification source (ADR-0031
// Decision 2). Fixture-only (ADR-0008): the metadata client is a fake; no test
// reaches TMDB.

// fakeCert makes the module's metadata client answer every GetMovieDetails with
// the given certification (or, if err is set, with that error).
func fakeCert(m *Module, cert, country string, err error) {
	m.movieDetailsFn = func(context.Context, int32) (*metadatav1.GetMovieDetailsResponse, error) {
		if err != nil {
			return nil, err
		}
		return &metadatav1.GetMovieDetailsResponse{Title: "Refreshed", Certification: cert, CertificationCountry: country}, nil
	}
}

func refresh(t *testing.T, m *Module, id string) {
	t.Helper()
	if _, err := m.RefreshMetadata(context.Background(), &mgmntv1.RefreshMetadataRequest{MovieId: id}); err != nil {
		t.Fatalf("RefreshMetadata(%s): %v", id, err)
	}
}

func clearRating(t *testing.T, m *Module, id string) { t.Helper(); setRating(t, m, id, "", false) }

func wantClass(t *testing.T, m *Module, id, rating, source string) {
	t.Helper()
	mv := getMovie(t, m, id)
	if mv.GetContentRating() != rating || mv.GetContentRatingSource() != source {
		t.Errorf("classification = %q/%q, want %q/%q", mv.GetContentRating(), mv.GetContentRatingSource(), rating, source)
	}
}

func TestNormalizeTMDBCertification(t *testing.T) {
	// Every ladder token is accepted, in any case and with surrounding space.
	for tok := range ratingLevels {
		for _, in := range []string{tok, " " + tok + " ", lower(tok), "\t" + lower(tok) + "\n"} {
			if got := normalizeTMDBCertification(in); got != tok {
				t.Errorf("normalizeTMDBCertification(%q) = %q, want %q", in, got, tok)
			}
		}
	}
	cases := map[string]string{
		"PG-13": "PG-13", "pg-13": "PG-13", "  Pg-13  ": "PG-13", "NC-17": "NC-17", "TV-MA": "TV-MA",
		// unrated markers collapse to the canonical NR
		"NR": "NR", "nr": "NR", "UR": "NR", "Not Rated": "NR", "NOT RATED": "NR", "  not   rated ": "NR",
		"Unrated": "NR", "UNRATED": "NR",
		// anything else is no value: country-specific, free text, empty
		"": "", "   ": "", "15": "", "12A": "", "12": "", "18": "", "FSK 16": "", "PG13": "", "TV-13": "",
		"Rated R": "", "R (restricted)": "", "NOTRATED": "", "adult": "", "true": "", "0": "", "N/A": "",
		"G+": "", "A": "", "U": "", "PG-": "",
	}
	for in, want := range cases {
		if got := normalizeTMDBCertification(in); got != want {
			t.Errorf("normalizeTMDBCertification(%q) = %q, want %q", in, got, want)
		}
	}
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// TestTMDBRatingFillsUnavailable: a refresh with a mappable certification
// classifies a movie that had nothing, with source "tmdb".
func TestTMDBRatingFillsUnavailable(t *testing.T) {
	m := newTestModule(t)
	id := addTestMovie(t, m, 10, "Fill")
	wantClass(t, m, id, "", "")
	fakeCert(m, " pg-13 ", " us ", nil)
	refresh(t, m, id)
	wantClass(t, m, id, "PG-13", "tmdb")

	var tok, country string
	if err := m.db.QueryRow(`SELECT content_rating, country FROM movie_tmdb_rating WHERE movie_id=?`, id).Scan(&tok, &country); err != nil {
		t.Fatal(err)
	}
	if tok != "PG-13" || country != "US" {
		t.Errorf("stored tmdb row = %q/%q, want PG-13/US", tok, country)
	}
	assertCount(t, m.db, `SELECT count(*) FROM movie_content_rating`, 0) // operator table untouched
}

// TestTMDBRatingUnmappableLeavesUnavailable: country-specific and free-text
// certifications never produce a value, and the item stays unavailable.
func TestTMDBRatingUnmappableLeavesUnavailable(t *testing.T) {
	m := newTestModule(t)
	id := addTestMovie(t, m, 11, "Unmappable")
	for _, cert := range []string{"15", "12A", "FSK 16", "", "   ", "adult", "Rated R"} {
		fakeCert(m, cert, "GB", nil)
		refresh(t, m, id)
		wantClass(t, m, id, "", "")
	}
	assertCount(t, m.db, `SELECT count(*) FROM movie_tmdb_rating`, 0)
	for _, f := range []*mgmntv1.ClassificationFilter{{Enabled: true, AllowUnrated: true}, {Enabled: true, MaxRating: "X", AllowUnrated: true}} {
		if got, _ := visibleNames(t, m, &mgmntv1.ListMoviesRequest{ClassificationFilter: f}); len(got) != 0 {
			t.Errorf("unmappable certification made the item visible: %v", got)
		}
	}
}

// TestTMDBRatingNRVariants: the unrated markers store canonical NR and read as
// unrated (visible only with allow_unrated).
func TestTMDBRatingNRVariants(t *testing.T) {
	m := newTestModule(t)
	id := addTestMovie(t, m, 12, "Unrated")
	for _, cert := range []string{"NR", "ur", "Not Rated", "UNRATED"} {
		fakeCert(m, cert, "US", nil)
		refresh(t, m, id)
		wantClass(t, m, id, "NR", "tmdb")
	}
	if got, _ := visibleNames(t, m, &mgmntv1.ListMoviesRequest{ClassificationFilter: &mgmntv1.ClassificationFilter{Enabled: true}}); len(got) != 0 {
		t.Errorf("tmdb NR visible without allow_unrated: %v", got)
	}
	if got, _ := visibleNames(t, m, &mgmntv1.ListMoviesRequest{ClassificationFilter: &mgmntv1.ClassificationFilter{Enabled: true, AllowUnrated: true}}); len(got) != 1 {
		t.Errorf("tmdb NR not visible with allow_unrated: %v", got)
	}
}

// TestTMDBRatingPrecedenceMatrix covers operator x tmdb in every combination
// the ADR-0031 precedence rule distinguishes, in both write orders.
func TestTMDBRatingPrecedenceMatrix(t *testing.T) {
	type op struct {
		rating  string
		unrated bool
	}
	cases := []struct {
		name       string
		operator   *op    // nil = no operator value
		cert       string // tmdb certification ("" = none)
		wantRating string
		wantSource string
	}{
		{"operator rated, tmdb different", &op{"G", false}, "R", "G", "operator"},
		{"operator rated, tmdb same", &op{"R", false}, "R", "R", "operator"},
		{"operator NR, tmdb R", &op{"", true}, "R", "NR", "operator"},
		{"operator rated, tmdb NR", &op{"PG", false}, "NR", "PG", "operator"},
		{"operator rated, tmdb none", &op{"PG", false}, "", "PG", "operator"},
		{"operator rated, tmdb unmappable", &op{"PG", false}, "15", "PG", "operator"},
		{"no operator, tmdb R", nil, "R", "R", "tmdb"},
		{"no operator, tmdb NR", nil, "UNRATED", "NR", "tmdb"},
		{"no operator, tmdb none", nil, "", "", ""},
		{"no operator, tmdb unmappable", nil, "12A", "", ""},
	}
	for i, tc := range cases {
		for _, operatorFirst := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/operatorFirst=%v", tc.name, operatorFirst), func(t *testing.T) {
				m := newTestModule(t)
				id := addTestMovie(t, m, int32(200+i), "M")
				fakeCert(m, tc.cert, "US", nil)
				doOp := func() {
					if tc.operator != nil {
						setRating(t, m, id, tc.operator.rating, tc.operator.unrated)
					}
				}
				if operatorFirst {
					doOp()
					refresh(t, m, id)
				} else {
					refresh(t, m, id)
					doOp()
				}
				wantClass(t, m, id, tc.wantRating, tc.wantSource)
				// A later refresh never overwrites or hides the operator value.
				refresh(t, m, id)
				wantClass(t, m, id, tc.wantRating, tc.wantSource)
			})
		}
	}
}

// TestOperatorClearReturnsToTMDB: clearing the operator value reveals the tmdb
// value, or unavailable when there is none.
func TestOperatorClearReturnsToTMDB(t *testing.T) {
	m := newTestModule(t)
	withTMDB := addTestMovie(t, m, 20, "with-tmdb")
	without := addTestMovie(t, m, 21, "without-tmdb")
	fakeCert(m, "PG-13", "US", nil)
	refresh(t, m, withTMDB)
	fakeCert(m, "", "US", nil)
	refresh(t, m, without)

	setRating(t, m, withTMDB, "G", false)
	setRating(t, m, without, "G", false)
	wantClass(t, m, withTMDB, "G", "operator")
	wantClass(t, m, without, "G", "operator")

	clearRating(t, m, withTMDB)
	clearRating(t, m, without)
	wantClass(t, m, withTMDB, "PG-13", "tmdb")
	wantClass(t, m, without, "", "")

	// An explicit operator NR also clears back to the tmdb value.
	setRating(t, m, withTMDB, "", true)
	wantClass(t, m, withTMDB, "NR", "operator")
	clearRating(t, m, withTMDB)
	wantClass(t, m, withTMDB, "PG-13", "tmdb")
}

// TestTMDBRefreshErrorKeepsSuccessEmptyClears pins the documented distinction:
// a failed fetch keeps the previous tmdb value; a successful fetch whose
// certification is empty or unmappable clears it; a successful one replaces it.
func TestTMDBRefreshErrorKeepsSuccessEmptyClears(t *testing.T) {
	m := newTestModule(t)
	id := addTestMovie(t, m, 30, "Refresh")
	fakeCert(m, "R", "US", nil)
	refresh(t, m, id)
	wantClass(t, m, id, "R", "tmdb")

	// Fetch error: refresh fails, value kept.
	fakeCert(m, "", "", errors.New("tmdb unavailable"))
	if _, err := m.RefreshMetadata(context.Background(), &mgmntv1.RefreshMetadataRequest{MovieId: id}); err == nil {
		t.Fatal("RefreshMetadata must fail when the metadata fetch fails")
	}
	wantClass(t, m, id, "R", "tmdb")

	// Success with a different mappable value: replaced.
	fakeCert(m, "PG", "US", nil)
	refresh(t, m, id)
	wantClass(t, m, id, "PG", "tmdb")

	// Success with unmappable value: cleared.
	fakeCert(m, "12A", "GB", nil)
	refresh(t, m, id)
	wantClass(t, m, id, "", "")
	assertCount(t, m.db, `SELECT count(*) FROM movie_tmdb_rating`, 0)

	// Success with empty value: cleared too (re-establish first).
	fakeCert(m, "R", "US", nil)
	refresh(t, m, id)
	wantClass(t, m, id, "R", "tmdb")
	fakeCert(m, "", "", nil)
	refresh(t, m, id)
	wantClass(t, m, id, "", "")
}

// TestTMDBRefreshNeverTouchesOperatorRow: the operator row is byte-identical
// (including updated_at) after any number of refreshes.
func TestTMDBRefreshNeverTouchesOperatorRow(t *testing.T) {
	m := newTestModule(t)
	id := addTestMovie(t, m, 31, "Operator")
	setRating(t, m, id, "PG", false)
	read := func() string {
		var s string
		if err := m.db.QueryRow(`SELECT content_rating||'|'||source||'|'||updated_at FROM movie_content_rating WHERE movie_id=?`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := read()
	for _, cert := range []string{"R", "", "NR", "15", "G"} {
		fakeCert(m, cert, "US", nil)
		refresh(t, m, id)
	}
	if after := read(); after != before {
		t.Errorf("operator row changed by refresh: %q -> %q", before, after)
	}
}

// TestTMDBStoredValuesMustBeValid: a hand-edited tmdb row outside the ladder
// reads as unavailable; a bad operator row does not count, so a valid tmdb value
// below it is used, never the bad value.
func TestTMDBStoredValuesMustBeValid(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	for i, bad := range []string{"15", "12A", "PG13", "", "adult", "R; DROP TABLE movies"} {
		id := addTestMovie(t, m, int32(300+i), fmt.Sprintf("T%d", i))
		if _, err := m.db.ExecContext(ctx,
			`INSERT INTO movie_tmdb_rating (movie_id, content_rating, country, updated_at) VALUES (?,?,?,'x')`, id, bad, "GB"); err != nil {
			t.Fatal(err)
		}
		wantClass(t, m, id, "", "")
	}
	resp, err := m.ListMovies(ctx, &mgmntv1.ListMoviesRequest{
		ClassificationFilter: &mgmntv1.ClassificationFilter{Enabled: true, AllowUnrated: true, MaxRating: "X"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total != 0 || len(resp.Movies) != 0 {
		t.Errorf("tampered tmdb rows must be invisible, got total=%d", resp.Total)
	}

	// Bad operator row + valid tmdb row: the bad row is ignored, tmdb counts.
	id := addTestMovie(t, m, 310, "mixed")
	if _, err := m.db.ExecContext(ctx, `INSERT INTO movie_content_rating (movie_id, content_rating, source, updated_at) VALUES (?, '15', 'operator', 'x')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.ExecContext(ctx, `INSERT INTO movie_tmdb_rating (movie_id, content_rating, country, updated_at) VALUES (?, 'R', 'US', 'x')`, id); err != nil {
		t.Fatal(err)
	}
	wantClass(t, m, id, "R", "tmdb")
}

// TestTMDBRatingInClassificationFilter runs the S2 filter over tmdb-sourced and
// mixed-source items.
func TestTMDBRatingInClassificationFilter(t *testing.T) {
	m := newTestModule(t)
	add := func(name string, tmdb int32, cert string, op *string) string {
		id := addTestMovie(t, m, tmdb, name)
		fakeCert(m, cert, "US", nil)
		refresh(t, m, id)
		if op != nil {
			setRating(t, m, id, *op, *op == "")
		}
		return id
	}
	str := func(s string) *string { return &s }
	// names are the titles AddMovie recorded; refresh renames them to "Refreshed",
	// so identify by id instead.
	g := add("t-g", 1, "G", nil)
	pg13 := add("t-pg13", 2, "PG-13", nil)
	r := add("t-r", 3, "R", nil)
	nr := add("t-nr", 4, "NR", nil)
	none := add("t-none", 5, "15", nil)
	opG := add("op-g-tmdb-r", 6, "R", str("G"))  // operator lowers: visible
	opR := add("op-r-tmdb-g", 7, "G", str("R"))  // operator raises: hidden
	opNR := add("op-nr-tmdb-g", 8, "G", str("")) // operator NR: needs allow_unrated

	visible := func(f *mgmntv1.ClassificationFilter) map[string]bool {
		resp, err := m.ListMovies(context.Background(), &mgmntv1.ListMoviesRequest{PageSize: 100, ClassificationFilter: f})
		if err != nil {
			t.Fatal(err)
		}
		if int(resp.Total) != len(resp.Movies) {
			t.Fatalf("total %d != %d movies", resp.Total, len(resp.Movies))
		}
		out := map[string]bool{}
		for _, mv := range resp.Movies {
			out[mv.GetId()] = true
			if mv.GetContentRating() == "" || mv.GetContentRatingSource() == "" {
				t.Errorf("visible item %s has no classification", mv.GetId())
			}
		}
		return out
	}
	check := func(label string, f *mgmntv1.ClassificationFilter, want ...string) {
		t.Helper()
		got := visible(f)
		wantSet := map[string]bool{}
		for _, id := range want {
			wantSet[id] = true
		}
		for id := range wantSet {
			if !got[id] {
				t.Errorf("%s: %s missing", label, id)
			}
		}
		for id := range got {
			if !wantSet[id] {
				t.Errorf("%s: %s unexpectedly visible", label, id)
			}
		}
	}
	check("max PG-13", &mgmntv1.ClassificationFilter{Enabled: true, MaxRating: "PG-13"}, g, pg13, opG)
	check("max PG-13 + unrated", &mgmntv1.ClassificationFilter{Enabled: true, MaxRating: "PG-13", AllowUnrated: true}, g, pg13, opG, nr, opNR)
	check("max G", &mgmntv1.ClassificationFilter{Enabled: true, MaxRating: "G"}, g, opG)
	check("no ceiling, unrated off", &mgmntv1.ClassificationFilter{Enabled: true}, g, pg13, r, opG, opR)
	check("no ceiling, unrated on", &mgmntv1.ClassificationFilter{Enabled: true, AllowUnrated: true}, g, pg13, r, nr, opG, opR, opNR)
	_ = none // unmappable certification: never in any enabled-filter result above

	// Source is reported per item.
	wantClass(t, m, g, "G", "tmdb")
	wantClass(t, m, opG, "G", "operator")
	wantClass(t, m, opR, "R", "operator")
	wantClass(t, m, opNR, "NR", "operator")
	wantClass(t, m, none, "", "")

	// A blocked tag still denies a tmdb-rated item; a tag never rescues one.
	tag, err := m.CreateTag(context.Background(), &mgmntv1.CreateTagRequest{Label: "scary"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetItemTags(context.Background(), &mgmntv1.SetItemTagsRequest{ItemId: g, TagIds: []string{tag.TagId}}); err != nil {
		t.Fatal(err)
	}
	check("blocked tag", &mgmntv1.ClassificationFilter{Enabled: true, MaxRating: "PG-13", BlockedTags: []string{"scary"}}, pg13, opG)
	check("allowed tag cannot unlock unavailable", &mgmntv1.ClassificationFilter{Enabled: true, MaxRating: "X", AllowedTags: []string{"scary"}}, g)
}

// TestTMDBRefreshLeavesTagLabelsUnaffected: tag_labels are independent of the
// rating source.
func TestTMDBRefreshLeavesTagLabelsUnaffected(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	id := addTestMovie(t, m, 40, "Tagged")
	var ids []string
	for _, l := range []string{"zeta", "alpha"} {
		r, err := m.CreateTag(ctx, &mgmntv1.CreateTagRequest{Label: l})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.TagId)
	}
	if _, err := m.SetItemTags(ctx, &mgmntv1.SetItemTagsRequest{ItemId: id, TagIds: ids}); err != nil {
		t.Fatal(err)
	}
	for _, cert := range []string{"R", "", "NR"} {
		fakeCert(m, cert, "US", nil)
		refresh(t, m, id)
		if got := getMovie(t, m, id).GetTagLabels(); len(got) != 2 || got[0] != "alpha" || got[1] != "zeta" {
			t.Errorf("tag_labels = %v after refresh with %q", got, cert)
		}
	}
}

// TestRemoveMovieDeletesTMDBRating: no orphan tmdb row survives a removal (the
// SQLite pool does not enforce the foreign key).
func TestRemoveMovieDeletesTMDBRating(t *testing.T) {
	m := newTestModule(t)
	id := addTestMovie(t, m, 50, "Gone")
	fakeCert(m, "R", "US", nil)
	refresh(t, m, id)
	assertCount(t, m.db, `SELECT count(*) FROM movie_tmdb_rating`, 1)
	if _, err := m.RemoveMovie(context.Background(), &mgmntv1.RemoveMovieRequest{MovieId: id}); err != nil {
		t.Fatal(err)
	}
	assertCount(t, m.db, `SELECT count(*) FROM movie_tmdb_rating`, 0)
}

// TestTMDBRefreshConcurrentWithSetContentRating races refreshes (alternating a
// mappable and an empty certification) against operator writes and reads. Under
// -race it must stay data-race free, and no reader may ever see a state outside
// {operator value, tmdb value, unavailable} or a tmdb value shadowing a present
// operator value; once the operator value is final, it must win.
func TestTMDBRefreshConcurrentWithSetContentRating(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	id := addTestMovie(t, m, 60, "Race")
	other := addTestMovie(t, m, 61, "Bystander")
	setRating(t, m, other, "PG", false)

	var flip sync.Mutex
	n := 0
	m.movieDetailsFn = func(context.Context, int32) (*metadatav1.GetMovieDetailsResponse, error) {
		flip.Lock()
		defer flip.Unlock()
		n++
		cert := "R"
		if n%3 == 0 {
			cert = ""
		}
		return &metadatav1.GetMovieDetailsResponse{Title: "Race", Certification: cert, CertificationCountry: "US"}, nil
	}

	var wg sync.WaitGroup
	errs := make(chan error, 128)
	report := func(err error) {
		select {
		case errs <- err:
		default:
		}
	}
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if _, err := m.RefreshMetadata(ctx, &mgmntv1.RefreshMetadataRequest{MovieId: id}); err != nil {
					report(err)
					return
				}
			}
		}()
	}
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				req := &mgmntv1.SetContentRatingRequest{MovieId: id, ContentRating: "G"}
				if (i+w)%4 == 0 {
					req = &mgmntv1.SetContentRatingRequest{MovieId: id} // clear
				}
				if _, err := m.SetContentRating(ctx, req); err != nil {
					report(err)
					return
				}
			}
		}(w)
	}
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				mv, err := m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: id})
				if err != nil {
					report(err)
					return
				}
				c, s := mv.GetMovie().GetContentRating(), mv.GetMovie().GetContentRatingSource()
				switch {
				case c == "G" && s == "operator", c == "R" && s == "tmdb", c == "" && s == "":
				default:
					report(fmt.Errorf("impossible classification %q/%q", c, s))
					return
				}
				if o, err := m.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: other}); err != nil {
					report(err)
					return
				} else if o.GetMovie().GetContentRating() != "PG" || o.GetMovie().GetContentRatingSource() != "operator" {
					report(fmt.Errorf("bystander changed: %q/%q", o.GetMovie().GetContentRating(), o.GetMovie().GetContentRatingSource()))
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

	// Operator value is final: a further refresh must not displace it.
	setRating(t, m, id, "G", false)
	fakeCert(m, "NC-17", "US", nil)
	refresh(t, m, id)
	wantClass(t, m, id, "G", "operator")
}
