package internal

// resolveMovieSortColumn maps admin/mgmt sort keys to SQL column names and
// rejects unknown values (defaults to title) to block ORDER BY injection.
func resolveMovieSortColumn(sortBy string) string {
	switch sortBy {
	case "rating":
		return "vote_average"
	case "added_at":
		return "created_at"
	case "sort_title":
		return "title"
	case "title", "year", "updated_at", "runtime", "vote_average", "created_at":
		return sortBy
	default:
		return "title"
	}
}

func validMovieSortKeys() map[string]bool {
	return map[string]bool{
		"title": true, "year": true, "rating": true,
		"added_at": true, "updated_at": true,
		"sort_title": true, "runtime": true,
	}
}
