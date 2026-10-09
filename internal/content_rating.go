package internal

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Parental classification (ADR-0031 Decision 2, T-M4-01 slice S2).
//
// media-movies is the authority for a movie's classification. An item is in
// exactly one of three states:
//
//   - rated:       a ladder token recorded by a trusted source;
//   - unrated:     an explicit "NR" recorded by a trusted source;
//   - unavailable: nothing recorded (the state of every pre-existing row).
//
// A rating is never inferred from other data: vote_average and TMDB's "adult"
// flag are not ratings. There are two sources, kept in two tables so that one
// can never overwrite the other (T-M4-01 slice S4c, ADR-0031 Decision 2):
//
//   - operator (highest precedence): movie_content_rating, written only by
//     SetContentRating;
//   - tmdb (lower precedence): movie_tmdb_rating, written only by
//     RefreshMetadata from the metadata module's certification, and only when
//     that certification maps onto the ladder or an unrated marker.
//
// The effective classification is the operator value if one is recorded, else
// the tmdb value, else unavailable. An explicit operator NR wins over a tmdb
// rating; clearing the operator value returns the item to its tmdb value.
//
// Authorization is NOT checked in this module. SetContentRating, like
// SetItemTags, trusts its caller; the consumer BFF restricts both to admin or
// manager principals before calling.

const (
	// contentRatingSourceOperator marks a value written by SetContentRating.
	contentRatingSourceOperator = "operator"
	// contentRatingSourceTMDB marks a value derived from the metadata module's
	// TMDB certification. It is never stored in movie_content_rating.
	contentRatingSourceTMDB = "tmdb"
	// contentRatingNR is the stored/exposed token for an explicit unrated
	// record. It is a state, not a rating, so it is not on the ladder.
	contentRatingNR = "NR"
)

// ratingLevels is a local copy of the ADR-0031 rating ladder. It must stay
// identical to parental.RatingLevel in userdata-local (package parental,
// evaluate.go); it is copied rather than imported so media-movies takes no new
// cross-module dependency. TestRatingLadderPinned pins the token list.
var ratingLevels = map[string]int{
	"G": 0, "TV-Y": 0, "TV-Y7": 0, "TV-Y7-FV": 0, "ALL": 0, "E": 0,
	"PG": 1, "TV-G": 1, "TV-PG": 1, "E10+": 1,
	"PG-13": 2, "TV-14": 2, "T": 2,
	"R": 3, "TV-MA": 3, "M": 3, "MA": 3,
	"NC-17": 4, "AO": 4, "X": 4,
}

// ratingLevel returns the ladder position of a token, ignoring case and
// surrounding space. ok is false for anything not on the ladder, including
// NR/UR and the empty string.
func ratingLevel(token string) (level int, ok bool) {
	level, ok = ratingLevels[strings.ToUpper(strings.TrimSpace(token))]
	return level, ok
}

// normalizeTMDBCertification maps a raw TMDB certification onto the stored
// token: a ladder token (upper case), "NR" for the unrated markers NR, UR,
// "NOT RATED" and "UNRATED", or "" for everything else. Country-specific values
// ("15", "12A", "FSK 16"), free text and the empty string are not ratings on
// this ladder and yield no value; nothing is inferred from them.
func normalizeTMDBCertification(raw string) string {
	tok := strings.ToUpper(strings.Join(strings.Fields(raw), " "))
	switch tok {
	case "NR", "UR", "NOT RATED", "UNRATED":
		return contentRatingNR
	}
	if _, ok := ratingLevel(tok); ok {
		return tok
	}
	return ""
}

// normalizeTag is the single tag comparison form (trimmed, case-folded) used by
// the classification filter on both sides of an exact match.
func normalizeTag(tag string) string {
	return strings.ToLower(strings.TrimSpace(tag))
}

