package internal

import (
	"strings"
	"time"
)

var movieAvailabilityRank = map[string]int{
	"announced": 0,
	"inCinemas": 1,
	"released":  2,
	"preDB":     3,
}

func normalizeMinimumAvailability(v string) string {
	v = strings.TrimSpace(v)
	switch v {
	case "announced", "inCinemas", "released", "preDB":
		return v
	default:
		return "released"
	}
}

func currentMovieAvailability(status, releaseDate string) string {
	status = strings.TrimSpace(status)
	releaseDate = strings.TrimSpace(releaseDate)
	if strings.EqualFold(status, "Released") {
		return "released"
	}
	if releaseDate == "" {
		return "announced"
	}
	rd, err := time.Parse("2006-01-02", releaseDate)
	if err != nil {
		if len(releaseDate) >= 10 {
			rd, err = time.Parse("2006-01-02", releaseDate[:10])
		}
		if err != nil {
			return "announced"
		}
	}
	now := time.Now().UTC()
	if rd.After(now) {
		return "announced"
	}
	if now.Sub(rd) <= 90*24*time.Hour {
		return "inCinemas"
	}
	return "released"
}

func movieMeetsMinimumAvailability(minAvail, status, releaseDate string) bool {
	minAvail = normalizeMinimumAvailability(minAvail)
	if strings.TrimSpace(releaseDate) == "" && strings.TrimSpace(status) == "" {
		// Legacy rows without schedule metadata stay eligible for missing.
		return true
	}
	current := currentMovieAvailability(status, releaseDate)
	return movieAvailabilityRank[current] >= movieAvailabilityRank[minAvail]
}
