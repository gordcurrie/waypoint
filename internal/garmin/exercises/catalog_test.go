package exercises

import "testing"

func TestLoad_RealisticSize(t *testing.T) {
	load()
	if len(catalog) < 1000 {
		t.Errorf("catalog: got %d entries, want > 1000 (regenerate via scripts/generate_exercise_catalog.py?)", len(catalog))
	}
	categories := make(map[string]bool)
	for _, e := range catalog {
		categories[e.Category] = true
	}
	if len(categories) < 20 {
		t.Errorf("catalog: got %d categories, want > 20", len(categories))
	}
}

func TestValid_KnownPair(t *testing.T) {
	if !Valid("BENCH_PRESS", "BARBELL_BENCH_PRESS") {
		t.Error("BENCH_PRESS/BARBELL_BENCH_PRESS should be valid — seen in a real Garmin workout")
	}
}

func TestValid_UnknownPair(t *testing.T) {
	if Valid("NOT_A_REAL_CATEGORY", "NOT_A_REAL_EXERCISE") {
		t.Error("nonexistent pair should not be valid")
	}
}

func TestValid_RightExerciseWrongCategory(t *testing.T) {
	if Valid("SQUAT", "BARBELL_BENCH_PRESS") {
		t.Error("exercise_name from a different category should not validate as a pair")
	}
}

func TestSearch_FindsKnownExercise(t *testing.T) {
	results := Search("bench press", "", 20)
	found := false
	for _, e := range results {
		if e.Category == "BENCH_PRESS" && e.ExerciseName == "BARBELL_BENCH_PRESS" {
			found = true
		}
	}
	if !found {
		t.Error("searching 'bench press' should find BARBELL_BENCH_PRESS")
	}
}

func TestSearch_CategoryFilter(t *testing.T) {
	results := Search("", "SQUAT", 50)
	if len(results) == 0 {
		t.Fatal("want at least one SQUAT exercise")
	}
	for _, e := range results {
		if e.Category != "SQUAT" {
			t.Errorf("category filter leaked: got %q, want SQUAT", e.Category)
		}
	}
}

func TestSearch_LimitClamped(t *testing.T) {
	results := Search("", "", 3)
	if len(results) != 3 {
		t.Errorf("want exactly 3 results, got %d", len(results))
	}
}

func TestSearch_DefaultLimit(t *testing.T) {
	results := Search("", "", 0)
	if len(results) != 20 {
		t.Errorf("want default limit 20, got %d", len(results))
	}
}

func TestSearch_EmptyQueryNoCategoryMatchesEverything(t *testing.T) {
	results := Search("", "", 1)
	if len(results) != 1 {
		t.Errorf("want 1 result, got %d", len(results))
	}
}

func TestSearch_NoMatch(t *testing.T) {
	results := Search("zzzznotarealexercise", "", 20)
	if len(results) != 0 {
		t.Errorf("want 0 results for nonsense query, got %d", len(results))
	}
}
