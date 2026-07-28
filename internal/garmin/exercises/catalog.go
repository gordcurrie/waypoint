// Package exercises provides Garmin's strength-exercise catalog (category +
// exercise_name pairs used to link a workout step to Garmin Connect's built-in
// exercise picker and demo animation).
//
// The catalog has no documented Garmin API — it's vendored from a live capture of
// the workout editor's exercise picker data (see scripts/generate_exercise_catalog.py
// for how it was captured and how to regenerate it).
package exercises

import (
	"embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed catalog.json
var catalogFS embed.FS

// Exercise is one entry in Garmin's exercise catalog.
type Exercise struct {
	Category         string   `json:"category"`
	ExerciseName     string   `json:"exercise_name"`
	DisplayName      string   `json:"display_name"`
	PrimaryMuscles   []string `json:"primary_muscles"`
	SecondaryMuscles []string `json:"secondary_muscles"`
}

var (
	loadOnce sync.Once
	catalog  []Exercise
	byPair   map[[2]string]bool
	// searchText[i] is the precomputed lowercase "exerciseName displayName" for
	// catalog[i], so Search doesn't re-lowercase ~1500 entries on every call.
	searchText []string
)

// normalizeKey uppercases and trims a category/exerciseName for lookup — Garmin's
// catalog keys are UPPER_SNAKE_CASE, but callers (especially an LLM) may not match
// case exactly.
func normalizeKey(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

func load() {
	loadOnce.Do(func() {
		data, err := catalogFS.ReadFile("catalog.json")
		if err != nil {
			panic("exercises: embedded catalog.json missing or unreadable: " + err.Error())
		}
		if err := json.Unmarshal(data, &catalog); err != nil {
			panic("exercises: embedded catalog.json failed to parse: " + err.Error())
		}
		byPair = make(map[[2]string]bool, len(catalog))
		searchText = make([]string, len(catalog))
		for i, e := range catalog {
			byPair[[2]string{normalizeKey(e.Category), normalizeKey(e.ExerciseName)}] = true
			searchText[i] = strings.ToLower(e.ExerciseName + " " + e.DisplayName)
		}
	})
}

// Valid reports whether (category, exerciseName) is a real pair in Garmin's catalog.
// Matching is case-insensitive.
func Valid(category, exerciseName string) bool {
	load()
	return byPair[[2]string{normalizeKey(category), normalizeKey(exerciseName)}]
}

// Search returns up to limit exercises matching query (case-insensitive substring
// match against exercise_name and display_name), optionally restricted to category
// (also case-insensitive). An empty query matches everything (subject to the
// category filter and limit).
func Search(query, category string, limit int) []Exercise {
	load()
	if limit <= 0 {
		limit = 20
	}
	q := strings.ToLower(strings.TrimSpace(query))
	cat := normalizeKey(category)

	results := make([]Exercise, 0, limit)
	for i, e := range catalog {
		if cat != "" && normalizeKey(e.Category) != cat {
			continue
		}
		if q != "" && !strings.Contains(searchText[i], q) {
			continue
		}
		results = append(results, e)
		if len(results) >= limit {
			break
		}
	}
	return results
}
