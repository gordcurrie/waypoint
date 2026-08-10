package garmin_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gordcurrie/waypoint/internal/garmin"
)

func TestExerciseSetFrom(t *testing.T) {
	row := map[string]any{
		"activity_id":   "21711179290",
		"set_index":     "2",
		"time":          "2026-01-30T14:32:17Z",
		"category":      "HIP_RAISE",
		"exercise_name": "SINGLE_LEG_HIP_RAISE",
		"duration_s":    float64(40),
		"reps":          float64(8),
		"weight_kg":     float64(20),
		"set_type":      "ACTIVE",
	}

	set := garmin.ExerciseSetFrom(row)

	if set.ActivityID != 21711179290 {
		t.Errorf("ActivityID: got %d, want 21711179290", set.ActivityID)
	}
	if set.SetIndex != 2 {
		t.Errorf("SetIndex: got %d, want 2", set.SetIndex)
	}
	want := time.Date(2026, 1, 30, 14, 32, 17, 0, time.UTC)
	if !set.Time.Equal(want) {
		t.Errorf("Time: got %v, want %v", set.Time, want)
	}
	if set.Category != "HIP_RAISE" {
		t.Errorf("Category: got %q, want HIP_RAISE", set.Category)
	}
	if set.ExerciseName != "SINGLE_LEG_HIP_RAISE" {
		t.Errorf("ExerciseName: got %q, want SINGLE_LEG_HIP_RAISE", set.ExerciseName)
	}
	if set.DurationS != 40 {
		t.Errorf("DurationS: got %v, want 40", set.DurationS)
	}
	if set.Reps == nil || *set.Reps != 8 {
		t.Errorf("Reps: got %v, want 8", set.Reps)
	}
	if set.WeightKg == nil || *set.WeightKg != 20 {
		t.Errorf("WeightKg: got %v, want 20", set.WeightKg)
	}
	if set.SetType != "ACTIVE" {
		t.Errorf("SetType: got %q, want ACTIVE", set.SetType)
	}
}

func TestExerciseSetFrom_RestSetHasNoCategory(t *testing.T) {
	row := map[string]any{
		"activity_id": "21711179290",
		"set_index":   "3",
		"time":        "2026-01-30T14:32:57Z",
		"duration_s":  float64(20),
		"set_type":    "REST",
	}

	set := garmin.ExerciseSetFrom(row)

	if set.Category != "" {
		t.Errorf("Category: got %q, want empty on a REST set", set.Category)
	}
	if set.Reps != nil {
		t.Errorf("Reps: got %v, want nil (absent) on a REST set", set.Reps)
	}
	if set.SetType != "REST" {
		t.Errorf("SetType: got %q, want REST", set.SetType)
	}
}

func TestExerciseSetFrom_RepsZeroSurvivesJSONMarshal(t *testing.T) {
	// Observed live: device detected the exercise but didn't count reps for
	// it — a real 0, not the same as a REST set having no reps at all. Must
	// not be dropped by omitempty the way a plain float64 would be.
	row := map[string]any{
		"activity_id": "21711179290",
		"set_index":   "8",
		"time":        "2026-01-30T14:35:17Z",
		"category":    "PLANK",
		"duration_s":  float64(40),
		"reps":        float64(0),
		"set_type":    "ACTIVE",
	}

	set := garmin.ExerciseSetFrom(row)
	if set.Reps == nil || *set.Reps != 0 {
		t.Fatalf("Reps: got %v, want a non-nil pointer to 0", set.Reps)
	}

	b, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if v, ok := decoded["reps"]; !ok || v != float64(0) {
		t.Errorf("JSON reps: got %v (present=%v), want 0", v, ok)
	}
}

func TestExerciseSetFrom_WeightKgZeroSurvivesJSONMarshal(t *testing.T) {
	// A bodyweight-only exercise can log an explicit 0 kg — a real value, not
	// the same as no weight ever having been logged at all.
	row := map[string]any{
		"activity_id": "21711179290",
		"set_index":   "0",
		"time":        "2026-01-30T14:31:18Z",
		"category":    "LUNGE",
		"duration_s":  float64(40),
		"weight_kg":   float64(0),
		"set_type":    "ACTIVE",
	}

	set := garmin.ExerciseSetFrom(row)
	if set.WeightKg == nil || *set.WeightKg != 0 {
		t.Fatalf("WeightKg: got %v, want a non-nil pointer to 0", set.WeightKg)
	}

	b, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if v, ok := decoded["weight_kg"]; !ok || v != float64(0) {
		t.Errorf("JSON weight_kg: got %v (present=%v), want 0", v, ok)
	}
}
