package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
)

type movieCalendarItem struct {
	ID        string `json:"id"`
	ParentID  string `json:"parent_id"`
	Title     string `json:"title"`
	Subtitle  string `json:"subtitle,omitempty"`
	Date      string `json:"date"`
	Monitored bool   `json:"monitored"`
	HasFile   bool   `json:"has_file"`
	Year      int32  `json:"year,omitempty"`
	Kind      string `json:"kind"`
}

func normalizeReleaseDate(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= 10 && s[4] == '-' && s[7] == '-' {
		if _, err := time.Parse("2006-01-02", s[:10]); err == nil {
			return s[:10]
		}
	}
	return ""
}

func yearFromISODate(raw string) int {
	if len(raw) < 4 {
		return 0
	}
	var y int
	if _, err := fmt.Sscanf(raw[:4], "%d", &y); err != nil {
		return 0
	}
	return y
}

func pendingReleaseDate(year int32, start, end string) (date, subtitle string) {
	if year <= 0 {
		return "", ""
	}
	yearStart := fmt.Sprintf("%04d-01-01", year)
	yearEnd := fmt.Sprintf("%04d-12-31", year)
	if yearEnd < start || yearStart > end {
		return "", ""
	}
	if yearStart < start {
		return start, "Release year (date pending)"
	}
	return yearStart, "Release year (date pending)"
}

func (m *Module) persistReleaseDate(ctx context.Context, movieID, raw string) {
	date := normalizeReleaseDate(raw)
	if date == "" || strings.TrimSpace(movieID) == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return
	}
	_, _ = m.db.ExecContext(ctx,
		`UPDATE movies SET release_date=?, updated_at=? WHERE id=? AND (release_date = '' OR release_date IS NULL)`,
		date, time.Now().UTC().Format(time.RFC3339), movieID,
	)
}

func (m *Module) listMovieCalendar(ctx context.Context, start, end string, includeUnmon bool) ([]movieCalendarItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	if start == "" || end == "" {
		return nil, fmt.Errorf("start_date and end_date required (YYYY-MM-DD)")
	}
	startYear := yearFromISODate(start)
	endYear := yearFromISODate(end)
	if startYear == 0 {
		startYear = 1
	}
	if endYear == 0 {
		endYear = 9999
	}
	query := `SELECT id, title, year, release_date, monitored, has_file
		FROM movies
		WHERE (
			(release_date != '' AND release_date >= ? AND release_date <= ?)
			OR (
				(release_date IS NULL OR release_date = '')
				AND has_file = 0
				AND year >= ? AND year <= ?
			)
		)`
	args := []any{start, end, startYear, endYear}
	if !includeUnmon {
		query += ` AND monitored = 1`
	}
	query += ` ORDER BY title`
	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var items []movieCalendarItem
	for rows.Next() {
		var id, title, release string
		var year int32
		var monitored, hasFile int
		if err := rows.Scan(&id, &title, &year, &release, &monitored, &hasFile); err != nil {
			return nil, err
		}
		date := normalizeReleaseDate(release)
		subtitle := "Theatrical / digital"
		if date == "" {
			if hasFile == 1 {
				continue
			}
			date, subtitle = pendingReleaseDate(year, start, end)
			if date == "" {
				continue
			}
		}
		items = append(items, movieCalendarItem{
			ID: id, ParentID: id, Title: title, Subtitle: subtitle,
			Date: date, Monitored: monitored == 1, HasFile: hasFile == 1,
			Year: year, Kind: "movie",
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Date != items[j].Date {
			return items[i].Date < items[j].Date
		}
		return items[i].Title < items[j].Title
	})
	return items, nil
}

func (m *Module) handleHTTPCalendar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	start := strings.TrimSpace(r.URL.Query().Get("start"))
	end := strings.TrimSpace(r.URL.Query().Get("end"))
	includeUnmon := r.URL.Query().Get("unmonitored") == "1"
	items, err := m.listMovieCalendar(r.Context(), start, end, includeUnmon)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if items == nil {
		items = []movieCalendarItem{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "total": len(items)})
}

func (s mediaAdminServer) GetCalendar(ctx context.Context, req *mediaadminv1.GetCalendarRequest) (*mediaadminv1.GetCalendarResponse, error) {
	items, err := s.m.listMovieCalendar(ctx, req.GetStartDate(), req.GetEndDate(), req.GetIncludeUnmonitored())
	if err != nil {
		return nil, err
	}
	out := make([]*mediaadminv1.CalendarItem, 0, len(items))
	for _, it := range items {
		out = append(out, &mediaadminv1.CalendarItem{
			Id: it.ID, ParentId: it.ParentID, Title: it.Title, Subtitle: it.Subtitle,
			Date: it.Date, Monitored: it.Monitored, HasFile: it.HasFile,
			Metadata: map[string]string{"kind": "movie", "year": fmt.Sprintf("%d", it.Year)},
		})
	}
	return &mediaadminv1.GetCalendarResponse{Items: out}, nil
}