func (m *Module) ensureContentRatingTable(ctx context.Context) error {
	// Forward-only and idempotent. Items without a row are "unavailable".
	if _, err := m.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS movie_content_rating (
			movie_id       TEXT PRIMARY KEY,
			content_rating TEXT NOT NULL,
			source         TEXT NOT NULL,
			updated_at     TEXT NOT NULL,
			FOREIGN KEY (movie_id) REFERENCES movies(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("create movie_content_rating: %w", err)
	}
	// The tmdb-derived value lives in its own table so no operator write can
	// touch it and no refresh can touch the operator row. A row exists only
	// while TMDB reports a mappable certification.
	if _, err := m.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS movie_tmdb_rating (
			movie_id       TEXT PRIMARY KEY,
			content_rating TEXT NOT NULL,
			country        TEXT NOT NULL DEFAULT '',
			updated_at     TEXT NOT NULL,
			FOREIGN KEY (movie_id) REFERENCES movies(id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("create movie_tmdb_rating: %w", err)
	}
	return nil
}

// setTMDBRatingLocked records the TMDB certification of a movie after a
// SUCCESSFUL metadata fetch. A mappable certification is stored (replacing any
// previous tmdb value); an empty or unmappable one clears the stored tmdb
// value, so a removed or changed certification is reflected. A failed fetch
// must not call this: the previous tmdb value is then kept. It never reads or
// writes the operator table. Callers hold m.mu for writing.
func (m *Module) setTMDBRatingLocked(ctx context.Context, movieID, certification, country string) error {
	token := normalizeTMDBCertification(certification)
	if token == "" {
		if _, err := m.db.ExecContext(ctx, `DELETE FROM movie_tmdb_rating WHERE movie_id = ?`, movieID); err != nil {
			return fmt.Errorf("clear tmdb rating: %w", err)
		}
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := m.db.ExecContext(ctx,
		`INSERT INTO movie_tmdb_rating (movie_id, content_rating, country, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(movie_id) DO UPDATE SET content_rating = excluded.content_rating,
		   country = excluded.country, updated_at = excluded.updated_at`,
		movieID, token, strings.ToUpper(strings.TrimSpace(country)), now,
	); err != nil {
		return fmt.Errorf("set tmdb rating: %w", err)
	}
	return nil
}

// SetContentRating records, replaces or clears the operator classification of
// a movie. It stores source "operator". A ladder token records a rating;
// explicit_unrated records an explicit NR; an empty rating without
// explicit_unrated clears the operator value, returning the item to
// "unavailable". Unknown tokens are rejected with InvalidArgument.
//
// No admin/role check happens here (see the package comment above).
func (m *Module) SetContentRating(ctx context.Context, req *mgmntv1.SetContentRatingRequest) (*mgmntv1.SetContentRatingResponse, error) {
	movieID := req.GetMovieId()
	if movieID == "" {
		return nil, status.Error(codes.InvalidArgument, "movie_id required")
	}
	token := strings.ToUpper(strings.TrimSpace(req.GetContentRating()))
	switch {
	case req.GetExplicitUnrated() && token != "":
		return nil, status.Error(codes.InvalidArgument, "content_rating must be empty when explicit_unrated is set")
	case token != "":
		if _, ok := ratingLevel(token); !ok {
			return nil, status.Errorf(codes.InvalidArgument, "unsupported content_rating %q", req.GetContentRating())
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	var exists string
	if err := m.db.QueryRowContext(ctx, `SELECT id FROM movies WHERE id = ?`, movieID).Scan(&exists); err != nil || exists == "" {
		return nil, status.Errorf(codes.NotFound, "movie not found: %s", movieID)
	}

	if token == "" && !req.GetExplicitUnrated() {
		if _, err := m.db.ExecContext(ctx, `DELETE FROM movie_content_rating WHERE movie_id = ?`, movieID); err != nil {
			return nil, fmt.Errorf("clear content rating: %w", err)
		}
		return &mgmntv1.SetContentRatingResponse{}, nil
	}
	if req.GetExplicitUnrated() {
		token = contentRatingNR
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := m.db.ExecContext(ctx,
		`INSERT INTO movie_content_rating (movie_id, content_rating, source, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(movie_id) DO UPDATE SET content_rating = excluded.content_rating,
		   source = excluded.source, updated_at = excluded.updated_at`,
		movieID, token, contentRatingSourceOperator, now,
	); err != nil {
		return nil, fmt.Errorf("set content rating: %w", err)
	}
	return &mgmntv1.SetContentRatingResponse{}, nil
}

// classification is the trusted parental data of one movie.
type classification struct {
	rating string // "" (unavailable), "NR", or a ladder token (upper case)
	source string // "", contentRatingSourceOperator or contentRatingSourceTMDB
	tags   []string
}

// validStoredRating reports whether a stored value is "NR" or on the ladder.
func validStoredRating(rating string) bool {
	_, onLadder := ratingLevel(rating)
	return onLadder || rating == contentRatingNR
}

// loadClassifications returns the classification of every id, in three queries per
// chunk (operator ratings, tmdb ratings, tag labels), independent of the number of ids. Callers hold m.mu
// and must not have a result cursor open (the pool has one connection).
//
// A stored row only counts when its source is trusted and its value is "NR" or
// on the ladder; anything else (including a hand-edited row) is ignored, so a
// bad value can never widen access. The effective value is the operator row if
// it counts, else the tmdb row if it counts, else unavailable.
func (m *Module) loadClassifications(ctx context.Context, ids []string) (map[string]classification, error) {
	out := make(map[string]classification, len(ids))
	for _, id := range ids {
		out[id] = classification{}
	}
	const chunk = 400
	for start := 0; start < len(ids); start += chunk {
		part := ids[start:min(start+chunk, len(ids))]
		ph := strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}

		rrows, err := m.db.QueryContext(ctx,
			`SELECT movie_id, content_rating, source FROM movie_content_rating WHERE movie_id IN (`+ph+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("load content ratings: %w", err)
		}
		for rrows.Next() {
			var id, rating, source string
			if err := rrows.Scan(&id, &rating, &source); err != nil {
				_ = rrows.Close()
				return nil, fmt.Errorf("scan content rating: %w", err)
			}
			rating = strings.ToUpper(strings.TrimSpace(rating))
			if source == contentRatingSourceOperator && validStoredRating(rating) {
				c := out[id]
				c.rating, c.source = rating, source
				out[id] = c
			}
		}
		if err := rrows.Err(); err != nil {
			_ = rrows.Close()
			return nil, fmt.Errorf("load content ratings: %w", err)
		}
		_ = rrows.Close()

		// The tmdb value only fills items the operator has not classified.
		mrows, err := m.db.QueryContext(ctx,
			`SELECT movie_id, content_rating FROM movie_tmdb_rating WHERE movie_id IN (`+ph+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("load tmdb ratings: %w", err)
		}
		for mrows.Next() {
			var id, rating string
			if err := mrows.Scan(&id, &rating); err != nil {
				_ = mrows.Close()
				return nil, fmt.Errorf("scan tmdb rating: %w", err)
			}
			rating = strings.ToUpper(strings.TrimSpace(rating))
			if c := out[id]; c.rating == "" && validStoredRating(rating) {
				c.rating, c.source = rating, contentRatingSourceTMDB
				out[id] = c
			}
		}
		if err := mrows.Err(); err != nil {
			_ = mrows.Close()
			return nil, fmt.Errorf("load tmdb ratings: %w", err)
		}
		_ = mrows.Close()

		trows, err := m.db.QueryContext(ctx,
			`SELECT it.item_id, t.label FROM item_tags it
			 INNER JOIN tags t ON t.id = it.tag_id
			 WHERE it.item_id IN (`+ph+`) ORDER BY t.label`, args...)
		if err != nil {
			return nil, fmt.Errorf("load item tags: %w", err)
		}
		for trows.Next() {
			var id, label string
			if err := trows.Scan(&id, &label); err != nil {
				_ = trows.Close()
				return nil, fmt.Errorf("scan item tag: %w", err)
			}
			c := out[id]
			c.tags = append(c.tags, label)
			out[id] = c
		}
		if err := trows.Err(); err != nil {
			_ = trows.Close()
			return nil, fmt.Errorf("load item tags: %w", err)
		}
		_ = trows.Close()
	}
	return out, nil
}

// attachClassification fills content_rating, content_rating_source and
// tag_labels on every movie. A lookup failure is returned, never swallowed: an
// item must not leave this module looking untagged because a query failed.
func (m *Module) attachClassification(ctx context.Context, movies ...*mgmntv1.MovieItem) error {
	if len(movies) == 0 {
		return nil
	}
	ids := make([]string, 0, len(movies))
	for _, mv := range movies {
		ids = append(ids, mv.GetId())
	}
	cls, err := m.loadClassifications(ctx, ids)
	if err != nil {
		return err
	}
	for _, mv := range movies {
		c := cls[mv.GetId()]
		mv.ContentRating, mv.ContentRatingSource = c.rating, c.source
		mv.TagLabels = c.tags
	}
	return nil
}

// classificationFilter is a validated ClassificationFilter.
type classificationFilter struct {
	maxLevel     int
	hasMax       bool
	allowUnrated bool
	blocked      []string
	allowed      []string
}

// compileClassificationFilter validates f. It returns nil (no narrowing) when f
// is absent or not enabled. Anything it cannot interpret is an error, never a
// silently weaker filter.
func compileClassificationFilter(f *mgmntv1.ClassificationFilter) (*classificationFilter, error) {
	if f == nil || !f.GetEnabled() {
		return nil, nil
	}
	cf := &classificationFilter{allowUnrated: f.GetAllowUnrated()}
	if ceiling := strings.TrimSpace(f.GetMaxRating()); ceiling != "" {
		level, ok := ratingLevel(ceiling)
		if !ok {
			return nil, status.Errorf(codes.InvalidArgument, "unsupported max_rating %q", f.GetMaxRating())
		}
		cf.maxLevel, cf.hasMax = level, true
	}
	norm := func(field string, in []string) ([]string, error) {
		out := make([]string, 0, len(in))
		for _, tag := range in {
			tag = normalizeTag(tag)
			if tag == "" {
				return nil, status.Errorf(codes.InvalidArgument, "%s contains an empty tag", field)
			}
			out = append(out, tag)
		}
		return out, nil
	}
	var err error
	if cf.blocked, err = norm("blocked_tags", f.GetBlockedTags()); err != nil {
		return nil, err
	}
	if cf.allowed, err = norm("allowed_tags", f.GetAllowedTags()); err != nil {
		return nil, err
	}
	return cf, nil
}

// visible reports whether an item with classification c passes the filter. It
// mirrors parental.Evaluate for a restricted policy: rating first (unavailable
// is always denied), then tags, so a tag match never overrides a rating denial.
func (cf *classificationFilter) visible(c classification) bool {
	switch {
	case c.rating == contentRatingNR:
		if !cf.allowUnrated {
			return false
		}
	case c.rating != "":
		level, ok := ratingLevel(c.rating)
		if !ok || (cf.hasMax && level > cf.maxLevel) {
			return false
		}
	default: // unavailable
		return false
	}
	if len(cf.blocked) == 0 && len(cf.allowed) == 0 {
		return true
	}
	tags := make([]string, 0, len(c.tags))
	for _, tag := range c.tags {
		if tag = normalizeTag(tag); tag != "" {
			tags = append(tags, tag)
		}
	}
	for _, b := range cf.blocked {
		if slices.Contains(tags, b) {
			return false
		}
	}
	if len(cf.allowed) > 0 && !slices.ContainsFunc(cf.allowed, func(a string) bool { return slices.Contains(tags, a) }) {
		return false
	}
	return true
}

// movieSelectCols is the column list scanMovie and scanSingle expect.
const movieSelectCols = `id, tmdb_id, title, original_title, year, overview, tagline,
		runtime, vote_average, status, imdb_id, genres, poster_path, backdrop_path,
		monitored, has_file, quality_profile_id, root_folder_path, created_at, updated_at`

// listMoviesFiltered serves ListMovies when a classification filter is
// enabled. Visibility is decided in Go with the item's trusted classification,
// over the full ordered candidate set, so total and pagination count only the
// visible items. The filter can only remove candidates, never add any.
// Callers hold m.mu (read or write).
func (m *Module) listMoviesFiltered(ctx context.Context, whereClause string, args []any, orderBy string, page, pageSize int, cf *classificationFilter) (*mgmntv1.ListMoviesResponse, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT id FROM movies`+whereClause+orderBy, args...)
	if err != nil {
		return nil, fmt.Errorf("query movies: %w", err)
	}
	var candidates []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan movie id: %w", err)
		}
		candidates = append(candidates, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("query movies: %w", err)
	}
	_ = rows.Close()

	cls, err := m.loadClassifications(ctx, candidates)
	if err != nil {
		return nil, err
	}
	visible := make([]string, 0, len(candidates))
	for _, id := range candidates {
		if cf.visible(cls[id]) {
			visible = append(visible, id)
		}
	}

	resp := &mgmntv1.ListMoviesResponse{Total: int32(len(visible)), Page: int32(page), PageSize: int32(pageSize)}
	start := (page - 1) * pageSize
	if start >= len(visible) {
		return resp, nil
	}
	pageIDs := visible[start:min(start+pageSize, len(visible))]

	ph := strings.TrimSuffix(strings.Repeat("?,", len(pageIDs)), ",")
	pargs := make([]any, len(pageIDs))
	for i, id := range pageIDs {
		pargs[i] = id
	}
	prows, err := m.db.QueryContext(ctx, `SELECT `+movieSelectCols+` FROM movies WHERE id IN (`+ph+`)`, pargs...)
	if err != nil {
		return nil, fmt.Errorf("query movies: %w", err)
	}
	byID := make(map[string]*mgmntv1.MovieItem, len(pageIDs))
	for prows.Next() {
		if mv := m.scanMovie(prows); mv != nil {
			byID[mv.GetId()] = mv
		}
	}
	if err := prows.Err(); err != nil {
		_ = prows.Close()
		return nil, fmt.Errorf("query movies: %w", err)
	}
	_ = prows.Close()

	for _, id := range pageIDs {
		if mv := byID[id]; mv != nil {
			// Reuse the classification the decision was made on.
			c := cls[id]
			mv.ContentRating, mv.ContentRatingSource, mv.TagLabels = c.rating, c.source, c.tags
			resp.Movies = append(resp.Movies, mv)
		}
	}
	return resp, nil
}
