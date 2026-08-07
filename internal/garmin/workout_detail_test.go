package garmin_test

import (
	"testing"

	"github.com/gordcurrie/waypoint/internal/garmin"
)

func TestWorkoutDetailFrom(t *testing.T) {
	row := map[string]any{
		"workout_id": "1656732143",
		"name":       "Full-Body — Day 2",
		"sport":      "strength_training",
		"steps_json": `[{"type":"ExecutableStepDTO","stepId":14222928952}]`,
	}

	d := garmin.WorkoutDetailFrom(row)

	if d.WorkoutID != 1656732143 {
		t.Errorf("WorkoutID: got %d, want 1656732143", d.WorkoutID)
	}
	if d.Name != "Full-Body — Day 2" {
		t.Errorf("Name: got %q, want %q", d.Name, "Full-Body — Day 2")
	}
	if d.Sport != "strength_training" {
		t.Errorf("Sport: got %q, want strength_training", d.Sport)
	}
	if string(d.Steps) != `[{"type":"ExecutableStepDTO","stepId":14222928952}]` {
		t.Errorf("Steps: got %s", d.Steps)
	}
}

func TestWorkoutDetailFrom_MissingStepsJSONDefaultsToEmptyArray(t *testing.T) {
	row := map[string]any{
		"workout_id": "42",
	}

	d := garmin.WorkoutDetailFrom(row)

	if string(d.Steps) != "[]" {
		t.Errorf("Steps: got %s, want []", d.Steps)
	}
}

func TestWorkoutDetailFrom_WorkoutIDFromNumericField(t *testing.T) {
	// workout_id is written as a tag (string) by sync.py, but exercise the numeric
	// path too since int64FromString accepts both.
	row := map[string]any{
		"workout_id": float64(999),
	}

	d := garmin.WorkoutDetailFrom(row)

	if d.WorkoutID != 999 {
		t.Errorf("WorkoutID: got %d, want 999", d.WorkoutID)
	}
}
