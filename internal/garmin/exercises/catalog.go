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
)

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
		for _, e := range catalog {
			byPair[[2]string{e.Category, e.ExerciseName}] = true
		}
	})
}

// Valid reports whether (category, exerciseName) is a real pair in Garmin's catalog.
func Valid(category, exerciseName string) bool {
	load()
	return byPair[[2]string{category, exerciseName}]
}

// Search returns up to limit exercises matching query (case-insensitive substring
// match against exercise_name and display_name), optionally restricted to category.
// An empty query matches everything (subject to the category filter and limit).
func Search(query, category string, limit int) []Exercise {
	load()
	if limit <= 0 {
		limit = 20
	}
	q := strings.ToLower(strings.TrimSpace(query))

	results := make([]Exercise, 0, limit)
	for _, e := range catalog {
		if category != "" && e.Category != category {
			continue
		}
		if q != "" &&
			!strings.Contains(strings.ToLower(e.ExerciseName), q) &&
			!strings.Contains(strings.ToLower(e.DisplayName), q) {
			continue
		}
		results = append(results, e)
		if len(results) >= limit {
			break
		}
	}
	return results
}
